package services

import (
	"bytes"
	"ca-scraper/agent/internal/events"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	// Removed zapcore import as LogToScanner is less distinct now
)

type RaydiumTransaction struct {
	PairID      string  `json:"pairId"`
	TokenSymbol string  `json:"tokenSymbol"`
	AmountSOL   float64 `json:"amountSOL"`
}

var seenTransactions = struct {
	sync.Mutex
	TxIDs map[string]struct{}
}{TxIDs: make(map[string]struct{})}

type SwapCacheEntry struct {
	Volumes     []float64
	LastUpdated time.Time
}

var swapCache = struct {
	sync.RWMutex
	Data map[string]SwapCacheEntry
}{Data: make(map[string]SwapCacheEntry)}

// TrackGraduatedToken remains conceptually, but implementation might change
func TrackGraduatedToken(tokenAddress string, appLogger *logger.Logger) {
	appLogger.Info("Scheduling monitoring for newly graduated token", zap.String("tokenAddress", tokenAddress))
	appLogger.Debug("Tracking initiated for token", zap.String("tokenAddress", tokenAddress))
}

const (
	validationVolumeThreshold = 500.0
	validationCheckInterval   = 2 * time.Minute
	swapCacheMaxRetention     = 30 * time.Minute
	swapCacheCleanupInterval  = 5 * time.Minute
)

// HandleTransactionWebhookWithPayload remains the same internally
func HandleTransactionWebhookWithPayload(transactions []map[string]interface{}, appLogger *logger.Logger) {
	processedCount := 0
	skippedAlreadySeen := 0
	skippedCriteria := 0
	skippedMissingData := 0
	batchSeen := make(map[string]struct{})

	for _, tx := range transactions {
		if tx == nil {
			continue
		}
		txSignature, _ := tx["signature"].(string)
		sigField := zap.String("signature", txSignature)
		if txSignature == "" {
			appLogger.Warn("Transaction missing signature, skipping processing.")
			skippedMissingData++
			continue
		}

		if _, exists := batchSeen[txSignature]; exists {
			skippedAlreadySeen++
			continue
		}

		seenTransactions.Lock()
		_, exists := seenTransactions.TxIDs[txSignature]
		if exists {
			seenTransactions.Unlock()
			skippedAlreadySeen++
			continue
		}

		if !processSwapTransaction(tx, appLogger) {
			seenTransactions.Unlock()
			skippedCriteria++
			continue
		}

		batchSeen[txSignature] = struct{}{}
		seenTransactions.TxIDs[txSignature] = struct{}{}
		seenTransactions.Unlock()

		appLogger.Debug("Transaction successfully processed and cached.", sigField)
		processedCount++
	}
	appLogger.Info("Webhook payload batch processing complete.",
		zap.Int("processed", processedCount),
		zap.Int("skippedSeen", skippedAlreadySeen),
		zap.Int("skippedCriteria", skippedCriteria),
		zap.Int("skippedMissingData", skippedMissingData))
}

// processSwapTransaction changes logging call
func processSwapTransaction(tx map[string]interface{}, appLogger *logger.Logger) bool {
	txSignature, _ := tx["signature"].(string)
	sigField := zap.String("signature", txSignature)
	tokenMint, foundMint := events.ExtractNonSolMintFromEvent(tx)
	mintField := zap.String("tokenMint", tokenMint)

	if !foundMint {
		appLogger.Warn("Transaction missing relevant non-SOL token mint, cannot cache.", sigField)
		return false
	}

	usdValue, hasValue := tx["usdValue"].(float64)
	if !hasValue {
		appLogger.Debug("Transaction missing USD value, caching with 0 value.", sigField, mintField)
		usdValue = 0
	}
	usdField := zap.Float64("usdValue", usdValue)

	swapCache.Lock()
	entry, exists := swapCache.Data[tokenMint]
	if !exists {
		entry = SwapCacheEntry{
			Volumes: make([]float64, 0, 5),
		}
	}
	entry.Volumes = append(entry.Volumes, usdValue)
	entry.LastUpdated = time.Now()
	swapCache.Data[tokenMint] = entry
	currentTotalVolume := sum(entry.Volumes)
	swapCache.Unlock()

	// Changed from LogToScanner to Debug
	appLogger.Debug("Cached swap for token",
		mintField,
		usdField,
		zap.Float64("newTotalVolume", currentTotalVolume),
		sigField)

	return true
}

