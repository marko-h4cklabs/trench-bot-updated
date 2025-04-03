package services

import (
	"bytes"
	// No separate dexscreener import needed as it's part of 'package services'
	"ca-scraper/shared/notifications" // Assuming this path is correct
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
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
	log.Println("DexScreener Validation Loop Started (using centralized check)...")

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
		if totalVolume >= 1000 {
			log.Printf("Checking DexScreener eligibility for token %s (Volume: %.2f)", token, totalVolume)

			isValid, err := IsTokenValid(token)
			if err != nil {
				log.Printf(" DexScreener check failed for %s: %v", token, err)
				failedCount++
				continue
			}

			if isValid {
				validatedCount++
				log.Printf("Token %s meets DexScreener criteria via periodic check!", token)
			} else {
				failedCount++
				log.Printf("Token %s failed DexScreener validation via periodic check.", token)
			}
		} else {
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

		// Check global seen cache
		seenTransactions.Lock()
		_, exists := seenTransactions.TxIDs[txSignature]
		if exists {
			seenTransactions.Unlock()
			skippedAlreadySeen++
			continue
		}
		seenTransactions.Unlock()

		if !processSwapTransaction(tx) {
			skippedCriteria++
			continue
		}

		batchSeen[txSignature] = struct{}{}
		seenTransactions.Lock()
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
		if err := json.NewDecoder(bodyReader).Decode(&singleTransaction); err != nil {
			log.Printf(" Invalid JSON format (neither array nor single object): %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format"})
			return
		}
		transactions = []map[string]interface{}{singleTransaction}
	}

	log.Printf("Processing %d transaction(s) from webhook for immediate validation...", len(transactions))

	validatedCount := 0
	for _, tx := range transactions {
		if tx == nil {
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
			continue
		}
		seenTransactions.TxIDs[txSignature] = struct{}{}
		seenTransactions.Unlock()

		var tokenMint string
		if events, hasEvents := tx["events"].(map[string]interface{}); hasEvents {
			if swapEvent, hasSwap := events["swap"].(map[string]interface{}); hasSwap {
				if tokenOutputs, hasOutputs := swapEvent["tokenOutputs"].([]interface{}); hasOutputs && len(tokenOutputs) > 0 {
					if firstOutput, ok := tokenOutputs[0].(map[string]interface{}); ok {
						mint, mintOk := firstOutput["mint"].(string)
						if mintOk && mint != "So11111111111111111111111111111111111111112" {
							tokenMint = mint
						}
					}
				}
			}
		}

		if tokenMint == "" {
			log.Printf("Could not extract relevant token mint from webhook transaction %s, skipping validation.", txSignature)
			continue
		}

		log.Printf("Performing immediate DexScreener check for token %s from webhook tx %s", tokenMint, txSignature)

		isValid, err := IsTokenValid(tokenMint)
		if err != nil {
			log.Printf("Error checking token %s (from tx %s): %v", tokenMint, txSignature, err)
			continue
		}

		if !isValid {
			log.Printf("Token %s (from tx %s) does not meet immediate criteria.", tokenMint, txSignature)
			continue
		}

		log.Printf("Valid Swap Detected via Webhook: Tx %s for token %s", txSignature, tokenMint)
		validatedCount++

		telegramMessage := fmt.Sprintf("Hot Swap Validated! Token: %s\nTx: %s\nDexScreener: https://dexscreener.com/solana/%s", tokenMint, txSignature, tokenMint)
		notifications.SendTelegramMessage(telegramMessage)

	}

	log.Printf("Webhook handler finished. Immediately validated and notified: %d", validatedCount)
	c.JSON(http.StatusOK, gin.H{"status": "success", "validated_now": validatedCount})
}

func CreateHeliusWebhook(webhookURL string) bool {
	if err := godotenv.Load(".env"); err != nil {
		log.Println("Info: .env file not found or error loading:", err)
	} else {
		log.Println(".env file loaded for Helius setup.")
	}

	apiKey := os.Getenv("HELIUS_API_KEY")
	webhookSecret := os.Getenv("WEBHOOK_SECRET")
	authHeader := os.Getenv("HELIUS_AUTH_HEADER")
	addressesRaw := os.Getenv("RAYDIUM_ACCOUNT_ADDRESSES")
	pumpFunAuthority := os.Getenv("PUMPFUN_AUTHORITY_ADDRESS")

	if apiKey == "" {
		log.Fatal("FATAL: HELIUS_API_KEY is missing! Set it in the .env file or environment variables.")
	}
	if webhookSecret == "" {
		log.Fatal("FATAL: WEBHOOK_SECRET is missing! This is required to authorize webhook creation with Helius.")
	}
	if authHeader == "" {
		log.Println("Warning: HELIUS_AUTH_HEADER is empty! Your webhook endpoint will be insecure.")
	}

	var accountList []string
	if addressesRaw != "" {
		for _, addr := range strings.Split(addressesRaw, ",") {
			trimmedAddr := strings.TrimSpace(addr)
			if trimmedAddr != "" {
				accountList = append(accountList, trimmedAddr)
			}
		}
		log.Printf("Using Raydium addresses: %v", accountList)
	}

	if pumpFunAuthority != "" {
		log.Printf("Adding Pump.fun Authority Address to Raydium tracker webhook: %s", pumpFunAuthority)
		accountList = append(accountList, pumpFunAuthority)
	} else {
		log.Println("Info: PUMPFUN_AUTHORITY_ADDRESS not set, won't be included in this webhook's accounts.")
	}

	if len(accountList) == 0 {
		log.Println("Warning: No addresses specified (RAYDIUM_ACCOUNT_ADDRESSES or PUMPFUN_AUTHORITY_ADDRESS). Webhook might not receive relevant transactions.")
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

	url := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
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
		log.Printf("Error: Failed to create/update Helius webhook. Status: %d", resp.StatusCode)
		return false
	}
}

