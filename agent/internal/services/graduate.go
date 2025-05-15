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
	// No direct Helius RPC client imports needed in this file anymore
	// "github.com/gagliardetto/solana-go"
	// "github.com/gagliardetto/solana-go/rpc"
)

// Structs (TrackedTokenInfo, WebhookRequest, tokenCache, GraduatedTokenCache) - Unchanged
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

// REMOVED HolderInfo struct

// TokenQualityAnalysis - MODIFIED: Removed fields related to bundling
type TokenQualityAnalysis struct {
	QualityRating int // 1-5
	// BundlingInfoString     string // REMOVED
	// Top1HolderEOAPercent   float64 // REMOVED
	// Top5HolderEOAPercent   float64 // REMOVED
	// LPTokensPercentOfTotal float64 // REMOVED
}

// Constants for Rating Logic - MODIFIED: Removed Holder-specific constants
const (
	BASE_RATING = 3.0

	RATING_STAGNATION_PENALTY            = 1.0
	RATING_HIGH_TXN_LOW_GROWTH_PENALTY   = 0.75
	RATING_MOD_TXN_LOW_GROWTH_PENALTY    = 0.5
	RATING_VOL_LIQ_LOW_MC_PENALTY        = 0.75
	RATING_HIGH_LIQ_LOW_MC_PENALTY       = 0.75
	RATING_BS_IMBALANCE_MODERATE_PENALTY = 0.5
	RATING_STRONG_GROWTH_BONUS           = 0.75
	// RATING_LOW_CONCENTRATION_BONUS       = 0.5 // REMOVED

	RATING_STAGNATION_GROWTH_FACTOR              = 1.15
	RATING_HIGH_TXN_THRESHOLD                    = 2000
	RATING_HIGH_TXN_LOW_GROWTH_FACTOR            = 1.30
	RATING_MODERATE_TXN_LOWER_BOUND              = 700
	RATING_MODERATE_TXN_UPPER_BOUND              = 1999
	RATING_MODERATE_TXN_LOW_GROWTH_FACTOR        = 1.40
	RATING_VOL_LIQ_IMBALANCE_RATIO_THRESHOLD     = 5.0
	RATING_MIN_MC_MULTIPLIER_FOR_VOL_LIQ         = 1.75
	RATING_HIGH_LIQUIDITY_THRESHOLD_FOR_MC_CHECK = 90000.0
	RATING_MIN_MC_TO_HIGH_LIQ_RATIO              = 1.10
	// REMOVED HOLDER_*_THRESHOLD constants
	LOCAL_RATING_MIN_MARKETCAP             = 80000.0 // Ensure this aligns with dexscreener.go's minMarketCap
	RATING_BS_IMBALANCE_MODERATE_THRESHOLD = 0.80
	RATING_MIN_TX_FOR_BS_RATING_CHECK      = 50
)

// REMOVED Helius Helper Function Stubs (GetTokenSupply, GetLPTokensForPair, GetTopEOAHolders)

