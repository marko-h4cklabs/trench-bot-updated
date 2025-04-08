package services

import (
	"bytes"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
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
		log.Info("Using Authorization Bearer token for Helius webhook creation.")
	} else {
		log.Warn("WEBHOOK_SECRET missing. Helius webhook creation might fail if authentication is required.")
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
		log.Info("Webhook created successfully", zap.String("url", webhookURL), zap.Int("status", resp.StatusCode))

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
	lockStatusStr := "Liquidity Locked (>1k): Unknown"
	if lockedErr != nil {
		lockStatusStr = fmt.Sprintf("Liquidity Locked (>1k): Check Failed (%s)", lockedErr.Error())
		log.Warn("Failed liquidity lock check", zap.String("token", tokenAddress), zap.Error(lockedErr))
	} else {
		lockStatusStr = fmt.Sprintf("Liquidity Locked (>1k): %t", isLocked)
	}
	log.Info("Liquidity lock check result", zap.String("token", tokenAddress), zap.String("status", lockStatusStr), zap.Bool("isLocked", isLocked))

	validationResult, validationErr := IsTokenValid(tokenAddress)

	if validationErr != nil {
		log.Error("Error checking DexScreener criteria", zap.String("token", tokenAddress), zap.Error(validationErr))

		return
	}

	if validationResult == nil || !validationResult.IsValid {
		reason := "Unknown validation failure"
		if validationResult != nil && len(validationResult.FailReasons) > 0 {
			reason = strings.Join(validationResult.FailReasons, "; ")
		} else if validationResult != nil && !validationResult.IsValid {
			reason = "Did not meet criteria (no specific reasons returned)"
		}
		log.Info("Token failed DexScreener criteria", zap.String("token", tokenAddress), zap.String("reason", reason))

		return
	}

	log.Info("Token passed validation! Preparing notification...", zap.String("token", tokenAddress))

	dexscreenerURLEsc := notifications.EscapeMarkdownV2(dexscreenerURL)
	lockStatusStrEsc := notifications.EscapeMarkdownV2(lockStatusStr)

	criteriaDetails := fmt.Sprintf(
		"Liquidity: `$%.2f` \\(Min: `$%.2f`\\)\n"+
			"Market Cap: `$%.2f` \\(Range: `$%.2f` \\- `$%.2f`\\)\n"+
			"Volume \\(5m\\): `$%.2f` \\(Min: `$%.2f`\\)\n"+
			"Volume \\(1h\\): `$%.2f` \\(Min: `$%.2f`\\)\n"+
			"TXNs \\(5m\\): `%d` \\(Min: `%d`\\)\n"+
			"TXNs \\(1h\\): `%d` \\(Min: `%d`\\)",
		validationResult.LiquidityUSD, minLiquidity,
		validationResult.MarketCap, minMarketCap, maxMarketCap,
		validationResult.Volume5m, min5mVolume,
		validationResult.Volume1h, min1hVolume,
		validationResult.Txns5m, min5mTx,
		validationResult.Txns1h, min1hTx,
	)

	telegramMessage := fmt.Sprintf(
		"*Token Graduated & Validated\\!* \n\n"+
			"CA: `%s`\n\n"+
			"DexScreener: %s\n\n"+
			"\\-\\-\\- Criteria Met \\-\\-\\-\n"+
			"%s\n\n"+
			"\\-\\-\\- Info \\-\\-\\-\n"+
			"%s",
		tokenAddress,
		dexscreenerURLEsc,
		criteriaDetails,
		lockStatusStrEsc,
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
				amountStr, amountOk := transferMap["tokenAmount"].(float64)
				userAccount, _ := transferMap["toUserAccount"].(string)

				if mintOk && mint != "" && mint != "So11111111111111111111111111111111111111112" && amountOk && amountStr > 0 {

					log.Printf("Extracted Token Address '%s' from tokenTransfers (Amount: %f, To: %s)", mint, amountStr, userAccount)
					return mint, true
				}
			}
		}
	}

	if events, hasEvents := event["events"].(map[string]interface{}); hasEvents {
		if swapEvent, hasSwap := events["swap"].(map[string]interface{}); hasSwap {

			if tokenOutputs, has := swapEvent["tokenOutputs"].([]interface{}); has {
				for _, output := range tokenOutputs {
					if outputMap, ok := output.(map[string]interface{}); ok {
						mint, mintOk := outputMap["mint"].(string)
						amount, amountOk := outputMap["tokenAmount"].(float64)
						if mintOk && mint != "" && mint != "So11111111111111111111111111111111111111112" && amountOk && amount > 0 {
							log.Printf("Extracted Token Address '%s' from events.swap.tokenOutputs", mint)
							return mint, true
						}
					}
				}
			}

			if tokenInputs, has := swapEvent["tokenInputs"].([]interface{}); has {
				for _, input := range tokenInputs {
					if inputMap, ok := input.(map[string]interface{}); ok {
						mint, mintOk := inputMap["mint"].(string)
						amount, amountOk := inputMap["tokenAmount"].(float64)
						if mintOk && mint != "" && mint != "So11111111111111111111111111111111111111112" && amountOk && amount > 0 {
							log.Printf("Extracted Token Address '%s' from events.swap.tokenInputs", mint)
							return mint, true
						}
					}
				}
			}
		}
	}

	log.Println("Could not extract target token address from graduation event.")
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
		now := time.Now()
		for token, addedTime := range graduatedTokenCache.Data {
			if now.Sub(addedTime) > cacheExpiry {
				tokensToRemove = append(tokensToRemove, token)
			} else {
				tokensToValidate = append(tokensToValidate, token)
			}
		}

		for _, token := range tokensToRemove {
			delete(graduatedTokenCache.Data, token)
			log.Printf("[DEBUG] Removed expired token from graduatedTokenCache: %s", token)
		}
		graduatedTokenCache.Unlock()

		log.Printf("Checking %d non-expired graduated tokens.", len(tokensToValidate))
		validatedCount := 0
		failedCount := 0

		for _, token := range tokensToValidate {

			validationResult, err := IsTokenValid(token)

			if err != nil {
				log.Printf("[WARN] Error validating %s during periodic check: %v", token, err)
				failedCount++
				continue
			}

			if validationResult != nil && validationResult.IsValid {
				validatedCount++
				log.Printf("[INFO] Token %s remains valid during periodic check.", token)

			} else {
				failedCount++
				reason := "Unknown reason"
				if validationResult != nil && len(validationResult.FailReasons) > 0 {
					reason = strings.Join(validationResult.FailReasons, "; ")
				} else if validationResult != nil && !validationResult.IsValid {
					reason = "Did not meet criteria"
				}

				log.Printf("[INFO] Token %s no longer valid during periodic check. Reason: %s", token, reason)

			}
		}
		log.Printf("Periodic validation check complete. Valid now: %d, Invalid/Error: %d", validatedCount, failedCount)
	}
}

