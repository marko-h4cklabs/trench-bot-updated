package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"go.uber.org/zap"
)

type WebhookRequest struct {
	WebhookURL       string   `json:"webhookURL"`
	TransactionTypes []string `json:"transactionTypes"`
	AccountAddresses []string `json:"accountAddresses"`
	WebhookType      string   `json:"webhookType"`
	TxnStatus        string   `json:"txnStatus"`
	AuthHeader       string   `json:"authHeader"`
}

var graduatedTokenCache = struct {
	sync.Mutex
	Data map[string]time.Time
}{Data: make(map[string]time.Time)}

var swapCache = struct {
	sync.Mutex
	Data map[string][]float64
}{Data: make(map[string][]float64)}

var tokenCache = struct {
	sync.Mutex
	Tokens map[string]time.Time
}{Tokens: make(map[string]time.Time)}

func SetupGraduationWebhook(apiKey, webhookURL, pumpFunAuthority string, log *logger.Logger) error {
	log.Info("Checking for existing Helius Webhook...")

	webhookSecret := os.Getenv("WEBHOOK_SECRET")
	authHeader := os.Getenv("HELIUS_AUTH_HEADER")

	if webhookSecret == "" {
		log.Info("WEBHOOK_SECRET is missing! Check .env file.")
	}
	if authHeader == "" {
		log.Info("HELIUS_AUTH_HEADER is missing! Check .env file.")
	}

	existingWebhook, err := CheckExistingHeliusWebhook(apiKey, webhookURL)
	if err != nil {
		log.Error(fmt.Sprintf(" Failed to check existing webhook: %v", err))
	}

	if existingWebhook {
		log.Info("Webhook already exists. Skipping creation.")
		return nil
	}

	log.Info(" No existing webhook found. Creating a new one...")

	requestBody := WebhookRequest{
		WebhookURL:       webhookURL,
		TransactionTypes: []string{"TRANSFER", "SWAP"},
		AccountAddresses: []string{pumpFunAuthority},
		WebhookType:      "enhanced",
		AuthHeader:       authHeader,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to serialize webhook request: %v", err))
		return err
	}

	url := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Error(fmt.Sprintf("Failed to create webhook request: %v", err))
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", webhookSecret)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to send webhook request: %v", err))
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		log.Error(fmt.Sprintf("Failed to create webhook. Status: %d, Response: %s", resp.StatusCode, string(body)))
		return fmt.Errorf("failed to create webhook")
	}

	log.Info("Webhook created successfully")
	return nil
}

func CheckExistingHeliusWebhook(apiKey, webhookURL string) (bool, error) {
	url := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	resp, err := http.Get(url)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var webhooks []map[string]interface{}
	if err := json.Unmarshal(body, &webhooks); err != nil {
		return false, err
	}

	for _, webhook := range webhooks {
		if webhook["webhookURL"] == webhookURL {
			log.Printf("Found existing webhook: %s", webhookURL)
			log.Printf("Existing webhook details: %+v", webhook)
			return true, nil
		}
	}

	return false, nil
}

func HandleWebhook(payload []byte, authHeader string, log *logger.Logger) {
	println(" Received Webhook Payload: %s", string(payload))
	if len(payload) == 0 {
		log.Error(" Empty webhook payload received!")
	}
	var eventsArray []map[string]interface{}
	if err := json.Unmarshal(payload, &eventsArray); err == nil {
		for _, event := range eventsArray {
			processGraduatedToken(event, log)
		}
		return
	}

	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		log.Error(fmt.Sprintf("Failed to parse webhook payload: %v", err))
		return
	}

	processGraduatedToken(event, log)
}

