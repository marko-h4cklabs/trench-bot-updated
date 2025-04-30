package services

import (
	"bytes"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http" // Import reflect package for type assertion/checking
	"strings"
	"time"

	"go.uber.org/zap"
)

// --- Structs for Helius getAsset Response ---
// These match the expected structure from Helius v1 getAsset RPC method.
// Double-check with Helius docs if their API changes.

type HeliusAssetResponse struct {
	Content *HeliusAssetContent `json:"content"`
	ID      string              `json:"id"`
	// Add other fields if needed (e.g., grouping, royalty, creators, ownership)
}

type HeliusAssetContent struct {
	Metadata *HeliusAssetMetadata `json:"metadata"`
	Links    *HeliusAssetLinks    `json:"links"`
	JsonUri  string               `json:"json_uri"`
	Files    []*HeliusAssetFile   `json:"files"` // Can contain multiple files including image
	// Add other fields like 'description', 'attributes' if needed
}

type HeliusAssetMetadata struct {
	Name        string `json:"name"`
	Symbol      string `json:"symbol"`
	Description string `json:"description"`
	// Add other metadata fields if needed
}

type HeliusAssetLinks struct {
	Image       string `json:"image"` // Primary image link (often CDN)
	ExternalURL string `json:"external_url"`
	// Add other link fields if needed
}

type HeliusAssetFile struct {
	Uri     string `json:"uri"`     // URI of the file (could be image or other media)
	Mime    string `json:"mime"`    // Mime type (e.g., "image/png")
	CdnUri  string `json:"cdn_uri"` // CDN link if available (often preferred)
	Context string `json:"context"` // Context like "image", "metadata", "video"
	// Add other file fields if needed
}

// --- End Helius Structs ---

func FetchWithRetry(url string, payload []byte, maxRetries int, appLogger *logger.Logger) (*http.Response, error) {
	var resp *http.Response
	var err error
	client := &http.Client{Timeout: 10 * time.Second} // Consider slightly longer timeout for RPC? 15s?
	urlField := zap.String("url", url)

	for i := 0; i < maxRetries; i++ {
		attemptField := zap.Int("attempt", i+1)
		// Use POST for RPC calls
		req, reqErr := http.NewRequest("POST", url, bytes.NewBuffer(payload))
		if reqErr != nil {
			appLogger.Error("HTTP request creation failed during fetch", urlField, zap.Error(reqErr))
			return nil, fmt.Errorf("failed http request creation: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)

		if err == nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				// Success case
				return resp, nil
			}
			statusField := zap.Int("statusCode", resp.StatusCode)
			// Read body for logging even on non-2xx if possible
			bodyBytes, readErr := io.ReadAll(resp.Body)
			if readErr == nil {
				appLogger.Warn("Request failed with non-2xx status", urlField, statusField, attemptField, zap.Int("maxRetries", maxRetries), zap.ByteString("responseBody", bodyBytes))
			} else {
				appLogger.Warn("Request failed with non-2xx status and failed to read body", urlField, statusField, attemptField, zap.Int("maxRetries", maxRetries), zap.Error(readErr))
			}
			resp.Body.Close() // Ensure body is closed
			err = fmt.Errorf("request failed with status: %s", resp.Status)

		} else {
			appLogger.Warn("HTTP request failed", urlField, attemptField, zap.Int("maxRetries", maxRetries), zap.Error(err))
		}

		if i < maxRetries-1 {
			// Exponential backoff
			backoff := time.Duration(math.Pow(2, float64(i))) * time.Second
			if backoff > 15*time.Second { // Cap backoff
				backoff = 15 * time.Second
			}
			appLogger.Info("Retrying request", urlField, attemptField, zap.Duration("backoff", backoff))
			time.Sleep(backoff)
		}
	}
	// Return the last error after all retries failed
	return nil, fmt.Errorf("request failed after %d retries for %s: %w", maxRetries, url, err)
}

