# Receipt OCR Microservice

This is a microservice for OCR (Optical Character Recognition) processing of receipts using Go and Google Cloud Document AI. It extracts text and structured data from receipt images and provides it through a RESTful API. It's designed to work with a Laravel budgeting application or any other service that needs OCR capabilities.

## Features

- Extract text from receipt images using Google Cloud Document AI
- Automatically parse receipt data (merchant, date, total amount, line items)
- RESTful API for easy integration with other services
- Containerized for easy deployment
- Comprehensive test suite for verifying functionality

## Prerequisites

- Go 1.16 or higher
- Google Cloud account with Document AI API enabled
- Google Cloud service account credentials
- Document AI processor for receipts

## Setup

### 1. Google Cloud Setup

1. Create a Google Cloud account if you don't have one
2. Create a new project in Google Cloud Console
3. Enable the Document AI API for your project
4. Create a Document AI processor for receipts:
   - Go to Document AI in the Google Cloud Console
   - Click "Create Processor"
   - Select "Expense Parser" (which includes receipt parsing)
   - Note the processor ID, location, and project ID
5. Create a service account with Document AI access and download the JSON credentials file
6. Save the credentials file as `service-account.json` in the project root

### 2. Local Development

```bash
# Install dependencies
go get cloud.google.com/go/documentai/apiv1
go get google.golang.org/api/option

# Set environment variables
export GOOGLE_APPLICATION_CREDENTIALS="./service-account.json"
export GOOGLE_CLOUD_PROJECT="your-project-id"
export DOCUMENT_AI_LOCATION="us" # or your chosen location
export DOCUMENT_AI_PROCESSOR_ID="your-processor-id"

# Run the service
go run main.go
```

### 3. Building and Running with Docker

```bash
# Build the Docker image
docker build -t receipt-ocr-service .

# Run the container with all required environment variables
docker run -p 8080:8080 \
  -v /path/to/service-account.json:/app/service-account.json \
  -e GOOGLE_APPLICATION_CREDENTIALS=/app/service-account.json \
  -e GOOGLE_CLOUD_PROJECT=your-project-id \
  -e DOCUMENT_AI_LOCATION=us \
  -e DOCUMENT_AI_PROCESSOR_ID=your-processor-id \
  receipt-ocr-service
```

## API Endpoints

### Health Check

```
GET /health
```

Response:
```json
{
  "status": true
}
```

### OCR Processing

```
POST /api/ocr
```

Request Body:
```json
{
  "image_url": "https://example.com/receipt.jpg"
}
```

OR

```json
{
  "base64_image": "base64encodedimagedata..."
}
```

You can also include instructions to customize the OCR processing:

```json
{
  "image_url": "https://example.com/receipt.jpg",
  "instructions": "this is shop receipt. i want you to return to me json with positions: name and price and also total price"
}
```

Response:
```json
{
  "success": true,
  "text": ["Line 1", "Line 2", "..."],
  "receipt": {
    "merchant_name": "GROCERY STORE",
    "date": "2023-04-15",
    "total_amount": "42.99",
    "items": [
      {
        "description": "Milk",
        "quantity": "1",
        "price": "3.99",
        "total_price": "3.99"
      },
      {
        "description": "Bread",
        "price": "2.49"
      }
    ],
    "fields": [
      {
        "name": "receipt_merchant_name",
        "confidence": 0.95,
        "value": "GROCERY STORE"
      },
      {
        "name": "receipt_date",
        "confidence": 0.92,
        "value": "2023-04-15"
      }
    ]
  }
}
```

The `receipt` object contains structured data extracted from the receipt image using Document AI. The exact fields available will depend on what Document AI is able to extract from the image.

## Integration with Laravel

### 1. Create an OCR Service in Laravel

Create a new service class in your Laravel application:

```php
<?php

namespace App\Services;

use Illuminate\Support\Facades\Http;
use Illuminate\Http\UploadedFile;

class OCRService
{
    protected $apiUrl;

    public function __construct()
    {
        $this->apiUrl = config('services.ocr.url');
    }

    public function processReceipt(UploadedFile $image, bool $parseReceipt = false)
    {
        $imageData = base64_encode(file_get_contents($image->path()));

        $requestData = [
            'base64_image' => $imageData
        ];

        // Add instructions if receipt parsing is requested
        if ($parseReceipt) {
            $requestData['instructions'] = 'this is shop receipt. i want you to return to me json with positions: name and price and also total price';
        }

        $response = Http::post($this->apiUrl . '/api/ocr', $requestData);

        if ($response->successful()) {
            return $response->json();
        }

        throw new \Exception('OCR processing failed: ' . $response->body());
    }

    /**
     * Get structured receipt data from the OCR response
     * 
     * @param array $ocrResponse The response from the OCR service
     * @return array|null The structured receipt data or null if not available
     */
    public function getReceiptData(array $ocrResponse)
    {
        return $ocrResponse['receipt'] ?? null;
    }

    /**
     * Get the merchant name from the receipt data
     * 
     * @param array $ocrResponse The response from the OCR service
     * @return string|null The merchant name or null if not available
     */
    public function getMerchantName(array $ocrResponse)
    {
        return $ocrResponse['receipt']['merchant_name'] ?? null;
    }

    /**
     * Get the total amount from the receipt data
     * 
     * @param array $ocrResponse The response from the OCR service
     * @return float|null The total amount or null if not available
     */
    public function getTotalAmount(array $ocrResponse)
    {
        $totalAmount = $ocrResponse['receipt']['total_amount'] ?? null;
        return $totalAmount ? (float) $totalAmount : null;
    }

    /**
     * Get the line items from the receipt data
     * 
     * @param array $ocrResponse The response from the OCR service
     * @return array The line items or empty array if not available
     */
    public function getLineItems(array $ocrResponse)
    {
        return $ocrResponse['receipt']['items'] ?? [];
    }
}
```