// CalculateQualityRating (Renamed, no longer accepts HeliusService or mintAddressForHelius for Helius calls)
func CalculateQualityRating(
	valResult *ValidationResult, // Contains all DexScreener metrics
	appLogger *logger.Logger,
) TokenQualityAnalysis { // No error returned if it's just rating
	var analysis TokenQualityAnalysis
	tokenField := zap.String("mintAddress", valResult.TokenName) // Or better, use valResult.PairAddress or a dedicated mint field
	if valResult.TokenName == "" || valResult.TokenName == "N/A" {
		tokenField = zap.String("pairAddressForContext", valResult.PairAddress) // Fallback for logging if name is bad
	}
	appLogger.Debug("CalculateQualityRating: Starting rating based on DexScreener data", tokenField)

	// --- Rating Calculation Logic (Only DexScreener metrics) ---
	currentRating := BASE_RATING

	// Penalties from DexScreener data
	if (valResult.Volume1h < valResult.Volume5m*RATING_STAGNATION_GROWTH_FACTOR) && (float64(valResult.Txns1h) < float64(valResult.Txns5m)*RATING_STAGNATION_GROWTH_FACTOR) {
		if !(valResult.Volume5m == 0 && valResult.Txns5m == 0) {
			currentRating -= RATING_STAGNATION_PENALTY
			appLogger.Debug("Rating: Applied Stagnation Penalty", tokenField, zap.Float64("penalty", RATING_STAGNATION_PENALTY), zap.Float64("newRating", currentRating))
		}
	}
	if (valResult.Txns5m > RATING_HIGH_TXN_THRESHOLD) && (float64(valResult.Txns1h) < float64(valResult.Txns5m)*RATING_HIGH_TXN_LOW_GROWTH_FACTOR) {
		currentRating -= RATING_HIGH_TXN_LOW_GROWTH_PENALTY
		appLogger.Debug("Rating: Applied High TXN Low Growth Penalty", tokenField, zap.Float64("penalty", RATING_HIGH_TXN_LOW_GROWTH_PENALTY), zap.Float64("newRating", currentRating))
	}
	if (valResult.Txns5m >= RATING_MODERATE_TXN_LOWER_BOUND && valResult.Txns5m <= RATING_MODERATE_TXN_UPPER_BOUND) && (float64(valResult.Txns1h) < float64(valResult.Txns5m)*RATING_MODERATE_TXN_LOW_GROWTH_FACTOR) {
		currentRating -= RATING_MOD_TXN_LOW_GROWTH_PENALTY
		appLogger.Debug("Rating: Applied Moderate TXN Low Growth Penalty", tokenField, zap.Float64("penalty", RATING_MOD_TXN_LOW_GROWTH_PENALTY), zap.Float64("newRating", currentRating))
	}
	if valResult.LiquidityUSD > 0 {
		if (valResult.Volume5m/valResult.LiquidityUSD > RATING_VOL_LIQ_IMBALANCE_RATIO_THRESHOLD) && (valResult.MarketCap < (LOCAL_RATING_MIN_MARKETCAP * RATING_MIN_MC_MULTIPLIER_FOR_VOL_LIQ)) {
			currentRating -= RATING_VOL_LIQ_LOW_MC_PENALTY
			appLogger.Debug("Rating: Applied Vol/Liq Low MC Penalty", tokenField, zap.Float64("penalty", RATING_VOL_LIQ_LOW_MC_PENALTY), zap.Float64("newRating", currentRating))
		}
	}
	if valResult.LiquidityUSD > RATING_HIGH_LIQUIDITY_THRESHOLD_FOR_MC_CHECK {
		if valResult.MarketCap < (valResult.LiquidityUSD * RATING_MIN_MC_TO_HIGH_LIQ_RATIO) {
			currentRating -= RATING_HIGH_LIQ_LOW_MC_PENALTY
			appLogger.Debug("Rating: Applied High Liq Low MC Penalty", tokenField, zap.Float64("penalty", RATING_HIGH_LIQ_LOW_MC_PENALTY), zap.Float64("newRating", currentRating))
		}
	}
	if valResult.Txns5m >= RATING_MIN_TX_FOR_BS_RATING_CHECK && valResult.Txns5m > 0 {
		buyRatio5m := float64(valResult.Txns5mBuys) / float64(valResult.Txns5m)
		if buyRatio5m > RATING_BS_IMBALANCE_MODERATE_THRESHOLD || (1.0-buyRatio5m) > RATING_BS_IMBALANCE_MODERATE_THRESHOLD {
			currentRating -= RATING_BS_IMBALANCE_MODERATE_PENALTY
			appLogger.Debug("Rating: Applied 5m B/S Imbalance Moderate Penalty", tokenField, zap.Float64("penalty", RATING_BS_IMBALANCE_MODERATE_PENALTY), zap.Float64("newRating", currentRating))
		}
	}
	if valResult.Txns1h >= RATING_MIN_TX_FOR_BS_RATING_CHECK && valResult.Txns1h > 0 {
		buyRatio1h := float64(valResult.Txns1hBuys) / float64(valResult.Txns1h)
		if buyRatio1h > RATING_BS_IMBALANCE_MODERATE_THRESHOLD || (1.0-buyRatio1h) > RATING_BS_IMBALANCE_MODERATE_THRESHOLD {
			currentRating -= RATING_BS_IMBALANCE_MODERATE_PENALTY
			appLogger.Debug("Rating: Applied 1h B/S Imbalance Moderate Penalty", tokenField, zap.Float64("penalty", RATING_BS_IMBALANCE_MODERATE_PENALTY), zap.Float64("newRating", currentRating))
		}
	}
	// REMOVED Holder Concentration Penalties/Bonuses

	// Bonuses for strong growth (from DexScreener data)
	if valResult.Volume5m > 0 && (valResult.Volume1h/valResult.Volume5m) >= 2.5 &&
		valResult.Txns5m > 0 && (float64(valResult.Txns1h)/float64(valResult.Txns5m)) >= 2.0 {
		currentRating += RATING_STRONG_GROWTH_BONUS
		appLogger.Debug("Rating: Applied Strong Growth Bonus", tokenField, zap.Float64("bonus", RATING_STRONG_GROWTH_BONUS), zap.Float64("newRating", currentRating))
	}

	if currentRating < 1.0 {
		currentRating = 1.0
	}
	if currentRating > 5.0 {
		currentRating = 5.0
	}
	analysis.QualityRating = int(math.Round(currentRating))
	appLogger.Info("CalculateQualityRating: Final Calculated Quality Rating", tokenField, zap.Int("rating", analysis.QualityRating), zap.Float64("floatRatingBeforeRounding", currentRating))

	return analysis
}

