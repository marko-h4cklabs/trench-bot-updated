package services

import (
	"bytes"
	"ca-scraper/shared/notifications"
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

const DexScreenerAPI = "https://api.dexscreener.com/tokens/v1/solana"

const (
	minLiquidity = 40000
	minMarketCap = 50000
	maxMarketCap = 250000
	min5mVolume  = 1000.0
	min1hVolume  = 10000.0
	min5mTx      = 100
	min1hTx      = 500
)

type DexScreenerResponse struct {
	Pairs []Pair `json:"pairs"`
}

type Pair struct {
	Liquidity    Liquidity          `json:"liquidity"`
	MarketCap    float64            `json:"marketCap"`
	Volume       map[string]float64 `json:"volume"`
	Transactions map[string]TxData  `json:"txns"`
}

type Liquidity struct {
	Usd float64 `json:"usd"`
}

type TxData struct {
	Buys  int `json:"buys"`
	Sells int `json:"sells"`
}

func TrackGraduatedToken(tokenAddress string) {
	log.Printf("Monitoring swaps for newly graduated token: %s", tokenAddress)

	go func() {
		time.Sleep(1 * time.Minute)
		log.Printf(" Started tracking token: %s", tokenAddress)
	}()
}

func StartDexScreenerValidation() {
	log.Println("DexScreener Validation Loop Started...")

	for {
		time.Sleep(3 * time.Minute)
		validateCachedTokens()
	}
}

func validateCachedTokens() {
	swapCache.Lock()
	defer swapCache.Unlock()

	for token, volumes := range swapCache.Data {
		totalVolume := sum(volumes)
		if totalVolume >= 1000 {
			isValid, err := IsTokenValid(token)
			if err != nil {
				log.Printf(" DexScreener check failed: %v", err)
				continue
			}

			if isValid {
				log.Printf(" Token %s meets DexScreener criteria!", token)
			} else {
				log.Printf(" Token %s failed DexScreener validation", token)
			}
		}
	}
}

func HandleTransactionWebhookWithPayload(transactions []map[string]interface{}) {
	seenTransactions.Lock()
	defer seenTransactions.Unlock()

	for _, tx := range transactions {
		if tx == nil {
			continue
		}

		txSignature, _ := tx["signature"].(string)
		if txSignature == "" {
			log.Println(" Transaction missing signature, skipping...")
			continue
		}

		log.Printf(" Swap Transaction Received: %s", txSignature)

		time.Sleep(500 * time.Millisecond)

		if _, exists := seenTransactions.TxIDs[txSignature]; exists {
			log.Printf(" Transaction %s already processed, skipping...", txSignature)
			continue
		}

		if !processSwapTransaction(tx) {
			log.Printf(" Transaction %s did not meet criteria, skipping...", txSignature)
			continue
		}

		seenTransactions.TxIDs[txSignature] = struct{}{}
		log.Printf("Transaction %s successfully processed.", txSignature)
	}
}

func processSwapTransaction(tx map[string]interface{}) bool {
	txSignature, _ := tx["signature"].(string)
	if txSignature == "" {
		log.Println("Missing transaction signature, skipping...")
		return false
	}

	seenTransactions.Lock()
	if _, exists := seenTransactions.TxIDs[txSignature]; exists {
		seenTransactions.Unlock()
		log.Printf("Transaction %s already processed, skipping...", txSignature)
		return false
	}
	seenTransactions.Unlock()

	tokenMint, hasMint := tx["tokenMint"].(string)
	usdValue, hasValue := tx["usdValue"].(float64)

	if !hasMint || tokenMint == "" || !hasValue {
		log.Printf("Transaction %s missing token mint or value, skipping...", txSignature)
		return false
	}

	swapCache.Lock()
	swapCache.Data[tokenMint] = append(swapCache.Data[tokenMint], usdValue)
	swapCache.Unlock()
	log.Printf("Cached swap token: %s with value $%.2f for DexScreener validation", tokenMint, usdValue)

	return true
}

func IsTokenValid(tokenCA string) (bool, error) {
	time.Sleep(2 * time.Second)

	log.Printf("Checking token validity on DexScreener: %s", tokenCA)

	url := fmt.Sprintf("%s/%s", DexScreenerAPI, tokenCA)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return false, fmt.Errorf("API request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		log.Println("Rate limit exceeded (429). Retrying in 5 seconds...")
		time.Sleep(5 * time.Second)
		return IsTokenValid(tokenCA)
	} else if resp.StatusCode == 404 {
		log.Printf("Token %s not found on DexScreener.", tokenCA)
		return false, nil
	} else if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("API request failed with status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("error reading API response: %v", err)
	}

	var response DexScreenerResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		log.Printf("JSON Parsing Failed: %v | Raw Response: %s", err, string(body))
		return false, fmt.Errorf("JSON parsing failed: %v", err)
	}

	if len(response.Pairs) == 0 {
		log.Printf("Token %s has no available trading pairs on DexScreener.", tokenCA)
		return false, nil
	}

	pair := response.Pairs[0]

	if pair.Liquidity.Usd >= minLiquidity &&
		pair.MarketCap >= minMarketCap && pair.MarketCap <= maxMarketCap &&
		pair.Volume["m5"] >= min5mVolume &&
		pair.Volume["h1"] >= min1hVolume &&
		pair.Transactions["m5"].Buys+pair.Transactions["m5"].Sells >= min5mTx &&
		pair.Transactions["h1"].Buys+pair.Transactions["h1"].Sells >= min1hTx {

		log.Printf(" Token %s meets DexScreener criteria!", tokenCA)
		return true, nil
	}

	log.Printf(" Token %s did not meet DexScreener criteria.", tokenCA)
	log.Printf(" Failure Reason - Liquidity: %f | Market Cap: %f | Volume (5m): %f | Volume (1h): %f",
		pair.Liquidity.Usd, pair.MarketCap, pair.Volume["m5"], pair.Volume["h1"])
	return false, nil
}

func HandleTransactionWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to read request body"})
		return
	}

	log.Printf(" Raw Webhook Payload: %s", string(body))

	var transactions []map[string]interface{}
	if err := json.Unmarshal(body, &transactions); err != nil {
		var singleTransaction map[string]interface{}
		if err := json.Unmarshal(body, &singleTransaction); err != nil {
			log.Printf(" Invalid JSON format: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format"})
			return
		}
		transactions = []map[string]interface{}{singleTransaction}
	}

	log.Printf("Parsed Transactions: %+v", transactions)

	seenTransactions.Lock()
	defer seenTransactions.Unlock()

	for _, tx := range transactions {
		if tx == nil {
			continue
		}

		txSignature, _ := tx["signature"].(string)
		if txSignature == "" {
			log.Println("Transaction missing signature, skipping...")
			continue
		}

		events, hasEvents := tx["events"].(map[string]interface{})
		if !hasEvents {
			log.Printf("Transaction %s has no events, skipping...", txSignature)
			continue
		}

		swapEvent, hasSwap := events["swap"].(map[string]interface{})
		if !hasSwap {
			log.Printf("Transaction %s is not a swap, skipping...", txSignature)
			continue
		}

		tokenOutputs, hasTokenOutputs := swapEvent["tokenOutputs"].([]interface{})
		if !hasTokenOutputs || len(tokenOutputs) == 0 {
			log.Printf("Transaction %s has no token outputs, skipping...", txSignature)
			continue
		}

		firstOutput, _ := tokenOutputs[0].(map[string]interface{})
		tokenMint, _ := firstOutput["mint"].(string)

		isValid, err := IsTokenValid(tokenMint)
		if err != nil {
			log.Printf("Error checking token: %v", err)
			continue
		}
		if !isValid {
			log.Printf(" Token %s does not meet criteria, skipping...", tokenMint)
			continue
		}

		log.Printf("Valid Swap Detected: %s swapped for %s", txSignature, tokenMint)
		seenTransactions.TxIDs[txSignature] = struct{}{}
	}

	c.JSON(http.StatusOK, gin.H{"status": "success"})
}

