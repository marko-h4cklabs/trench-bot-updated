package services

import (
	"bytes"
	"ca-scraper/shared/env"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"time"
)

func FetchWithRetry(url string, payload []byte, maxRetries int) (*http.Response, error) {
	var resp *http.Response
	var err error
	client := &http.Client{Timeout: 10 * time.Second}
	for i := 0; i < maxRetries; i++ {
		req, reqErr := http.NewRequest("POST", url, bytes.NewBuffer(payload))
		if reqErr != nil {
			return nil, fmt.Errorf("failed http request creation: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err = client.Do(req)
		if err == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		}
		if err != nil {
			log.Printf("Retry %d/%d: Request failed for %s: %v", i+1, maxRetries, url, err)
		} else {
			log.Printf("Retry %d/%d: Request failed for %s with status: %s", i+1, maxRetries, url, resp.Status)
			resp.Body.Close()
		}
		backoff := time.Duration(math.Pow(2, float64(i))) * time.Second
		time.Sleep(backoff)
	}
	if err != nil {
		return nil, fmt.Errorf("Helius RPC request failed after %d retries: %w", maxRetries, err)
	} else if resp != nil {
		return nil, fmt.Errorf("Helius RPC request failed after %d retries with status: %s", maxRetries, resp.Status)
	} else {
		return nil, fmt.Errorf("Helius RPC request failed after %d retries for unknown reasons", maxRetries)
	}
}

func HeliusRPCRequest(method string, params interface{}) (map[string]interface{}, error) {
	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		return nil, fmt.Errorf(" Missing HELIUS_API_KEY from env configuration")
	}
	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", apiKey)
	payload := map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf(" Failed to marshal Helius RPC payload: %w", err)
	}
	resp, err := FetchWithRetry(url, payloadBytes, 3)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var result map[string]interface{}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&result); decodeErr != nil {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("Raw Helius Response Body on Decode Error: %s", string(bodyBytes))
		return nil, fmt.Errorf(" Failed to parse Helius response JSON: %w", decodeErr)
	}
	if errorData, exists := result["error"]; exists {
		if errorMap, ok := errorData.(map[string]interface{}); ok {
			return nil, fmt.Errorf(" Helius API Error: Code=%v, Message=%v", errorMap["code"], errorMap["message"])
		}
		return nil, fmt.Errorf(" Helius API Error: %v", errorData)
	}
	if _, hasResult := result["result"]; !hasResult {
		log.Printf("Warning: Helius response missing 'result' field. Full Response: %v", result)
	}
	return result, nil
}

func InitializeSolana() {
	log.Println(" Solana Service Configuration Initialized (API Key loaded via env package).")
}

func CheckExistingHeliusWebhook(webhookURL string) (bool, error) {
	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		return false, fmt.Errorf("HELIUS_API_KEY missing from env package, cannot check webhooks")
	}

	url := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Error getting webhooks from Helius: %v", err)
		return false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		log.Printf("Failed to get webhooks from Helius. Status: %d, Body: %s", resp.StatusCode, string(body))

		return false, fmt.Errorf("failed to get webhooks from Helius: status %d, response body: %s", resp.StatusCode, string(body))

	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		log.Printf("Error reading Helius webhook list response body: %v", readErr)
		return false, readErr
	}

	type HeliusWebhook struct {
		WebhookID  string `json:"webhookID"`
		WebhookURL string `json:"webhookURL"`
	}
	var webhooks []HeliusWebhook

	if err := json.Unmarshal(body, &webhooks); err != nil {
		log.Printf("Error unmarshalling Helius webhook list response: %v", err)
		log.Printf("Response Body: %s", string(body))
		return false, err
	}

	found := false
	for _, webhook := range webhooks {
		if webhook.WebhookURL == webhookURL {
			log.Printf("Found existing webhook matching URL: %s (ID: %s)", webhookURL, webhook.WebhookID)
			found = true
			break
		}
	}

	if !found {
		log.Printf("No existing Helius webhook found matching URL: %s", webhookURL)
	}
	return found, nil
}