// processGraduatedToken - MODIFIED: no longer uses HeliusService for bundling, calls CalculateQualityRating
func processGraduatedToken(event map[string]interface{}, appLogger *logger.Logger) error { // REMOVED heliusSvc
	appLogger.Debug("Processing single graduation event")
	tokenAddressFromEvent, ok := events.ExtractNonSolMintFromEvent(event)
	if !ok {
		appLogger.Debug("Could not extract relevant non-SOL token from graduation event.")
		return nil
	}

	tokenField := zap.String("mintAddress", tokenAddressFromEvent)
	appLogger.Debug("Extracted mint address for graduation", tokenField)

	tokenCache.Lock()
	if _, exists := tokenCache.Tokens[tokenAddressFromEvent]; exists {
		tokenCache.Unlock()
		appLogger.Info("Token already processed (debounce)", tokenField)
		return nil
	}
	tokenCache.Tokens[tokenAddressFromEvent] = time.Now()
	tokenCache.Unlock()

	validationResult, validationErr := IsTokenValid(tokenAddressFromEvent, appLogger)
	if validationErr != nil {
		appLogger.Error("Error in IsTokenValid for graduated token", tokenField, zap.Error(validationErr))
		return validationErr
	}

	if validationResult == nil || !validationResult.IsValid {
		reason := "Unknown"
		if validationResult != nil {
			if len(validationResult.FailReasons) > 0 {
				reason = strings.Join(validationResult.FailReasons, "; ")
			} else if !validationResult.IsValid {
				reason = "Token did not meet basic criteria"
			}
		}
		appLogger.Info("Token failed basic validation or hard rug checks.", tokenField, zap.String("reason", reason))
		return nil
	}
	appLogger.Info("Token passed basic validation. Proceeding to quality rating...", tokenField)

	qualityAnalysis := CalculateQualityRating(validationResult, appLogger) // MODIFIED call
	appLogger.Info("Token quality rating complete", tokenField, zap.Int("qualityRating", qualityAnalysis.QualityRating))

	heliusImageURL, heliusErr := GetHeliusTokenImageURL(tokenAddressFromEvent, appLogger)
	finalImageURL := validationResult.ImageURL
	if heliusErr == nil && heliusImageURL != "" {
		finalImageURL = heliusImageURL
	}
	usePhoto := false
	if finalImageURL != "" {
		parsedURL, urlErr := url.ParseRequestURI(finalImageURL)
		if urlErr == nil && (parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
			usePhoto = true
		}
	}

	criteriaDetails := fmt.Sprintf(
		"🩸 Liq: $%.0f\n"+
			"🏛️ MC: $%.0f\n"+
			"⌛ 5m Vol: $%.0f\n"+
			"⏳ 1h Vol: $%.0f\n"+
			"🔎 5m TXN: %d\n"+
			"🔍 1h TXN: %d",
		validationResult.LiquidityUSD, validationResult.MarketCap,
		validationResult.Volume5m, validationResult.Volume1h,
		validationResult.Txns5m, validationResult.Txns1h,
	)

	socialLinksBuilder := new(strings.Builder)
	hasSocials := false
	if validationResult.WebsiteURL != "" {
		fmt.Fprintf(socialLinksBuilder, "🌐 Website: %s\n", validationResult.WebsiteURL)
		hasSocials = true
	}
	if validationResult.TwitterURL != "" {
		fmt.Fprintf(socialLinksBuilder, "🐦 Twitter: %s\n", validationResult.TwitterURL)
		hasSocials = true
	}
	if validationResult.TelegramURL != "" {
		fmt.Fprintf(socialLinksBuilder, "✈️ Telegram: %s\n", validationResult.TelegramURL)
		hasSocials = true
	}
	if validationResult.OtherSocials != nil {
		for name, webUrl := range validationResult.OtherSocials {
			if webUrl != "" {
				emoji := "🔗"
				if strings.Contains(strings.ToLower(name), "discord") {
					emoji = "👾"
				}
				fmt.Fprintf(socialLinksBuilder, "%s %s: %s\n", emoji, name, webUrl)
				hasSocials = true
			}
		}
	}
	socialsSectionRaw := ""
	if hasSocials {
		socialsSectionRaw = "---\nSocials\n" + strings.TrimRight(socialLinksBuilder.String(), "\n")
	}

	captionBuilder := new(strings.Builder)
	tokenNameDisplay := validationResult.TokenName
	if tokenNameDisplay == "" || tokenNameDisplay == "N/A" {
		tokenNameDisplay = "Unknown Name"
	}
	tokenSymbolDisplay := validationResult.TokenSymbol
	if tokenSymbolDisplay == "" || tokenSymbolDisplay == "N/A" {
		tokenSymbolDisplay = "N/A"
	}

	fmt.Fprintf(captionBuilder, "🚨 %s ($%s)\n", tokenNameDisplay, tokenSymbolDisplay)
	fmt.Fprintf(captionBuilder, "📃 CA: `%s`\n\n", tokenAddressFromEvent)

	if qualityAnalysis.QualityRating >= 1 && qualityAnalysis.QualityRating <= 5 { // Ensure rating is valid before creating stars
		stars := strings.Repeat("⭐", qualityAnalysis.QualityRating) + strings.Repeat("☆", 5-qualityAnalysis.QualityRating)
		fmt.Fprintf(captionBuilder, "🔎 Quality: %s (%d/5)\n\n", stars, qualityAnalysis.QualityRating)
	} else {
		fmt.Fprintf(captionBuilder, "🔎 Quality: N/A\n\n")
	}
	// REMOVED: fmt.Fprintf(captionBuilder, "%s\n\n", qualityAnalysis.BundlingInfoString)

	dexscreenerURL := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddressFromEvent)
	fmt.Fprintf(captionBuilder, "📊 [DexScreener](%s)\n\n", dexscreenerURL)
	fmt.Fprintf(captionBuilder, "---\n*DexScreener Stats*\n")
	fmt.Fprintf(captionBuilder, "%s\n", criteriaDetails)
	if socialsSectionRaw != "" {
		fmt.Fprintf(captionBuilder, "\n%s", socialsSectionRaw)
	}
	rawCaptionToSend := strings.TrimSpace(captionBuilder.String())

	buttons := map[string]string{
		"🚀 Axiom":    fmt.Sprintf("https://axiom.trade/t/%s", tokenAddressFromEvent),
		"🧪 Pump.fun": fmt.Sprintf("https://pump.fun/coin/%s", tokenAddressFromEvent),
		"📸 Photon":   fmt.Sprintf("https://photon-sol.tinyastro.io/en/lp/%s", tokenAddressFromEvent),
	}

	if usePhoto {
		notifications.SendBotCallPhotoMessage(finalImageURL, rawCaptionToSend, buttons)
	} else {
		notifications.SendBotCallMessage(rawCaptionToSend, buttons)
	}

	graduatedTokenCache.Lock()
	graduatedTokenCache.Data[tokenAddressFromEvent] = time.Now()
	graduatedTokenCache.Unlock()
	if validationResult.MarketCap > 0 {
		baselineMC := validationResult.MarketCap
		trackedProgressCache.Lock()
		if _, exists := trackedProgressCache.Data[tokenAddressFromEvent]; !exists {
			trackedProgressCache.Data[tokenAddressFromEvent] = TrackedTokenInfo{BaselineMarketCap: baselineMC, HighestMarketCapSeen: baselineMC, AddedAt: time.Now(), LastNotifiedMultiplierLevel: 0}
			appLogger.Info("Added token to progress tracking.", tokenField, zap.Float64("baselineMC", baselineMC))
		} else {
			appLogger.Info("Token already in progress tracking.", tokenField)
		}
		trackedProgressCache.Unlock()
	} else {
		appLogger.Info("Token not added to progress tracking (MC=0).", tokenField)
	}

	return nil
}

