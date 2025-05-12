package services

import (
	"bytes"
	"ca-scraper/agent/internal/events"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications" // Keep context for potential future use in Helius helpers
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	// If using gagliardetto/solana-go for Helius RPC helpers, add imports:
	// "github.com/gagliardetto/solana-go"
	// "github.com/gagliardetto/solana-go/rpc"
)

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

// HolderInfo and TokenQualityAnalysis structs
type HolderInfo struct {
	Address    string
	Amount     uint64  // Raw amount of tokens
	Percentage float64 // Percentage of *circulating EOA supply*
}

type TokenQualityAnalysis struct {
	QualityRating          int    // 1-5
	BundlingInfoString     string // Formatted string for Telegram
	Top1HolderEOAPercent   float64
	Top5HolderEOAPercent   float64
	LPTokensPercentOfTotal float64 // Percentage of LP tokens relative to total supply
}

// Constants for Rating Logic
// In agent/internal/services/graduate.go

const (
	// Rating Penalties/Bonuses
	BASE_RATING = 3.0 // Added this as it was used but not defined as const

	RATING_STAGNATION_PENALTY            = 1.0
	RATING_HIGH_TXN_LOW_GROWTH_PENALTY   = 0.75
	RATING_MOD_TXN_LOW_GROWTH_PENALTY    = 0.5
	RATING_VOL_LIQ_LOW_MC_PENALTY        = 0.75
	RATING_HIGH_LIQ_LOW_MC_PENALTY       = 0.75
	RATING_BS_IMBALANCE_MODERATE_PENALTY = 0.5

	RATING_EXTREME_TOP_1_HOLDER_PENALTY  = 1.5
	RATING_HIGH_TOP_1_HOLDER_PENALTY     = 1.0
	RATING_EXTREME_TOP_5_HOLDERS_PENALTY = 1.0
	RATING_HIGH_TOP_5_HOLDERS_PENALTY    = 0.5
	RATING_STRONG_GROWTH_BONUS           = 0.75
	RATING_LOW_CONCENTRATION_BONUS       = 0.5

	// Thresholds used *within* rating logic
	RATING_STAGNATION_GROWTH_FACTOR              = 1.15
	RATING_HIGH_TXN_THRESHOLD                    = 2000 // This is from dexscreener.go, ensure consistency or pass as param
	RATING_HIGH_TXN_LOW_GROWTH_FACTOR            = 1.30
	RATING_MODERATE_TXN_LOWER_BOUND              = 700  // This is from dexscreener.go
	RATING_MODERATE_TXN_UPPER_BOUND              = 1999 // This is from dexscreener.go
	RATING_MODERATE_TXN_LOW_GROWTH_FACTOR        = 1.40
	RATING_VOL_LIQ_IMBALANCE_RATIO_THRESHOLD     = 5.0
	RATING_MIN_MC_MULTIPLIER_FOR_VOL_LIQ         = 1.75
	RATING_HIGH_LIQUIDITY_THRESHOLD_FOR_MC_CHECK = 90000.0
	RATING_MIN_MC_TO_HIGH_LIQ_RATIO              = 1.10

	// ***** THESE WERE MISSING *****
	HOLDER_TOP1_EXTREME_THRESHOLD = 35.0 // Top 1 EOA > 35%
	HOLDER_TOP1_HIGH_THRESHOLD    = 20.0 // Top 1 EOA > 20%
	HOLDER_TOP5_EXTREME_THRESHOLD = 55.0 // Top 5 EOAs > 55%
	HOLDER_TOP5_HIGH_THRESHOLD    = 40.0 // Top 5 EOAs > 40%
	HOLDER_LOW_CONC_TOP1_MAX      = 5.0  // Bonus if Top 1 EOA < 5%
	HOLDER_LOW_CONC_TOP5_MAX      = 15.0 // Bonus if Top 5 EOAs < 15% (and Top 1 also low)
	// ***** END OF MISSING CONSTANTS *****
	LOCAL_RATING_MIN_MARKETCAP             = 80000.0
	RATING_BS_IMBALANCE_MODERATE_THRESHOLD = 0.80
	RATING_MIN_TX_FOR_BS_RATING_CHECK      = 50
)