// HandleTransactionWebhook changes logging calls and notification call
func HandleTransactionWebhook(payload []byte, appLogger *logger.Logger) {
	requestID := zap.String("requestID", generateRequestID())
	appLogger.Info("Handling Transaction Webhook Request", requestID)

	var transactions []map[string]interface{}
	if err := json.Unmarshal(payload, &transactions); err != nil {
		var singleTransaction map[string]interface{}
		bodyReader := bytes.NewReader(payload)
		if decodeErr := json.NewDecoder(bodyReader).Decode(&singleTransaction); decodeErr != nil {
			appLogger.Error("Invalid swap webhook JSON format (neither array nor single object)", zap.Error(decodeErr), zap.ByteString("payload", payload), requestID)
			return
		}
		transactions = []map[string]interface{}{singleTransaction}
	}

	appLogger.Info("Processing transactions from swap webhook for immediate validation", zap.Int("count", len(transactions)), requestID)

	validatedCount := 0
	for _, tx := range transactions {
		if tx == nil {
			appLogger.Warn("Skipping nil transaction in swap webhook payload", requestID)
			continue
		}

		txSignature, _ := tx["signature"].(string)
		if txSignature == "" {
			appLogger.Warn("Swap webhook transaction missing signature, skipping...", requestID)
			continue
		}
		sigField := zap.String("signature", txSignature)

		seenTransactions.Lock()
		_, exists := seenTransactions.TxIDs[txSignature]
		if exists {
			seenTransactions.Unlock()
			appLogger.Debug("Transaction already seen, skipping immediate validation.", sigField, requestID)
			continue
		}
		seenTransactions.TxIDs[txSignature] = struct{}{}
		seenTransactions.Unlock()

		tokenMint, foundMint := events.ExtractNonSolMintFromEvent(tx)
		mintField := zap.String("tokenMint", tokenMint)

		if !foundMint {
			appLogger.Debug("Could not extract relevant non-SOL token mint from swap webhook transaction, skipping validation.", sigField, requestID)
			continue
		}

		// Changed from LogToScanner to Info
		appLogger.Info("Performing immediate DexScreener check (from swap webhook)", mintField, sigField, requestID)

		validationResult, err := IsTokenValid(tokenMint, appLogger)

		if err != nil {
			// Changed from LogToScanner to Warn
			appLogger.Warn("Error checking token with DexScreener (swap webhook)", mintField, sigField, zap.Error(err), requestID)
			continue
		}

		if validationResult == nil || !validationResult.IsValid {
			reason := "Did not meet criteria or validation failed (nil result)"
			if validationResult != nil && len(validationResult.FailReasons) > 0 {
				reason = strings.Join(validationResult.FailReasons, "; ")
			}
			// Changed from LogToScanner to Info
			appLogger.Info("Token does not meet immediate criteria (swap webhook).", mintField, sigField, zap.String("reason", reason), requestID)
			continue
		}

		// Changed from LogToScanner to Info
		appLogger.Info("Valid Swap Detected via Webhook (Immediate Check)", mintField, sigField, requestID)
		validatedCount++

		dexscreenerLink := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenMint)
		// NOTE: Escaping inside Sprintf can be fragile. Ensure the final string is escaped by the notification function.
		// Let's prepare the raw message and let SendTelegramMessage handle escaping.
		rawMessage := fmt.Sprintf(
			"🔥 Hot Swap Validated! 🔥\n\nToken: `%s`\nDexScreener: %s\nTx: `%s`",
			tokenMint,
			dexscreenerLink, // Send raw link
			txSignature,     // Send raw signature
		)
		// Changed from SendScannerLogMessage to SendTelegramMessage
		notifications.SendTelegramMessage(rawMessage)
	}

	appLogger.Info("Swap webhook processing finished.",
		zap.Int("processed", len(transactions)),
		zap.Int("validatedNow", validatedCount),
		requestID)

}