func init() {
	log.Println("Initializing Graduation Service background tasks...")

}

func CheckLiquidityLock(mintAddress string) (bool, error) {

	if err := dexScreenerLimiter.Wait(context.Background()); err != nil {

		log.Printf("ERROR: DexScreener rate limiter wait error during *liquidity check* for %s: %v", mintAddress, err)
		return false, fmt.Errorf("rate limiter error during liquidity check for %s: %w", mintAddress, err)
	}

	url := fmt.Sprintf("https://api.dexscreener.com/v1/solana/tokens/%s", mintAddress)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Error: Liquidity check API request failed for %s: %v", mintAddress, err)
		return false, fmt.Errorf("API request failed for %s: %w", mintAddress, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		log.Printf("Rate limit hit (429) during liquidity check for %s.", mintAddress)
		return false, fmt.Errorf("rate limit exceeded (429)")
	} else if resp.StatusCode == http.StatusNotFound {

		log.Printf("Info: Liquidity info N/A via DexScreener token endpoint for %s (404). Assuming not locked.", mintAddress)
		return false, nil
	} else if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		errorMsg := fmt.Sprintf("Liquidity check API request failed for %s status: %s", mintAddress, resp.Status)
		if readErr == nil && len(bodyBytes) > 0 {
			errorMsg += fmt.Sprintf(". Body: %s", string(bodyBytes))
		} else if readErr != nil {
			errorMsg += fmt.Sprintf(". Failed to read response body: %v", readErr)
		}
		log.Println(errorMsg)
		return false, fmt.Errorf("API request failed status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading liquidity API response body for %s: %v", mintAddress, err)
		return false, fmt.Errorf("error reading API response for %s: %w", mintAddress, err)
	}

	var tokenInfo struct {
		Pairs []struct {
			PairAddress string     `json:"pairAddress"`
			Liquidity   *Liquidity `json:"liquidity"`
		} `json:"pairs"`
	}

	err = json.Unmarshal(body, &tokenInfo)
	if err != nil {

		log.Printf("Error: JSON parsing failed for liquidity response %s: %v. Raw Response: %s", mintAddress, err, string(body))
		return false, fmt.Errorf("JSON parsing failed for %s: %w", mintAddress, err)
	}

	if len(tokenInfo.Pairs) == 0 {
		log.Printf("Info: No pairs found in DexScreener token info for %s. Assuming not locked.", mintAddress)
		return false, nil
	}

	highestLiquidity := 0.0
	foundLiquidityData := false
	for _, pair := range tokenInfo.Pairs {
		if pair.Liquidity != nil && pair.Liquidity.Usd > 0 {
			foundLiquidityData = true
			if pair.Liquidity.Usd > highestLiquidity {
				highestLiquidity = pair.Liquidity.Usd
			}
		} else {

		}
	}

	if !foundLiquidityData {
		log.Printf("Info: Pairs found for %s, but none contained valid liquidity data. Assuming not locked.", mintAddress)
		return false, nil
	}

	const liquidityLockThreshold = 1000.0
	isLocked := highestLiquidity > liquidityLockThreshold

	log.Printf("Info: Liquidity lock status for %s: %t (Highest Liq: $%.2f, Threshold: $%.2f)",
		mintAddress, isLocked, highestLiquidity, liquidityLockThreshold)

	return isLocked, nil
}