// --- Helius RPC Helper Function Placeholders ---
// YOU MUST IMPLEMENT THESE WITH ACTUAL HELIUS RPC CALLS
// These functions would ideally live in your existing `solana.go` (if it has HeliusRPCRequest)
// or a new `helius_service.go`. They need access to your Helius API key and an HTTP client or Solana RPC client.

func CalculateQualityAndBundling(
	mintAddressForHelius string,
	valResult *ValidationResult,
	heliusSvc *HeliusService, // <-- MODIFIED: Accept HeliusService instance
	appLogger *logger.Logger,
) (TokenQualityAnalysis, error) {
	var analysis TokenQualityAnalysis
	tokenField := zap.String("mintAddress", mintAddressForHelius)

	if heliusSvc == nil {
		appLogger.Error("Helius service client is nil in CalculateQualityAndBundling", tokenField)
		analysis.BundlingInfoString = "--- Holder Concentration ---\n⚠️ Holder analysis service unavailable."
		analysis.QualityRating = 1 // Or some other way to indicate error
		return analysis, errors.New("helius service client not available for bundling analysis")
	}

	totalSupply, err := heliusSvc.GetTokenSupply(mintAddressForHelius) // <-- Use HeliusService method
	if err != nil || totalSupply == 0 {
		appLogger.Error("Failed to get total supply or supply is zero for bundling analysis", tokenField, zap.Error(err))
		analysis.BundlingInfoString = "--- Holder Concentration ---\n⚠️ Holder data unavailable (supply error)."
		analysis.QualityRating = 1
		return analysis, fmt.Errorf("failed to get total supply for %s: %w", mintAddressForHelius, err)
	}

	tokensInLP, err := heliusSvc.GetLPTokensInPair(valResult.PairAddress, mintAddressForHelius) // <-- Use HeliusService method
	if err != nil {
		appLogger.Warn("Could not determine tokens in LP for bundling analysis", tokenField, zap.Error(err))
		tokensInLP = 0
	}
	if totalSupply > 0 {
		analysis.LPTokensPercentOfTotal = (float64(tokensInLP) / float64(totalSupply)) * 100
	} else {
		analysis.LPTokensPercentOfTotal = 0
	}

	solanaBurnAddress := "11111111111111111111111111111111" // Common burn address
	// Query for more holders initially (e.g., 20) to ensure we have enough EOAs after filtering
	topEOAHoldersRaw, err := heliusSvc.GetTopEOAHolders(mintAddressForHelius, 20, valResult.PairAddress, solanaBurnAddress) // <-- Use HeliusService method

	circulatingSupplyForEOAs := float64(totalSupply - tokensInLP)

	if err != nil {
		appLogger.Warn("Failed to get top EOA holders", tokenField, zap.Error(err))
		analysis.BundlingInfoString = fmt.Sprintf("--- Holder Concentration ---\n⚠️ EOA holder data unavailable.\n💧 LP Pool Tokens: %.2f%% of Total", analysis.LPTokensPercentOfTotal)
	} else {
		// Recalculate percentages for the actual EOA holders based on circulating EOA supply
		var processedEOAHolders []HolderInfo
		for _, rawHolder := range topEOAHoldersRaw {
			var perc float64
			if circulatingSupplyForEOAs > 0 {
				perc = (float64(rawHolder.Amount) / circulatingSupplyForEOAs) * 100
			}
			processedEOAHolders = append(processedEOAHolders, HolderInfo{
				Address:    rawHolder.Address,
				Amount:     rawHolder.Amount,
				Percentage: perc,
			})
		}
		// Ensure processedEOAHolders is sorted by Percentage or Amount if GetTopEOAHolders doesn't guarantee final sort after EOA filtering
		// For simplicity, assume GetTopEOAHolders already returns sorted EOA list by amount.

		if len(processedEOAHolders) > 0 {
			analysis.Top1HolderEOAPercent = processedEOAHolders[0].Percentage
		}
		top5CombinedPercent := 0.0
		for i := 0; i < len(processedEOAHolders) && i < 5; i++ {
			top5CombinedPercent += processedEOAHolders[i].Percentage
		}
		analysis.Top5HolderEOAPercent = top5CombinedPercent

		// Update bundling string with calculated percentages
		analysis.BundlingInfoString = fmt.Sprintf(
			"--- Holder Concentration ---\n"+
				"📈 Top 1 Holder (EOA): %.2f%%\n"+
				"📊 Top 5 Holders (EOA): %.2f%%\n"+ // This is now sum of top 5 percentages
				"💧 LP Pool Tokens: %.2f%% of Total",
			analysis.Top1HolderEOAPercent, analysis.Top5HolderEOAPercent, analysis.LPTokensPercentOfTotal,
		)
	}

	// --- Rating Calculation Logic (Same as before) ---
	currentRating := BASE_RATING
	if (valResult.Volume1h < valResult.Volume5m*RATING_STAGNATION_GROWTH_FACTOR) && (float64(valResult.Txns1h) < float64(valResult.Txns5m)*RATING_STAGNATION_GROWTH_FACTOR) {
		if !(valResult.Volume5m == 0 && valResult.Txns5m == 0) {
			currentRating -= RATING_STAGNATION_PENALTY
		}
	}
	if (valResult.Txns5m > RATING_HIGH_TXN_THRESHOLD) && (float64(valResult.Txns1h) < float64(valResult.Txns5m)*RATING_HIGH_TXN_LOW_GROWTH_FACTOR) {
		currentRating -= RATING_HIGH_TXN_LOW_GROWTH_PENALTY
	}
	if (valResult.Txns5m >= RATING_MODERATE_TXN_LOWER_BOUND && valResult.Txns5m <= RATING_MODERATE_TXN_UPPER_BOUND) && (float64(valResult.Txns1h) < float64(valResult.Txns5m)*RATING_MODERATE_TXN_LOW_GROWTH_FACTOR) {
		currentRating -= RATING_MOD_TXN_LOW_GROWTH_PENALTY
	}
	if valResult.LiquidityUSD > 0 {
		if (valResult.Volume5m/valResult.LiquidityUSD > RATING_VOL_LIQ_IMBALANCE_RATIO_THRESHOLD) && (valResult.MarketCap < (LOCAL_RATING_MIN_MARKETCAP * RATING_MIN_MC_MULTIPLIER_FOR_VOL_LIQ)) {
			currentRating -= RATING_VOL_LIQ_LOW_MC_PENALTY
		}
	}
	if valResult.LiquidityUSD > RATING_HIGH_LIQUIDITY_THRESHOLD_FOR_MC_CHECK {
		if valResult.MarketCap < (valResult.LiquidityUSD * RATING_MIN_MC_TO_HIGH_LIQ_RATIO) {
			currentRating -= RATING_HIGH_LIQ_LOW_MC_PENALTY
		}
	}
	if valResult.Txns5m >= RATING_MIN_TX_FOR_BS_RATING_CHECK && valResult.Txns5m > 0 {
		buyRatio5m := float64(valResult.Txns5mBuys) / float64(valResult.Txns5m)
		if buyRatio5m > RATING_BS_IMBALANCE_MODERATE_THRESHOLD || (1.0-buyRatio5m) > RATING_BS_IMBALANCE_MODERATE_THRESHOLD {
			currentRating -= RATING_BS_IMBALANCE_MODERATE_PENALTY
		}
	}
	if valResult.Txns1h >= RATING_MIN_TX_FOR_BS_RATING_CHECK && valResult.Txns1h > 0 {
		buyRatio1h := float64(valResult.Txns1hBuys) / float64(valResult.Txns1h)
		if buyRatio1h > RATING_BS_IMBALANCE_MODERATE_THRESHOLD || (1.0-buyRatio1h) > RATING_BS_IMBALANCE_MODERATE_THRESHOLD {
			currentRating -= RATING_BS_IMBALANCE_MODERATE_PENALTY
		}
	}
	if analysis.Top1HolderEOAPercent > 0 {
		if analysis.Top1HolderEOAPercent > HOLDER_TOP1_EXTREME_THRESHOLD {
			currentRating -= RATING_EXTREME_TOP_1_HOLDER_PENALTY
		} else if analysis.Top1HolderEOAPercent > HOLDER_TOP1_HIGH_THRESHOLD {
			currentRating -= RATING_HIGH_TOP_1_HOLDER_PENALTY
		}
	}
	if analysis.Top5HolderEOAPercent > 0 {
		if analysis.Top5HolderEOAPercent > HOLDER_TOP5_EXTREME_THRESHOLD {
			currentRating -= RATING_EXTREME_TOP_5_HOLDERS_PENALTY
		} else if analysis.Top5HolderEOAPercent > HOLDER_TOP5_HIGH_THRESHOLD {
			currentRating -= RATING_HIGH_TOP_5_HOLDERS_PENALTY
		}
	}
	if analysis.Top1HolderEOAPercent > 0 && analysis.Top1HolderEOAPercent < HOLDER_LOW_CONC_TOP1_MAX &&
		analysis.Top5HolderEOAPercent > 0 && analysis.Top5HolderEOAPercent < HOLDER_LOW_CONC_TOP5_MAX {
		currentRating += RATING_LOW_CONCENTRATION_BONUS
	}
	if valResult.Volume5m > 0 && (valResult.Volume1h/valResult.Volume5m) >= 2.5 &&
		valResult.Txns5m > 0 && (float64(valResult.Txns1h)/float64(valResult.Txns5m)) >= 2.0 {
		currentRating += RATING_STRONG_GROWTH_BONUS
	}
	if currentRating < 1.0 {
		currentRating = 1.0
	}
	if currentRating > 5.0 {
		currentRating = 5.0
	}
	analysis.QualityRating = int(math.Round(currentRating))

	return analysis, nil
}

