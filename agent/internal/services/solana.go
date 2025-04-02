package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"
)

func FetchWithRetry(url string, payload []byte, maxRetries int) (*http.Response, error) {
	var resp *http.Response
	var err error

	client := &http.Client{Timeout: 5 * time.Second}
	for i := 0; i < maxRetries; i++ {
		req, _ := http.NewRequest("POST", url, bytes.NewBuffer(payload))
		req.Header.Set("Content-Type", "application/json")

		resp, err = client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		time.Sleep(time.Duration(2<<i) * time.Second) // Exponential backoff
	}

	return nil, fmt.Errorf("❌ Helius RPC request failed after %d retries: %v", maxRetries, err)
}

// HeliusRPCRequest sends a generic request to Helius RPC
func HeliusRPCRequest(apiKey, method string, params interface{}) (map[string]interface{}, error) {
	if apiKey == "" {
		return nil, fmt.Errorf("❌ Missing HELIUS_API_KEY")
	}

	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", apiKey)
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	}

	payloadBytes, _ := json.Marshal(payload)
	resp, err := FetchWithRetry(url, payloadBytes, 3)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("❌ Failed to parse response: %v", err)
	}

	// ✅ Additional error handling to check if Helius API returned an error
	if errorData, exists := result["error"]; exists {
		return nil, fmt.Errorf("❌ Helius API Error: %v", errorData)
	}

	return result, nil
}

// InitializeSolana ensures API key is loaded
func InitializeSolana() {
	apiKey := os.Getenv("HELIUS_API_KEY")
	if apiKey == "" {
		panic("❌ Helius API key is missing. Set HELIUS_API_KEY in environment.")
	}
	fmt.Println("✅ Solana API initialized successfully.")
}

// CheckTokenOwnership checks if a wallet owns an NFT from a specific collection
func CheckTokenOwnership(apiKey, walletAddress, collectionAddress string) (bool, error) {
	if apiKey == "" {
		return false, fmt.Errorf("❌ API Key is required")
	}

	response, err := HeliusRPCRequest(apiKey, "getAssetsByOwner", map[string]interface{}{
		"ownerAddress": walletAddress,
	})
	if err != nil {
		return false, err
	}

	// Validate the response structure
	result, ok := response["result"].(map[string]interface{})
	if !ok {
		return false, fmt.Errorf("❌ Invalid API response: missing 'result' field")
	}

	items, ok := result["items"].([]interface{})
	if !ok || len(items) == 0 {
		return false, nil
	}

	// Loop through NFTs to check for matching collection
	for _, item := range items {
		asset, _ := item.(map[string]interface{})
		grouping, _ := asset["grouping"].([]interface{})
		for _, group := range grouping {
			groupData, _ := group.(map[string]interface{})
			if groupData["group_key"] == "collection" && groupData["group_value"] == collectionAddress {
				return true, nil
			}
		}
	}

	return false, nil
}
