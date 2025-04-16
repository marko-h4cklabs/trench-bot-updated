package services

import (
	"bytes"
	"ca-scraper/agent/internal/events"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

type TrackedTokenInfo struct {
	BaselineMarketCap float64
	AddedAt           time.Time
}

var trackedProgressCache = struct {
	sync.Mutex
	Data map[string]TrackedTokenInfo
}{Data: make(map[string]TrackedTokenInfo)}

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

func SetupGraduationWebhook(webhookURL string, appLogger *logger.Logger) error {
	appLogger.Info("Setting up Graduation Webhook...", zap.String("url", webhookURL))

	apiKey := env.HeliusAPIKey
	webhookSecret := env.WebhookSecret
	authHeader := env.HeliusAuthHeader
	pumpFunAuthority := env.PumpFunAuthority

	if apiKey == "" {
		appLogger.Fatal("HELIUS_API_KEY missing!")
		return fmt.Errorf("missing HELIUS_API_KEY")
	}
	if pumpFunAuthority == "" {
		appLogger.Warn("PUMPFUN_AUTHORITY_ADDRESS missing!")
	}
	if webhookSecret == "" {
		appLogger.Warn("WEBHOOK_SECRET missing!")
	}
	if authHeader == "" {
		appLogger.Info("HELIUS_AUTH_HEADER empty.")
	}

	appLogger.Info("Checking for existing Graduation Helius Webhook...")
	existingWebhook, err := CheckExistingHeliusWebhook(webhookURL, appLogger)
	if err != nil {
		appLogger.Error("Failed check for existing graduation webhook, attempting creation regardless.", zap.Error(err))
	}
	if existingWebhook {
		appLogger.Info("Graduation webhook already exists.", zap.String("url", webhookURL))
		return nil
	}

	appLogger.Info("Creating new graduation webhook...")
	requestBody := WebhookRequest{
		WebhookURL:       webhookURL,
		TransactionTypes: []string{"TRANSFER", "SWAP"},
		AccountAddresses: []string{pumpFunAuthority},
		WebhookType:      "enhanced",
		AuthHeader:       authHeader,
	}

	if pumpFunAuthority == "" {
		appLogger.Error("Cannot create graduation webhook without PUMPFUN_AUTHORITY_ADDRESS.")
		return fmt.Errorf("PUMPFUN_AUTHORITY_ADDRESS is required for graduation webhook")
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		appLogger.Error("Failed to serialize graduation webhook request", zap.Error(err))
		return err
	}

	url := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	if err != nil {
		appLogger.Error("Failed to create graduation webhook request object", zap.Error(err))
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	if webhookSecret != "" {
		req.Header.Set("Authorization", "Bearer "+webhookSecret)
		appLogger.Info("Using Authorization Bearer token for Helius graduation webhook creation.")
	} else {
		appLogger.Warn("WEBHOOK_SECRET missing. Helius graduation webhook creation might fail if authentication is required.")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		appLogger.Error("Failed to send graduation webhook request", zap.Error(err))
		return err
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	responseBodyStr := ""
	if readErr == nil {
		responseBodyStr = string(body)
	} else {
		appLogger.Warn("Failed to read graduation webhook creation response body", zap.Error(readErr))
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		appLogger.Info("Graduation webhook created successfully", zap.String("url", webhookURL), zap.Int("status", resp.StatusCode))
		return nil
	} else {
		appLogger.Error("Failed to create graduation webhook.",
			zap.Int("status", resp.StatusCode),
			zap.String("response", responseBodyStr))
		return fmt.Errorf("failed to create graduation webhook: status %d, response body: %s", resp.StatusCode, responseBodyStr)
	}
}

func HandleWebhook(payload []byte, appLogger *logger.Logger) {
	appLogger.Debug("Received Graduation Webhook Payload", zap.Int("size", len(payload)))
	if len(payload) == 0 {
		appLogger.Error("Empty graduation webhook payload received!")
		return
	}

	var eventsArray []map[string]interface{}
	if err := json.Unmarshal(payload, &eventsArray); err == nil {
		appLogger.Debug("Graduation webhook payload is an array.", zap.Int("count", len(eventsArray)))
		for i, event := range eventsArray {
			appLogger.Debug("Processing graduation event from array", zap.Int("index", i))
			processGraduatedToken(event, appLogger)
		}
		return
	}

	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		appLogger.Error("Failed to parse graduation webhook payload (neither array nor object)", zap.Error(err), zap.String("payload", string(payload)))
		return
	}
	appLogger.Debug("Graduation webhook payload is a single event object. Processing...")
	processGraduatedToken(event, appLogger)
}

func processGraduatedToken(event map[string]interface{}, appLogger *logger.Logger) {
	appLogger.Debug("Processing single graduation event")

	tokenAddress, ok := events.ExtractNonSolMintFromEvent(event)
	if !ok {
		appLogger.Debug("Could not extract relevant non-SOL token from graduation event.")
		return
	}
	tokenField := zap.String("tokenAddress", tokenAddress)
	appLogger.Debug("Extracted token address from graduation event", tokenField)

	dexscreenerURL := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

	tokenCache.Lock()
	if _, exists := tokenCache.Tokens[tokenAddress]; exists {
		tokenCache.Unlock()
		appLogger.Info("Token already processed recently (graduation debounce), skipping.", tokenField)
		return
	}
	tokenCache.Tokens[tokenAddress] = time.Now()
	tokenCache.Unlock()
	appLogger.Info("Added token to graduation processing debounce cache", tokenField)

	validationResult, validationErr := IsTokenValid(tokenAddress, appLogger)

	if validationErr != nil {
		appLogger.Error("Error checking DexScreener criteria for graduated token", tokenField, zap.Error(validationErr))
		return
	}

	if validationResult == nil || !validationResult.IsValid {
		reason := "Unknown validation failure"
		if validationResult != nil && len(validationResult.FailReasons) > 0 {
			reason = strings.Join(validationResult.FailReasons, "; ")
		} else if validationResult != nil && !validationResult.IsValid {
			reason = "Did not meet criteria (no specific reasons returned)"
		}
		appLogger.Info("Graduated token failed DexScreener criteria", tokenField, zap.String("reason", reason))
		return
	}

	appLogger.Info("Graduated token passed validation! Preparing notification...", tokenField)

	dexscreenerURLEsc := notifications.EscapeMarkdownV2(dexscreenerURL)

	criteriaDetails := fmt.Sprintf(
		"🩸 Liquidity: `$%.0f`\n"+
			"🏛️ Market Cap: `$%.0f`\n"+
			"⌛ \\(5m\\) Volume : `$%.0f`\n"+
			"⏳ \\(1h\\) Volume : `$%.0f`\n"+
			"🔎 \\(5m\\) TXNs : `%d`\n"+
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
	for name, url := range validationResult.OtherSocials {
		if url != "" {
			emoji := "🔗"
			lowerName := strings.ToLower(name)
			if strings.Contains(lowerName, "discord") {
				emoji = "<:discord:10014198 discord icon emoji ID>"
			}
			if strings.Contains(lowerName, "medium") {
				emoji = "📰"
			}

			socialLinksBuilder.WriteString(fmt.Sprintf("%s %s: %s\n", emoji, notifications.EscapeMarkdownV2(name), notifications.EscapeMarkdownV2(url)))
			hasSocials = true
		}
	}
	socialsSection := ""
	if hasSocials {
		socialsSection = "\\-\\-\\- Socials \\-\\-\\-\n" + socialLinksBuilder.String()
	}

	var iconStatus string
	usePhoto := false
	if validationResult.ImageURL != "" {
		if _, urlErr := url.ParseRequestURI(validationResult.ImageURL); urlErr == nil && (strings.HasPrefix(validationResult.ImageURL, "http://") || strings.HasPrefix(validationResult.ImageURL, "https://")) {
			iconStatus = "✅ Icon Found"
			usePhoto = true
		} else {
			appLogger.Warn("Invalid ImageURL format received from DexScreener", tokenField, zap.String("url", validationResult.ImageURL))
			iconStatus = "⚠️ Icon URL Invalid"
			usePhoto = false
		}
	} else {
		iconStatus = "❌ Icon Missing"
		usePhoto = false
	}

	caption := fmt.Sprintf(
		"*Token Graduated & Validated\\!* 🚀\n\n"+
			"CA: `%s`\n"+
			"Icon: %s\n\n"+
			"DexScreener: %s\n\n"+
			"\\-\\-\\- Criteria Met \\-\\-\\-\n"+
			"%s\n\n"+
			"%s",
		tokenAddress,
		iconStatus,
		dexscreenerURLEsc,
		criteriaDetails,
		socialsSection,
	)
	caption = strings.TrimRight(caption, "\n")

	if usePhoto {
		notifications.SendBotCallPhotoMessage(validationResult.ImageURL, caption)
		appLogger.Info("Telegram 'Bot Call' photo notification initiated", tokenField)
	} else {
		notifications.SendBotCallMessage(caption)
		appLogger.Info("Telegram 'Bot Call' text notification initiated", tokenField)
	}

	graduatedTokenCache.Lock()
	graduatedTokenCache.Data[tokenAddress] = time.Now()
	graduatedTokenCache.Unlock()
	appLogger.Info("Added token to graduatedTokenCache", tokenField)

	if validationResult.MarketCap > 0 {
		mcField := zap.Float64("baselineMC", validationResult.MarketCap)
		trackedProgressCache.Lock()
		trackedProgressCache.Data[tokenAddress] = TrackedTokenInfo{
			BaselineMarketCap: validationResult.MarketCap,
			AddedAt:           time.Now(),
		}
		trackedProgressCache.Unlock()
		appLogger.Info("Added token to progress tracking cache", tokenField, mcField)
	} else {
		appLogger.Info("Token not added to progress tracking (Market Cap is zero)", tokenField)
	}
}

func CheckTokenProgress(appLogger *logger.Logger) {
	checkInterval := 10 * time.Minute
	appLogger.Info("Token progress tracking routine started", zap.Duration("interval", checkInterval))
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for range ticker.C {
		appLogger.Debug("Running token progress check cycle...")

		tokensToCheck := make(map[string]TrackedTokenInfo)
		trackedProgressCache.Lock()
		for addr, info := range trackedProgressCache.Data {
			tokensToCheck[addr] = info
		}
		trackedProgressCache.Unlock()

		count := len(tokensToCheck)
		if count > 0 {
			appLogger.Info("Checking progress for tokens", zap.Int("count", count))
		} else {
			appLogger.Debug("No tokens currently in progress tracking cache.")
			continue
		}

		tokensToRemove := []string{}

		for tokenAddress, trackedInfo := range tokensToCheck {
			tokenField := zap.String("tokenAddress", tokenAddress)
			appLogger.Debug("Checking specific token progress", tokenField)

			currentValidationResult, err := IsTokenValid(tokenAddress, appLogger)

			if err != nil {
				appLogger.Warn("Error fetching current data during progress check", tokenField, zap.Error(err))
				continue
			}

			if currentValidationResult != nil && currentValidationResult.MarketCap > 0 && trackedInfo.BaselineMarketCap > 0 {
				currentMarketCap := currentValidationResult.MarketCap
				baselineMarketCap := trackedInfo.BaselineMarketCap
				mcBaselineField := zap.Float64("baselineMC", baselineMarketCap)
				mcCurrentField := zap.Float64("currentMC", currentMarketCap)
				multiplier := 2.0

				if currentMarketCap >= (baselineMarketCap * multiplier) {
					appLogger.Info("Token hit target market cap multiplier!", tokenField, mcBaselineField, mcCurrentField, zap.Float64("multiplier", multiplier))

					progressMessage := fmt.Sprintf(
						"🚀 Token `%s` just did *%.1fx* from verification\\!\n\n"+
							"Initial MC: `$%.0f`\n"+
							"Current MC: `$%.0f`\n\n"+
							"DexScreener: %s",
						tokenAddress,
						currentMarketCap/baselineMarketCap,
						baselineMarketCap,
						currentMarketCap,
						notifications.EscapeMarkdownV2(fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)),
					)

					notifications.SendTrackingUpdateMessage(progressMessage)
					appLogger.Info("Sent tracking update notification", tokenField)

					tokensToRemove = append(tokensToRemove, tokenAddress)

				} else {
					appLogger.Debug("Token progress check: Target condition not met.", tokenField, mcBaselineField, mcCurrentField)
				}
			} else {
				appLogger.Debug("Token progress check: Market cap is zero, baseline missing, or validation result nil.", tokenField, zap.Bool("hasResult", currentValidationResult != nil))
			}
			time.Sleep(100 * time.Millisecond)
		}
		if len(tokensToRemove) > 0 {
			trackedProgressCache.Lock()
			for _, addr := range tokensToRemove {
				delete(trackedProgressCache.Data, addr)
				appLogger.Info("Removed token from progress tracking cache", zap.String("tokenAddress", addr), zap.String("reason", "hit_target"))
			}
			trackedProgressCache.Unlock()
		}
		appLogger.Debug("Token progress check cycle finished.")
	}
}