// HeliusRPCRequest makes a JSON-RPC request to Helius with retries.
func HeliusRPCRequest(method string, params interface{}, appLogger *logger.Logger) (map[string]interface{}, error) {
	methodField := zap.String("method", method)
	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		appLogger.Error("HELIUS_API_KEY missing, cannot make RPC request", methodField)
		return nil, fmt.Errorf("missing HELIUS_API_KEY from env configuration")
	}
	// Use the mainnet RPC URL
	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", apiKey)
	urlField := zap.String("url", url)

	// Construct the JSON-RPC payload
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      fmt.Sprintf("helius-%s-%d", method, time.Now().UnixNano()), // More unique ID
		"method":  method,
		"params":  params,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		appLogger.Error("Failed to marshal Helius RPC payload", methodField, zap.Error(err))
		return nil, fmt.Errorf("failed to marshal Helius RPC payload: %w", err)
	}

	// Use FetchWithRetry for the request
	resp, err := FetchWithRetry(url, payloadBytes, 3, appLogger) // 3 Retries
	if err != nil {
		// FetchWithRetry already logs details, just wrap the error
		return nil, fmt.Errorf("Helius RPC request failed after retries: %w", err)
	}
	defer resp.Body.Close()

	// Read the response body
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		appLogger.Error("Failed to read Helius response body", methodField, urlField, zap.Int("status", resp.StatusCode), zap.Error(readErr))
		return nil, fmt.Errorf("failed to read Helius response body: %w", readErr)
	}

	// Unmarshal into a generic map first to check for errors
	var result map[string]interface{}
	if decodeErr := json.Unmarshal(bodyBytes, &result); decodeErr != nil {
		appLogger.Error("Failed to parse Helius response JSON", methodField, urlField, zap.Int("status", resp.StatusCode), zap.Error(decodeErr), zap.ByteString("rawBody", bodyBytes))
		return nil, fmt.Errorf("failed to parse Helius response JSON: %w", decodeErr)
	}

	// Check for JSON-RPC level errors within the response body
	if errorData, exists := result["error"]; exists && errorData != nil {
		errMap, ok := errorData.(map[string]interface{})
		var apiErr error
		if ok {
			apiErr = fmt.Errorf("Helius API Error: Code=%v, Message=%v", errMap["code"], errMap["message"])
		} else {
			apiErr = fmt.Errorf("Helius API Error: %v", errorData)
		}
		appLogger.Warn("Helius API returned an error in JSON response", methodField, urlField, zap.Any("errorDetail", errorData))
		return nil, apiErr
	}

	// Check if the 'result' field exists before returning
	if _, hasResult := result["result"]; !hasResult {
		appLogger.Warn("Helius response missing 'result' field", methodField, urlField, zap.ByteString("rawBody", bodyBytes))
		// Consider this an error, as we expect a result for successful calls
		return nil, fmt.Errorf("Helius response missing 'result' field")
	}

	appLogger.Debug("Helius RPC Request successful", methodField)
	return result, nil // Return the full map including the "result" field
}

// GetHeliusTokenImageURL fetches asset info from Helius and extracts the image URL.
func GetHeliusTokenImageURL(tokenAddress string, appLogger *logger.Logger) (string, error) {
	tokenField := zap.String("tokenAddress", tokenAddress)
	appLogger.Debug("Attempting Helius getAsset RPC call", tokenField)

	// Parameters for the getAsset method
	params := map[string]interface{}{
		"id": tokenAddress,
		// Optionally add displayOptions if needed, e.g.:
		// "displayOptions": map[string]bool{"showFungible": true},
	}

	// Use the generic HeliusRPCRequest helper
	rpcResultMap, err := HeliusRPCRequest("getAsset", params, appLogger)
	if err != nil {
		appLogger.Warn("Helius getAsset RPC call failed", tokenField, zap.Error(err))
		return "", fmt.Errorf("getAsset RPC failed: %w", err)
	}

	// --- Process the result ---
	// The actual asset data is nested under the "result" key in the map
	assetDataGeneric, ok := rpcResultMap["result"]
	if !ok || assetDataGeneric == nil {
		appLogger.Warn("Helius getAsset response missing or has nil 'result' field", tokenField)
		return "", fmt.Errorf("helius getAsset response missing 'result' field")
	}

	// We need to marshal the 'result' part back to JSON and then unmarshal into our specific struct
	// This is necessary because HeliusRPCRequest returns map[string]interface{}
	assetDataBytes, err := json.Marshal(assetDataGeneric)
	if err != nil {
		appLogger.Error("Failed to re-marshal Helius asset data for specific parsing", tokenField, zap.Error(err))
		return "", fmt.Errorf("failed to re-marshal asset data: %w", err)
	}

	var assetResponse HeliusAssetResponse
	err = json.Unmarshal(assetDataBytes, &assetResponse)
	if err != nil {
		appLogger.Error("Helius getAsset JSON parsing into specific struct failed", tokenField, zap.Error(err), zap.ByteString("assetDataJson", assetDataBytes))
		return "", fmt.Errorf("JSON parsing into HeliusAssetResponse failed for %s: %w", tokenAddress, err)
	}

	// --- Extract the image URL ---
	// Prioritize CDN URI from files if available, then links.image
	if assetResponse.Content != nil {
		// Check Files first for CDN link
		if len(assetResponse.Content.Files) > 0 {
			for _, file := range assetResponse.Content.Files {
				// Prefer CDN URI if it exists and mime type starts with "image/"
				if file != nil && file.CdnUri != "" && strings.HasPrefix(strings.ToLower(file.Mime), "image/") {
					appLogger.Debug("Using CDN image URL from Helius files", tokenField, zap.String("cdnUri", file.CdnUri))
					return file.CdnUri, nil
				}
				// Fallback to non-CDN file URI if mime type matches
				if file != nil && file.Uri != "" && strings.HasPrefix(strings.ToLower(file.Mime), "image/") {
					appLogger.Debug("Using standard image URI from Helius files", tokenField, zap.String("uri", file.Uri))
					return file.Uri, nil // Return the first valid image file URI found
				}
			}
		}

		// If no suitable image found in files, check links.image
		if assetResponse.Content.Links != nil && assetResponse.Content.Links.Image != "" {
			imageURL := assetResponse.Content.Links.Image
			appLogger.Debug("Using image URL from Helius links.image", tokenField, zap.String("imageURL", imageURL))
			return imageURL, nil
		}
	}

	// If we reach here, no suitable image URL was found in the response
	appLogger.Debug("Image URL not found within Helius getAsset response structure", tokenField)
	return "", fmt.Errorf("image URL not found in Helius response")
}

