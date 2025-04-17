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
	"math" // <-- Import math package
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// NEW: Struct to hold tracked token info including baseline market cap
type TrackedTokenInfo struct {
	BaselineMarketCap float64
	AddedAt           time.Time
}

// NEW: Cache for tokens being tracked for progress
var trackedProgressCache = struct {
	sync.Mutex
	Data map[string]TrackedTokenInfo // Map CA -> TrackedTokenInfo
}{Data: make(map[string]TrackedTokenInfo)}

// --- Existing variables ---
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

// --- Existing SetupGraduationWebhook function (unchanged) ---
func SetupGraduationWebhook(webhookURL string, appLogger *logger.Logger) error {
	// ... (existing code remains the same) ...
	appLogger.Info("Setting up Graduation Webhook...", zap.String("url", webhookURL))

	apiKey := env.HeliusAPIKey
	webhookSecret := env.WebhookSecret
	authHeader := env.HeliusAuthHeader
	pumpFunAuthority := env.PumpFunAuthority
	raydiumAddressesStr := env.RaydiumAccountAddresses

	if apiKey == "" {
		appLogger.Error("HELIUS_API_KEY missing! Cannot set up webhook.")
		return fmt.Errorf("missing HELIUS_API_KEY")
	}
	if webhookURL == "" {
		appLogger.Error("Webhook URL is empty! Cannot set up webhook.")
		return fmt.Errorf("webhookURL provided is empty")
	}
	if pumpFunAuthority == "" {
		appLogger.Warn("PUMPFUN_AUTHORITY_ADDRESS missing!")
	}
	if raydiumAddressesStr == "" {
		appLogger.Warn("RAYDIUM_ACCOUNT_ADDRESSES missing or empty!")
	}
	if webhookSecret == "" {
		appLogger.Warn("WEBHOOK_SECRET missing! Authorization might fail if required.")
	}
	if authHeader == "" {
		appLogger.Info("HELIUS_AUTH_HEADER is not set (this is often optional).")
	}
	addressesToMonitor := []string{}
	if pumpFunAuthority != "" {
		addressesToMonitor = append(addressesToMonitor, pumpFunAuthority)
		appLogger.Info("Adding PumpFun authority address to webhook monitor list.", zap.String("address", pumpFunAuthority))
	}

	if raydiumAddressesStr != "" {
		parsedRaydiumAddrs := strings.Split(raydiumAddressesStr, ",")
		count := 0
		for _, addr := range parsedRaydiumAddrs {
			trimmedAddr := strings.TrimSpace(addr)
			if trimmedAddr != "" {
				addressesToMonitor = append(addressesToMonitor, trimmedAddr)
				count++
			}
		}
		appLogger.Info("Adding Raydium addresses to webhook monitor list.", zap.Int("count", count))
	}

	if len(addressesToMonitor) == 0 {
		appLogger.Error("Cannot create webhook: No addresses found to monitor (neither PUMPFUN_AUTHORITY_ADDRESS nor RAYDIUM_ACCOUNT_ADDRESSES provided/valid).")
		return fmt.Errorf("no addresses provided to monitor")
	}
	appLogger.Info("Total addresses to monitor in webhook", zap.Int("count", len(addressesToMonitor)))

	appLogger.Info("Checking for existing Helius Webhook for the specific URL...")
	existingWebhook, err := CheckExistingHeliusWebhook(webhookURL, appLogger)
	if err != nil {
		appLogger.Error("Failed check for existing webhook, attempting creation regardless.", zap.Error(err))
	}
	if existingWebhook {
		appLogger.Info("Webhook already exists for this URL. Skipping creation.", zap.String("url", webhookURL))
		appLogger.Warn("Existing webhook check passed, but address list might not be updated. Manual check/update via Helius dashboard or API recommended if addresses changed.")
		return nil
	}

	appLogger.Info("Creating new Helius webhook...")
	requestBody := WebhookRequest{
		WebhookURL:       webhookURL,
		TransactionTypes: []string{"TRANSFER", "SWAP"},
		AccountAddresses: addressesToMonitor,
		WebhookType:      "enhanced",
		AuthHeader:       authHeader,
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		appLogger.Error("Failed to serialize webhook request body", zap.Error(err))
		return fmt.Errorf("failed to serialize webhook request: %w", err)
	}

	apiURL := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		appLogger.Error("Failed to create webhook request object", zap.Error(err))
		return fmt.Errorf("failed to create webhook request object: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if webhookSecret != "" {
		appLogger.Info("Sending Helius API key via query parameter.")
	} else {
		appLogger.Warn("WEBHOOK_SECRET is missing. Ensure API key in URL is sufficient for authentication.")
	}

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		appLogger.Error("Failed to send webhook creation request to Helius", zap.Error(err))
		return fmt.Errorf("failed to send webhook creation request: %w", err)
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	responseBodyStr := ""
	if readErr == nil {
		responseBodyStr = string(body)
	} else {
		appLogger.Warn("Failed to read webhook creation response body", zap.Error(readErr))
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		appLogger.Info("Helius webhook created successfully",
			zap.String("url", webhookURL),
			zap.Int("status", resp.StatusCode),
			zap.Int("monitored_address_count", len(addressesToMonitor)))
		return nil
	} else {
		appLogger.Error("Failed to create Helius webhook.",
			zap.Int("status", resp.StatusCode),
			zap.String("response", responseBodyStr))
		return fmt.Errorf("failed to create helius webhook: status %d, response body: %s", resp.StatusCode, responseBodyStr)
	}
}