// CreateHeliusWebhook remains the same
func CreateHeliusWebhook(webhookURL string, appLogger *logger.Logger) bool {
	appLogger.Info("Setting up/Verifying Raydium Swap Webhook", zap.String("url", webhookURL))

	apiKey := env.HeliusAPIKey
	webhookSecret := env.WebhookSecret
	authHeader := env.HeliusAuthHeader
	addressesRaw := env.RaydiumAccountAddresses

	if apiKey == "" {
		appLogger.Fatal("HELIUS_API_KEY is missing! Cannot create webhook.")
		return false
	}
	if webhookSecret == "" {
		// Changed from Fatal to Error or Warn, as secret might not always be mandatory depending on Helius config
		appLogger.Error("WEBHOOK_SECRET is missing! Webhook creation might fail if secret is required by Helius.")
		// return false // Decide if this should prevent creation
	}
	if webhookURL == "" {
		appLogger.Error("CreateHeliusWebhook called with empty webhookURL.")
		return false
	}
	if authHeader == "" {
		appLogger.Warn("HELIUS_AUTH_HEADER is empty! Webhook endpoint might be insecure if auth header is expected.")
	}

	var accountList []string
	if addressesRaw != "" {
		for _, addr := range strings.Split(addressesRaw, ",") {
			trimmedAddr := strings.TrimSpace(addr)
			if trimmedAddr != "" {
				accountList = append(accountList, trimmedAddr)
			}
		}
		appLogger.Info("Using Raydium addresses from env", zap.Strings("addresses", accountList))
	} else {
		appLogger.Warn("RAYDIUM_ACCOUNT_ADDRESSES is empty. Swap webhook might not receive relevant transactions.")
	}

	if len(accountList) == 0 {
		appLogger.Error("No addresses specified for Raydium webhook. Aborting creation.")
		return false
	}

	appLogger.Info("Final address list for Raydium webhook", zap.Strings("addresses", accountList))
	appLogger.Info("Expecting Raydium webhook Authorization header if configured", zap.Bool("authHeaderSet", authHeader != ""))

	found, err := CheckExistingHeliusWebhook(webhookURL, appLogger)
	if err != nil {
		appLogger.Error("Failed check for existing Raydium webhook, attempting creation regardless.", zap.Error(err))
	}
	if found {
		appLogger.Info("Raydium webhook already exists.", zap.String("url", webhookURL))
		appLogger.Warn("Existing Raydium webhook found. Ensure monitored addresses and auth header are correct.")
		return true
	}

	payload := map[string]interface{}{
		"webhookURL":       webhookURL,
		"transactionTypes": []string{"SWAP"},
		"accountAddresses": accountList,
		"webhookType":      "enhanced",
		"txnStatus":        "success",
	}
	// Only include authHeader if it's set
	if authHeader != "" {
		payload["authHeader"] = authHeader
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		appLogger.Error("Failed to marshal Helius webhook payload", zap.Error(err))
		return false
	}

	heliusURL := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, err := http.NewRequest("POST", heliusURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		appLogger.Error("Failed to create Helius webhook request", zap.Error(err))
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	// Authorization for Helius API itself might use the API key directly or a different secret
	// The 'webhookSecret' might be intended for securing *your* endpoint receiving the webhook.
	// Helius docs clarification needed here. Assuming API key in URL is sufficient for now.
	// req.Header.Set("Authorization", "Bearer "+webhookSecret) // Re-evaluate if needed for Helius API auth

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		appLogger.Error("Failed to send request to Helius API", zap.Error(err))
		return false
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	responseBodyStr := ""
	if readErr == nil {
		responseBodyStr = string(body)
	} else {
		appLogger.Warn("Failed to read Helius webhook creation response body", zap.Error(readErr))
	}

	statusField := zap.Int("statusCode", resp.StatusCode)
	bodyField := zap.String("responseBody", responseBodyStr)
	appLogger.Info("Helius Webhook API Response", zap.String("status", resp.Status), statusField, bodyField)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		appLogger.Info("Helius Raydium webhook created or updated successfully.")
		return true
	} else {
		appLogger.Error("Failed to create/update Helius Raydium webhook.", statusField, bodyField)
		return false
	}
}

