// agent/internal/services/graduate.go

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

type WebhookRequest struct {
	WebhookURL       string   `json:"webhookURL"`
	TransactionTypes []string `json:"transactionTypes"`
	AccountAddresses []string `json:"accountAddresses"`
	WebhookType      string   `json:"webhookType"`
	TxnStatus        string   `json:"txnStatus,omitempty"` // Assuming based on previous context
	AuthHeader       string   `json:"authHeader"`          // Assuming based on previous context
}

type TrackedTokenInfo struct {
	BaselineMarketCap           float64
	HighestMarketCapSeen        float64
	AddedAt                     time.Time
	LastNotifiedMultiplierLevel int
}

// Global cache for graduation debounce
var tokenCache = struct {
	sync.Mutex
	Tokens map[string]time.Time
}{Tokens: make(map[string]time.Time)}

// Global cache for already graduated tokens (maybe for CheckTokenProgress?)
// Ensure TrackedTokenInfo struct is defined (it seems to be within graduate.go already)
// type TrackedTokenInfo struct { ... } // Make sure this exists above or below
var graduatedTokenCache = struct { // Changed name to match error (was GraduatedTokenCache struct before)
	sync.Mutex
	Data map[string]time.Time
}{Data: make(map[string]time.Time)}

// Global cache for tracking market cap progress
var trackedProgressCache = struct {
	sync.Mutex
	Data map[string]TrackedTokenInfo // Uses the TrackedTokenInfo struct
}{Data: make(map[string]TrackedTokenInfo)}

// --- Struct definitions (TrackedTokenInfo, WebhookRequest, GraduatedTokenCache) remain the same ---
// --- Global variables (trackedProgressCache, tokenCache, graduatedTokenCache) remain the same ---

// --- SetupGraduationWebhook function remains the same ---
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

// --- HandleWebhook function remains the same ---
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

