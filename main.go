package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	documentai "cloud.google.com/go/documentai/apiv1"
	"github.com/joho/godotenv"
	documentaipb "google.golang.org/genproto/googleapis/cloud/documentai/v1"
)

type OCRRequest struct {
	ImageURL     string `json:"image_url,omitempty"`
	Base64Image  string `json:"base64_image,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

type OCRResponse struct {
	Success bool     `json:"success"`
	Text    []string `json:"text,omitempty"`
	Error   string   `json:"error,omitempty"`
}

type ReceiptField struct {
	Name       string  `json:"name"`
	Confidence float32 `json:"confidence"`
	Value      string  `json:"value"`
}

type ReceiptItem struct {
	Description string `json:"description"`
	Quantity    string `json:"quantity,omitempty"`
	Price       string `json:"price,omitempty"`
	TotalPrice  string `json:"total_price,omitempty"`
}

type Receipt struct {
	MerchantName string         `json:"merchant_name,omitempty"`
	Date         string         `json:"date,omitempty"`
	TotalAmount  string         `json:"total_amount,omitempty"`
	Items        []ReceiptItem  `json:"items,omitempty"`
	Fields       []ReceiptField `json:"fields,omitempty"`
}

func testGoogleCloudConnection() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := documentai.NewDocumentProcessorClient(ctx)
	if err != nil {
		return fmt.Errorf("failed to create Document AI client: %v", err)
	}
	defer client.Close()

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	location := os.Getenv("DOCUMENT_AI_LOCATION")
	processorID := os.Getenv("DOCUMENT_AI_PROCESSOR_ID")

	if projectID == "" || location == "" || processorID == "" {
		return fmt.Errorf("missing required environment variables: GOOGLE_CLOUD_PROJECT, DOCUMENT_AI_LOCATION, or DOCUMENT_AI_PROCESSOR_ID")
	}

	name := fmt.Sprintf("projects/%s/locations/%s/processors/%s", projectID, location, processorID)
	_, err = client.GetProcessor(ctx, &documentaipb.GetProcessorRequest{
		Name: name,
	})
	if err != nil {
		return fmt.Errorf("failed to access Document AI processor: %v", err)
	}

	return nil
}

func main() {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using environment variables")
	}

	requiredEnvVars := []string{
		"GOOGLE_APPLICATION_CREDENTIALS",
		"GOOGLE_CLOUD_PROJECT",
		"DOCUMENT_AI_LOCATION",
		"DOCUMENT_AI_PROCESSOR_ID",
	}

	for _, envVar := range requiredEnvVars {
		if os.Getenv(envVar) == "" {
			log.Printf("ERROR: Required environment variable %s is not set", envVar)
			os.Exit(1)
		}
	}

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

		log.Println("Testing connection to Google Cloud Document AI...")
		if err := testGoogleCloudConnection(); err != nil {
			log.Printf("ERROR: Failed to connect to Google Cloud Document AI: %v", err)
			os.Exit(1)
		}
		log.Println("Successfully connected to Google Cloud Document AI")
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
			json.NewEncoder(w).Encode(map[string]string{"status": "Google Cloud Document AI is disabled"})
		})
	}
	log.Println("HTTP handlers registered successfully")

	log.Printf("OCR Service starting on port %s...\n", port)

	server := &http.Server{
		Addr:         ":" + port,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	log.Printf("Starting HTTP server on port %s...", port)
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
	texts, receipt, err := processDocument(ctx, req)
	if err != nil {
		sendErrorResponse(w, fmt.Sprintf("Error processing document: %v", err), http.StatusInternalServerError)
		return
	}

	response := OCRResponse{
		Success: true,
		Text:    texts,
	}

	// If we have receipt data, include it in the response
	if receipt != nil {
		// Add receipt data to the response
		responseBytes, err := json.Marshal(response)
		if err != nil {
			sendErrorResponse(w, fmt.Sprintf("Error serializing response: %v", err), http.StatusInternalServerError)
			return
		}

		var responseMap map[string]interface{}
		if err := json.Unmarshal(responseBytes, &responseMap); err != nil {
			sendErrorResponse(w, fmt.Sprintf("Error processing response: %v", err), http.StatusInternalServerError)
			return
		}

		receiptBytes, err := json.Marshal(receipt)
		if err != nil {
			sendErrorResponse(w, fmt.Sprintf("Error serializing receipt: %v", err), http.StatusInternalServerError)
			return
		}

		var receiptMap map[string]interface{}
		if err := json.Unmarshal(receiptBytes, &receiptMap); err != nil {
			sendErrorResponse(w, fmt.Sprintf("Error processing receipt: %v", err), http.StatusInternalServerError)
			return
		}

		responseMap["receipt"] = receiptMap

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(responseMap)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func processDocument(ctx context.Context, req OCRRequest) ([]string, *Receipt, error) {
	log.Println("Initializing Document AI client...")
	client, err := documentai.NewDocumentProcessorClient(ctx)
	if err != nil {
		log.Printf("ERROR: Failed to create Document AI client: %v", err)
		return nil, nil, fmt.Errorf("failed to create client: %v", err)
	}
	log.Println("Document AI client initialized successfully")
	defer client.Close()

	// Get image bytes
	var imageBytes []byte
	if req.ImageURL != "" {
		log.Printf("Processing image from URL: %s", req.ImageURL)
		imageBytes, err = downloadImage(req.ImageURL)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to download image: %v", err)
		}
	} else if req.Base64Image != "" {
		imageBytes, err = base64.StdEncoding.DecodeString(req.Base64Image)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to decode base64 image: %v", err)
		}
	} else {
		return nil, nil, fmt.Errorf("no image provided")
	}

	projectID := os.Getenv("GOOGLE_CLOUD_PROJECT")
	location := os.Getenv("DOCUMENT_AI_LOCATION")
	processorID := os.Getenv("DOCUMENT_AI_PROCESSOR_ID")

	name := fmt.Sprintf("projects/%s/locations/%s/processors/%s", projectID, location, processorID)
	mimeType := "image/jpeg"
	if len(imageBytes) > 2 && imageBytes[0] == 0x89 && imageBytes[1] == 0x50 { // PNG signature
		mimeType = "image/png"
	}

	processRequest := &documentaipb.ProcessRequest{
		Name: name,
		Source: &documentaipb.ProcessRequest_RawDocument{
			RawDocument: &documentaipb.RawDocument{
				Content:  imageBytes,
				MimeType: mimeType,
			},
		},
	}
	if req.Instructions != "" {
		log.Printf("Processing with instructions: %s", req.Instructions)
	}

	log.Println("Sending request to Document AI...")
	response, err := client.ProcessDocument(ctx, processRequest)
	if err != nil {
		log.Printf("ERROR: Document AI request failed: %v", err)
		return nil, nil, fmt.Errorf("failed to process document: %v", err)
	}
	log.Println("Received response from Document AI")

	// Extract text and structured data from the response
	texts, receipt := extractDataFromDocument(response.Document, req.Instructions)

	return texts, receipt, nil
}

func downloadImage(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download image, status code: %d", resp.StatusCode)
	}

	return ioutil.ReadAll(resp.Body)
}

func extractDataFromDocument(document *documentaipb.Document, instructions string) ([]string, *Receipt) {
	var texts []string
	receipt := &Receipt{
		Items:  []ReceiptItem{},
		Fields: []ReceiptField{},
	}

	if document.Text != "" {
		texts = append(texts, document.Text)
	}
	isShopReceipt := false
	if instructions != "" {
		isShopReceipt = strings.Contains(strings.ToLower(instructions), "shop receipt")
		log.Printf("Processing as shop receipt: %v", isShopReceipt)
	}

	for _, entity := range document.Entities {
		field := ReceiptField{
			Name:       entity.Type,
			Confidence: entity.Confidence,
			Value:      entity.MentionText,
		}
		receipt.Fields = append(receipt.Fields, field)
		switch entity.Type {
		case "receipt_merchant_name":
			receipt.MerchantName = entity.MentionText
		case "receipt_date":
			receipt.Date = entity.MentionText
		case "receipt_total_amount":
			receipt.TotalAmount = entity.MentionText
		case "line_item":
			item := ReceiptItem{}
			for _, property := range entity.Properties {
				switch property.Type {
				case "line_item/description":
					item.Description = property.MentionText
				case "line_item/quantity":
					item.Quantity = property.MentionText
				case "line_item/price":
					item.Price = property.MentionText
				case "line_item/total_price":
					item.TotalPrice = property.MentionText
				}
			}
			if item.Description != "" {
				receipt.Items = append(receipt.Items, item)
			}
		}
	}

	if len(receipt.Items) == 0 && isShopReceipt && document.Text != "" {
		log.Println("No structured items found, attempting to extract items from text")
		extractItemsFromText(document.Text, receipt)
	}

	return texts, receipt
}

func extractItemsFromText(text string, receipt *Receipt) {
	lines := strings.Split(text, "\n")
	priceRegex := regexp.MustCompile(`(\d+[.,]\d{2})`)
	var prices []float64
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "total") ||
			strings.Contains(strings.ToLower(line), "suma") ||
			strings.Contains(strings.ToLower(line), "razem") {
			matches := priceRegex.FindAllString(line, -1)
			for _, match := range matches {
				// Replace comma with dot for proper float parsing
				match = strings.Replace(match, ",", ".", -1)
				price, err := strconv.ParseFloat(match, 64)
				if err == nil {
					prices = append(prices, price)
				}
			}
		}
	}

	if len(prices) > 0 {
		sort.Float64s(prices)
		for i, j := 0, len(prices)-1; i < j; i, j = i+1, j-1 {
			prices[i], prices[j] = prices[j], prices[i]
		}
		if receipt.TotalAmount == "" {
			receipt.TotalAmount = fmt.Sprintf("%.2f", prices[0])
		}
	}

	var currentItem string
	for i, line := range lines {
		if strings.Contains(strings.ToLower(line), "total") ||
			strings.Contains(strings.ToLower(line), "suma") ||
			strings.Contains(strings.ToLower(line), "razem") ||
			strings.Contains(strings.ToLower(line), "receipt") ||
			strings.Contains(strings.ToLower(line), "paragon") ||
			strings.Contains(strings.ToLower(line), "thank you") ||
			strings.Contains(strings.ToLower(line), "dziękujemy") {
			continue
		}

		priceMatches := priceRegex.FindAllString(line, -1)
		if len(priceMatches) > 0 {
			if len(strings.TrimSpace(line)) == len(priceMatches[0]) && i > 0 {
				currentItem = strings.TrimSpace(lines[i-1])
			} else {
				currentItem = strings.TrimSpace(priceRegex.ReplaceAllString(line, ""))
			}
			priceStr := strings.Replace(priceMatches[0], ",", ".", -1)
			price, err := strconv.ParseFloat(priceStr, 64)
			if err == nil && price > 0 && price < 10000 {
				receipt.Items = append(receipt.Items, ReceiptItem{
					Description: currentItem,
					Price:       priceStr,
				})
			}
		}
	}
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
