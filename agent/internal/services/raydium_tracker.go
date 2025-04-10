package services

import (
	"bytes"
	"ca-scraper/shared/env"
	"ca-scraper/shared/notifications"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
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

func TrackGraduatedToken(tokenAddress string) {
	log.Printf("Monitoring swaps for newly graduated token: %s", tokenAddress)
	go func() {
		time.Sleep(1 * time.Minute)
		log.Printf(" Started tracking token: %s", tokenAddress)
	}()
}

func StartDexScreenerValidation() {
	log.Println("DexScreener Validation Loop Started (using centralized check!)")
	for {
		time.Sleep(3 * time.Minute)
		validateCachedTokens()
	}
}

func validateCachedTokens() {
	swapCache.RLock()
	cacheLen := len(swapCache.Data)
	tokensToCheck := make(map[string][]float64, cacheLen)
	for token, volumes := range swapCache.Data {
		tokensToCheck[token] = volumes
	}
	swapCache.RUnlock()

	log.Printf("Running validation check on %d cached tokens...", cacheLen)
	validatedCount := 0
	failedCount := 0

	for token, volumes := range tokensToCheck {
		totalVolume := sum(volumes)
		if totalVolume >= 500 {
			log.Printf("Checking DexScreener eligibility for token %s (Volume: %.2f)", token, totalVolume)

			validationResult, err := IsTokenValid(token)
			if err != nil {
				log.Printf(" DexScreener check failed for %s: %v", token, err)
				failedCount++
				continue
			}

			if validationResult != nil && validationResult.IsValid {
				validatedCount++
				log.Printf("Token %s meets DexScreener criteria via periodic check!", token)

			} else {
				failedCount++

				reason := "Did not meet criteria or validation failed"
				if validationResult != nil && len(validationResult.FailReasons) > 0 {
					reason = strings.Join(validationResult.FailReasons, "; ")
				}
				log.Printf("Token %s failed DexScreener validation via periodic check. Reason: %s", token, reason)
			}
		}
	}
	log.Printf("Finished validation check. Validated: %d, Failed/Not Valid: %d", validatedCount, failedCount)
}

func HandleTransactionWebhookWithPayload(transactions []map[string]interface{}) {
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
		if txSignature == "" {
			log.Println(" Transaction missing signature, skipping...")
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
		if !processSwapTransaction(tx) {
			seenTransactions.Unlock()
			skippedCriteria++
			continue
		}

		batchSeen[txSignature] = struct{}{}
		seenTransactions.TxIDs[txSignature] = struct{}{}
		seenTransactions.Unlock()

		log.Printf("Transaction %s successfully processed and cached.", txSignature)
		processedCount++
	}
	log.Printf("Webhook payload processing complete. Processed: %d, Skipped (Seen): %d, Skipped (Criteria): %d, Skipped (Missing Data): %d",
		processedCount, skippedAlreadySeen, skippedCriteria, skippedMissingData)
}

func processSwapTransaction(tx map[string]interface{}) bool {
	txSignature, _ := tx["signature"].(string)
	tokenMint, hasMint := tx["tokenMint"].(string)
	usdValue, hasValue := tx["usdValue"].(float64)

	if !hasMint || tokenMint == "" {
		log.Printf("Transaction %s missing token mint, cannot cache.", txSignature)
		return false
	}
	if !hasValue {
		log.Printf("Transaction %s missing USD value, caching with 0 value.", txSignature)
		usdValue = 0
	}

	swapCache.Lock()
	swapCache.Data[tokenMint] = append(swapCache.Data[tokenMint], usdValue)
	swapCache.Unlock()
	log.Printf("Cached swap for token: %s with value $%.2f (Tx: %s)", tokenMint, usdValue, txSignature)

	return true
}

func HandleTransactionWebhook(c *gin.Context) {
	if env.HeliusAuthHeader != "" {
		if c.GetHeader("Authorization") != env.HeliusAuthHeader {
			log.Printf("Unauthorized webhook call received. Expected Header: %s, Received: %s", env.HeliusAuthHeader, c.GetHeader("Authorization"))
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
	} else {
		log.Println("Warning: No HELIUS_AUTH_HEADER set, accepting webhook calls without Authorization check.")
	}

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		log.Printf("Error reading webhook body: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

	var transactions []map[string]interface{}
	if err := json.Unmarshal(body, &transactions); err != nil {
		var singleTransaction map[string]interface{}
		bodyReader := bytes.NewReader(body)
		if decodeErr := json.NewDecoder(bodyReader).Decode(&singleTransaction); decodeErr != nil {
			log.Printf(" Invalid webhook JSON format (neither array nor single object): %v", decodeErr)
			log.Printf("Received Body: %s", string(body))
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format"})
			return
		}
		transactions = []map[string]interface{}{singleTransaction}
	}

	log.Printf("Processing %d transaction(s) from webhook for immediate validation...", len(transactions))

	validatedCount := 0
	for _, tx := range transactions {
		if tx == nil {
			log.Println("Skipping nil transaction in webhook payload.")
			continue
		}

		txSignature, _ := tx["signature"].(string)
		if txSignature == "" {
			log.Println("Webhook transaction missing signature, skipping...")
			continue
		}

		seenTransactions.Lock()
		_, exists := seenTransactions.TxIDs[txSignature]
		if exists {
			seenTransactions.Unlock()
			log.Printf("Transaction %s already seen, skipping.", txSignature)
			continue
		}
		seenTransactions.TxIDs[txSignature] = struct{}{}
		seenTransactions.Unlock()

		var tokenMint string
		var foundMint bool
		if transfers, hasTransfers := tx["tokenTransfers"].([]interface{}); hasTransfers {
			for _, transfer := range transfers {
				if transferMap, ok := transfer.(map[string]interface{}); ok {
					mint, mintOk := transferMap["mint"].(string)
					if mintOk && mint != "" && mint != "So11111111111111111111111111111111111111112" {
						tokenMint = mint
						foundMint = true
						break
					}
				}
			}
		}
		if !foundMint {
			if events, hasEvents := tx["events"].(map[string]interface{}); hasEvents {
				if swapEvent, hasSwap := events["swap"].(map[string]interface{}); hasSwap {
					if tokenOutputs, hasOutputs := swapEvent["tokenOutputs"].([]interface{}); hasOutputs {
						for _, output := range tokenOutputs {
							if outputMap, ok := output.(map[string]interface{}); ok {
								mint, mintOk := outputMap["mint"].(string)
								if mintOk && mint != "" && mint != "So11111111111111111111111111111111111111112" {
									tokenMint = mint
									foundMint = true
									break
								}
							}
						}
					}
					if !foundMint {
						if tokenInputs, hasInputs := swapEvent["tokenInputs"].([]interface{}); hasInputs {
							for _, input := range tokenInputs {
								if inputMap, ok := input.(map[string]interface{}); ok {
									mint, mintOk := inputMap["mint"].(string)
									if mintOk && mint != "" && mint != "So11111111111111111111111111111111111111112" {
										tokenMint = mint
										foundMint = true
										break
									}
								}
							}
						}
					}
				}
			}
		}

		if !foundMint || tokenMint == "" {
			log.Printf("Could not extract relevant non-SOL token mint from webhook transaction %s, skipping validation.", txSignature)
			continue
		}

		log.Printf("Performing immediate DexScreener check for token %s from webhook tx %s", tokenMint, txSignature)

		validationResult, err := IsTokenValid(tokenMint)
		if err != nil {
			log.Printf("Error checking token %s (from tx %s) with DexScreener: %v", tokenMint, txSignature, err)
			continue
		}

		if validationResult == nil || !validationResult.IsValid {

			reason := "Did not meet criteria or validation failed"
			if validationResult != nil && len(validationResult.FailReasons) > 0 {
				reason = strings.Join(validationResult.FailReasons, "; ")
			}
			log.Printf("Token %s (from tx %s) does not meet immediate criteria. Reason: %s", tokenMint, txSignature, reason)
			continue
		}

		log.Printf("Valid Swap Detected via Webhook: Tx %s for token %s", txSignature, tokenMint)
		validatedCount++

		dexscreenerLink := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenMint)
		dexscreenerLinkEsc := notifications.EscapeMarkdownV2(dexscreenerLink)

		telegramMessage := fmt.Sprintf(
			"Hot Swap Validated\\! \nToken: `%s`\nDexScreener: %s\nTx: `%s`",
			tokenMint,
			dexscreenerLinkEsc,
			notifications.EscapeMarkdownV2(txSignature), // Escape Tx Signature
		)
		notifications.SendTelegramMessage(telegramMessage)

	}

	log.Printf("Webhook handler finished. Processed: %d transaction(s), Immediately validated & notified: %d", len(transactions), validatedCount)
	c.JSON(http.StatusOK, gin.H{"status": "success", "processed": len(transactions), "validated_now": validatedCount})
}

func CreateHeliusWebhook(webhookURL string) bool {

	apiKey := env.HeliusAPIKey
	webhookSecret := env.WebhookSecret
	authHeader := env.HeliusAuthHeader
	addressesRaw := env.RaydiumAccountAddresses
	pumpFunAuthority := env.PumpFunAuthority

	if apiKey == "" {
		log.Fatal("FATAL: HELIUS_API_KEY is missing from env package! Ensure env.LoadEnv() ran successfully in main.")
	}
	if webhookSecret == "" {
		log.Fatal("FATAL: WEBHOOK_SECRET is missing from env package! Ensure env.LoadEnv() ran successfully in main.")
	}
	if webhookURL == "" {
		log.Println("Error: CreateHeliusWebhook called with empty webhookURL.")
		return false
	}
	if authHeader == "" {
		log.Println("Warning: HELIUS_AUTH_HEADER is empty in env package! Webhook endpoint might be insecure.")
	}

	var accountList []string
	if addressesRaw != "" {
		for _, addr := range strings.Split(addressesRaw, ",") {
			trimmedAddr := strings.TrimSpace(addr)
			if trimmedAddr != "" {
				accountList = append(accountList, trimmedAddr)
			}
		}
		log.Printf("Using Raydium addresses from env: %v", accountList)
	}

	if pumpFunAuthority != "" {
		trimmedPumpAddr := strings.TrimSpace(pumpFunAuthority)
		alreadyExists := false
		for _, existing := range accountList {
			if existing == trimmedPumpAddr {
				alreadyExists = true
				break
			}
		}
		if !alreadyExists {
			log.Printf("Adding Pump.fun Authority Address from env: %s", trimmedPumpAddr)
			accountList = append(accountList, trimmedPumpAddr)
		} else {
			log.Printf("Pump.fun Authority Address from env (%s) already in list.", trimmedPumpAddr)
		}
	} else {
		log.Println("Info: PUMPFUN_AUTHORITY_ADDRESS not set in env package.")
	}

	if len(accountList) == 0 {
		log.Println("Warning: No addresses specified in env package (RAYDIUM_ACCOUNT_ADDRESSES or PUMPFUN_AUTHORITY_ADDRESS). Webhook might not receive relevant transactions.")

	}

	log.Printf("Final List of Addresses for Helius Webhook: %v", accountList)
	log.Printf("Expecting incoming Helius webhooks to have Authorization header: %s", authHeader)

	payload := map[string]interface{}{
		"webhookURL":       webhookURL,
		"transactionTypes": []string{"SWAP"},
		"accountAddresses": accountList,
		"webhookType":      "enhanced",
		"txnStatus":        "success",
		"authHeader":       authHeader,
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Error: Failed to marshal Helius webhook payload: %v", err)
		return false
	}

	heliusURL := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, err := http.NewRequest("POST", heliusURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		log.Printf("Error: Failed to create Helius webhook request: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+webhookSecret)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Error: Failed to send request to Helius API: %v", err)
		return false
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		log.Printf("Warning: Failed to read Helius webhook creation response body: %v", readErr)
	}

	log.Printf("Helius Webhook API Response Status: %s", resp.Status)
	if len(body) > 0 {
		log.Printf("Helius Webhook API Response Body: %s", string(body))
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Println("Helius webhook created or updated successfully.")
		return true
	} else {
		log.Printf("Error: Failed to create/update Helius webhook. Status: %d Body: %s", resp.StatusCode, string(body))
		return false
	}
}

func TestWebhookWithAuth() {
	webhookURL := env.WebhookURL
	authHeader := env.HeliusAuthHeader

	if webhookURL == "" {
		log.Fatal("FATAL: WEBHOOK_LISTENER_URL_DEV is missing from env package for testing!")
	}
	if authHeader == "" {
		log.Println("Warning: HELIUS_AUTH_HEADER is empty in env package! Sending test webhook without Authorization.")
	}

	reqBody := []map[string]interface{}{
		{
			"description": "Test swap SOL -> GUM " + time.Now().Format(time.RFC3339),
			"type":        "SWAP",
			"source":      "RAYDIUM",
			"signature":   fmt.Sprintf("test-sig-%d", time.Now().UnixNano()),
			"timestamp":   time.Now().Unix(),
			"tokenTransfers": []interface{}{
				map[string]interface{}{
					"fromUserAccount": "SourceWallet",
					"toUserAccount":   "DestinationWalletATA",
					"mint":            "21AErpiB8uSb94oQKRcwuHqyHF93njAxBSbdUrpupump", // Example mint
					"tokenAmount":     1500000000.0,
				},
			},
			"nativeTransfers": []interface{}{
				map[string]interface{}{
					"fromUserAccount": "SourceWallet",
					"toUserAccount":   "SomeRaydiumAccount",
					"amount":          100000000,
				},
			},
			"accountData":      []interface{}{},
			"transactionError": nil,
			"events": map[string]interface{}{
				"swap": map[string]interface{}{
					"nativeInput": map[string]interface{}{
						"account": "SourceWallet",
						"amount":  100000000,
					},
					"tokenOutputs": []interface{}{
						map[string]interface{}{
							"account":     "DestinationWalletATA",
							"mint":        "21AErpiB8uSb94oQKRcwuHqyHF93njAxBSbdUrpupump",
							"tokenAmount": 1500000000.0,
						},
					},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		log.Fatalf("TestWebhook: Failed to marshal test body: %v", err)
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Fatalf("TestWebhook: Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
		log.Printf("Sending Test Webhook to: %s with Auth Header: %s", webhookURL, authHeader)
	} else {
		log.Printf("Sending Test Webhook to: %s (No Auth Header)", webhookURL)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf("TestWebhook: Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("Test Webhook Response Status: %s", resp.Status)
	log.Printf("Test Webhook Response Body: %s", string(respBody))

	if resp.StatusCode != http.StatusOK {
		log.Printf("Test Webhook received non-OK status: %s", resp.Status)
	} else {
		log.Println("Test Webhook received OK status.")
	}
}

func ValidateCachedSwaps() {
	log.Println("ValidateCachedSwaps Loop Started...")
	for {
		time.Sleep(3 * time.Minute)

		log.Println("Running ValidateCachedSwaps check...")

		swapCache.RLock()
		tokensToValidate := make([]string, 0, len(swapCache.Data))
		volumeMap := make(map[string]float64, len(swapCache.Data))
		const validationVolumeThreshold = 500
		for token, volumes := range swapCache.Data {
			totalVolume := sum(volumes)
			if totalVolume >= validationVolumeThreshold {
				tokensToValidate = append(tokensToValidate, token)
				volumeMap[token] = totalVolume
			}
		}
		cacheSize := len(swapCache.Data)
		swapCache.RUnlock()

		log.Printf("Found %d tokens in cache, %d meet volume threshold ($%.2f) for validation.", cacheSize, len(tokensToValidate), float64(validationVolumeThreshold))

		validatedCount := 0
		failedCount := 0
		for _, token := range tokensToValidate {

			log.Printf("Checking cached token %s (Vol: $%.2f) via ValidateCachedSwaps loop.", token, volumeMap[token])

			validationResult, err := IsTokenValid(token)
			if err != nil {
				log.Printf(" Error checking token %s during ValidateCachedSwaps: %v", token, err)
				failedCount++

				continue
			}

			if validationResult != nil && validationResult.IsValid {
				validatedCount++
				log.Printf("Token %s (Vol: $%.2f) validated via ValidateCachedSwaps loop.", token, volumeMap[token])

				dexscreenerLink := fmt.Sprintf("https://dexscreener.com/solana/%s", token)
				dexscreenerLinkEsc := notifications.EscapeMarkdownV2(dexscreenerLink)

				telegramMessage := fmt.Sprintf(
					" Tracking validated swap token: `%s`\nDexScreener: %s",
					token,
					dexscreenerLinkEsc,
				)
				notifications.SendTelegramMessage(telegramMessage)

				swapCache.Lock()
				delete(swapCache.Data, token)
				swapCache.Unlock()
				log.Printf("   Removed validated token %s from cache.", token)
			} else {
				failedCount++

				reason := "Did not meet criteria or validation failed"
				if validationResult != nil && len(validationResult.FailReasons) > 0 {
					reason = strings.Join(validationResult.FailReasons, "; ")
				}
				log.Printf("Token %s (Vol: $%.2f) failed validation via ValidateCachedSwaps loop. Reason: %s", token, volumeMap[token], reason)

				swapCache.Lock()
				delete(swapCache.Data, token)
				swapCache.Unlock()
				log.Printf("   Removed failed/invalid token %s from cache.", token)
			}
		}
		log.Printf("ValidateCachedSwaps check complete. Validated: %d, Failed/Not Valid: %d", validatedCount, failedCount)
	}
}

func sum(volumes []float64) float64 {
	var total float64
	for _, v := range volumes {
		total += v
	}
	return total
}

func init() {
	go ValidateCachedSwaps()
	go ClearSwapCacheEvery1Hours()
	log.Println("Raydium Tracker service background routines started.")
}

func ClearSwapCacheEvery1Hours() {
	clearInterval := 1 * time.Hour
	log.Printf("Swap cache clearing routine started (interval: %v).", clearInterval)
	ticker := time.NewTicker(clearInterval)
	defer ticker.Stop()

	for range ticker.C {
		swapCache.Lock()
		cacheSizeBefore := len(swapCache.Data)
		swapCache.Data = make(map[string][]float64)
		swapCache.Unlock()
		log.Printf("Cleared swapCache. Removed %d entries.", cacheSizeBefore)
	}
}