### 2. Configure the Service URL

Add the OCR service URL to your `config/services.php` file:

```php
'ocr' => [
    'url' => env('OCR_SERVICE_URL', 'http://localhost:8080'),
],
```

### 3. Create a Controller to Handle Receipt Uploads

```php
<?php

namespace App\Http\Controllers;

use App\Services\OCRService;
use Illuminate\Http\Request;
use App\Models\Receipt;
use App\Models\ReceiptItem;

class ReceiptController extends Controller
{
    protected $ocrService;

    public function __construct(OCRService $ocrService)
    {
        $this->ocrService = $ocrService;
    }

    public function upload(Request $request)
    {
        $request->validate([
            'receipt_image' => 'required|image|max:5000',
            'parse_receipt' => 'boolean',
        ]);

        $image = $request->file('receipt_image');
        $parseReceipt = $request->input('parse_receipt', false);

        try {
            $ocrResult = $this->ocrService->processReceipt($image, $parseReceipt);

            // Create a new receipt record using the structured data
            if ($receiptData = $this->ocrService->getReceiptData($ocrResult)) {
                $receipt = new Receipt();
                $receipt->merchant_name = $this->ocrService->getMerchantName($ocrResult);
                $receipt->total_amount = $this->ocrService->getTotalAmount($ocrResult);
                $receipt->date = $receiptData['date'] ?? null;
                $receipt->user_id = auth()->id();
                $receipt->raw_text = json_encode($ocrResult['text']);
                $receipt->save();

                // Save receipt items
                foreach ($this->ocrService->getLineItems($ocrResult) as $itemData) {
                    $item = new ReceiptItem();
                    $item->receipt_id = $receipt->id;
                    $item->description = $itemData['description'] ?? 'Unknown Item';
                    $item->price = isset($itemData['price']) ? (float) $itemData['price'] : 0;
                    $item->quantity = isset($itemData['quantity']) ? (int) $itemData['quantity'] : 1;
                    $item->save();
                }

                return response()->json([
                    'success' => true,
                    'message' => 'Receipt processed successfully',
                    'receipt_id' => $receipt->id,
                    'data' => $ocrResult
                ]);
            }

            return response()->json([
                'success' => true,
                'message' => 'Receipt processed but no structured data available',
                'data' => $ocrResult
            ]);
        } catch (\Exception $e) {
            return response()->json([
                'success' => false,
                'message' => $e->getMessage()
            ], 500);
        }
    }
}
```

### 4. Add a Route for Receipt Uploads

In your `routes/api.php` file:

```php
Route::post('/receipts/upload', [ReceiptController::class, 'upload'])->middleware('auth:api');
```

## Testing

The service includes a test suite to verify its functionality. For detailed testing instructions, see [README_TEST.md](README_TEST.md).

### Running Tests

To run the tests, you need to have Google Cloud Document AI credentials and a configured processor set up. Make sure all required environment variables are set:

```bash
export GOOGLE_APPLICATION_CREDENTIALS="./service-account.json"
export GOOGLE_CLOUD_PROJECT="your-project-id"
export DOCUMENT_AI_LOCATION="us"
export DOCUMENT_AI_PROCESSOR_ID="your-processor-id"
```

Then you can use the provided test script:

```bash
# Make the script executable (if not already)
chmod +x test.sh

# Run the tests
./test.sh
```

This will run all the tests and display the results. The tests include:

1. Testing OCR with a sample image URL
2. Testing error handling with an invalid image URL
3. Testing the health endpoint
4. Testing structured receipt data extraction

### Example Image

The test suite uses the following example image:
[Example Receipt](https://images.iberion.media/images/origin/Image_20240504_160538_214_6fbb160765.jpg)

### Manual Testing

You can also test the service manually using curl:

```bash
curl -X POST -H "Content-Type: application/json" \
  -d '{"image_url":"https://images.iberion.media/images/origin/Image_20240504_160538_214_6fbb160765.jpg"}' \
  http://localhost:8080/api/ocr
```

## Future Improvements

1. Add authentication between the Laravel app and OCR service
2. Add caching for improved performance
3. Implement custom Document AI processor training for better accuracy
4. Add support for different receipt formats and languages
5. Implement batch processing for multiple receipts
6. Add fallback to Vision API when Document AI fails
7. Implement receipt categorization based on merchant and items