func processGraduatedToken(event map[string]interface{}, log *logger.Logger) {
	log.Debug("Processing received event", zap.Any("event", event))

	tokenAddress, ok := extractGraduatedToken(event)
	if !ok {
		log.Warn("Missing token address in event, cannot process.")
		return
	}

	dexscreenerURL := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

	tokenCache.Lock()
	if _, exists := tokenCache.Tokens[tokenAddress]; exists {
		tokenCache.Unlock()
		log.Debug("Token already processed recently, skipping debounce.", zap.String("tokenAddress", tokenAddress))
		return
	}
	tokenCache.Tokens[tokenAddress] = time.Now()
	tokenCache.Unlock()

	isRenounced, renouncedErr := IsMintRenounced(tokenAddress)
	mintStatusStr := ""
	if renouncedErr != nil {
		mintStatusStr = "Mint Renounced: Check Failed"
		log.Warn("Failed to check mint status", zap.String("tokenAddress", tokenAddress), zap.Error(renouncedErr))
	} else {
		if isRenounced {
			mintStatusStr = "Mint Renounced: Yes"
		} else {
			mintStatusStr = "Mint Renounced: No"
		}
	}
	log.Info("Mint status check result", zap.String("tokenAddress", tokenAddress), zap.String("status", mintStatusStr))

	isLocked, lockedErr := CheckLiquidityLock(tokenAddress)
	lockStatusStr := ""
	if lockedErr != nil {
		lockStatusStr = "Liquidity Locked (>1k): Check Failed"
		log.Warn("Failed to check liquidity lock status", zap.String("tokenAddress", tokenAddress), zap.Error(lockedErr))
	} else {
		if isLocked {
			lockStatusStr = "Liquidity Locked (>1k): Yes"
		} else {
			lockStatusStr = "Liquidity Locked (>1k): No / Unavailable"
		}
	}
	log.Info("Liquidity lock check result", zap.String("tokenAddress", tokenAddress), zap.String("status", lockStatusStr))

	isValid, validationErr := IsTokenValid(tokenAddress)
	if validationErr != nil {
		log.Error("Error checking DexScreener criteria", zap.String("tokenAddress", tokenAddress), zap.Error(validationErr))
		return
	}
	if !isValid {
		log.Info("Token failed mandatory DexScreener criteria. No notification sent.", zap.String("tokenAddress", tokenAddress))
		return
	}
	log.Info("Token passed DexScreener validation criteria! Preparing notification...", zap.String("tokenAddress", tokenAddress))

	telegramMessage := fmt.Sprintf(
		"Validated Token Found!\n\n"+
			"CA: %s\n\n"+
			"DexScreener: %s\n\n"+
			"--- Info ---\n"+
			"🔹 %s\n"+
			"🔹 %s",
		tokenAddress,
		dexscreenerURL,
		mintStatusStr,
		lockStatusStr,
	)

	notifications.SendTelegramMessage(telegramMessage)

	log.Info("Telegram notification send attempt initiated", zap.String("tokenAddress", tokenAddress))
	graduatedTokenCache.Lock()
	graduatedTokenCache.Data[tokenAddress] = time.Now()
	graduatedTokenCache.Unlock()
	log.Info("Added token to graduatedTokenCache", zap.String("tokenAddress", tokenAddress))
	TrackGraduatedToken(tokenAddress)
}

func extractGraduatedToken(event map[string]interface{}) (string, bool) {
	if transaction, hasTransaction := event["transaction"].(map[string]interface{}); hasTransaction {
		if tokenMint, ok := extractTokenFromTransaction(transaction); ok {
			log.Printf("Extracted Token Address from Transaction: %s", tokenMint)
			return tokenMint, true
		}
	}

	if events, hasEvents := event["events"].(map[string]interface{}); hasEvents {
		if tokenMint, ok := extractTokenFromEvents(events); ok {
			log.Printf(" Extracted Token Address from Events: %s", tokenMint)
			return tokenMint, true
		}
	}

	log.Println("Could not extract token address from graduation event.")
	log.Printf("Full event data for debugging: %+v", event)
	return "", false
}

func extractTokenFromTransaction(transaction map[string]interface{}) (string, bool) {
	events, hasEvents := transaction["events"].(map[string]interface{})
	if !hasEvents {
		log.Println("No 'events' found inside 'transaction'")
		return "", false
	}

	swapEvent, hasSwap := events["swap"].(map[string]interface{})
	if !hasSwap {
		log.Println("No 'swap' event found inside 'transaction'")
		return "", false
	}

	return extractTokenFromSwap(swapEvent)
}

func extractTokenFromEvents(events map[string]interface{}) (string, bool) {
	swapEvent, hasSwap := events["swap"].(map[string]interface{})
	if !hasSwap {
		log.Println(" No 'swap' event found inside 'events'")
		return "", false
	}

	return extractTokenFromSwap(swapEvent)
}

func extractTokenFromSwap(swapEvent map[string]interface{}) (string, bool) {
	tokenOutputs, hasTokenOutputs := swapEvent["tokenOutputs"].([]interface{})
	if hasTokenOutputs && len(tokenOutputs) > 0 {
		firstOutput, ok := tokenOutputs[0].(map[string]interface{})
		if ok {
			tokenMint, mintExists := firstOutput["mint"].(string)
			if mintExists && tokenMint != "" {
				log.Printf(" Found token address from tokenOutputs: %s", tokenMint)
				return tokenMint, true
			}
		}
	}

	log.Println(" No 'tokenOutputs' found. Trying 'tokenInputs' instead...")

	tokenInputs, hasTokenInputs := swapEvent["tokenInputs"].([]interface{})
	if hasTokenInputs && len(tokenInputs) > 0 {
		firstInput, ok := tokenInputs[0].(map[string]interface{})
		if ok {
			tokenMint, mintExists := firstInput["mint"].(string)
			if mintExists && tokenMint != "" {
				log.Printf(" Found token address from tokenInputs: %s", tokenMint)
				return tokenMint, true
			}
		}
	}

	log.Println(" Could not extract token address from graduation event.")
	log.Printf(" Full swap event data for debugging: %+v", swapEvent)
	return "", false
}