func GetTokenSupply(mintAddress string, appLogger *logger.Logger) (uint64, error) {
	appLogger.Debug("[PLACEHOLDER] GetTokenSupply: Needs real implementation.", zap.String("mint", mintAddress))
	// TODO: Use your HeliusRPCRequest or a Solana RPC client to call "getTokenSupply"
	// Example using a hypothetical HeliusRPCRequest structure:
	// params := []interface{}{mintAddress} // Params might be an array or map depending on RPC method
	// response, err := HeliusRPCRequest("getTokenSupply", params, appLogger) // Ensure HeliusRPCRequest is accessible
	// if err != nil { return 0, err }
	// // Parse response (e.g., response["result"].(map[string]interface{})["value"].(map[string]interface{})["uiAmountString"])
	// // and convert to uint64.
	return 1000000000, nil // Dummy: 1 Billion total supply for testing
}

func GetLPTokensForPair(lpPairAddress string, targetTokenMint string, appLogger *logger.Logger) (uint64, error) {
	appLogger.Debug("[PLACEHOLDER] GetLPTokensForPair: Needs real implementation.", zap.String("lpPair", lpPairAddress), zap.String("mint", targetTokenMint))
	// TODO: Implement actual Helius/Solana RPC logic. This is the most complex helper.
	// 1. Get all token accounts owned by the lpPairAddress. (e.g., using getProgramAccounts filtered by owner, or getTokenAccountsByOwner).
	//    Make sure to use the *LP Pair Account Address* from DexScreener as the owner.
	// 2. Iterate through these token accounts to find the one whose `mint` field matches targetTokenMint.
	// 3. Get the `amount` (balance) from that specific token account.
	return 450000000, nil // Dummy: 45% of 1B supply for testing
}

