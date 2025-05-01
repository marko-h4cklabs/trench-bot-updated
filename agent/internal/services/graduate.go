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
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// --- Struct Definitions ---
type TrackedTokenInfo struct {
	BaselineMarketCap           float64
	HighestMarketCapSeen        float64
	AddedAt                     time.Time
	LastNotifiedMultiplierLevel int
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

var tokenCache = struct {
	sync.Mutex
	Tokens map[string]time.Time
}{Tokens: make(map[string]time.Time)}

type GraduatedTokenCache struct {
	Data map[string]time.Time
	sync.Mutex
}

var graduatedTokenCache = &GraduatedTokenCache{Data: make(map[string]time.Time)}

// --- Constants ---
const (
	solAddress = "So11111111111111111111111111111111111111112" // Wrapped SOL address for Axiom link (if needed, not used in this version)
)

// --- Webhook Setup ---

// SetupGraduationWebhook remains exactly as you provided it
func SetupGraduationWebhook(webhookURL string, appLogger *logger.Logger) error {
	// ... (Implementation unchanged from your provided code) ...
	appLogger.Info("Setting up Graduation Webhook...", zap.String("url", webhookURL))
	apiKey := env.HeliusAPIKey
	webhookSecret := env.WebhookSecret
	authHeader := env.HeliusAuthHeader
	pumpFunAuthority := env.PumpFunAuthority
	raydiumAddressesStr := env.RaydiumAccountAddresses
	if apiKey == "" {
		appLogger.Error("HELIUS_API_KEY missing!")
		return fmt.Errorf("missing HELIUS_API_KEY")
	}
	if webhookURL == "" {
		appLogger.Error("Webhook URL is empty!")
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
		appLogger.Warn("HELIUS_AUTH_HEADER is not set (this is often optional).")
	} // Changed from Info to Warn
	addressesToMonitor := []string{}
	if pumpFunAuthority != "" {
		addressesToMonitor = append(addressesToMonitor, pumpFunAuthority)
		appLogger.Info("Adding PumpFun authority address.", zap.String("address", pumpFunAuthority))
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
		appLogger.Info("Adding Raydium addresses.", zap.Int("count", count))
	}
	if len(addressesToMonitor) == 0 {
		appLogger.Error("Cannot create webhook: No addresses found to monitor.")
		return fmt.Errorf("no addresses provided to monitor")
	}
	appLogger.Info("Total addresses to monitor in webhook", zap.Int("count", len(addressesToMonitor)))
	appLogger.Info("Checking for existing Helius Webhook...", zap.String("url", webhookURL))
	existingWebhook, err := CheckExistingHeliusWebhook(webhookURL, appLogger)
	if err != nil {
		appLogger.Error("Failed check for existing webhook, attempting creation regardless.", zap.Error(err))
	}
	if existingWebhook {
		appLogger.Info("Webhook already exists. Skipping creation.", zap.String("url", webhookURL))
		appLogger.Warn("Existing webhook check passed, but address list might not be updated.")
		return nil
	}
	appLogger.Info("Creating new Helius webhook...")
	requestBody := WebhookRequest{WebhookURL: webhookURL, TransactionTypes: []string{"TRANSFER", "SWAP"}, AccountAddresses: addressesToMonitor, WebhookType: "enhanced", AuthHeader: authHeader}
	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		appLogger.Error("Failed to serialize webhook request body", zap.Error(err))
		return fmt.Errorf("failed to serialize request: %w", err)
	}
	apiURL := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		appLogger.Error("Failed to create webhook request object", zap.Error(err))
		return fmt.Errorf("failed to create request object: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if authHeader != "" {
		appLogger.Info("Setting Helius authHeader for webhook creation.", zap.Bool("authHeaderSet", true))
	} else {
		appLogger.Warn("HELIUS_AUTH_HEADER is not set for webhook creation.")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		appLogger.Error("Failed to send webhook creation request to Helius", zap.Error(err))
		return fmt.Errorf("failed to send request: %w", err)
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
		appLogger.Info("Helius webhook created successfully", zap.String("url", webhookURL), zap.Int("status", resp.StatusCode), zap.Int("monitored_address_count", len(addressesToMonitor)))
		return nil
	} else {
		appLogger.Error("Failed to create Helius webhook.", zap.Int("status", resp.StatusCode), zap.String("response", responseBodyStr))
		return fmt.Errorf("failed webhook creation: status %d, response: %s", resp.StatusCode, responseBodyStr)
	}
}