// --- Existing HandleWebhook function (unchanged) ---
func HandleWebhook(payload []byte, appLogger *logger.Logger) {
	// ... (existing code remains the same) ...
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

// --- Modified processGraduatedToken function ---
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

	// Debounce check
	tokenCache.Lock()
	if _, exists := tokenCache.Tokens[tokenAddress]; exists {
		tokenCache.Unlock()
		appLogger.Info("Token already processed recently (graduation debounce), skipping.", tokenField)
		return
	}
	tokenCache.Tokens[tokenAddress] = time.Now()
	tokenCache.Unlock()
	appLogger.Info("Added token to graduation processing debounce cache", tokenField)

	// Validate against DexScreener
	validationResult, validationErr := IsTokenValid(tokenAddress, appLogger)

	if validationErr != nil {
		appLogger.Error("Error checking DexScreener criteria for graduated token", tokenField, zap.Error(validationErr))
		// Clean up debounce cache on error? Maybe not, let it expire naturally or succeed on retry
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
		// Clean up debounce cache on failure? Maybe not.
		return
	}

	// --- Token PASSED validation ---
	appLogger.Info("Graduated token passed validation! Preparing notification...", tokenField)

	dexscreenerURLEsc := notifications.EscapeMarkdownV2(dexscreenerURL)

	// Format criteria details
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

	// Format social links
	var socialLinksBuilder strings.Builder
	hasSocials := false
	// ... (existing social link building code remains the same) ...
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
			emoji := "🔗" // Default emoji
			lowerName := strings.ToLower(name)
			if strings.Contains(lowerName, "discord") {
				emoji = "<:discord:10014198 discord icon emoji ID>" // Replace with actual emoji or ID if needed
			}
			// Add more specific emojis if desired
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

	// Check icon status
	var iconStatus string
	usePhoto := false
	if validationResult.ImageURL != "" {
		if _, urlErr := url.ParseRequestURI(validationResult.ImageURL); urlErr == nil && (strings.HasPrefix(validationResult.ImageURL, "http://") || strings.HasPrefix(validationResult.ImageURL, "https://")) {
			iconStatus = "✅ Icon Found"
			usePhoto = true
		} else {
			appLogger.Warn("Invalid ImageURL format received from DexScreener", tokenField, zap.String("url", validationResult.ImageURL))
			iconStatus = "⚠️ Icon URL Invalid"
			usePhoto = false // Fallback to text if URL is bad
		}
	} else {
		iconStatus = "❌ Icon Missing"
		usePhoto = false
	}

	// Format main message caption
	caption := fmt.Sprintf(
		"*Token Graduated & Validated\\!* 🚀\n\n"+
			"CA: `%s`\n"+
			"Icon: %s\n\n"+
			"DexScreener: %s\n\n"+
			"\\-\\-\\- Criteria Met \\-\\-\\-\n"+
			"%s\n\n"+
			"%s", // Socials section already includes newline if present
		tokenAddress,
		iconStatus,
		dexscreenerURLEsc,
		criteriaDetails,
		socialsSection, // Add the built socials string
	)
	caption = strings.TrimRight(caption, "\n") // Clean up trailing newline

	// Send notification to "bot calls" topic
	if usePhoto {
		notifications.SendBotCallPhotoMessage(validationResult.ImageURL, caption)
		appLogger.Info("Telegram 'Bot Call' photo notification initiated", tokenField)
	} else {
		notifications.SendBotCallMessage(caption)
		appLogger.Info("Telegram 'Bot Call' text notification initiated", tokenField)
	}

	// Add to graduated cache (prevents immediate re-processing if webhook fires again)
	graduatedTokenCache.Lock()
	graduatedTokenCache.Data[tokenAddress] = time.Now()
	graduatedTokenCache.Unlock()
	appLogger.Info("Added token to graduatedTokenCache", tokenField)

	// *** NEW: Add token to the progress tracking cache ***
	if validationResult.MarketCap > 0 {
		mcField := zap.Float64("baselineMC", validationResult.MarketCap)
		trackedProgressCache.Lock()
		// Only add if not already being tracked (safety check, though unlikely)
		if _, exists := trackedProgressCache.Data[tokenAddress]; !exists {
			trackedProgressCache.Data[tokenAddress] = TrackedTokenInfo{
				BaselineMarketCap: validationResult.MarketCap,
				AddedAt:           time.Now(),
			}
			trackedProgressCache.Unlock()
			appLogger.Info("Added token to progress tracking cache", tokenField, mcField)
		} else {
			trackedProgressCache.Unlock()
			appLogger.Info("Token already exists in progress tracking cache, skipping add.", tokenField, mcField)
		}
	} else {
		// Don't add if market cap is zero, as it breaks multiplier calculation
		appLogger.Info("Token not added to progress tracking (Market Cap is zero)", tokenField)
	}
}