// --- *** MODIFIED processGraduatedToken FUNCTION *** ---
func processGraduatedToken(event map[string]interface{}, appLogger *logger.Logger) error {
	appLogger.Debug("Processing single graduation event")

	tokenAddress, ok := events.ExtractNonSolMintFromEvent(event)
	if !ok {
		appLogger.Debug("Could not extract relevant non-SOL token from graduation event.")
		return nil
	}
	tokenField := zap.String("tokenAddress", tokenAddress)
	appLogger.Debug("Extracted token address from graduation event", tokenField)

	dexscreenerURL := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

	tokenCache.Lock()
	if _, exists := tokenCache.Tokens[tokenAddress]; exists {
		tokenCache.Unlock()
		appLogger.Info("Token already processed recently (graduation debounce), skipping.", tokenField)
		return nil
	}
	tokenCache.Tokens[tokenAddress] = time.Now()
	tokenCache.Unlock()
	appLogger.Info("Added token to graduation processing debounce cache", tokenField)

	validationResult, validationErr := IsTokenValid(tokenAddress, appLogger) // Assume this comes from DexScreener or similar

	if validationErr != nil {
		appLogger.Error("Error checking DexScreener criteria for graduated token", tokenField, zap.Error(validationErr))
		return validationErr
	}

	if validationResult == nil || !validationResult.IsValid {
		reason := "Unknown validation failure"
		if validationResult != nil && len(validationResult.FailReasons) > 0 {
			reason = strings.Join(validationResult.FailReasons, "; ")
		} else if validationResult != nil && !validationResult.IsValid {
			reason = "Did not meet criteria (no specific reasons returned)"
		}
		appLogger.Info("Graduated token failed DexScreener criteria", tokenField, zap.String("reason", reason))
		return nil
	}

	appLogger.Info("Graduated token passed validation! Preparing notification...", tokenField)

	// --- START: Attempt to get better image URL from Helius ---
	finalImageURL := validationResult.ImageURL // Default to DexScreener/validation image
	usePhoto := false
	iconStatus := "❌ Icon Missing" // Default status

	if finalImageURL != "" {
		// Basic initial validation of the DexScreener URL
		if _, urlErr := url.ParseRequestURI(finalImageURL); urlErr == nil && (strings.HasPrefix(finalImageURL, "http://") || strings.HasPrefix(finalImageURL, "https://")) {
			iconStatus = "⚠️ Icon from DexScreener/Cache" // Update status
			usePhoto = true
		} else {
			appLogger.Warn("Invalid ImageURL format received from validation source", tokenField, zap.String("url", finalImageURL))
			finalImageURL = "" // Invalidate it
			iconStatus = "❌ Icon URL Invalid"
			usePhoto = false
		}
	}

	appLogger.Info("Attempting to fetch richer metadata via Helius getAsset", tokenField)
	heliusResult, heliusErr := HeliusRPCRequest("getAsset", map[string]string{"id": tokenAddress}, appLogger)

	if heliusErr != nil {
		appLogger.Warn("Failed to fetch asset details from Helius, will use validation image URL if available", tokenField, zap.Error(heliusErr))
		// Keep using finalImageURL from validationResult if it was valid
	} else if heliusResult != nil {
		appLogger.Info("Successfully fetched asset details from Helius", tokenField)
		if resultData, ok := heliusResult["result"].(map[string]interface{}); ok {
			if contentData, ok := resultData["content"].(map[string]interface{}); ok {
				// 1. Check content.links.image
				heliusImageURL := ""
				if linksData, ok := contentData["links"].(map[string]interface{}); ok {
					if imgURL, ok := linksData["image"].(string); ok && imgURL != "" {
						heliusImageURL = imgURL
					}
				}

				// 2. Check content.files for a primary image URI
				heliusFilesImageURL := ""
				if filesData, ok := contentData["files"].([]interface{}); ok {
					for _, fileEntry := range filesData {
						if fileMap, ok := fileEntry.(map[string]interface{}); ok {
							// Prioritize if it's explicitly marked main/primary or has image mime
							// Simple check: just grab the first valid image URI found for now
							if uri, ok := fileMap["uri"].(string); ok && uri != "" {
								mime, _ := fileMap["mime"].(string)                  // Optional: check mime type starts with "image/"
								if strings.HasPrefix(mime, "image/") || mime == "" { // Allow if mime is missing but uri exists
									heliusFilesImageURL = uri
									appLogger.Debug("Found potential image URI in Helius content.files", tokenField, zap.String("uri", uri), zap.String("mime", mime))
									break // Take the first one found in files for simplicity
								}
							}
						}
					}
				}

				// Decide which URL to use: Prioritize files URI, then links image, fallback to original validation image
				if heliusFilesImageURL != "" {
					if _, urlErr := url.ParseRequestURI(heliusFilesImageURL); urlErr == nil {
						appLogger.Info("Using image URL from Helius content.files", tokenField, zap.String("url", heliusFilesImageURL))
						finalImageURL = heliusFilesImageURL
						iconStatus = "✅ Icon Found (Helius Files)"
						usePhoto = true
					} else {
						appLogger.Warn("Invalid URL format found in Helius content.files", tokenField, zap.String("url", heliusFilesImageURL))
					}
				} else if heliusImageURL != "" {
					// If files didn't yield a result, use the links.image one
					if _, urlErr := url.ParseRequestURI(heliusImageURL); urlErr == nil {
						appLogger.Info("Using image URL from Helius content.links.image", tokenField, zap.String("url", heliusImageURL))
						finalImageURL = heliusImageURL
						iconStatus = "✅ Icon Found (Helius Links)"
						usePhoto = true
					} else {
						appLogger.Warn("Invalid URL format found in Helius content.links.image", tokenField, zap.String("url", heliusImageURL))
					}
				}
				// If neither Helius URL was valid/found, we stick with the initial finalImageURL (from validation) if it was valid.
				// If the initial URL was also invalid, usePhoto remains false.
			} else {
				appLogger.Warn("Helius response missing 'content' field", tokenField)
			}
		} else {
			appLogger.Warn("Helius response missing 'result' field", tokenField)
		}
	}
	// --- END: Attempt to get better image URL from Helius ---

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
				emoji = "<:discord:10014198 discord icon emoji ID>" // Replace with actual emoji or text
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
		socialsSection = "--- Socials ---\n" + socialLinksBuilder.String()
	}

	caption := fmt.Sprintf(
		"*Token Graduated & Validated!* 🚀\n\n"+
			"CA: `%s`\n"+
			"Icon: %s\n\n"+ // Use updated iconStatus
			"DexScreener: %s\n\n"+
			"--- Criteria Met ---\n"+
			"%s\n\n"+
			"%s",
		tokenAddress,
		iconStatus, // Use the potentially updated status
		dexscreenerURL,
		criteriaDetails,
		socialsSection,
	)
	caption = strings.TrimRight(caption, "\n")

	// *** Use finalImageURL for the photo ***
	if usePhoto && finalImageURL != "" {
		notifications.SendBotCallPhotoMessage(finalImageURL, caption) // Use the potentially improved URL
		appLogger.Info("Telegram 'Bot Call' photo notification initiated", tokenField, zap.String("usedImageUrl", finalImageURL))
	} else {
		notifications.SendBotCallMessage(caption)
		appLogger.Info("Telegram 'Bot Call' text notification initiated (no valid photo found)", tokenField)
	}

	graduatedTokenCache.Lock()
	graduatedTokenCache.Data[tokenAddress] = time.Now()
	graduatedTokenCache.Unlock()
	appLogger.Info("Added token to graduatedTokenCache", tokenField)

	if validationResult.MarketCap > 0 {
		baselineMC := validationResult.MarketCap
		mcField := zap.Float64("baselineMC", baselineMC)
		trackedProgressCache.Lock()
		if _, exists := trackedProgressCache.Data[tokenAddress]; !exists {
			trackedProgressCache.Data[tokenAddress] = TrackedTokenInfo{
				BaselineMarketCap:           baselineMC,
				HighestMarketCapSeen:        baselineMC,
				AddedAt:                     time.Now(),
				LastNotifiedMultiplierLevel: 0,
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
	return nil
}

// --- CheckTokenProgress function remains the same ---
func CheckTokenProgress(appLogger *logger.Logger) {
	// ... (Implementation unchanged) ...
	checkInterval := 2 * time.Minute

	appLogger.Info("Token progress tracking routine started",
		zap.Duration("interval", checkInterval),
		zap.String("notificationTrigger", "Every integer multiple (2x, 3x, 4x...) of initial MC based on ATH seen"))

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
			appLogger.Debug("No tokens currently in progress tracking cache.")
			continue
		}

		appLogger.Info("Checking progress for tracked tokens", zap.Int("count", count))

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
				appLogger.Warn("Token found with invalid baseline MC, skipping.", tokenField, mcBaselineField)
				continue
			}

			appLogger.Debug("Checking progress for specific token", tokenField, mcBaselineField, mcHighestField, lastLevelField)

			currentValidationResult, err := IsTokenValid(tokenAddress, appLogger)
			if err != nil {
				if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
					appLogger.Info("Token not found during progress check (possibly rugged or delisted)", tokenField, zap.Error(err))
					// Optional: Remove token
				} else {
					appLogger.Warn("Error fetching current data during progress check", tokenField, zap.Error(err))
				}
				continue
			}

			if currentValidationResult != nil && currentValidationResult.MarketCap > 0 {
				currentMarketCap := currentValidationResult.MarketCap
				mcCurrentField := zap.Float64("currentMC", currentMarketCap)

				newATH := false
				if currentMarketCap > highestMCSeen {
					appLogger.Debug("New highest market cap recorded", tokenField, mcCurrentField, zap.Float64("previousHighest", highestMCSeen))
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
				appLogger.Debug("Progress check calculation", tokenField, mcBaselineField, mcCurrentField, zap.Float64("highestMCRecorded", highestMCSeen), multiplierField, notifyLevelField, lastLevelField)

				if athNotifyLevel > lastNotifiedLevel && athNotifyLevel >= 2 {
					appLogger.Info("Token hit new notification level based on ATH!", tokenField, mcBaselineField, zap.Float64("highestMC", highestMCSeen), notifyLevelField, lastLevelField)

					dexScreenerLinkRaw := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

					progressMessage := fmt.Sprintf(
						"🚀 Token `%s` \n\n"+
							"hit: *%dx* \n\n"+
							"Initial marketcap: `$%.0f`\n"+
							"ATH marketcap: `$%.0f`\n\n"+
							"DexScreener: %s",
						tokenAddress,
						athNotifyLevel,
						baselineMarketCap,
						highestMCSeen,
						dexScreenerLinkRaw,
					)

					notifications.SendTrackingUpdateMessage(progressMessage)
					appLogger.Info("Sent ATH tracking update notification", tokenField, notifyLevelField)

					infoToUpdate := trackedInfo
					if existingUpdate, ok := updatesToCache[tokenAddress]; ok {
						infoToUpdate = existingUpdate
					}
					infoToUpdate.LastNotifiedMultiplierLevel = athNotifyLevel
					updatesToCache[tokenAddress] = infoToUpdate

				} else if newATH {
					appLogger.Debug("New ATH recorded, but notification level not increased yet.", tokenField, notifyLevelField, lastLevelField)
				} else {
					appLogger.Debug("Token progress check: Notification condition not met.", tokenField, notifyLevelField, lastLevelField)
				}
			} else {
				mcValue := 0.0
				if currentValidationResult != nil {
					mcValue = currentValidationResult.MarketCap
				}
				appLogger.Debug("Token progress check: Current market cap is zero or validation result missing/invalid.", tokenField, zap.Bool("hasResult", currentValidationResult != nil), zap.Float64("currentMC", mcValue))
			}

			time.Sleep(200 * time.Millisecond)

		}
		if len(updatesToCache) > 0 {
			trackedProgressCache.Lock()
			for tokenAddr, updatedInfo := range updatesToCache {
				if _, ok := trackedProgressCache.Data[tokenAddr]; ok {
					trackedProgressCache.Data[tokenAddr] = updatedInfo
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