// HandleWebhook - MODIFIED: no longer uses HeliusService
func HandleWebhook(payload []byte, appLogger *logger.Logger) error { // REMOVED heliusSvc
	appLogger.Debug("Received Graduation Webhook Payload", zap.Int("size", len(payload)))
	if len(payload) == 0 {
		return fmt.Errorf("empty payload received")
	}
	var eventsArray []map[string]interface{}
	if err := json.Unmarshal(payload, &eventsArray); err == nil {
		var firstErr error
		// FIX 1: Changed 'i' to '_' because 'i' was unused.
		for _, event := range eventsArray {
			err := processGraduatedToken(event, appLogger) // REMOVED heliusSvc
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
		return firstErr
	}
	var event map[string]interface{}
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse payload: %w", err)
	}
	return processGraduatedToken(event, appLogger) // REMOVED heliusSvc
}

// SetupGraduationWebhook - Unchanged, does not use HeliusService instance
func SetupGraduationWebhook(webhookURL string, appLogger *logger.Logger) error { /* ... as before ... */
	appLogger.Info("Setting up Graduation Webhook...", zap.String("url", webhookURL))
	apiKey := env.HeliusAPIKey
	authHeader := env.HeliusAuthHeader
	pumpFunAuthority := env.PumpFunAuthority
	raydiumAddressesStr := env.RaydiumAccountAddresses
	if apiKey == "" {
		return fmt.Errorf("missing HELIUS_API_KEY")
	}
	if webhookURL == "" {
		return fmt.Errorf("webhookURL for graduation provided is empty")
	}
	addressesToMonitor := []string{}
	if pumpFunAuthority != "" {
		addressesToMonitor = append(addressesToMonitor, pumpFunAuthority)
	}
	if raydiumAddressesStr != "" {
		parsedRaydiumAddrs := strings.Split(raydiumAddressesStr, ",")
		for _, addr := range parsedRaydiumAddrs {
			trimmedAddr := strings.TrimSpace(addr)
			if trimmedAddr != "" {
				addressesToMonitor = append(addressesToMonitor, trimmedAddr)
			}
		}
	}
	if len(addressesToMonitor) == 0 {
		appLogger.Warn("No specific addresses for Graduation webhook monitoring.")
	}
	existingWebhook, err := CheckExistingHeliusWebhook(webhookURL, appLogger)
	if err != nil {
		appLogger.Error("Failed to check for existing graduation webhook", zap.Error(err))
	}
	if existingWebhook {
		appLogger.Info("Graduation webhook already exists.", zap.String("url", webhookURL))
		return nil
	}
	requestBody := WebhookRequest{WebhookURL: webhookURL, TransactionTypes: []string{"TRANSFER", "SWAP"}, AccountAddresses: addressesToMonitor, WebhookType: "enhanced", AuthHeader: authHeader}
	bodyBytes, _ := json.Marshal(requestBody)
	managementApiURL := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, _ := http.NewRequest("POST", managementApiURL, bytes.NewBuffer(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 20 * time.Second}
	resp, errClient := client.Do(req) // Renamed err to errClient
	if errClient != nil {
		return fmt.Errorf("failed to send graduation webhook creation request: %w", errClient)
	} // Use errClient
	defer resp.Body.Close()
	respBodyBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		appLogger.Info("Helius graduation webhook created successfully")
		return nil
	}
	appLogger.Error("Failed to create Helius graduation webhook.", zap.Int("status", resp.StatusCode), zap.String("response", string(respBodyBytes)))
	return fmt.Errorf("failed to create helius graduation webhook: status %d", resp.StatusCode)
}