func TestWebhookWithAuth() {
	godotenv.Load(".env")
	webhookURL := os.Getenv("WEBHOOK_LISTENER_URL_DEV")
	authHeader := os.Getenv("HELIUS_AUTH_HEADER")

	if webhookURL == "" || authHeader == "" {
		log.Fatal("FATAL: Missing WEBHOOK_LISTENER_URL_DEV or HELIUS_AUTH_HEADER in .env for testing.")
	}

	reqBody := []map[string]interface{}{
		{
			"description": "Test swap SOL -> GUM",
			"type":        "SWAP",
			"source":      "RAYDIUM",
			"signature":   fmt.Sprintf("test-sig-%d", time.Now().Unix()),
			"timestamp":   time.Now().Unix(),
			"events": map[string]interface{}{
				"swap": map[string]interface{}{
					"nativeInput": map[string]interface{}{
						"account": "SourceWallet",
						"amount":  100000000,
					},
					"nativeOutput": nil,
					"tokenInputs":  nil,
					"tokenOutputs": []interface{}{
						map[string]interface{}{
							"account":       "DestinationWalletATA",
							"mint":          "21AErpiB8uSb94oQKRcwuHqyHF93njAxBSbdUrpupump",
							"tokenAmount":   1500000000.0,
							"tokenStandard": "Fungible",
						},
					},
					"tokenFees":  []interface{}{ /* ... */ },
					"nativeFees": []interface{}{ /* ... */ },
				},
			},
			"accountData": []interface{}{ /* ... */ },
		},
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		log.Fatalf("TestWebhook: Failed to marshal test body: %v", err)
	}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Fatalf(" TestWebhook: Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)

	log.Printf("📡 Sending Test Webhook to: %s with Auth Header: %s", webhookURL, authHeader)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf(" TestWebhook: Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	log.Printf("📡 Test Webhook Response Status: %s", resp.Status)
	log.Printf("📡 Test Webhook Response Body: %s", string(respBody))
}

func ValidateCachedSwaps() {
	log.Println("ValidateCachedSwaps Loop Started...")
	for {
		time.Sleep(5 * time.Minute)

		log.Println("Running ValidateCachedSwaps check...")

		swapCache.RLock()
		tokensToValidate := make([]string, 0, len(swapCache.Data))
		volumeMap := make(map[string]float64, len(swapCache.Data))
		for token, volumes := range swapCache.Data {
			totalVolume := sum(volumes)
			if totalVolume >= 1000 {
				tokensToValidate = append(tokensToValidate, token)
				volumeMap[token] = totalVolume
			}
		}
		cacheSize := len(swapCache.Data)
		swapCache.RUnlock()

		log.Printf("Found %d tokens in cache, %d meet volume threshold for validation.", cacheSize, len(tokensToValidate))

		validatedCount := 0
		failedCount := 0
		for _, token := range tokensToValidate {
			time.Sleep(1 * time.Second)

			isValid, err := IsTokenValid(token)
			if err != nil {
				log.Printf(" Error checking token %s during ValidateCachedSwaps: %v", token, err)
				failedCount++
				continue
			}

			if isValid {
				validatedCount++
				log.Printf("Token %s (Vol: %.2f) validated via ValidateCachedSwaps loop.", token, volumeMap[token])
				telegramMessage := fmt.Sprintf("📈 Tracking validated swap token: %s\nDexScreener: https://dexscreener.com/solana/%s", token, token)
				notifications.SendTelegramMessage(telegramMessage)
				log.Println(telegramMessage)

				swapCache.Lock()
				delete(swapCache.Data, token)
				swapCache.Unlock()
			} else {
				failedCount++
				log.Printf("Token %s (Vol: %.2f) failed validation via ValidateCachedSwaps loop.", token, volumeMap[token])

				swapCache.Lock()
				delete(swapCache.Data, token)
				swapCache.Unlock()
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
	log.Println("Raydium Tracker service initialized.")
}

func ClearSwapCacheEvery1Hours() {
	for {
		time.Sleep(1 * time.Hour)

		swapCache.Lock()
		swapCache.Data = make(map[string][]float64)
		swapCache.Unlock()

		log.Println("Cleared swapCache after 1 hours to purge inactive tokens.")
	}
}