// TestWebhookWithAuth remains the same
func TestWebhookWithAuth(appLogger *logger.Logger) {
	webhookURL := env.WebhookURL
	authHeader := env.HeliusAuthHeader

	if webhookURL == "" {
		appLogger.Fatal("Webhook URL env var missing for testing!")
	}

	reqBody := []map[string]interface{}{
		{
			"description": "Test swap " + time.Now().Format(time.RFC3339),
			"type":        "SWAP",
			"source":      "RAYDIUM",
			"signature":   fmt.Sprintf("test-sig-%d", time.Now().UnixNano()),
			"timestamp":   time.Now().Unix(),
			"tokenTransfers": []interface{}{
				map[string]interface{}{"mint": "TESTMINTADDRESSPLACEHOLDERxxxxxxxxxxxxxxx"},
			},
			"events": map[string]interface{}{"swap": map[string]interface{}{}},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		appLogger.Fatal("TestWebhook failed to marshal test body", zap.Error(err))
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		appLogger.Fatal("TestWebhook failed to create request", zap.Error(err))
	}
	req.Header.Set("Content-Type", "application/json")

	urlField := zap.String("url", webhookURL)
	authField := zap.String("authHeader", "missing")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
		authField = zap.String("authHeader", "present")
	} else {
		appLogger.Warn("Sending Test Webhook without Authorization header")
	}
	appLogger.Info("Sending Test Webhook...", urlField, authField)

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		appLogger.Error("TestWebhook failed to send request", zap.Error(err), urlField)
		return
	}
	defer resp.Body.Close()

	respBody, readErr := io.ReadAll(resp.Body)
	statusField := zap.String("status", resp.Status)
	if readErr != nil {
		appLogger.Warn("TestWebhook failed to read response body", zap.Error(readErr), statusField)
	}

	appLogger.Info("Test Webhook Response", statusField, zap.ByteString("body", respBody))

	if resp.StatusCode != http.StatusOK {
		appLogger.Warn("Test Webhook received non-OK status", statusField)
	} else {
		appLogger.Info("Test Webhook received OK status.")
	}
}

// sum remains the same
func sum(volumes []float64) float64 {
	var total float64
	for _, v := range volumes {
		total += v
	}
	return total
}

// ValidateAndNotifyCachedSwaps changes logging calls and notification call
func ValidateAndNotifyCachedSwaps(appLogger *logger.Logger) {
	appLogger.Info("Swap validation & notification loop started",
		zap.Duration("interval", validationCheckInterval),
		zap.Float64("volumeThreshold", validationVolumeThreshold))

	ticker := time.NewTicker(validationCheckInterval)
	defer ticker.Stop()

	for range ticker.C {
		appLogger.Debug("Running validation check cycle on cached swaps...")

		swapCache.RLock()
		tokensToValidate := make(map[string]float64)
		cacheSize := len(swapCache.Data)
		for token, entry := range swapCache.Data {
			if entry.Volumes == nil {
				appLogger.Warn("Found cache entry with nil Volumes", zap.String("token", token))
				continue
			}
			totalVolume := sum(entry.Volumes)
			if totalVolume >= validationVolumeThreshold {
				tokensToValidate[token] = totalVolume
			}
		}
		swapCache.RUnlock()

		count := len(tokensToValidate)
		appLogger.Info("Swap validation check", zap.Int("cachedTokens", cacheSize), zap.Int("tokensMeetingThreshold", count))

		if count == 0 {
			continue
		}

		validatedCount := 0
		failedOrRateLimitedCount := 0
		processedCount := 0

		for token, totalVolume := range tokensToValidate {
			processedCount++
			tokenField := zap.String("tokenAddress", token)
			volumeField := zap.Float64("totalVolume", totalVolume)
			appLogger.Debug("Checking cached token via validation loop.", tokenField, volumeField)

			validationResult, err := IsTokenValid(token, appLogger)

			if err != nil {
				// Changed from LogToScanner to Warn
				appLogger.Warn("Error/RateLimit checking token during validation loop", tokenField, volumeField, zap.Error(err))
				if !errors.Is(err, ErrRateLimited) { // Assuming ErrRateLimited exists from dexscreener.go
					swapCache.Lock()
					delete(swapCache.Data, token)
					swapCache.Unlock()
					// Changed from LogToScanner to Info
					appLogger.Info("Removed token from cache due to non-rate-limit validation error.", tokenField)
				}
				failedOrRateLimitedCount++
				continue
			}

			if validationResult != nil && validationResult.IsValid {
				validatedCount++
				// Changed from LogToScanner to Info
				appLogger.Info("Token PASSED validation via volume check loop.", tokenField, volumeField)

				dexscreenerLink := fmt.Sprintf("https://dexscreener.com/solana/%s", token)
				// Prepare raw message for SendTelegramMessage
				rawMessage := fmt.Sprintf(
					"✅ Validated Swap Token (Volume Check)\n\n"+
						"CA: `%s`\n"+
						"Volume Trigger: `$%.2f`\n\n"+
						"DexScreener: %s\n\n"+
						"*(Removed from volume tracking cache)*",
					token,
					totalVolume,
					dexscreenerLink, // Send raw link
				)
				// Changed from SendScannerLogMessage to SendTelegramMessage
				notifications.SendTelegramMessage(rawMessage)

				swapCache.Lock()
				delete(swapCache.Data, token)
				swapCache.Unlock()
				// Changed from LogToScanner to Info
				appLogger.Info("Removed validated token from swap cache.", tokenField)

			} else {
				failedOrRateLimitedCount++
				reason := "Did not meet criteria or validation failed (nil result)"
				if validationResult != nil && len(validationResult.FailReasons) > 0 {
					reason = strings.Join(validationResult.FailReasons, "; ")
				} else if validationResult != nil && !validationResult.IsValid {
					reason = "Did not meet criteria (no specific reasons returned)" // Adjusted message slightly
				}
				// Changed from LogToScanner to Info
				appLogger.Info("Token FAILED validation via volume check loop.", tokenField, volumeField, zap.String("reason", reason))

				swapCache.Lock()
				delete(swapCache.Data, token)
				swapCache.Unlock()
				// Changed from LogToScanner to Info
				appLogger.Info("Removed failed/invalid token from swap cache.", tokenField)
			}
			// Add a small delay between checks if needed
			time.Sleep(50 * time.Millisecond)
		}
		appLogger.Info("Swap validation check cycle complete.",
			zap.Int("processed", processedCount),
			zap.Int("validated", validatedCount),
			zap.Int("failedOrRateLimited", failedOrRateLimitedCount))
	}
}

