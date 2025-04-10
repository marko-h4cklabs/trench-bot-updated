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

	criteriaDetails := fmt.Sprintf(
		"🩸 Liquidity: `$%.0f` \n"+
			"🏛️ Market Cap: `$%.0f` \n"+
			"⌛ \\(5m\\) Volume : `$%.0f` \n"+
			"⏳ \\(1h\\) Volume : `$%.0f` \n"+
			"🔎 \\(5m\\) TXNs : `%d` \n"+
			"🔍 \\(1h\\) TXNs : `%d`",
		validationResult.LiquidityUSD,
		validationResult.MarketCap,
		validationResult.Volume5m,
		validationResult.Volume1h,
		validationResult.Txns5m,
		validationResult.Txns1h,
	)

	var socialLinksBuilder strings.Builder
	hasSocials := false

	if validationResult.WebsiteURL != "" {
		socialLinksBuilder.WriteString(fmt.Sprintf("🌐 Website: %s\n", notifications.EscapeMarkdownV2(validationResult.WebsiteURL)))
		hasSocials = true
	}
	if validationResult.TwitterURL != "" {
		socialLinksBuilder.WriteString(fmt.Sprintf("🐦 Twitter: %s\n", notifications.EscapeMarkdownV2(validationResult.TwitterURL)))
		hasSocials = true
	}
	if validationResult.TelegramURL != "" {
		socialLinksBuilder.WriteString(fmt.Sprintf("✈️ Telegram: %s\n", notifications.EscapeMarkdownV2(validationResult.TelegramURL)))
		hasSocials = true
	}

	socialsSection := ""
	if hasSocials {
		socialsSection = "\\-\\-\\- Socials \\-\\-\\-\n" + socialLinksBuilder.String()
	}

	caption := fmt.Sprintf(
		"*Token Graduated & Validated\\!* \n\n"+
			"CA: `%s`\n\n"+
			"DexScreener: %s\n\n"+
			"\\-\\-\\- Criteria Met \\-\\-\\-\n"+
			"%s\n\n"+
			"%s",
		tokenAddress,
		dexscreenerURLEsc,
		criteriaDetails,
		socialsSection,
	)

	caption = strings.TrimRight(caption, "\n")

	if validationResult.ImageURL != "" {
		notifications.SendPhotoMessage(validationResult.ImageURL, caption)
		log.Info("Telegram photo notification initiated", zap.String("token", tokenAddress))
	} else {
		notifications.SendTelegramMessage(caption)
		log.Warn("Token image URL not found, sending text notification", zap.String("token", tokenAddress))
		log.Info("Telegram text notification initiated", zap.String("token", tokenAddress))
	}

	time.Sleep(1 * time.Second)
	notifications.SendTelegramMessage(tokenAddress)
	log.Info("Follow-up CA message initiated.", zap.String("token", tokenAddress))

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

func init() {
	log.Println("Initializing Graduation Service background tasks...")
}
