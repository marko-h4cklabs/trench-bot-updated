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
var graduatedTokenCache = struct {
	sync.Mutex
	Data map[string]time.Time
}{Data: make(map[string]time.Time)}

// Global cache for tracking market cap progress
var trackedProgressCache = struct {
	sync.Mutex
	Data map[string]TrackedTokenInfo // Uses the TrackedTokenInfo struct
}{Data: make(map[string]TrackedTokenInfo)}

// --- SetupGraduationWebhook function (unchanged from your provided code) ---
func SetupGraduationWebhook(webhookURL string, appLogger *logger.Logger) error {
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
		TransactionTypes: []string{"TRANSFER", "SWAP"}, // Keep both as per original
		AccountAddresses: addressesToMonitor,
		WebhookType:      "enhanced",
		AuthHeader:       authHeader, // Include auth header if provided
		// TxnStatus: "success", // Can add this if only successful needed
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
	// Helius auth is typically via API key in URL or potentially a Bearer token if configured differently
	// if webhookSecret != "" { // Re-evaluate if webhookSecret is for Helius API auth or endpoint auth
	// 	req.Header.Set("Authorization", "Bearer "+webhookSecret)
	// 	appLogger.Info("Setting Authorization header for Helius API.")
	// } else {
	// 	appLogger.Info("Using API key in query parameter for Helius API authentication.")
	// }

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

// --- HandleWebhook function (unchanged from your provided code) ---
func HandleWebhook(payload []byte, appLogger *logger.Logger) error {
	appLogger.Debug("Received Graduation Webhook Payload", zap.Int("size", len(payload)))
	if len(payload) == 0 {
		appLogger.Error("Empty graduation webhook payload received!")
		return fmt.Errorf("empty payload received")
	}

	var eventsArray []map[string]interface{}
	// Try parsing as array first
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
		// If both fail, log the error
		appLogger.Error("Failed to parse graduation webhook payload (neither array nor object)", zap.Error(err), zap.String("payload", string(payload)))
		return fmt.Errorf("failed to parse webhook payload: %w", err)
	}

	appLogger.Debug("Graduation webhook payload is a single event object. Processing...")
	return processGraduatedToken(event, appLogger) // Process and return potential error
}

