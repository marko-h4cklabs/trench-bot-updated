package services

import (
	"bytes"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
	"encoding/json"
	"fmt"
	"io"
	"log"

	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

type WebhookRequest struct {
	WebhookURL       string   `json:"webhookURL"`
	TransactionTypes []string `json:"transactionTypes"`
	AccountAddresses []string `json:"accountAddresses"`
	WebhookType      string   `json:"webhookType"`
	TxnStatus        string   `json:"txnStatus,omitempty"`
	AuthHeader       string   `json:"authHeader"`
}

var graduatedTokenCache = struct {
	sync.Mutex
	Data map[string]time.Time
}{Data: make(map[string]time.Time)}

var tokenCache = struct {
	sync.Mutex
	Tokens map[string]time.Time
}{Tokens: make(map[string]time.Time)}

func SetupGraduationWebhook(webhookURL string, log *logger.Logger) error {
	log.Info("Setting up Graduation Webhook...")

	apiKey := env.HeliusAPIKey
	webhookSecret := env.WebhookSecret
	authHeader := env.HeliusAuthHeader
	pumpFunAuthority := env.PumpFunAuthority
	if apiKey == "" {
		log.Fatal("FATAL: HELIUS_API_KEY missing!")
		return fmt.Errorf("missing HELIUS_API_KEY")
	}
	if pumpFunAuthority == "" {
		log.Warn("PUMPFUN_AUTHORITY_ADDRESS missing!")
		return fmt.Errorf("missing PUMPFUN_AUTHORITY_ADDRESS")
	}
	if webhookSecret == "" {
		log.Warn("WEBHOOK_SECRET missing!")
	}
	if authHeader == "" {
		log.Info("HELIUS_AUTH_HEADER empty.")
	}

	log.Info("Checking for existing Graduation Helius Webhook...")
	existingWebhook, err := CheckExistingHeliusWebhook(webhookURL)
	if err != nil {
		log.Error("Failed check for existing webhook, attempting creation regardless.", zap.Error(err))
	}
	if existingWebhook {
		log.Info("Graduation webhook already exists.", zap.String("url", webhookURL))
		return nil
	}

	log.Info("Creating new graduation webhook...")
	requestBody := WebhookRequest{
		WebhookURL:       webhookURL,
		TransactionTypes: []string{"TRANSFER", "SWAP"},
		AccountAddresses: []string{pumpFunAuthority},
		WebhookType:      "enhanced",
		AuthHeader:       authHeader,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		log.Error("Failed to serialize webhook request", zap.Error(err))
		return err
	}

	url := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		log.Error("Failed to create webhook request", zap.Error(err))
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	if webhookSecret != "" {
		req.Header.Set("Authorization", "Bearer "+webhookSecret)
	} else {
		log.Warn("Cannot set Authorization header for webhook creation: WEBHOOK_SECRET missing.")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Error("Failed to send webhook request", zap.Error(err))
		return err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		log.Warn("Failed to read webhook creation response body", zap.Error(readErr))
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Info("Webhook created successfully", zap.String("url", webhookURL))
		return nil
	} else {
		log.Error("Failed to create graduation webhook.",
			zap.Int("status", resp.StatusCode),
			zap.String("response", string(body)))

		return fmt.Errorf("failed to create graduation webhook: status %d, response body: %s", resp.StatusCode, string(body))
	}
}

func HandleWebhook(payload []byte, log *logger.Logger) {
	log.Debug("Received Webhook Payload", zap.Int("size", len(payload)))
	if len(payload) == 0 {
		log.Error("Empty webhook payload received!")
		return
	}

	var eventsArray []map[string]interface{}
	if err := json.Unmarshal(payload, &eventsArray); err == nil {
		log.Debug("Webhook payload is an array.", zap.Int("count", len(eventsArray)))
		for i, event := range eventsArray {
			log.Debug("Processing event from array", zap.Int("index", i))
			processGraduatedToken(event, log)
		}
		return
	}

	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		log.Error("Failed to parse webhook payload (neither array nor object)", zap.Error(err), zap.String("payload", string(payload)))
		return
	}
	log.Debug("Webhook payload is a single event object. Processing...")
	processGraduatedToken(event, log)
}

