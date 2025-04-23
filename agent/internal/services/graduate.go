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
	"math" // Ensure math is imported
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Modified TrackedTokenInfo to include HighestMarketCapSeen
type TrackedTokenInfo struct {
	BaselineMarketCap           float64
	HighestMarketCapSeen        float64 // <-- ADDED for ATH tracking
	AddedAt                     time.Time
	LastNotifiedMultiplierLevel int // Keep for multi-level notifications
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

// Assume SetupGraduationWebhook, HandleWebhook, CheckExistingHeliusWebhook, IsTokenValid, events.ExtractNonSolMintFromEvent exist and are correct

func SetupGraduationWebhook(webhookURL string, appLogger *logger.Logger) error {
	// ... (Implementation unchanged) ...
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
	existingWebhook, err := CheckExistingHeliusWebhook(webhookURL, appLogger) // Assume this exists
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
		appLogger.Info("Sending Helius API key via query parameter.") // Or however auth works
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

func HandleWebhook(payload []byte, appLogger *logger.Logger) error {
	// ... (Implementation unchanged) ...
	appLogger.Debug("Received Graduation Webhook Payload", zap.Int("size", len(payload)))
	if len(payload) == 0 {
		appLogger.Error("Empty graduation webhook payload received!")
		return fmt.Errorf("empty payload received")
	}

	var eventsArray []map[string]interface{}
	if err := json.Unmarshal(payload, &eventsArray); err == nil {
		appLogger.Debug("Graduation webhook payload is an array.", zap.Int("count", len(eventsArray)))
		var firstErr error
		for i, event := range eventsArray {
			appLogger.Debug("Processing graduation event from array", zap.Int("index", i))
			err := processGraduatedToken(event, appLogger) // Capture potential error from processor
			if err != nil && firstErr == nil {
				firstErr = err // Store the first error encountered
			}
		}
		return firstErr // Return the first error, if any
	}

	// Try parsing as a single object if array parse failed
	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		appLogger.Error("Failed to parse graduation webhook payload (neither array nor object)", zap.Error(err), zap.String("payload", string(payload)))
		return fmt.Errorf("failed to parse webhook payload: %w", err)
	}
	appLogger.Debug("Graduation webhook payload is a single event object. Processing...")
	return processGraduatedToken(event, appLogger) // Process and return potential error
}

func processGraduatedToken(event map[string]interface{}, appLogger *logger.Logger) error {
	// ... (Token extraction, debounce check, initial validation unchanged) ...
	appLogger.Debug("Processing single graduation event")

	tokenAddress, ok := events.ExtractNonSolMintFromEvent(event)
	if !ok {
		appLogger.Debug("Could not extract relevant non-SOL token from graduation event.")
		return nil // Not an error, just not relevant
	}
	tokenField := zap.String("tokenAddress", tokenAddress)
	appLogger.Debug("Extracted token address from graduation event", tokenField)

	// *** No escaping here ***
	dexscreenerURL := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

	tokenCache.Lock()
	if _, exists := tokenCache.Tokens[tokenAddress]; exists {
		tokenCache.Unlock()
		appLogger.Info("Token already processed recently (graduation debounce), skipping.", tokenField)
		return nil // Not an error, just debounced
	}
	tokenCache.Tokens[tokenAddress] = time.Now()
	tokenCache.Unlock()
	appLogger.Info("Added token to graduation processing debounce cache", tokenField)

	validationResult, validationErr := IsTokenValid(tokenAddress, appLogger)

	if validationErr != nil {
		appLogger.Error("Error checking DexScreener criteria for graduated token", tokenField, zap.Error(validationErr))
		return validationErr // Propagate the error
	}

	if validationResult == nil || !validationResult.IsValid {
		reason := "Unknown validation failure"
		if validationResult != nil && len(validationResult.FailReasons) > 0 {
			reason = strings.Join(validationResult.FailReasons, "; ")
		} else if validationResult != nil && !validationResult.IsValid {
			reason = "Did not meet criteria (no specific reasons returned)"
		}
		appLogger.Info("Graduated token failed DexScreener criteria", tokenField, zap.String("reason", reason))
		return nil // Not an error, just failed validation
	}

	appLogger.Info("Graduated token passed validation! Preparing notification...", tokenField)

	// *** Format raw criteria details, no manual escaping ***
	criteriaDetails := fmt.Sprintf(
		"🩸 Liquidity: $%.0f\n"+
			"🏛️ Market Cap: $%.0f\n"+
			"⌛ (5m) Volume : $%.0f\n"+
			"⏳ (1h) Volume : $%.0f\n"+
			"🔎 (5m) TXNs : %d\n"+
			"🔍 (1h) TXNs : %d",
		validationResult.LiquidityUSD,
		validationResult.MarketCap,
		validationResult.Volume5m,
		validationResult.Volume1h,
		validationResult.Txns5m,
		validationResult.Txns1h,
	)

	// *** Format raw social links, no escaping here ***
	var socialLinksBuilder strings.Builder
	hasSocials := false
	if validationResult.WebsiteURL != "" {
		socialLinksBuilder.WriteString(fmt.Sprintf("🌐 Website: %s\n", validationResult.WebsiteURL)) // Raw URL
		hasSocials = true
	}
	if validationResult.TwitterURL != "" {
		socialLinksBuilder.WriteString(fmt.Sprintf("🐦 Twitter: %s\n", validationResult.TwitterURL)) // Raw URL
		hasSocials = true
	}
	if validationResult.TelegramURL != "" {
		socialLinksBuilder.WriteString(fmt.Sprintf("✈️ Telegram: %s\n", validationResult.TelegramURL)) // Raw URL
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
			// Raw name, raw URL
			socialLinksBuilder.WriteString(fmt.Sprintf("%s %s: %s\n", emoji, name, url))
			hasSocials = true
		}
	}
	socialsSection := ""
	if hasSocials {
		// *** No manual escapes for separator ***
		socialsSection = "--- Socials ---\n" + socialLinksBuilder.String()
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

	// *** Format raw caption, use intended markdown, no escaping ***
	caption := fmt.Sprintf(
		"*Token Graduated & Validated!* 🚀\n\n"+ // Keep intended markdown *!
			"CA: `%s`\n"+ // Keep intended markdown `
			"Icon: %s\n\n"+
			"DexScreener: %s\n\n"+ // Pass raw URL
			"--- Criteria Met ---\n"+ // No manual escapes
			"%s\n\n"+ // Pass raw criteria details
			"%s", // Pass raw socials section
		tokenAddress,
		iconStatus,
		dexscreenerURL, // Use raw URL
		criteriaDetails,
		socialsSection,
	)
	caption = strings.TrimRight(caption, "\n")

	// *** Send raw caption - notifications functions will handle escaping ***
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

	// *** Initialize tracking cache WITH HighestMarketCapSeen ***
	if validationResult.MarketCap > 0 {
		baselineMC := validationResult.MarketCap // Use the MC at time of graduation as baseline
		mcField := zap.Float64("baselineMC", baselineMC)
		trackedProgressCache.Lock()
		if _, exists := trackedProgressCache.Data[tokenAddress]; !exists {
			trackedProgressCache.Data[tokenAddress] = TrackedTokenInfo{
				BaselineMarketCap:           baselineMC,
				HighestMarketCapSeen:        baselineMC, // <-- Initialize ATH here
				AddedAt:                     time.Now(),
				LastNotifiedMultiplierLevel: 0, // Initialize notification level to 0
			}
			trackedProgressCache.Unlock()
			appLogger.Info("Added token to progress tracking cache", tokenField, mcField)
		} else {
			trackedProgressCache.Unlock()
			appLogger.Info("Token already exists in progress tracking cache, skipping add.", tokenField, mcField)
		}
	} else {
		appLogger.Info("Token not added to progress tracking (Market Cap is zero)", tokenField)
	}
	return nil // Success
}

// Modified CheckTokenProgress function
func CheckTokenProgress(appLogger *logger.Logger) {
	// *** Use the more frequent check interval ***
	checkInterval := 3 * time.Minute // Check every 3 minutes (or adjust as needed)

	appLogger.Info("Token progress tracking routine started (with ATH tracking & multi-level notifications)",
		zap.Duration("interval", checkInterval), // Log the used interval
		zap.String("notificationTrigger", "Every 2x multiple of initial MC based on ATH seen"))

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for range ticker.C {
		appLogger.Debug("Running token progress check cycle...")

		// --- Snapshot and Locking Strategy ---
		// Get read lock to copy data, release quickly.
		trackedProgressCache.Lock() // Use Lock since we might need to update later
		tokensToCheck := make(map[string]TrackedTokenInfo)
		for addr, info := range trackedProgressCache.Data {
			tokensToCheck[addr] = info
		}
		trackedProgressCache.Unlock() // Release lock after copying

		count := len(tokensToCheck)
		if count == 0 {
			appLogger.Debug("No tokens currently in progress tracking cache.")
			continue
		}

		appLogger.Info("Checking progress for tracked tokens", zap.Int("count", count))

		// --- Store updates separately to apply under a single lock later ---
		updatesToCache := make(map[string]TrackedTokenInfo) // Store full updated struct

		for tokenAddress, trackedInfo := range tokensToCheck {
			tokenField := zap.String("tokenAddress", tokenAddress)
			baselineMarketCap := trackedInfo.BaselineMarketCap
			highestMCSeen := trackedInfo.HighestMarketCapSeen // Get stored ATH
			lastNotifiedLevel := trackedInfo.LastNotifiedMultiplierLevel
			mcBaselineField := zap.Float64("baselineMC", baselineMarketCap)
			mcHighestField := zap.Float64("highestMCSoFar", highestMCSeen)
			lastLevelField := zap.Int("lastNotifiedLevel", lastNotifiedLevel)

			if baselineMarketCap <= 0 {
				appLogger.Warn("Token found with invalid baseline MC, skipping.", tokenField, mcBaselineField)
				continue
			}

			appLogger.Debug("Checking progress for specific token", tokenField, mcBaselineField, mcHighestField, lastLevelField)

			// Fetch current data
			currentValidationResult, err := IsTokenValid(tokenAddress, appLogger)
			if err != nil {
				appLogger.Warn("Error fetching current data during progress check", tokenField, zap.Error(err))
				continue // Skip this token for this cycle
			}

			// Check if we got valid data and positive MC
			if currentValidationResult != nil && currentValidationResult.MarketCap > 0 {
				currentMarketCap := currentValidationResult.MarketCap
				mcCurrentField := zap.Float64("currentMC", currentMarketCap)

				// *** Update Highest Market Cap Seen (if applicable) ***
				newATH := false
				if currentMarketCap > highestMCSeen {
					appLogger.Debug("New highest market cap recorded", tokenField, mcCurrentField, zap.Float64("previousHighest", highestMCSeen))
					highestMCSeen = currentMarketCap // Update local variable for calculations below
					newATH = true
					// Mark this info for update later, don't lock here
					updatedInfo := trackedInfo                       // Copy existing info
					updatedInfo.HighestMarketCapSeen = highestMCSeen // Update the field
					updatesToCache[tokenAddress] = updatedInfo       // Store the whole updated struct
				}

				// *** Calculate Multiplier and Level based on the potentially updated ATH ***
				athMultiplier := 0.0
				if baselineMarketCap > 0 {
					athMultiplier = highestMCSeen / baselineMarketCap
				}
				athNotifyLevelFactor := int(math.Floor(athMultiplier / 2.0))
				athNotifyLevel := athNotifyLevelFactor * 2 // The 2x, 4x, 6x... level based on ATH

				multiplierField := zap.Float64("athMultiplier", athMultiplier)
				notifyLevelField := zap.Int("athNotifyLevel", athNotifyLevel)
				appLogger.Debug("Progress check calculation", tokenField, mcBaselineField, mcCurrentField, zap.Float64("highestMCRecorded", highestMCSeen), multiplierField, notifyLevelField, lastLevelField)

				// *** Check if the ATH has reached a NEW notification level ***
				if athNotifyLevel > lastNotifiedLevel && athNotifyLevel >= 2 {
					appLogger.Info("Token hit new notification level based on ATH!", tokenField, mcBaselineField, zap.Float64("highestMC", highestMCSeen), notifyLevelField, lastLevelField)

					dexScreenerLinkRaw := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

					// Format message using ATH level and the highest recorded MC
					progressMessage := fmt.Sprintf(
						"🚀 Token `%s` hit *%dx* ATH (seen during checks)!\n\n"+ // Use ATH level
							"Initial MC: `$%.0f`\n"+
							"Highest Recorded MC: `$%.0f`\n\n"+ // Report highest MC
							"DexScreener: %s",
						tokenAddress,
						athNotifyLevel, // Use the calculated level (2, 4, 6...)
						baselineMarketCap,
						highestMCSeen, // Report the highest MC recorded
						dexScreenerLinkRaw,
					)

					// Send raw message - notifications will escape
					notifications.SendTrackingUpdateMessage(progressMessage)
					appLogger.Info("Sent ATH tracking update notification", tokenField, notifyLevelField)

					// Mark this info for update later, ensuring LastNotifiedLevel is updated
					infoToUpdate := trackedInfo // Start with original info
					if existingUpdate, ok := updatesToCache[tokenAddress]; ok {
						infoToUpdate = existingUpdate // Use already updated info if ATH was also just hit
					}
					infoToUpdate.LastNotifiedMultiplierLevel = athNotifyLevel // Set the new level
					updatesToCache[tokenAddress] = infoToUpdate               // Store for batch update

				} else if newATH {
					appLogger.Debug("New ATH recorded, but notification level not increased yet.", tokenField, notifyLevelField, lastLevelField)
				} else {
					// Target level not met or not higher than last notification
					appLogger.Debug("Token progress check: Notification condition not met.", tokenField, notifyLevelField, lastLevelField)
				}
			} else {
				appLogger.Debug("Token progress check: Current market cap is zero or validation result missing/invalid.", tokenField, zap.Bool("hasResult", currentValidationResult != nil))
			}

			// Optional delay between tokens
			time.Sleep(150 * time.Millisecond)

		} // End loop through tokensToCheck

		// --- Apply updates to the cache under a single lock ---
		if len(updatesToCache) > 0 {
			trackedProgressCache.Lock()
			for tokenAddr, updatedInfo := range updatesToCache {
				// Check if it still exists before overwriting
				if _, ok := trackedProgressCache.Data[tokenAddr]; ok {
					trackedProgressCache.Data[tokenAddr] = updatedInfo // Update with new HighestMC and/or LastNotifiedLevel
					appLogger.Info("Updated token tracking info in cache", zap.String("tokenAddress", tokenAddr), zap.Float64("newHighestMC", updatedInfo.HighestMarketCapSeen), zap.Int("newNotifiedLevel", updatedInfo.LastNotifiedMultiplierLevel))
				} else {
					appLogger.Warn("Attempted to update info for token no longer in cache", zap.String("tokenAddress", tokenAddr))
				}
			}
			trackedProgressCache.Unlock()
		}

		appLogger.Debug("Token progress check cycle finished.")
	} // End ticker loop
}
