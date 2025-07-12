# Receipt OCR Microservice

This is a microservice for OCR (Optical Character Recognition) processing of receipts using Go and Google Cloud Vision API. It's designed to work with a Laravel budgeting application.

## Features

- Extract text from receipt images using Google Cloud Vision API
- Parse receipt data to extract structured information (merchant, date, total amount, items)
- RESTful API for easy integration with other services
- Containerized for easy deployment

## Prerequisites

- Go 1.16 or higher
- Google Cloud account with Vision API enabled
- Google Cloud service account credentials

## Setup

### 1. Google Cloud Setup

1. Create a Google Cloud account if you don't have one
2. Create a new project in Google Cloud Console
3. Enable the Cloud Vision API for your project
4. Create a service account and download the JSON credentials file
5. Save the credentials file as `service-account.json` in the project root

### 2. Local Development

```bash
# Install dependencies
go get cloud.google.com/go/vision/apiv1
go get google.golang.org/api/option

# Set environment variable for Google Cloud credentials
export GOOGLE_APPLICATION_CREDENTIALS="./service-account.json"

# Run the service
go run main.go
```

### 3. Building and Running with Docker

```bash
# Build the Docker image
docker build -t receipt-ocr-service .

# Run the container
docker run -p 8080:8080 -v /path/to/service-account.json:/root/service-account.json receipt-ocr-service
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

Response:
```json
{
  "success": true,
  "text": ["Line 1", "Line 2", "..."],
  "receipt": {
    "merchant": "GROCERY STORE",
    "date": "01/15/2023",
    "total_amount": 42.99,
    "items": [
      {
        "name": "Milk",
        "price": 3.99,
        "quantity": 1
      },
      {
        "name": "Bread",
        "price": 2.49,
        "quantity": 1
      }
    ],
    "raw_text": ["Line 1", "Line 2", "..."]
  }
}
```

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

    public function processReceipt(UploadedFile $image)
    {
        $imageData = base64_encode(file_get_contents($image->path()));

        $response = Http::post($this->apiUrl . '/api/ocr', [
            'base64_image' => $imageData
        ]);

        if ($response->successful()) {
            return $response->json();
        }

        throw new \Exception('OCR processing failed: ' . $response->body());
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
        ]);

        $image = $request->file('receipt_image');

        try {
            $result = $this->ocrService->processReceipt($image);

            return response()->json([
                'success' => true,
                'data' => $result
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

## Future Improvements

1. Add authentication between the Laravel app and OCR service
2. Implement more sophisticated receipt parsing
3. Add caching for improved performance
4. Transition to on-site OCR using Tesseract or other libraries
5. Add support for different receipt formats and languages
