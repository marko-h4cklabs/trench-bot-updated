package services

import (
	"bytes"
	"ca-scraper/shared/env" // Use your actual import path
	"ca-scraper/shared/logger"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// Helius API request structure for getAssetsByOwner
type HeliusGetAssetsRequest struct {
	JsonRPC string                 `json:"jsonrpc"`
	ID      string                 `json:"id"`
	Method  string                 `json:"method"`
	Params  map[string]interface{} `json:"params"`
}

// Relevant part of the Helius API response structure
type HeliusAsset struct {
	ID string `json:"id"`
	// Add other fields if needed, e.g., content.metadata.name
}
type HeliusAssetsResponseData struct {
	Total int           `json:"total"`
	Limit int           `json:"limit"`
	Page  int           `json:"page"`
	Items []HeliusAsset `json:"items"`
}
type HeliusGetAssetsResponse struct {
	JsonRPC string                   `json:"jsonrpc"`
	ID      string                   `json:"id"`
	Result  HeliusAssetsResponseData `json:"result"`
}

// Checks if a wallet holds the minimum required NFTs from the configured collection.
func CheckNFTHoldings(walletAddress string, appLogger *logger.Logger) (bool, error) {
	if env.NFTCollectionAddress == "" || env.HeliusAPIKey == "" || env.NFTMinimumHolding <= 0 {
		appLogger.Error("NFT Verification skipped: Configuration missing (Collection Address, Min Holding, or Helius API Key).")
		// Return false, as verification cannot proceed. Maybe return a specific error?
		return false, fmt.Errorf("NFT verification configuration incomplete")
	}

	walletField := zap.String("walletAddress", walletAddress)
	collectionField := zap.String("collection", env.NFTCollectionAddress)
	minField := zap.Int("minimumRequired", env.NFTMinimumHolding)
	appLogger.Debug("Checking NFT holdings via Helius DAS API", walletField, collectionField, minField)

	heliusURL := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", env.HeliusAPIKey)
	httpClient := &http.Client{Timeout: 25 * time.Second}

	// Construct the Helius API request payload
	// IMPORTANT: Filter by collection and limit results for efficiency
	requestPayload := HeliusGetAssetsRequest{
		JsonRPC: "2.0",
		ID:      "cascraper-nft-check", // Identifier for the request
		Method:  "getAssetsByOwner",
		Params: map[string]interface{}{
			"ownerAddress": walletAddress,
			"grouping":     []string{"collection", env.NFTCollectionAddress}, // Filter by collection!
			"page":         1,
			"limit":        env.NFTMinimumHolding, // Fetch only up to the minimum needed
			"displayOptions": map[string]bool{ // Reduce response size
				"showUnverifiedCollections": false,
				"showCollectionMetadata":    false,
				"showFungible":              false,
				"showNativeBalance":         false,
				"showInscription":           false,
			},
		},
	}

	bodyBytes, err := json.Marshal(requestPayload)
	if err != nil {
		appLogger.Error("Failed to marshal Helius request body", zap.Error(err), walletField)
		return false, fmt.Errorf("internal error preparing verification request")
	}

	req, err := http.NewRequestWithContext(context.Background(), "POST", heliusURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		appLogger.Error("Failed to create Helius request", zap.Error(err), walletField)
		return false, fmt.Errorf("internal error creating verification request")
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		appLogger.Error("Failed to call Helius API", zap.Error(err), walletField)
		return false, fmt.Errorf("failed to contact verification service")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Consider reading resp.Body for more detailed errors from Helius
		appLogger.Error("Helius API returned non-OK status", zap.Int("status", resp.StatusCode), walletField)
		return false, fmt.Errorf("verification service returned status %d", resp.StatusCode)
	}

	var apiResp HeliusGetAssetsResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		appLogger.Error("Failed to decode Helius response", zap.Error(err), walletField)
		return false, fmt.Errorf("internal error parsing verification response")
	}

	// Check the number of items returned. Since we limited the request,
	// if len(items) == NFTMinimumHolding, it means they have AT LEAST that many.
	// Alternatively, check apiResp.Result.Total if you didn't limit the request.
	nftCount := len(apiResp.Result.Items)
	// nftCount := apiResp.Result.Total // Use this if limit is > minimum or not set

	hasEnough := nftCount >= env.NFTMinimumHolding

	appLogger.Debug("Helius NFT check result",
		walletField,
		collectionField,
		minField,
		zap.Int("foundCount", nftCount),
		zap.Bool("hasEnough", hasEnough),
	)

	return hasEnough, nil
}