func processGraduatedToken(event map[string]interface{}, log *logger.Logger) {
	log.Debug("Processing event", zap.Any("event", event))

	tokenAddress, ok := extractGraduatedToken(event)
	if !ok {
		return
	}

	dexscreenerURL := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

	tokenCache.Lock()
	if _, exists := tokenCache.Tokens[tokenAddress]; exists {
		tokenCache.Unlock()
		log.Info("Token already processed recently, skipping.", zap.String("tokenAddress", tokenAddress))
		return
	}
	tokenCache.Tokens[tokenAddress] = time.Now()
	tokenCache.Unlock()
	log.Info("Added token to processing debounce cache", zap.String("tokenAddress", tokenAddress))
	isLocked, lockedErr := CheckLiquidityLock(tokenAddress)
	isValid, validationErr := IsTokenValid(tokenAddress)
	lockStatusStr := "Liquidity Locked (>1k): Unknown"
	if lockedErr != nil {
		lockStatusStr = "Liquidity Locked (>1k): Check Failed"
		log.Warn("Failed liquidity lock check", zap.String("token", tokenAddress), zap.Error(lockedErr))
	} else {
		lockStatusStr = fmt.Sprintf("Liquidity Locked (>1k): %t", isLocked)
	}
	log.Info("Liquidity lock check result", zap.String("token", tokenAddress), zap.String("status", lockStatusStr), zap.Bool("isLocked", isLocked))

	if validationErr != nil {
		log.Error("Error checking DexScreener criteria", zap.String("token", tokenAddress), zap.Error(validationErr))
		return
	}
	if !isValid {
		log.Info("Token failed DexScreener criteria", zap.String("token", tokenAddress))
		return
	}

	log.Info("Token passed validation! Preparing notification...", zap.String("token", tokenAddress))
	telegramMessage := fmt.Sprintf(
		"🎓 Token Graduated & Validated! 🎓\n\nCA: `%s`\n\nDexScreener: %s\n\n--- Info ---\n🔹 %s",
		tokenAddress, dexscreenerURL, lockStatusStr,
	)
	notifications.SendTelegramMessage(telegramMessage)
	log.Info("Telegram notification initiated", zap.String("token", tokenAddress))
	graduatedTokenCache.Lock()
	graduatedTokenCache.Data[tokenAddress] = time.Now()
	graduatedTokenCache.Unlock()
	log.Info("Added token to graduatedTokenCache", zap.String("token", tokenAddress))
	TrackGraduatedToken(tokenAddress)

}

func extractGraduatedToken(event map[string]interface{}) (string, bool) {
	if transfers, hasTransfers := event["tokenTransfers"].([]interface{}); hasTransfers {
		for _, transfer := range transfers {
			if transferMap, ok := transfer.(map[string]interface{}); ok {
				mint, mintOk := transferMap["mint"].(string)
				if mintOk && mint != "" && mint != "So11111111111111111111111111111111111111112" {
					log.Printf("Extracted Token Address from tokenTransfers: %s", mint)
					return mint, true
				}
			}
		}
	}
	if events, hasEvents := event["events"].(map[string]interface{}); hasEvents {
		if tokenMint, ok := extractTokenFromEvents(events); ok {
			log.Printf("Extracted Token Address from Events: %s", tokenMint)
			return tokenMint, true
		}
	}
	if transaction, hasTransaction := event["transaction"].(map[string]interface{}); hasTransaction {
		if tokenMint, ok := extractTokenFromTransaction(transaction); ok {
			log.Printf("Extracted Token Address from Transaction: %s", tokenMint)
			return tokenMint, true
		}
	}
	log.Println("Could not extract target token address from graduation event.")
	return "", false
}

func extractTokenFromTransaction(transaction map[string]interface{}) (string, bool) {
	if events, hasEvents := transaction["events"].(map[string]interface{}); hasEvents {
		return extractTokenFromEvents(events)
	}
	return "", false
}

func extractTokenFromEvents(events map[string]interface{}) (string, bool) {
	if swapEvent, hasSwap := events["swap"].(map[string]interface{}); hasSwap {
		return extractTokenFromSwap(swapEvent)
	}
	return "", false
}

