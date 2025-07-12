package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	vision "cloud.google.com/go/vision/apiv1"
	"github.com/joho/godotenv"
	visionpb "google.golang.org/genproto/googleapis/cloud/vision/v1"
)

type OCRRequest struct {
	ImageURL    string `json:"image_url,omitempty"`
	Base64Image string `json:"base64_image,omitempty"`
}

type OCRResponse struct {
	Success bool     `json:"success"`
	Text    []string `json:"text,omitempty"`
	Error   string   `json:"error,omitempty"`
}


func testGoogleCloudConnection() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := vision.NewImageAnnotatorClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Vision API client: %v", err)
	}
	defer client.Close()

	// Create a simple test image with a single pixel
	image := &visionpb.Image{
		Content: []byte{137, 80, 78, 71, 13, 10, 26, 10, 0, 0, 0, 13, 73, 72, 68, 82, 0, 0, 0, 1, 0, 0, 0, 1, 8, 2, 0, 0, 0, 144, 119, 83, 222, 0, 0, 0, 12, 73, 68, 65, 84, 8, 215, 99, 248, 207, 0, 0, 3, 1, 1, 0, 242, 129, 255, 233, 0, 0, 0, 0, 73, 69, 78, 68, 174, 66, 96, 130},
	}

	// Try to detect labels in the image (this will verify API access)
	req := &visionpb.AnnotateImageRequest{
		Image: image,
		Features: []*visionpb.Feature{
			{
				Type:       visionpb.Feature_LABEL_DETECTION,
				MaxResults: 1,
			},
		},
	}

	_, err = client.AnnotateImage(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to access Vision API: %v", err)
	}

	return nil
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	// Use Google Cloud Vision API
	skipGoogleCloud := false

	if !skipGoogleCloud {
		credentialsPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")
		log.Printf("Using Google Cloud credentials from: %s", credentialsPath)
		if _, err := os.Stat(credentialsPath); os.IsNotExist(err) {
			log.Printf("ERROR: Google Cloud credentials file not found at %s", credentialsPath)
			os.Exit(1)
		} else {
			log.Println("Google Cloud credentials file exists")
		}

		log.Println("Testing connection to Google Cloud Vision API...")
		if err := testGoogleCloudConnection(); err != nil {
			log.Printf("ERROR: Failed to connect to Google Cloud Vision API: %v", err)
			os.Exit(1)
		}
		log.Println("Successfully connected to Google Cloud Vision API")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Using port: %s", port)

	debug := os.Getenv("DEBUG") == "true"
	if debug {
		log.Println("Debug mode enabled")
	}

	log.Println("Registering HTTP handlers...")
	http.HandleFunc("/health", handleHealth)
	if !skipGoogleCloud {
		http.HandleFunc("/api/ocr", handleOCR)
	} else {
		// Add a simple handler for /api/ocr that doesn't use Google Cloud
		http.HandleFunc("/api/ocr", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{"status": "Google Cloud Vision API is disabled"})
		})
	}
	log.Println("HTTP handlers registered successfully")

	log.Printf("OCR Service starting on port %s...\n", port)

	// Create a server with a timeout
	server := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Printf("Starting HTTP server on port %s...", port)

	// Start the server with error handling
	if err := server.ListenAndServe(); err != nil {
		log.Printf("ERROR: Server failed: %v", err)
		os.Exit(1)
	}
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"status": true})
}

func handleOCR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendErrorResponse(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req OCRRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendErrorResponse(w, "Invalid request format", http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	texts, err := processImage(ctx, req)
	if err != nil {
		sendErrorResponse(w, fmt.Sprintf("Error processing image: %v", err), http.StatusInternalServerError)
		return
	}

	response := OCRResponse{
		Success: true,
		Text:    texts,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func processImage(ctx context.Context, req OCRRequest) ([]string, error) {
	log.Println("Initializing Vision API client...")
	client, err := vision.NewImageAnnotatorClient(ctx)
	if err != nil {
		log.Printf("ERROR: Failed to create Vision API client: %v", err)
		return nil, fmt.Errorf("failed to create client: %v", err)
	}
	log.Println("Vision API client initialized successfully")
	defer client.Close()

	var image *visionpb.Image

	if req.ImageURL != "" {
		log.Printf("Processing image from URL: %s", req.ImageURL)
		image = &visionpb.Image{
			Source: &visionpb.ImageSource{
				ImageUri: req.ImageURL,
			},
		}
	} else if req.Base64Image != "" {
		imgBytes, err := base64.StdEncoding.DecodeString(req.Base64Image)
		if err != nil {
			return nil, fmt.Errorf("failed to decode base64 image: %v", err)
		}
		image = &visionpb.Image{
			Content: imgBytes,
		}
	} else {
		return nil, fmt.Errorf("no image provided")
	}

	texts, err := detectText(ctx, client, image)
	if err != nil {
		return nil, err
	}

	return texts, nil
}

func detectText(ctx context.Context, client *vision.ImageAnnotatorClient, image *visionpb.Image) ([]string, error) {
	log.Println("Preparing Vision API request...")
	req := &visionpb.AnnotateImageRequest{
		Image: image,
		Features: []*visionpb.Feature{
			{
				Type:       visionpb.Feature_TEXT_DETECTION,
				MaxResults: 10,
			},
		},
	}
	log.Println("Sending request to Vision API...")
	resp, err := client.AnnotateImage(ctx, req)
	if err != nil {
		log.Printf("ERROR: Vision API request failed: %v", err)
		return nil, fmt.Errorf("failed to detect text: %v", err)
	}
	log.Println("Received response from Vision API")

	var texts []string
	if resp.TextAnnotations != nil && len(resp.TextAnnotations) > 0 {
		log.Printf("Found %d text annotations", len(resp.TextAnnotations))
		for _, annotation := range resp.TextAnnotations {
			texts = append(texts, annotation.Description)
		}
	} else {
		log.Println("No text annotations found in the image")
	}

	log.Printf("Extracted %d text elements", len(texts))
	return texts, nil
}


func sendErrorResponse(w http.ResponseWriter, message string, statusCode int) {
	response := OCRResponse{
		Success: false,
		Error:   message,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(response)
}