// *** NEW: Function to periodically check token progress ***
func CheckTokenProgress(appLogger *logger.Logger) {
	checkInterval := 30 * time.Minute // Check every 30 minutes
	targetMultiplier := 4.0           // Target multiplier (e.g., 4x)

	appLogger.Info("Token progress tracking routine started",
		zap.Duration("interval", checkInterval),
		zap.Float64("targetMultiplier", targetMultiplier))

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
		if count == 0 {
			appLogger.Debug("No tokens currently in progress tracking cache.")
			continue
		}

		appLogger.Info("Checking progress for tracked tokens", zap.Int("count", count))

		tokensToRemove := []string{}

		for tokenAddress, trackedInfo := range tokensToCheck {
			tokenField := zap.String("tokenAddress", tokenAddress)
			baselineMarketCap := trackedInfo.BaselineMarketCap
			mcBaselineField := zap.Float64("baselineMC", baselineMarketCap)

			if baselineMarketCap <= 0 {
				appLogger.Warn("Token found in progress cache with zero or negative baseline MC, removing.", tokenField, mcBaselineField)
				tokensToRemove = append(tokensToRemove, tokenAddress)
				continue
			}

			appLogger.Debug("Checking progress for specific token", tokenField, mcBaselineField)

			currentValidationResult, err := IsTokenValid(tokenAddress, appLogger)

			if err != nil {
				appLogger.Warn("Error fetching current data during progress check", tokenField, zap.Error(err))
				continue
			}

			if currentValidationResult != nil && currentValidationResult.MarketCap > 0 {
				currentMarketCap := currentValidationResult.MarketCap
				mcCurrentField := zap.Float64("currentMC", currentMarketCap)
				multiplier := currentMarketCap / baselineMarketCap

				appLogger.Debug("Progress check calculation", tokenField, mcBaselineField, mcCurrentField, zap.Float64("multiplier", multiplier))

				if multiplier >= targetMultiplier {
					roundedMultiplier := math.Floor(multiplier)

					appLogger.Info("Token hit target market cap multiplier!", tokenField, mcBaselineField, mcCurrentField, zap.Float64("multiplier", multiplier), zap.Float64("roundedMultiplier", roundedMultiplier))

					// *** CHANGE HERE: Generate raw link first ***
					dexScreenerLinkRaw := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

					// *** CHANGE HERE: Format the message WITHOUT internal escaping ***
					// Use intended markdown like *, `, \! directly.
					progressMessage := fmt.Sprintf(
						"🚀 Token `%s` did *%.0fx* from initial call\\!\n\n"+ // Keep manual escapes for !, *, `
							"Initial MC: `$%.0f`\n"+ // Keep manual escapes for `
							"Current MC: `$%.0f`\n\n"+ // Keep manual escapes for `
							"DexScreener: %s", // Use the raw link string here
						tokenAddress,
						roundedMultiplier,
						baselineMarketCap,
						currentMarketCap,
						dexScreenerLinkRaw, // Pass the raw link
					)

					// *** NO CHANGE HERE: Send the raw formatted message ***
					// Escaping will happen inside SendTrackingUpdateMessage
					notifications.SendTrackingUpdateMessage(progressMessage)
					appLogger.Info("Sent tracking update notification", tokenField)

					tokensToRemove = append(tokensToRemove, tokenAddress)

				} else {
					appLogger.Debug("Token progress check: Target multiplier not met.", tokenField, mcBaselineField, mcCurrentField, zap.Float64("multiplier", multiplier))
				}
			} else {
				appLogger.Debug("Token progress check: Current market cap is zero or validation result missing.", tokenField, zap.Bool("hasResult", currentValidationResult != nil))
			}

			time.Sleep(150 * time.Millisecond) // Keep the small delay
		}

		if len(tokensToRemove) > 0 {
			trackedProgressCache.Lock()
			for _, addr := range tokensToRemove {
				delete(trackedProgressCache.Data, addr)
				appLogger.Info("Removed token from progress tracking cache after hitting target", zap.String("tokenAddress", addr))
			}
			trackedProgressCache.Unlock()
		}

		appLogger.Debug("Token progress check cycle finished.")
	}
}