func IsMintRenounced(mintAddress string) (bool, error) {
	log.Printf("Checking mint status for %s", mintAddress)
	client := rpc.New(os.Getenv("SOLANA_RPC_URL"))
	if os.Getenv("SOLANA_RPC_URL") == "" {
		client = rpc.New("https://api.mainnet-beta.solana.com")
		log.Println("Warning: SOLANA_RPC_URL not set, using default public endpoint.")
	}

	mintPubKey, err := solana.PublicKeyFromBase58(mintAddress)
	if err != nil {
		log.Printf("Error: Invalid public key for mint check %s: %v", mintAddress, err)
		return false, fmt.Errorf("invalid public key: %v", err)
	}

	accountInfo, err := client.GetAccountInfo(context.Background(), mintPubKey)
	if err != nil {
		log.Printf("Error: Failed to get account info for mint check %s: %v", mintAddress, err)
		return false, fmt.Errorf("failed to get account info: %v", err)
	}
	if accountInfo == nil || accountInfo.Value == nil {
		log.Printf("Error: No account info value returned for mint check %s", mintAddress)
		return false, fmt.Errorf("account info or value is nil for mint %s", mintAddress)
	}

	binaryData := accountInfo.Value.Data.GetBinary()
	if len(binaryData) == 0 {
		log.Printf("Error: No data found in account info for mint: %s", mintAddress)
		return false, fmt.Errorf("account data missing")
	}
	if len(binaryData) < 4 {
		log.Printf("Error: Mint account data too short for mint authority option check: %s (len %d)", mintAddress, len(binaryData))
		return false, fmt.Errorf("account data too short (%d bytes) for option field", len(binaryData))
	}

	mintAuthorityOption := binaryData[0]
	isRenounced := mintAuthorityOption == 0

	if isRenounced {
		log.Printf("Info: Minting is RENOUNCED for token: %s", mintAddress)
	} else {
		log.Printf("Info: Minting is STILL POSSIBLE for token: %s", mintAddress)
	}

	return isRenounced, nil
}

func ValidateCachedTokens() {
	for {
		time.Sleep(5 * time.Minute)

		graduatedTokenCache.Lock()
		for token, addedTime := range graduatedTokenCache.Data {
			if time.Since(addedTime) > 30*time.Minute {
				delete(graduatedTokenCache.Data, token)
				continue
			}

			isValid, err := IsTokenValid(token)
			if err != nil {
				log.Printf(" Error checking token: %v", err)
				continue
			}

			if isValid {
				telegramMessage := fmt.Sprintf(" Tracking validated graduated token: %s\n DexScreener: https://dexscreener.com/solana/%s", token, token)
				log.Println(telegramMessage)
			}
		}
		graduatedTokenCache.Unlock()
	}
}

func init() {
	go ValidateCachedTokens()
}

func CheckLiquidityLock(mintAddress string) (bool, error) {
	url := fmt.Sprintf("https://api.dexscreener.com/tokens/v1/solana/%s/liquidity", mintAddress)
	log.Printf("Checking liquidity lock for %s at %s", mintAddress, url)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Error: Liquidity check API request failed for %s: %v", mintAddress, err)
		return false, fmt.Errorf("API request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 {
		log.Printf("Info: Liquidity lock info not available via DexScreener for %s (404). Treating as unlocked.", mintAddress)
		return false, nil
	} else if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("Error: Liquidity check API request failed for %s with status: %s. Body: %s", mintAddress, resp.Status, string(bodyBytes))
		return false, fmt.Errorf("API request failed with status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error: reading liquidity API response for %s: %v", mintAddress, err)
		return false, fmt.Errorf("error reading API response: %v", err)
	}

	var liquidityData []struct {
		PairAddress string `json:"pairAddress"`
		Liquidity   struct {
			Usd   float64 `json:"usd"`
			Base  float64 `json:"base"`
			Quote float64 `json:"quote"`
		} `json:"liquidity"`
	}

	err = json.Unmarshal(body, &liquidityData)
	if err != nil {
		log.Printf("Error: JSON parsing failed for liquidity response %s: %v. Raw: %s", mintAddress, err, string(body))
		return false, fmt.Errorf("JSON parsing failed: %v", err)
	}

	if len(liquidityData) == 0 {
		log.Printf("Info: No liquidity data array returned from DexScreener for token: %s. Treating as unlocked.", mintAddress)
		return false, nil
	}

	pair := liquidityData[0]
	isLocked := pair.Liquidity.Usd > 1000

	if isLocked {
		log.Printf("Info: Liquidity appears LOCKED for token: %s (>$1000 USD: %.2f)", mintAddress, pair.Liquidity.Usd)
	} else {
		log.Printf("Info: Liquidity IS NOT locked for token: %s (<=$1000 USD: %.2f)", mintAddress, pair.Liquidity.Usd)
	}
	return isLocked, nil
}