func CreateHeliusWebhook(webhookURL string) bool {

	time.Sleep(2 * time.Second)

	if err := godotenv.Load(".env"); err != nil {
		println(".env file NOT found in the current directory.")
	} else {
		println(".env file successfully loaded.")
	}

	apiKey := os.Getenv("HELIUS_API_KEY")
	webhookSecret := os.Getenv("WEBHOOK_SECRET")
	authHeader := os.Getenv("HELIUS_AUTH_HEADER")
	addressesRaw := os.Getenv("RAYDIUM_ACCOUNT_ADDRESSES")
	pumpFunAuthority := os.Getenv("PUMPFUN_AUTHORITY_ADDRESS")

	if apiKey == "" {
		log.Fatal("HELIUS_API_KEY is missing! Check .env file.")
	}
	if webhookSecret == "" {
		log.Fatal("WEBHOOK_SECRET is missing! Check .env file.")
	}
	if authHeader == "" {
		log.Println("Warning: HELIUS_AUTH_HEADER is empty! This might cause verification issues.")
	}

	var accountList []string
	if addressesRaw != "" {
		for _, addr := range strings.Split(addressesRaw, ",") {
			trimmedAddr := strings.TrimSpace(addr)
			if trimmedAddr != "" {
				accountList = append(accountList, trimmedAddr)
			}
		}
	}

	if pumpFunAuthority != "" {
		log.Printf("Adding Pump.fun Authority Address: %s", pumpFunAuthority)
		accountList = append(accountList, pumpFunAuthority)
	} else {
		log.Println("Warning: PUMPFUN_AUTHORITY_ADDRESS is missing. Pump.fun tokens may not be tracked.")
	}

	log.Printf("Final List of Addresses for Webhook: %v", accountList)
	log.Printf("Auth Header Being Sent: %s", authHeader)

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
		log.Printf("Failed to marshal webhook payload: %v", err)
		return false
	}

	url := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
	if err != nil {
		log.Printf("Failed to create new request: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", webhookSecret)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to send request to Helius: %v", err)
		return false
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Failed to read webhook creation response: %v", err)
	} else {
		log.Printf("Helius Webhook API Response: %s", string(body))
	}

	if resp.StatusCode == http.StatusOK {
		log.Println("Webhook created successfully.")
		return true
	} else {
		log.Printf("Failed to create webhook. Status: %d, Response: %s", resp.StatusCode, string(body))
		return false
	}
}

func TestWebhookWithAuth() {
	webhookURL := os.Getenv("WEBHOOK_LISTENER_URL_DEV")
	authHeader := os.Getenv("HELIUS_AUTH_HEADER")

	if webhookURL == "" || authHeader == "" {
		log.Fatal("Missing webhook URL or auth header in .env")
	}

	reqBody := map[string]interface{}{
		"signature": "test-transaction-123",
		"events": map[string]interface{}{
			"swap": map[string]interface{}{
				"tokenOutputs": []map[string]interface{}{
					{"mint": "21AErpiB8uSb94oQKRcwuHqyHF93njAxBSbdUrpupump"},
				},
			},
		},
	}

	jsonBody, _ := json.Marshal(reqBody)
	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Fatalf(" Failed to create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Fatalf(" Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("📡 Test Webhook Response: %s", string(body))
	log.Printf("🔹 Response Code: %d", resp.StatusCode)
}

func ValidateCachedSwaps() {
	for {
		time.Sleep(3 * time.Minute)

		swapCache.Lock()
		for token, volumes := range swapCache.Data {
			if time.Since(time.Unix(int64(volumes[0]), 0)) > 30*time.Minute {
				delete(swapCache.Data, token)
				continue
			}

			totalVolume := sum(volumes)
			if totalVolume < 1000 {
				continue
			}

			isValid, err := IsTokenValid(token)
			if err != nil {
				log.Printf(" Error checking token: %v", err)
				continue
			}

			if isValid {
				telegramMessage := fmt.Sprintf(" Tracking validated swap token: %s\n DexScreener: https://dexscreener.com/solana/%s", token, token)
				notifications.SendTelegramMessage(telegramMessage)
				log.Println(telegramMessage)
			}
		}
		swapCache.Unlock()
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
}