func InitializeSolana(appLogger *logger.Logger) {
	appLogger.Info("Solana Service Configuration Initialized (using Helius RPC)")
}

func CheckExistingHeliusWebhook(webhookURL string, appLogger *logger.Logger) (bool, error) {
	urlField := zap.String("webhookURL", webhookURL)
	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		appLogger.Error("HELIUS_API_KEY missing, cannot check webhooks", urlField)
		return false, fmt.Errorf("HELIUS_API_KEY missing")
	}

	// Helius v0 Webhook Management API URL
	url := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	client := &http.Client{Timeout: 15 * time.Second}

	appLogger.Debug("Sending GET request to check webhooks", zap.String("targetURL", url))
	req, err := http.NewRequest("GET", url, nil) // Use GET for listing webhooks
	if err != nil {
		appLogger.Error("Failed to create GET request for checking webhooks", zap.Error(err))
		return false, fmt.Errorf("failed to create GET request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		appLogger.Error("Error getting webhooks from Helius", urlField, zap.Error(err))
		return false, err
	}
	defer resp.Body.Close()

	statusField := zap.Int("statusCode", resp.StatusCode)

	body, readErr := io.ReadAll(resp.Body) // Read body regardless of status for potential error messages
	if readErr != nil {
		// Log read error but proceed to check status code
		appLogger.Warn("Error reading Helius webhook list response body, status check will proceed", urlField, statusField, zap.Error(readErr))
	}

	if resp.StatusCode != http.StatusOK {
		bodyField := zap.Skip()
		if readErr == nil { // Only log body if read was successful
			bodyField = zap.ByteString("responseBody", body)
		}
		appLogger.Error("Failed to get webhooks from Helius API", urlField, statusField, bodyField)
		return false, fmt.Errorf("failed to get webhooks from Helius: status %d", resp.StatusCode)
	}

	// If status is OK but we couldn't read the body earlier, it's an error
	if readErr != nil {
		appLogger.Error("Helius webhook list GET successful but failed to read body", urlField, statusField, zap.Error(readErr))
		return false, fmt.Errorf("failed reading webhook list body: %w", readErr)
	}

	// Define struct specifically for webhook list response
	type HeliusWebhookListItem struct {
		WebhookID  string `json:"webhookID"`
		WebhookURL string `json:"webhookURL"`
		// Add other fields if needed, like AccountAddresses, TxnStatus, etc.
	}
	var webhooks []HeliusWebhookListItem

	if err := json.Unmarshal(body, &webhooks); err != nil {
		appLogger.Error("Error unmarshalling Helius webhook list response", urlField, zap.Error(err), zap.ByteString("rawBody", body))
		return false, fmt.Errorf("failed to unmarshal webhook list: %w", err)
	}

	found := false
	for _, webhook := range webhooks {
		if webhook.WebhookURL == webhookURL {
			appLogger.Info("Found existing webhook matching URL", urlField, zap.String("webhookID", webhook.WebhookID))
			found = true
			break
		}
	}

	if !found {
		appLogger.Info("No existing Helius webhook found matching URL", urlField)
	}
	return found, nil
}