// CleanSwapCachePeriodically remains the same
func CleanSwapCachePeriodically(appLogger *logger.Logger) {
	appLogger.Info("Swap cache cleanup routine started",
		zap.Duration("interval", swapCacheCleanupInterval),
		zap.Duration("retention", swapCacheMaxRetention))

	ticker := time.NewTicker(swapCacheCleanupInterval)
	defer ticker.Stop()

	for currentTime := range ticker.C {
		appLogger.Debug("Running periodic swap cache cleanup...")
		tokensToDelete := []string{}
		cutoffTime := currentTime.Add(-swapCacheMaxRetention)

		swapCache.RLock()
		cacheSizeBefore := len(swapCache.Data)
		for token, entry := range swapCache.Data {
			if entry.LastUpdated.Before(cutoffTime) {
				tokensToDelete = append(tokensToDelete, token)
			}
		}
		swapCache.RUnlock()

		deletedCount := 0
		if len(tokensToDelete) > 0 {
			swapCache.Lock()
			for _, token := range tokensToDelete {
				// Double check condition before deleting under lock
				if entry, exists := swapCache.Data[token]; exists && entry.LastUpdated.Before(cutoffTime) {
					delete(swapCache.Data, token)
					deletedCount++
				}
			}
			swapCache.Unlock()
		}

		if deletedCount > 0 {
			appLogger.Info("Periodic swap cache cleanup finished.",
				zap.Int("removed", deletedCount),
				zap.Duration("retention", swapCacheMaxRetention),
				zap.Int("sizeBefore", cacheSizeBefore),
				zap.Int("sizeAfter", cacheSizeBefore-deletedCount))
		} else {
			appLogger.Debug("Periodic swap cache cleanup finished. No expired entries found.", zap.Int("currentSize", cacheSizeBefore))
		}
	}
}

// generateRequestID remains the same
func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// Assume CheckExistingHeliusWebhook function exists elsewhere
// func CheckExistingHeliusWebhook(webhookURL string, appLogger *logger.Logger) (bool, error) { ... }
// Assume IsTokenValid function exists elsewhere (dexscreener.go)
// func IsTokenValid(tokenCA string, appLogger *logger.Logger) (*ValidationResult, error) { ... }
// Assume events.ExtractNonSolMintFromEvent exists elsewhere
// func ExtractNonSolMintFromEvent(event map[string]interface{}) (string, bool) { ... }
// Assume ErrRateLimited exists elsewhere (dexscreener.go)
// var ErrRateLimited = errors.New(...)