func GetTopEOAHolders(mintAddress string, numHoldersToQuery int, lpPairAddress string, knownBurnAddress string, appLogger *logger.Logger) ([]HolderInfo, error) {
	appLogger.Debug("[PLACEHOLDER] GetTopEOAHolders: Needs real implementation.", zap.String("mint", mintAddress))
	// TODO: Implement actual Helius/Solana RPC logic:
	// 1. Call `getTokenLargestAccounts` for `mintAddress` (fetch e.g., top 20-50 to have enough candidates).
	// 2. For each account returned by getTokenLargestAccounts:
	//    a. Get its owner. This often requires an additional `getAccountInfo` call on the token account address to find the `owner` field.
	//    b. If the owner is the `lpPairAddress` (or any known LP program address), skip.
	//    c. If the owner is the `knownBurnAddress`, skip.
	//    d. If it's an EOA, add its address and raw token amount to a list.
	// 3. Sort this list of EOAs by amount.
	// 4. Return the top `numHoldersToQuery` from this filtered & sorted list.
	// HolderInfo.Percentage will be calculated later in CalculateQualityAndBundling.
	return []HolderInfo{ // Dummy data for testing
		{Address: "EOAWhale1...", Amount: 185000000}, {Address: "EOAWhale2...", Amount: 70000000},
		{Address: "EOAWhale3...", Amount: 30000000}, {Address: "EOAWhale4...", Amount: 20000000},
		{Address: "EOAWhale5...", Amount: 10000000}, {Address: "EOAWhale6...", Amount: 8000000},
		{Address: "EOAWhale7...", Amount: 7000000}, {Address: "EOAWhale8...", Amount: 6000000},
		{Address: "EOAWhale9...", Amount: 5000000}, {Address: "EOAWhale10...", Amount: 4000000},
	}, nil
}

