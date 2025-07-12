package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
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
	Receipt *Receipt `json:"receipt,omitempty"`
	Error   string   `json:"error,omitempty"`
}

type ReceiptItem struct {
	Name     string  `json:"name"`
	Price    float64 `json:"price"`
	Quantity int     `json:"quantity,omitempty"`
	Category string  `json:"category,omitempty"`
}

type Receipt struct {
	Merchant    string        `json:"merchant"`
	Date        string        `json:"date"`
	TotalAmount float64       `json:"total_amount"`
	Items       []ReceiptItem `json:"items"`
	RawText     []string      `json:"raw_text"`
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

	receipt, err := parseReceiptData(texts)
	if err != nil {
		log.Printf("Warning: Could not fully parse receipt: %v", err)
	}

	response := OCRResponse{
		Success: true,
		Text:    texts,
		Receipt: &receipt,
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

//specific service?
func categorizeItem(name string) string {
	name = strings.ToLower(name)

	// Define category mappings
	categories := map[string][]string{
		"Meat": {"mięso", "wiep", "woł", "drób", "kurczak", "indyk", "szynka", "kiełbasa", "parówki"},
		"Dairy": {"mleko", "ser", "jogurt", "śmietana", "masło", "twaróg", "kefir"},
		"Vegetables": {"kapusta", "marchew", "cebula", "ziemniak", "pomidor", "ogórek", "sałata", "szczypiorek"},
		"Fruits": {"jabłko", "banan", "gruszka", "śliwka", "winogrono", "truskawka", "malina"},
		"Bakery": {"chleb", "bułka", "rogal", "drożdżówka", "ciasto"},
		"Beverages": {"woda", "sok", "napój", "kawa", "herbata", "piwo"},
		"Groceries": {"ryż", "makaron", "mąka", "cukier", "sól", "olej", "przyprawy", "bulion", "koncentrat"},
		"Household": {"papier", "ręcznik", "mydło", "proszek", "płyn"},
	}

	for category, keywords := range categories {
		for _, keyword := range keywords {
			if strings.Contains(name, keyword) {
				return category
			}
		}
	}

	return "Other"
}

func parseReceiptData(texts []string) (Receipt, error) {
	receipt := Receipt{
		RawText: texts,
		Items:   []ReceiptItem{},
	}

	// Extract merchant name from the first few lines
	if len(texts) > 0 {
		lines := strings.Split(texts[0], "\n")
		if len(lines) > 5 {
			// Try to find a line with "sp. z o.o." or similar
			for i, line := range lines {
				if strings.Contains(line, "sp. z o.o.") || strings.Contains(line, "sp. z o. o.") {
					receipt.Merchant = line
					break
				}
				// If we haven't found a company name by line 5, use that line
				if i == 5 {
					receipt.Merchant = line
					break
				}
			}
		} else {
			receipt.Merchant = lines[0]
		}
	}

	// Patterns specifically for Polish Lidl receipts
	datePattern := regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`)
	totalPattern := regexp.MustCompile(`(?i)(suma|total|razem|suma plc|suma pln)[\s:]*(\d+[.,]\d{2})`)
	// Pattern for standalone total amount
	standaloneTotalPattern := regexp.MustCompile(`^(\d+[.,]\d{2})$`)
	// Pattern for items like "3,798 x4,19 15,91C"
	itemPattern := regexp.MustCompile(`(\d+[.,]?\d*)\s*x(\d+[.,]\d{2})\s*(\d+[.,]\d{2})C?`)
	// Pattern for items like "1 x12,89 12,83C"
	simpleItemPattern := regexp.MustCompile(`(\d+)\s*x(\d+[.,]\d{2})\s*(\d+[.,]\d{2})C?`)
	// Pattern for items with × (Unicode multiplication sign)
	unicodeItemPattern := regexp.MustCompile(`(\d+)\s*×(\d+[.,]\d{2})\s*(\d+[.,]\d{2})C?`)
	// Pattern for standalone items
	productPattern := regexp.MustCompile(`^([A-Za-zżźćńółęąśŻŹĆĄŚĘŁÓŃ].+?)$`)

	// Process each line of text
	for _, line := range texts {
		// Extract date
		if dateMatch := datePattern.FindString(line); dateMatch != "" && receipt.Date == "" {
			receipt.Date = dateMatch
		}

		// Extract total amount
		if totalMatch := totalPattern.FindStringSubmatch(line); len(totalMatch) > 2 && receipt.TotalAmount == 0 {
			totalStr := strings.Replace(totalMatch[2], ",", ".", -1)
			total, err := strconv.ParseFloat(totalStr, 64)
			if err == nil {
				receipt.TotalAmount = total
			}
		}

		// Look for standalone numbers that might be the total
		if receipt.TotalAmount == 0 {
			if sumMatch := standaloneTotalPattern.FindStringSubmatch(line); len(sumMatch) > 1 {
				sumStr := strings.Replace(sumMatch[1], ",", ".", -1)
				sum, err := strconv.ParseFloat(sumStr, 64)
				if err == nil && sum > 10.0 { // Assume totals are usually more than 10
					log.Printf("Found standalone total amount: %s", sumMatch[1])
					receipt.TotalAmount = sum
				}
			}
		}

		// Extract items with quantity and price
		if itemMatch := itemPattern.FindStringSubmatch(line); len(itemMatch) > 3 {
			quantityStr := strings.Replace(itemMatch[1], ",", ".", -1)
			quantity, _ := strconv.ParseFloat(quantityStr, 64)
			priceStr := strings.Replace(itemMatch[3], ",", ".", -1)
			price, err := strconv.ParseFloat(priceStr, 64)

			// Try to find the item name in the previous line
			itemName := "Unknown Item"
			for i, t := range texts {
				if t == line && i > 0 {
					itemName = texts[i-1]
					break
				}
			}

			if err == nil && !strings.Contains(strings.ToLower(itemName), "suma") && 
			   !strings.Contains(strings.ToLower(itemName), "total") {
				category := categorizeItem(itemName)
				log.Printf("Found item (quantity pattern): %s, price: %f, quantity: %f, category: %s", itemName, price, quantity, category)
				receipt.Items = append(receipt.Items, ReceiptItem{
					Name:     itemName,
					Price:    price,
					Quantity: int(quantity),
					Category: category,
				})
			}
		} else if simpleMatch := simpleItemPattern.FindStringSubmatch(line); len(simpleMatch) > 3 {
			quantity, _ := strconv.Atoi(simpleMatch[1])
			priceStr := strings.Replace(simpleMatch[3], ",", ".", -1)
			price, err := strconv.ParseFloat(priceStr, 64)

			// Try to find the item name in the previous line
			itemName := "Unknown Item"
			for i, t := range texts {
				if t == line && i > 0 {
					itemName = texts[i-1]
					break
				}
			}

			if err == nil && !strings.Contains(strings.ToLower(itemName), "suma") && 
			   !strings.Contains(strings.ToLower(itemName), "total") &&
			   !strings.Contains(strings.ToLower(itemName), "razem") {
				category := categorizeItem(itemName)
				log.Printf("Found item (simple pattern): %s, price: %f, quantity: %d, category: %s", itemName, price, quantity, category)
				receipt.Items = append(receipt.Items, ReceiptItem{
					Name:     itemName,
					Price:    price,
					Quantity: quantity,
					Category: category,
				})
			}
		} else if unicodeMatch := unicodeItemPattern.FindStringSubmatch(line); len(unicodeMatch) > 3 {
			quantity, _ := strconv.Atoi(unicodeMatch[1])
			priceStr := strings.Replace(unicodeMatch[3], ",", ".", -1)
			price, err := strconv.ParseFloat(priceStr, 64)

			// Try to find the item name in the previous line
			itemName := "Unknown Item"
			for i, t := range texts {
				if t == line && i > 0 {
					itemName = texts[i-1]
					break
				}
			}

			if err == nil && !strings.Contains(strings.ToLower(itemName), "suma") && 
			   !strings.Contains(strings.ToLower(itemName), "total") &&
			   !strings.Contains(strings.ToLower(itemName), "razem") {
				category := categorizeItem(itemName)
				log.Printf("Found item (unicode pattern): %s, price: %f, quantity: %d, category: %s", itemName, price, quantity, category)
				receipt.Items = append(receipt.Items, ReceiptItem{
					Name:     itemName,
					Price:    price,
					Quantity: quantity,
					Category: category,
				})
			}
		}
	}

	return receipt, nil
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
