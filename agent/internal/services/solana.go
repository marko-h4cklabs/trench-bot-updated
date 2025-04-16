package services

import (
	"bytes"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"

	"go.uber.org/zap"
)

func FetchWithRetry(url string, payload []byte, maxRetries int, appLogger *logger.Logger) (*http.Response, error) {
	var resp *http.Response
	var err error
	client := &http.Client{Timeout: 10 * time.Second}
	urlField := zap.String("url", url)

	for i := 0; i < maxRetries; i++ {
		attemptField := zap.Int("attempt", i+1)
		req, reqErr := http.NewRequest("POST", url, bytes.NewBuffer(payload))
		if reqErr != nil {
			appLogger.Error("HTTP request creation failed during fetch", urlField, zap.Error(reqErr))
			return nil, fmt.Errorf("failed http request creation: %w", reqErr)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)

		if err == nil {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return resp, nil
			}
			statusField := zap.Int("statusCode", resp.StatusCode)
			appLogger.Warn("Request failed with non-2xx status", urlField, statusField, attemptField, zap.Int("maxRetries", maxRetries))
			resp.Body.Close()
			err = fmt.Errorf("request failed with status: %s", resp.Status)

		} else {
			appLogger.Warn("HTTP request failed", urlField, attemptField, zap.Int("maxRetries", maxRetries), zap.Error(err))

		}

		if i < maxRetries-1 {
			backoff := time.Duration(math.Pow(2, float64(i))) * time.Second
			appLogger.Info("Retrying request", urlField, attemptField, zap.Duration("backoff", backoff))
			time.Sleep(backoff)
		}
	}
	return nil, fmt.Errorf("Helius RPC request failed after %d retries for %s: %w", maxRetries, url, err)
}

func HeliusRPCRequest(method string, params interface{}, appLogger *logger.Logger) (map[string]interface{}, error) {
	methodField := zap.String("method", method)
	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		appLogger.Error("HELIUS_API_KEY missing, cannot make RPC request", methodField)
		return nil, fmt.Errorf("missing HELIUS_API_KEY from env configuration")
	}
	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", apiKey)
	urlField := zap.String("url", url)

	payload := map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		appLogger.Error("Failed to marshal Helius RPC payload", methodField, zap.Error(err))
		return nil, fmt.Errorf("failed to marshal Helius RPC payload: %w", err)
	}

	resp, err := FetchWithRetry(url, payloadBytes, 3, appLogger)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	bodyBytes, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		appLogger.Error("Failed to read Helius response body", methodField, urlField, zap.Int("status", resp.StatusCode), zap.Error(readErr))
		return nil, fmt.Errorf("failed to read Helius response body: %w", readErr)
	}

	if decodeErr := json.Unmarshal(bodyBytes, &result); decodeErr != nil {
		appLogger.Error("Failed to parse Helius response JSON", methodField, urlField, zap.Int("status", resp.StatusCode), zap.Error(decodeErr), zap.ByteString("rawBody", bodyBytes))
		return nil, fmt.Errorf("failed to parse Helius response JSON: %w", decodeErr)
	}

	if errorData, exists := result["error"]; exists {
		errMap, ok := errorData.(map[string]interface{})
		var apiErr error
		if ok {
			apiErr = fmt.Errorf("Helius API Error: Code=%v, Message=%v", errMap["code"], errMap["message"])
		} else {
			apiErr = fmt.Errorf("Helius API Error: %v", errorData)
		}
		appLogger.Warn("Helius API returned an error", methodField, urlField, zap.Any("errorDetail", errorData))
		return nil, apiErr
	}

	if _, hasResult := result["result"]; !hasResult {
		appLogger.Debug("Helius response missing 'result' field", methodField, urlField, zap.Any("fullResponse", result))
	}

	return result, nil
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

	url := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	client := &http.Client{Timeout: 15 * time.Second}

	appLogger.Debug("Sending GET request to check webhooks", zap.String("targetURL", url))
	resp, err := client.Get(url)
	if err != nil {
		appLogger.Error("Error getting webhooks from Helius", urlField, zap.Error(err))
		return false, err
	}
	defer resp.Body.Close()

	statusField := zap.Int("statusCode", resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		bodyField := zap.Skip()
		if readErr == nil {
			bodyField = zap.ByteString("responseBody", body)
		}
		appLogger.Error("Failed to get webhooks from Helius API", urlField, statusField, bodyField, zap.NamedError("readError", readErr))
		return false, fmt.Errorf("failed to get webhooks from Helius: status %d", resp.StatusCode)
	}

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		appLogger.Error("Error reading Helius webhook list response body", urlField, statusField, zap.Error(readErr))
		return false, readErr
	}

	type HeliusWebhook struct {
		WebhookID  string `json:"webhookID"`
		WebhookURL string `json:"webhookURL"`
	}
	var webhooks []HeliusWebhook

	if err := json.Unmarshal(body, &webhooks); err != nil {
		appLogger.Error("Error unmarshalling Helius webhook list response", urlField, zap.Error(err), zap.ByteString("rawBody", body))
		return false, err
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