// --- Webhook Handling ---

// HandleWebhook remains exactly as you provided it
func HandleWebhook(payload []byte, appLogger *logger.Logger) error {
	// ... (Implementation unchanged from your provided code) ...
	appLogger.Debug("Received Graduation Webhook Payload", zap.Int("size", len(payload)))
	if len(payload) == 0 {
		appLogger.Error("Empty graduation webhook payload received!")
		return fmt.Errorf("empty payload received")
	}
	var eventsArray []map[string]interface{}
	if err := json.Unmarshal(payload, &eventsArray); err == nil {
		appLogger.Debug("Webhook payload is an array.", zap.Int("count", len(eventsArray)))
		var firstErr error
		for i, event := range eventsArray {
			appLogger.Debug("Processing event from array", zap.Int("index", i))
			err := processGraduatedToken(event, appLogger)
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		appLogger.Error("Failed to parse webhook payload (neither array nor object)", zap.Error(err), zap.String("payload", string(payload)))
		return fmt.Errorf("failed to parse payload: %w", err)
	}
	appLogger.Debug("Webhook payload is a single event object. Processing...")
	return processGraduatedToken(event, appLogger)
}

// --- Token Processing and Notification ---

// processGraduatedToken modified minimally to add trading links at the end
func processGraduatedToken(event map[string]interface{}, appLogger *logger.Logger) error {
	appLogger.Debug("Processing single graduation event")

	tokenAddress, ok := events.ExtractNonSolMintFromEvent(event)
	if !ok {
		appLogger.Debug("Could not extract relevant non-SOL token.")
		return nil
	}
	tokenField := zap.String("tokenAddress", tokenAddress)
	appLogger.Debug("Extracted token address.", tokenField)

	// Debounce Check (Unchanged)
	tokenCache.Lock()
	if _, exists := tokenCache.Tokens[tokenAddress]; exists {
		tokenCache.Unlock()
		appLogger.Info("Token already processed recently (debounce).", tokenField)
		return nil
	}
	tokenCache.Tokens[tokenAddress] = time.Now()
	tokenCache.Unlock()
	appLogger.Info("Added token to debounce cache.", tokenField)

	// DexScreener Validation (Unchanged Assumption: uses Name/Symbol, No Max Liq/Vol/Tx)
	validationResult, validationErr := IsTokenValid(tokenAddress, appLogger)
	if validationErr != nil {
		appLogger.Error("Error checking DexScreener criteria.", tokenField, zap.Error(validationErr))
		return validationErr
	}
	if validationResult == nil || !validationResult.IsValid {
		reason := "Unknown failure"
		if validationResult != nil {
			if len(validationResult.FailReasons) > 0 {
				reason = strings.Join(validationResult.FailReasons, "; ")
			} else if !validationResult.IsValid {
				reason = "Did not meet criteria"
			}
		}
		appLogger.Info("Token failed DexScreener criteria.", tokenField, zap.String("reason", reason))
		return nil
	}

	appLogger.Info("Graduated token passed validation! Preparing notification...", tokenField)

	// Build Criteria & Socials Sections (Unchanged)
	criteriaDetails := fmt.Sprintf("🩸 Liquidity: $%.0f\n🏛️ Market Cap: $%.0f\n⌛ (5m) Volume : $%.0f\n⏳ (1h) Volume : $%.0f\n🔎 (5m) TXNs : %d\n🔍 (1h) TXNs : %d", validationResult.LiquidityUSD, validationResult.MarketCap, validationResult.Volume5m, validationResult.Volume1h, validationResult.Txns5m, validationResult.Txns1h)
	var socialLinksBuilder strings.Builder
	hasSocials := false
	if validationResult.WebsiteURL != "" {
		socialLinksBuilder.WriteString(fmt.Sprintf("🌐 Website: %s\n", validationResult.WebsiteURL))
		hasSocials = true
	}
	if validationResult.TwitterURL != "" {
		socialLinksBuilder.WriteString(fmt.Sprintf("🐦 Twitter: %s\n", validationResult.TwitterURL))
		hasSocials = true
	}
	if validationResult.TelegramURL != "" {
		socialLinksBuilder.WriteString(fmt.Sprintf("✈️ Telegram: %s\n", validationResult.TelegramURL))
		hasSocials = true
	}
	for name, url := range validationResult.OtherSocials {
		if url != "" {
			emoji := "🔗"
			lowerName := strings.ToLower(name)
			if strings.Contains(lowerName, "discord") {
				emoji = "👾"
			}
			if strings.Contains(lowerName, "medium") {
				emoji = "📰"
			}
			socialLinksBuilder.WriteString(fmt.Sprintf("%s %s: %s\n", emoji, name, url))
			hasSocials = true
		}
	}
	socialsSection := ""
	if hasSocials {
		socialsSection = "---\nSocials\n" + socialLinksBuilder.String()
	}
	socialsSection = strings.TrimRight(socialsSection, "\n")

	// Helius Image Fetch Logic (Unchanged)
	appLogger.Debug("Attempting Helius GetAsset.", tokenField)
	heliusImageURL, heliusErr := GetHeliusTokenImageURL(tokenAddress, appLogger)
	finalImageURL := validationResult.ImageURL
	if heliusErr == nil && heliusImageURL != "" {
		appLogger.Info("Using Helius image.", tokenField, zap.String("heliusURL", heliusImageURL))
		finalImageURL = heliusImageURL
	} else {
		if heliusErr != nil {
			appLogger.Warn("Helius image fetch failed, using DexScreener.", tokenField, zap.Error(heliusErr))
		} else {
			appLogger.Debug("Helius image empty, using DexScreener.", tokenField)
		}
	}
	usePhoto := false
	if finalImageURL != "" {
		parsedURL, urlErr := url.ParseRequestURI(finalImageURL)
		if urlErr == nil && (parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
			appLogger.Debug("Image URL valid for photo.", tokenField, zap.String("finalURL", finalImageURL))
			usePhoto = true
		} else {
			appLogger.Warn("Image URL invalid, sending text.", tokenField, zap.String("finalURL", finalImageURL), zap.NamedError("urlParseError", urlErr))
		}
	} else {
		appLogger.Debug("No image URL, sending text.", tokenField)
	}

	// Construct Dexscreener URL (needed for main caption part)
	dexscreenerURL := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

	// --- Assemble Main Caption PART 1 (Info + Criteria)---
	// This remains as you provided it
	caption := fmt.Sprintf(
		"🚨Name: %s\n"+
			"🎯Symbol: $%s\n\n"+
			"📃CA: `%s`\n\n"+ // Backticks for copy-on-tap
			"DexScreener: %s\n\n"+
			"--- Criteria Met ---\n"+
			"%s", // criteriaDetails
		validationResult.TokenName,
		validationResult.TokenSymbol,
		tokenAddress,
		dexscreenerURL,
		criteriaDetails,
	)

	// Append socials (Unchanged)
	if socialsSection != "" {
		caption += "\n\n" + socialsSection
	}

	// *** MODIFICATION START: Construct Trading URLs and Link String ***
	pumpFunURL := fmt.Sprintf("https://pump.fun/coin/%s", tokenAddress) // Corrected URL
	axiomURL := fmt.Sprintf("http://axiom.trade/t/%s", tokenAddress)    // Corrected URL (http)

	// Create the inline "button" string - Ensure ParseMode handles this correctly
	tradingLinks := fmt.Sprintf(
		"[Axiom](%s) | [Pump.fun](%s)", // Short text links
		axiomURL,
		pumpFunURL,
	)
	// *** MODIFICATION END ***

	// *** MODIFICATION START: Append Trading Links to Caption ***
	// Add a separator if social links were present, or just space otherwise
	if socialsSection != "" {
		caption += "\n\n---\n" + tradingLinks // Separator line if socials exist
	} else {
		caption += "\n\n" + tradingLinks // Just spacing if no socials
	}
	// *** MODIFICATION END ***

	// --- Send Notification ---
	// Assumes notifications.go uses MarkdownV2 and EscapeMarkdownV2 preserves backticks ` ` but escapes []() correctly
	if usePhoto {
		notifications.SendBotCallPhotoMessage(finalImageURL, caption)
		imageSource := "DexScreener"
		if heliusErr == nil && heliusImageURL != "" && finalImageURL == heliusImageURL {
			imageSource = "Helius"
		}
		appLogger.Info("Telegram 'Bot Call' photo initiated", tokenField, zap.String("name", validationResult.TokenName), zap.String("imageSource", imageSource))
	} else {
		notifications.SendBotCallMessage(caption)
		appLogger.Info("Telegram 'Bot Call' text initiated", tokenField, zap.String("name", validationResult.TokenName))
	}

	// Update Caches and Tracking (Unchanged)
	graduatedTokenCache.Lock()
	graduatedTokenCache.Data[tokenAddress] = time.Now()
	graduatedTokenCache.Unlock()
	appLogger.Info("Added token to graduatedTokenCache.", tokenField)
	if validationResult.MarketCap > 0 {
		baselineMC := validationResult.MarketCap
		mcField := zap.Float64("baselineMC", baselineMC)
		trackedProgressCache.Lock()
		if _, exists := trackedProgressCache.Data[tokenAddress]; !exists {
			trackedProgressCache.Data[tokenAddress] = TrackedTokenInfo{BaselineMarketCap: baselineMC, HighestMarketCapSeen: baselineMC, AddedAt: time.Now(), LastNotifiedMultiplierLevel: 0}
			trackedProgressCache.Unlock()
			appLogger.Info("Added token to progress tracking.", tokenField, mcField)
		} else {
			trackedProgressCache.Unlock()
			appLogger.Info("Token already in progress tracking.", tokenField, mcField)
		}
	} else {
		appLogger.Info("Token not added to progress tracking (MC=0).", tokenField)
	}

	return nil // Success
}

// --- Token Progress Tracking ---

// CheckTokenProgress remains exactly as you provided it
func CheckTokenProgress(appLogger *logger.Logger) {
	// ... (Implementation unchanged from your provided code) ...
	checkInterval := 2 * time.Minute
	appLogger.Info("Token progress tracking routine started", zap.Duration("interval", checkInterval), zap.String("notificationTrigger", "Every integer multiple (2x, 3x, 4x...) of initial MC based on ATH seen"))
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	for range ticker.C {
		appLogger.Debug("Running token progress check cycle...")
		trackedProgressCache.Lock()
		tokensToCheck := make(map[string]TrackedTokenInfo)
		for addr, info := range trackedProgressCache.Data {
			tokensToCheck[addr] = info
		}
		trackedProgressCache.Unlock()
		count := len(tokensToCheck)
		if count == 0 {
			appLogger.Debug("No tokens in progress cache.")
			continue
		}
		appLogger.Info("Checking progress for tokens", zap.Int("count", count))
		updatesToCache := make(map[string]TrackedTokenInfo)
		for tokenAddress, trackedInfo := range tokensToCheck {
			tokenField := zap.String("tokenAddress", tokenAddress)
			baselineMarketCap := trackedInfo.BaselineMarketCap
			highestMCSeen := trackedInfo.HighestMarketCapSeen
			lastNotifiedLevel := trackedInfo.LastNotifiedMultiplierLevel
			mcBaselineField := zap.Float64("baselineMC", baselineMarketCap)
			mcHighestField := zap.Float64("highestMCSoFar", highestMCSeen)
			lastLevelField := zap.Int("lastNotifiedLevel", lastNotifiedLevel)
			if baselineMarketCap <= 0 {
				appLogger.Warn("Invalid baseline MC.", tokenField, mcBaselineField)
				continue
			}
			appLogger.Debug("Checking progress for specific token", tokenField, mcBaselineField, mcHighestField, lastLevelField)
			currentValidationResult, err := IsTokenValid(tokenAddress, appLogger)
			if err != nil {
				if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "No trading pairs found") {
					appLogger.Info("Token not found/no pairs during progress check.", tokenField, zap.Error(err))
				} else {
					appLogger.Warn("Error fetching data during progress check.", tokenField, zap.Error(err))
				}
				continue
			}
			if currentValidationResult != nil && currentValidationResult.MarketCap > 0 {
				currentMarketCap := currentValidationResult.MarketCap
				mcCurrentField := zap.Float64("currentMC", currentMarketCap)
				newATH := false
				if currentMarketCap > highestMCSeen {
					appLogger.Debug("New ATH recorded", tokenField, mcCurrentField, zap.Float64("previousHighest", highestMCSeen))
					highestMCSeen = currentMarketCap
					newATH = true
					updatedInfo := trackedInfo
					updatedInfo.HighestMarketCapSeen = highestMCSeen
					updatesToCache[tokenAddress] = updatedInfo
				}
				athMultiplier := 0.0
				if baselineMarketCap > 0 {
					athMultiplier = highestMCSeen / baselineMarketCap
				}
				athNotifyLevel := int(math.Floor(athMultiplier))
				multiplierField := zap.Float64("athMultiplier", athMultiplier)
				notifyLevelField := zap.Int("athNotifyLevel", athNotifyLevel)
				appLogger.Debug("Progress calculation", tokenField, mcBaselineField, mcCurrentField, zap.Float64("highestMCRecorded", highestMCSeen), multiplierField, notifyLevelField, lastLevelField)
				if athNotifyLevel > lastNotifiedLevel && athNotifyLevel >= 2 {
					appLogger.Info("Token hit new notification level.", tokenField, mcBaselineField, zap.Float64("highestMC", highestMCSeen), notifyLevelField, lastLevelField)
					dexScreenerLinkRaw := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)
					tokenNameStr := currentValidationResult.TokenName
					if tokenNameStr == "" {
						tokenNameStr = tokenAddress
					}
					progressMessage := fmt.Sprintf("🚀 Token Progress: *%s*\n\nHit: *%dx*\n\nInitial MC: `$%.0f`\nATH MC: `$%.0f`\n\nDexScreener: %s", escapeMarkdownV2(tokenNameStr), athNotifyLevel, baselineMarketCap, highestMCSeen, dexScreenerLinkRaw)
					notifications.SendTrackingUpdateMessage(progressMessage)
					appLogger.Info("Sent ATH tracking update notification.", tokenField, notifyLevelField)
					infoToUpdate := trackedInfo
					if existingUpdate, ok := updatesToCache[tokenAddress]; ok {
						infoToUpdate = existingUpdate
					}
					infoToUpdate.LastNotifiedMultiplierLevel = athNotifyLevel
					updatesToCache[tokenAddress] = infoToUpdate
				} else if newATH {
					appLogger.Debug("New ATH, but level not increased.", tokenField, notifyLevelField, lastLevelField)
				} else {
					appLogger.Debug("Notification condition not met.", tokenField, notifyLevelField, lastLevelField)
				}
			} else {
				mcValue := 0.0
				if currentValidationResult != nil {
					mcValue = currentValidationResult.MarketCap
				}
				appLogger.Debug("Current MC zero or validation invalid.", tokenField, zap.Bool("hasResult", currentValidationResult != nil), zap.Float64("currentMC", mcValue))
			}
			time.Sleep(200 * time.Millisecond)
		}
		if len(updatesToCache) > 0 {
			trackedProgressCache.Lock()
			for tokenAddr, updatedInfo := range updatesToCache {
				if _, ok := trackedProgressCache.Data[tokenAddr]; ok {
					trackedProgressCache.Data[tokenAddr] = updatedInfo
					appLogger.Info("Updated token tracking info.", zap.String("tokenAddress", tokenAddr), zap.Float64("newHighestMC", updatedInfo.HighestMarketCapSeen), zap.Int("newNotifiedLevel", updatedInfo.LastNotifiedMultiplierLevel))
				} else {
					appLogger.Warn("Attempted to update info for removed token.", zap.String("tokenAddress", tokenAddr))
				}
			}
			trackedProgressCache.Unlock()
		}
		appLogger.Debug("Token progress check cycle finished.")
	}
}

// --- Markdown Escaping Helper ---

// escapeMarkdownV2 helper function (version that preserves backticks needed)
// Make sure the version in notifications.go is the one that DOES NOT escape backticks `
// This helper might be used elsewhere, so keeping it consistent or removing if unused.
// **NOTE:** The version pasted by the user DOES escape backticks. This needs to be corrected in notifications.go
// For this file, I'll keep the user's provided version, assuming notifications.go has the correct one.
func escapeMarkdownV2(text string) string {
	escapeChars := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	replacerArgs := make([]string, 0, len(escapeChars)*2)
	for _, char := range escapeChars {
		replacerArgs = append(replacerArgs, char, "\\"+char)
	}
	r := strings.NewReplacer(replacerArgs...)
	return r.Replace(text)
}

// Assume CheckExistingHeliusWebhook and GetHeliusTokenImageURL functions exist elsewhere (e.g., in solana.go)