func processGraduatedToken(event map[string]interface{}, appLogger *logger.Logger, heliusSvc *HeliusService) error { // ADDED heliusSvc
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
	appLogger.Info("Token passed basic validation. Proceeding to quality rating & bundling analysis...", tokenField)

	// Pass HeliusService instance
	qualityAnalysis, analysisErr := CalculateQualityAndBundling(tokenAddressFromEvent, validationResult, heliusSvc, appLogger) // MODIFIED
	if analysisErr != nil {
		appLogger.Error("Failed to perform quality and bundling analysis", tokenField, zap.Error(analysisErr))
		qualityAnalysis.QualityRating = 0
		qualityAnalysis.BundlingInfoString = "--- Holder Concentration ---\n⚠️ Analysis data unavailable."
	}

	appLogger.Info("Token analysis complete", tokenField, zap.Int("qualityRating", qualityAnalysis.QualityRating))

	// --- Message Assembly (Same as your last complete version) ---
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
	criteriaDetails := fmt.Sprintf("🩸 Liq: $%.0f | 🏛️ MC: $%.0f\n⌛ 5m Vol: $%.0f | ⏳ 1h Vol: $%.0f\n🔎 5m TXN: %d | 🔍 1h TXN: %d", validationResult.LiquidityUSD, validationResult.MarketCap, validationResult.Volume5m, validationResult.Volume1h, validationResult.Txns5m, validationResult.Txns1h)
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
	if qualityAnalysis.QualityRating > 0 {
		stars := strings.Repeat("⭐", qualityAnalysis.QualityRating) + strings.Repeat("☆", 5-qualityAnalysis.QualityRating)
		fmt.Fprintf(captionBuilder, "🔎 Quality: %s (%d/5)\n", stars, qualityAnalysis.QualityRating)
	} else {
		fmt.Fprintf(captionBuilder, "🔎 Quality: Analysis Error or N/A\n")
	}
	fmt.Fprintf(captionBuilder, "%s\n\n", qualityAnalysis.BundlingInfoString)
	dexscreenerURL := fmt.Sprintf("https://dexscreener.com/solana/%s", tokenAddressFromEvent)
	fmt.Fprintf(captionBuilder, "📊 [DexScreener](%s)\n\n", dexscreenerURL)
	fmt.Fprintf(captionBuilder, "---\n*DexScreener Stats*\n")
	fmt.Fprintf(captionBuilder, "%s\n", criteriaDetails)
	if socialsSectionRaw != "" {
		fmt.Fprintf(captionBuilder, "\n%s", socialsSectionRaw)
	}
	rawCaptionToSend := strings.TrimSpace(captionBuilder.String())
	buttons := map[string]string{ /* ... Axiom, Pump.fun, Photon ... */
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
	if validationResult.MarketCap > 0 { /* ... add to trackedProgressCache ... */
	}
	return nil
}

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
		if count := len(tokensToCheck); count == 0 {
			appLogger.Debug("No tokens in progress cache.")
			continue
		} else {
			appLogger.Info("Checking progress for tokens", zap.Int("count", count))
		}
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

func HandleWebhook(payload []byte, appLogger *logger.Logger, heliusSvc *HeliusService) error {
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
			err := processGraduatedToken(event, appLogger, heliusSvc)
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
	return processGraduatedToken(event, appLogger, heliusSvc)
}

func SetupGraduationWebhook(webhookURL string, appLogger *logger.Logger) error {
	appLogger.Info("Setting up Graduation Webhook...", zap.String("url", webhookURL))
	apiKey := env.HeliusAPIKey
	authHeader := env.HeliusAuthHeader // This is the header Helius will SEND TO YOU.
	// webhookSecret := env.WebhookSecret // This might be Helius API secret if not using key in URL for management API calls, or not used.

	pumpFunAuthority := env.PumpFunAuthority
	raydiumAddressesStr := env.RaydiumAccountAddresses

	if apiKey == "" {
		appLogger.Error("HELIUS_API_KEY missing! Cannot set up graduation webhook.")
		return fmt.Errorf("missing HELIUS_API_KEY")
	}
	if webhookURL == "" {
		appLogger.Error("Webhook URL for graduation is empty! Cannot set up webhook.")
		return fmt.Errorf("webhookURL for graduation provided is empty")
	}

	addressesToMonitor := []string{}
	if pumpFunAuthority != "" {
		addressesToMonitor = append(addressesToMonitor, pumpFunAuthority)
		appLogger.Info("Adding PumpFun authority address to graduation webhook.", zap.String("address", pumpFunAuthority))
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
		appLogger.Info("Adding Raydium addresses to graduation webhook.", zap.Int("count", count))
	}

	if len(addressesToMonitor) == 0 {
		appLogger.Warn("No specific addresses (PumpFun authority, Raydium) provided for Graduation webhook monitoring. Webhook might be too broad or ineffective if not targeting specific events.")
		// Decide if this is an error or just a warning. If it must have addresses:
		// return fmt.Errorf("no addresses provided for graduation webhook monitor")
	}
	appLogger.Info("Total addresses to monitor in graduation webhook", zap.Int("count", len(addressesToMonitor)))

	// Check if webhook already exists
	existingWebhook, err := CheckExistingHeliusWebhook(webhookURL, appLogger) // Use your implemented helper
	if err != nil {
		appLogger.Error("Failed to check for existing graduation webhook, attempting creation regardless.", zap.Error(err))
	}
	if existingWebhook {
		appLogger.Info("Graduation webhook already exists for this URL. Skipping creation.", zap.String("url", webhookURL))
		appLogger.Warn("Ensure existing graduation webhook's monitored addresses and transaction types are correct.")
		return nil
	}

	appLogger.Info("Creating new Helius graduation webhook...")
	// For graduation, you might want TRANSFER and SWAP, or more specific types if Helius supports Pump.fun specific events
	requestBody := WebhookRequest{
		WebhookURL:       webhookURL,
		TransactionTypes: []string{"TRANSFER", "SWAP"}, // Adjust as needed for "graduation" events
		AccountAddresses: addressesToMonitor,
		WebhookType:      "enhanced", // Or "raw" or "rawEnhanced" depending on needs
		AuthHeader:       authHeader, // The header Helius will send to your /webhook endpoint
		// TxnStatus: "success", // Optional
	}

	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		appLogger.Error("Failed to serialize graduation webhook request body", zap.Error(err))
		return fmt.Errorf("failed to serialize graduation webhook request: %w", err)
	}

	// Use the Helius Management API endpoint for webhooks
	managementApiURL := fmt.Sprintf("https://api.helius.xyz/v0/webhooks?api-key=%s", apiKey)
	req, err := http.NewRequest("POST", managementApiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		appLogger.Error("Failed to create graduation webhook request object", zap.Error(err))
		return fmt.Errorf("failed to create graduation webhook request object: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// If Helius Management API requires a Bearer token in addition to api-key in URL:
	// if env.HeliusManagementApiToken != "" { req.Header.Set("Authorization", "Bearer "+env.HeliusManagementApiToken) }

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		appLogger.Error("Failed to send graduation webhook creation request to Helius", zap.Error(err))
		return fmt.Errorf("failed to send graduation webhook creation request: %w", err)
	}
	defer resp.Body.Close()

	respBodyBytes, readErr := io.ReadAll(resp.Body)
	responseBodyStr := ""
	if readErr == nil {
		responseBodyStr = string(respBodyBytes)
	} else {
		appLogger.Warn("Failed to read graduation webhook creation response body", zap.Error(readErr))
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		appLogger.Info("Helius graduation webhook created successfully", zap.String("url", webhookURL), zap.Int("status", resp.StatusCode))
		return nil
	}

	appLogger.Error("Failed to create Helius graduation webhook.", zap.Int("status", resp.StatusCode), zap.String("response", responseBodyStr))
	return fmt.Errorf("failed to create helius graduation webhook: status %d, response: %s", resp.StatusCode, responseBodyStr)
}