// --- *** MODIFIED processGraduatedToken FUNCTION *** ---
func processGraduatedToken(event map[string]interface{}, appLogger *logger.Logger) error {
	appLogger.Debug("Processing single graduation event")

	// Extract token address
	tokenAddress, ok := events.ExtractNonSolMintFromEvent(event)
	if !ok {
		appLogger.Debug("Could not extract relevant non-SOL token from graduation event.")
		return nil // Not an error, just irrelevant event
	}
	tokenField := zap.String("tokenAddress", tokenAddress)
	appLogger.Debug("Extracted token address from graduation event", tokenField)

	// Prepare Dexscreener URL (used in message)
	dexscreenerURL := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

	// Debounce check
	tokenCache.Lock()
	if _, exists := tokenCache.Tokens[tokenAddress]; exists {
		tokenCache.Unlock()
		appLogger.Info("Token already processed recently (graduation debounce), skipping.", tokenField)
		return nil
	}
	// Add to debounce cache if not found
	tokenCache.Tokens[tokenAddress] = time.Now()
	tokenCache.Unlock()
	appLogger.Info("Added token to graduation processing debounce cache", tokenField)

	// Validate token against DexScreener criteria
	validationResult, validationErr := IsTokenValid(tokenAddress, appLogger) // Uses the modified IsTokenValid

	if validationErr != nil {
		appLogger.Error("Error checking DexScreener criteria for graduated token", tokenField, zap.Error(validationErr))
		// Decide if we should remove from debounce cache on error? Maybe not, could be transient.
		return validationErr // Propagate the error
	}

	// Check if validation passed
	if validationResult == nil || !validationResult.IsValid {
		reason := "Unknown validation failure or nil result"
		if validationResult != nil && len(validationResult.FailReasons) > 0 {
			reason = strings.Join(validationResult.FailReasons, "; ")
		} else if validationResult != nil && !validationResult.IsValid {
			reason = "Did not meet criteria (no specific reasons returned)"
		}
		appLogger.Info("Graduated token failed DexScreener criteria", tokenField, zap.String("reason", reason))
		// No notification needed, remove from debounce maybe? For now, leave it.
		return nil // Not an application error, just failed criteria
	}

	// --- Token PASSED Validation ---
	appLogger.Info("Graduated token passed validation! Preparing notification...", tokenField)

	// --- START: Attempt to get better image URL from Helius ---
	// (This image fetching logic remains unchanged from your provided code)
	finalImageURL := validationResult.ImageURL // Default to DexScreener/validation image
	usePhoto := false
	// iconStatus := "❌ Icon Missing" // We removed this status from the message

	if finalImageURL != "" {
		if _, urlErr := url.ParseRequestURI(finalImageURL); urlErr == nil && (strings.HasPrefix(finalImageURL, "http://") || strings.HasPrefix(finalImageURL, "https://")) {
			// iconStatus = "⚠️ Icon from DexScreener/Cache" // Status no longer needed
			usePhoto = true
		} else {
			appLogger.Warn("Invalid ImageURL format received from validation source", tokenField, zap.String("url", finalImageURL))
			finalImageURL = "" // Invalidate it
			// iconStatus = "❌ Icon URL Invalid" // Status no longer needed
			usePhoto = false
		}
	}

	appLogger.Info("Attempting to fetch richer metadata via Helius getAsset", tokenField)
	heliusResult, heliusErr := HeliusRPCRequest("getAsset", map[string]string{"id": tokenAddress}, appLogger) // Assume HeliusRPCRequest exists

	if heliusErr != nil {
		appLogger.Warn("Failed to fetch asset details from Helius, will use validation image URL if available", tokenField, zap.Error(heliusErr))
	} else if heliusResult != nil {
		appLogger.Info("Successfully fetched asset details from Helius", tokenField)
		if resultData, ok := heliusResult["result"].(map[string]interface{}); ok {
			if contentData, ok := resultData["content"].(map[string]interface{}); ok {
				heliusImageURL := ""
				if linksData, ok := contentData["links"].(map[string]interface{}); ok {
					if imgURL, ok := linksData["image"].(string); ok && imgURL != "" {
						heliusImageURL = imgURL
					}
				}

				heliusFilesImageURL := ""
				if filesData, ok := contentData["files"].([]interface{}); ok {
					for _, fileEntry := range filesData {
						if fileMap, ok := fileEntry.(map[string]interface{}); ok {
							if uri, ok := fileMap["uri"].(string); ok && uri != "" {
								mime, _ := fileMap["mime"].(string)
								if strings.HasPrefix(mime, "image/") || mime == "" {
									heliusFilesImageURL = uri
									appLogger.Debug("Found potential image URI in Helius content.files", tokenField, zap.String("uri", uri), zap.String("mime", mime))
									break
								}
							}
						}
					}
				}

				// Prioritize files URI, then links image
				if heliusFilesImageURL != "" {
					if _, urlErr := url.ParseRequestURI(heliusFilesImageURL); urlErr == nil {
						appLogger.Info("Using image URL from Helius content.files", tokenField, zap.String("url", heliusFilesImageURL))
						finalImageURL = heliusFilesImageURL
						// iconStatus = "✅ Icon Found (Helius Files)" // Status no longer needed
						usePhoto = true
					} else {
						appLogger.Warn("Invalid URL format found in Helius content.files", tokenField, zap.String("url", heliusFilesImageURL))
						// Fallback logic: if the original finalImageURL was valid, keep it.
						if !usePhoto { // Only reset usePhoto if it wasn't already true
							finalImageURL = validationResult.ImageURL // Reset to original potentially valid one
						}
					}
				} else if heliusImageURL != "" {
					// Use links.image if files didn't yield a valid result
					if _, urlErr := url.ParseRequestURI(heliusImageURL); urlErr == nil {
						appLogger.Info("Using image URL from Helius content.links.image", tokenField, zap.String("url", heliusImageURL))
						finalImageURL = heliusImageURL
						// iconStatus = "✅ Icon Found (Helius Links)" // Status no longer needed
						usePhoto = true
					} else {
						appLogger.Warn("Invalid URL format found in Helius content.links.image", tokenField, zap.String("url", heliusImageURL))
						// Fallback logic
						if !usePhoto {
							finalImageURL = validationResult.ImageURL
						}
					}
				}
				// If neither Helius source provided a valid URL, stick with the initial check result for finalImageURL and usePhoto
			} else {
				appLogger.Warn("Helius response missing 'content' field", tokenField)
			}
		} else {
			appLogger.Warn("Helius response missing 'result' field", tokenField)
		}
	}
	// --- END: Attempt to get better image URL from Helius ---

	// --- Prepare Message Sections ---

	// 1. Token Name & Symbol (from ValidationResult)
	tokenName := validationResult.TokenName
	tokenSymbol := validationResult.TokenSymbol
	// Provide placeholders if name/symbol are missing
	if tokenName == "" {
		tokenName = "N/A"
	}
	if tokenSymbol == "" {
		tokenSymbol = "N/A"
	}
	// Format Name (Symbol) - Escaping handled by notification sender
	nameSymbolPart := fmt.Sprintf("%s (%s)", tokenName, tokenSymbol)

	// 2. Criteria Details (Keep this logic)
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
	criteriaSection := "--- Criteria Met ---\n" + criteriaDetails

	// 3. Social Links (Keep this logic)
	var socialLinksBuilder strings.Builder
	hasSocials := false
	if validationResult.WebsiteURL != "" {
		// Format links for MarkdownV2 automatically later - just add raw URL here
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
	// Add other socials if they exist
	if validationResult.OtherSocials != nil {
		for name, url := range validationResult.OtherSocials {
			if url != "" {
				emoji := "🔗" // Default emoji
				lowerName := strings.ToLower(name)
				if strings.Contains(lowerName, "discord") {
					emoji = "💻" // Use a standard emoji for Discord
				}
				if strings.Contains(lowerName, "medium") {
					emoji = "📰"
				}
				// Add more emoji mappings as needed
				socialLinksBuilder.WriteString(fmt.Sprintf("%s %s: %s\n", emoji, name, url))
				hasSocials = true
			}
		}
	}

	socialsSection := "" // Default to empty
	if hasSocials {
		// Trim trailing newline before adding header
		socialsContent := strings.TrimRight(socialLinksBuilder.String(), "\n")
		socialsSection = "---\n" + socialsContent // Use simple divider
	}

	// --- Assemble the Final Caption ---
	// Order: Name (Symbol)\n\nCA\n\nDexScreener\n\nCriteria\n\nSocials
	// NOTE: Raw strings are used here. Escaping happens in notifications.go
	rawCaption := fmt.Sprintf(
		"%s\n\n"+ // Name (Symbol)
			"CA: `%s`\n\n"+ // CA (keep backticks for code block)
			"DexScreener: %s\n\n"+ // DexScreener Link
			"%s"+ // Criteria Section (includes header and details)
			"%s", // Socials Section (includes divider and details, or empty string)
		nameSymbolPart,
		tokenAddress,
		dexscreenerURL,
		criteriaSection,
		socialsSection, // This might be empty if no socials found
	)
	rawCaption = strings.TrimSpace(rawCaption) // Remove trailing newlines if socials are empty

	// --- Send Notification ---
	if usePhoto && finalImageURL != "" {
		// Send with Photo using the potentially improved Helius URL
		notifications.SendBotCallPhotoMessage(finalImageURL, rawCaption)
		appLogger.Info("Telegram 'Bot Call' photo notification initiated", tokenField, zap.String("usedImageUrl", finalImageURL))
	} else {
		// Send Text Only
		notifications.SendBotCallMessage(rawCaption)
		appLogger.Info("Telegram 'Bot Call' text notification initiated (no valid photo found)", tokenField)
	}

	// --- Post-Notification Caching & Tracking ---
	// Add to graduated cache
	graduatedTokenCache.Lock()
	graduatedTokenCache.Data[tokenAddress] = time.Now()
	graduatedTokenCache.Unlock()
	appLogger.Info("Added token to graduatedTokenCache", tokenField)

	// Add to progress tracking cache if MC > 0
	if validationResult.MarketCap > 0 {
		baselineMC := validationResult.MarketCap
		mcField := zap.Float64("baselineMC", baselineMC)
		trackedProgressCache.Lock()
		if _, exists := trackedProgressCache.Data[tokenAddress]; !exists {
			trackedProgressCache.Data[tokenAddress] = TrackedTokenInfo{
				BaselineMarketCap:           baselineMC,
				HighestMarketCapSeen:        baselineMC,
				AddedAt:                     time.Now(),
				LastNotifiedMultiplierLevel: 0, // Start at level 0
			}
			appLogger.Info("Added token to progress tracking cache", tokenField, mcField)
		} else {
			// Optionally update baseline if seen again? For now, just log.
			appLogger.Info("Token already exists in progress tracking cache, skipping add.", tokenField, mcField)
		}
		trackedProgressCache.Unlock()
	} else {
		appLogger.Info("Token not added to progress tracking (Market Cap is zero)", tokenField)
	}

	return nil // Success
}

// --- CheckTokenProgress function (unchanged from your provided code) ---
func CheckTokenProgress(appLogger *logger.Logger) {
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
		// Create a copy to avoid holding lock during API calls
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

		updatesToCache := make(map[string]TrackedTokenInfo) // Store updates

		for tokenAddress, trackedInfo := range tokensToCheck {
			tokenField := zap.String("tokenAddress", tokenAddress)
			baselineMarketCap := trackedInfo.BaselineMarketCap
			highestMCSeen := trackedInfo.HighestMarketCapSeen
			lastNotifiedLevel := trackedInfo.LastNotifiedMultiplierLevel
			mcBaselineField := zap.Float64("baselineMC", baselineMarketCap)
			mcHighestField := zap.Float64("highestMCSoFar", highestMCSeen)
			lastLevelField := zap.Int("lastNotifiedLevel", lastNotifiedLevel)

			if baselineMarketCap <= 0 {
				appLogger.Warn("Token found with invalid baseline MC in tracking cache, skipping.", tokenField, mcBaselineField)
				// Consider removing invalid entry here?
				continue
			}

			appLogger.Debug("Checking progress for specific token", tokenField, mcBaselineField, mcHighestField, lastLevelField)

			// Get current data using the same validation function
			// Note: This re-checks criteria, which might not be necessary, but reuses the MC fetch logic.
			// Consider a lighter function just for MC if performance is critical.
			currentValidationResult, err := IsTokenValid(tokenAddress, appLogger)
			if err != nil {
				// Handle errors gracefully (e.g., token not found anymore)
				if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "404") {
					appLogger.Info("Token not found during progress check (possibly rugged or delisted)", tokenField, zap.Error(err))
					// Optional: Remove token from trackedProgressCache here
					// delete(trackedProgressCache.Data, tokenAddress) // Requires write lock
				} else {
					appLogger.Warn("Error fetching current data during progress check", tokenField, zap.Error(err))
				}
				continue // Skip this token for this cycle
			}

			// Process if result is valid and has market cap
			if currentValidationResult != nil && currentValidationResult.MarketCap > 0 {
				currentMarketCap := currentValidationResult.MarketCap
				mcCurrentField := zap.Float64("currentMC", currentMarketCap)

				newATH := false
				// Check for new All-Time High (ATH) based on *tracked* highest
				if currentMarketCap > highestMCSeen {
					appLogger.Debug("New highest market cap recorded for tracking", tokenField, mcCurrentField, zap.Float64("previousHighest", highestMCSeen))
					highestMCSeen = currentMarketCap // Update local copy
					newATH = true

					// Prepare to update the cache
					updatedInfo := trackedInfo                       // Start with current info
					updatedInfo.HighestMarketCapSeen = highestMCSeen // Update the highest MC field
					updatesToCache[tokenAddress] = updatedInfo       // Store the update needed
				}

				// Calculate multiplier based on the *tracked* ATH
				athMultiplier := 0.0
				if baselineMarketCap > 0 {
					athMultiplier = highestMCSeen / baselineMarketCap
				}

				// Determine the current notification level based on ATH
				athNotifyLevel := int(math.Floor(athMultiplier)) // Integer floor (e.g., 2.9x -> level 2)

				multiplierField := zap.Float64("athMultiplier", athMultiplier)
				notifyLevelField := zap.Int("athNotifyLevel", athNotifyLevel)
				appLogger.Debug("Progress check calculation", tokenField, mcBaselineField, mcCurrentField, zap.Float64("highestMCRecorded", highestMCSeen), multiplierField, notifyLevelField, lastLevelField)

				// Check if the new level is higher than the last notified level and >= 2x
				if athNotifyLevel > lastNotifiedLevel && athNotifyLevel >= 2 {
					appLogger.Info("Token hit new notification level based on ATH!", tokenField, mcBaselineField, zap.Float64("highestMC", highestMCSeen), notifyLevelField, lastLevelField)

					// Prepare notification message
					dexScreenerLinkRaw := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress)

					progressMessage := fmt.Sprintf(
						"🚀 Token `%s` \n\n"+ // CA in backticks
							"hit: *%dx* \n\n"+ // Multiplier (e.g., 2x, 3x)
							"Initial marketcap: `$%.0f`\n"+ // Baseline MC
							"ATH marketcap: `$%.0f`\n\n"+ // Highest MC Seen
							"DexScreener: %s", // Raw DexScreener link
						tokenAddress,
						athNotifyLevel,
						baselineMarketCap,
						highestMCSeen, // Use the updated highest MC
						dexScreenerLinkRaw,
					)

					// Send the tracking update notification
					notifications.SendTrackingUpdateMessage(progressMessage) // Assumes this function exists and handles escaping
					appLogger.Info("Sent ATH tracking update notification", tokenField, notifyLevelField)

					// Update the notification level in the info we plan to cache
					infoToUpdate := trackedInfo
					if existingUpdate, ok := updatesToCache[tokenAddress]; ok {
						// If already marked for update (e.g., due to new ATH), use that structure
						infoToUpdate = existingUpdate
					}
					infoToUpdate.LastNotifiedMultiplierLevel = athNotifyLevel // Set new notified level
					updatesToCache[tokenAddress] = infoToUpdate               // Store updated info

				} else if newATH {
					// Log if new ATH but didn't cross a notification threshold
					appLogger.Debug("New ATH recorded, but notification level not increased yet.", tokenField, notifyLevelField, lastLevelField)
				} else {
					// Log if no new ATH and no new level
					appLogger.Debug("Token progress check: Notification condition not met.", tokenField, notifyLevelField, lastLevelField)
				}

			} else {
				// Log if current validation failed or MC is zero
				mcValue := 0.0
				if currentValidationResult != nil {
					mcValue = currentValidationResult.MarketCap
				}
				appLogger.Debug("Token progress check: Current market cap is zero or validation result missing/invalid.", tokenField, zap.Bool("hasResult", currentValidationResult != nil), zap.Float64("currentMC", mcValue))
				// Optionally remove from tracking if persistently zero?
			}

			// Small delay between checks to avoid hammering APIs/rate limits
			time.Sleep(200 * time.Millisecond)

		} // End loop through tokensToCheck

		// Apply updates to the main cache under a lock
		if len(updatesToCache) > 0 {
			trackedProgressCache.Lock()
			for tokenAddr, updatedInfo := range updatesToCache {
				// Check if the token still exists in the cache before updating
				if _, ok := trackedProgressCache.Data[tokenAddr]; ok {
					trackedProgressCache.Data[tokenAddr] = updatedInfo
					appLogger.Info("Updated token tracking info in cache", zap.String("tokenAddress", tokenAddr), zap.Float64("newHighestMC", updatedInfo.HighestMarketCapSeen), zap.Int("newNotifiedLevel", updatedInfo.LastNotifiedMultiplierLevel))
				} else {
					// This might happen if the token was removed between copy and update
					appLogger.Warn("Attempted to update info for token no longer in tracking cache", zap.String("tokenAddress", tokenAddr))
				}
			}
			trackedProgressCache.Unlock()
		}

		appLogger.Debug("Token progress check cycle finished.")
	} // End ticker loop
}