// CheckTokenProgress - Unchanged, calls the simpler IsTokenValid
func CheckTokenProgress(appLogger *logger.Logger) {
	checkInterval := 2 * time.Minute
	appLogger.Info("Token progress tracking routine started", zap.Duration("interval", checkInterval))
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

		// FIX 2: Moved 'count' declaration out of the 'if' statement's scope.
		count := len(tokensToCheck)
		if count == 0 {
			appLogger.Debug("No tokens in progress cache.")
			continue
		}
		appLogger.Info("Checking progress for tokens", zap.Int("count", count)) // Now 'count' is in scope

		updatesToCache := make(map[string]TrackedTokenInfo)
		for tokenAddress, trackedInfo := range tokensToCheck {
			tokenField := zap.String("tokenAddress", tokenAddress)
			baselineMarketCap := trackedInfo.BaselineMarketCap
			highestMCSeen := trackedInfo.HighestMarketCapSeen
			lastNotifiedLevel := trackedInfo.LastNotifiedMultiplierLevel
			if baselineMarketCap <= 0 {
				appLogger.Warn("Invalid baseline MC.", tokenField, zap.Float64("baselineMC", baselineMarketCap))
				continue
			}
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
				newATH := false
				if currentMarketCap > highestMCSeen {
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
				if athNotifyLevel > lastNotifiedLevel && athNotifyLevel >= 2 {
					tokenNameStr := currentValidationResult.TokenName
					if tokenNameStr == "" {
						tokenNameStr = tokenAddress
					}
					progressMessage := fmt.Sprintf("🚀 *Token Progress Alert*\n\n📛 *Name:* %s\n📈 *Milestone:* %dx ATH\n\n💰 *Initial MC:* `$%.0f`\n🔺 *ATH MC:* `$%.0f`\n\n📊 [View on DexScreener](%s)", tokenNameStr, athNotifyLevel, baselineMarketCap, highestMCSeen, fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddress))
					notifications.SendTrackingUpdateMessage(progressMessage)
					infoToUpdate := trackedInfo
					if existingUpdate, ok := updatesToCache[tokenAddress]; ok {
						infoToUpdate = existingUpdate
					}
					infoToUpdate.LastNotifiedMultiplierLevel = athNotifyLevel
					updatesToCache[tokenAddress] = infoToUpdate
				} else if newATH {
					appLogger.Debug("New ATH, but level not increased.", tokenField, zap.Int("notifyLevel", athNotifyLevel), zap.Int("lastNotified", lastNotifiedLevel))
				}
			}
			time.Sleep(200 * time.Millisecond)
		}
		if len(updatesToCache) > 0 {
			trackedProgressCache.Lock()
			for tokenAddr, updatedInfo := range updatesToCache {
				if _, ok := trackedProgressCache.Data[tokenAddr]; ok {
					trackedProgressCache.Data[tokenAddr] = updatedInfo
				}
			}
			trackedProgressCache.Unlock()
		}
	}
}