func extractTokenFromSwap(swapEvent map[string]interface{}) (string, bool) {
	if tokenOutputs, has := swapEvent["tokenOutputs"].([]interface{}); has {
		for _, output := range tokenOutputs {
			if outputMap, ok := output.(map[string]interface{}); ok {
				if mint, mintOk := outputMap["mint"].(string); mintOk && mint != "" && mint != "So11111111111111111111111111111111111111112" {
					log.Printf("Found non-SOL token from tokenOutputs: %s", mint)
					return mint, true
				}
			}
		}
	}
	if tokenInputs, has := swapEvent["tokenInputs"].([]interface{}); has {
		for _, input := range tokenInputs {
			if inputMap, ok := input.(map[string]interface{}); ok {
				if mint, mintOk := inputMap["mint"].(string); mintOk && mint != "" && mint != "So11111111111111111111111111111111111111112" {
					log.Printf("Found non-SOL token from tokenInputs: %s", mint)
					return mint, true
				}
			}
		}
	}
	log.Println("Could not extract non-SOL token address from swap event.")
	return "", false
}

func ValidateCachedTokens() {
	validationInterval := 5 * time.Minute
	cacheExpiry := 30 * time.Minute
	log.Printf("Periodic graduated token validation routine started (Interval: %v, Expiry: %v)", validationInterval, cacheExpiry)
	ticker := time.NewTicker(validationInterval)
	defer ticker.Stop()
	for range ticker.C {
		log.Println("Running periodic graduated token validation check...")
		tokensToRemove := []string{}
		tokensToValidate := []string{}
		graduatedTokenCache.Lock()
		for token, addedTime := range graduatedTokenCache.Data {
			if time.Since(addedTime) > cacheExpiry {
				tokensToRemove = append(tokensToRemove, token)
			} else {
				tokensToValidate = append(tokensToValidate, token)
			}
		}
		for _, token := range tokensToRemove {
			delete(graduatedTokenCache.Data, token)
			log.Printf("[DEBUG] Removed expired token: %s", token)
		}
		graduatedTokenCache.Unlock()
		log.Printf("Checking %d non-expired graduated tokens.", len(tokensToValidate))
		validatedCount := 0
		for _, token := range tokensToValidate {
			time.Sleep(500 * time.Millisecond)
			isValid, err := IsTokenValid(token)
			if err != nil {
				log.Printf("[WARN] Error validating %s: %v", token, err)
				continue
			}
			if isValid {
				validatedCount++
				log.Printf("[INFO] Token %s remains valid.", token)
			} else {
				log.Printf("[INFO] Token %s no longer valid.", token)
			}
		}
		log.Printf("Periodic validation check complete. Validated now: %d", validatedCount)
	}
}

func init() {
	log.Println("Initializing Graduation Service background tasks...")
}

func CheckLiquidityLock(mintAddress string) (bool, error) {
	url := fmt.Sprintf("https://api.dexscreener.com/v1/solana/tokens/%s", mintAddress)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Error: Liquidity check API request failed for %s: %v", mintAddress, err)
		return false, fmt.Errorf("API req failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		log.Printf("Info: Liquidity info N/A via DexScreener for %s (404).", mintAddress)
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("Error: Liquidity check API req failed for %s status: %s. Body: %s", mintAddress, resp.Status, string(bodyBytes))
		return false, fmt.Errorf("API req failed status: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading liquidity API response for %s: %v", mintAddress, err)
		return false, fmt.Errorf("err reading API resp: %v", err)
	}
	var tokenInfo struct {
		Pairs []struct {
			Liquidity *struct {
				Usd float64 `json:"usd"`
			} `json:"liquidity"`
		} `json:"pairs"`
	}
	err = json.Unmarshal(body, &tokenInfo)
	if err != nil {
		log.Printf("Error: JSON parsing failed for liquidity resp %s: %v. Raw: %s", mintAddress, err, string(body))
		return false, fmt.Errorf("JSON parsing failed: %v", err)
	}
	if len(tokenInfo.Pairs) == 0 {
		log.Printf("Info: No pairs in DexScreener info for %s.", mintAddress)
		return false, nil
	}
	highestLiquidity := 0.0
	foundLiquidity := false
	for _, pair := range tokenInfo.Pairs {
		if pair.Liquidity != nil {
			foundLiquidity = true
			if pair.Liquidity.Usd > highestLiquidity {
				highestLiquidity = pair.Liquidity.Usd
			}
		}
	}
	if !foundLiquidity {
		log.Printf("Info: No liquidity data in pairs for %s.", mintAddress)
		return false, nil
	}
	const liquidityLockThreshold = 1000.0
	isLocked := highestLiquidity > liquidityLockThreshold
	return isLocked, nil
}
