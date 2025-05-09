package services

import (
	"ca-scraper/shared/logger"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

var ErrRateLimited = errors.New("dexscreener rate limit exceeded")

var dexScreenerLimiter = rate.NewLimiter(rate.Limit(4.66), 5)

const (
	dexScreenerAPI        = "https://api.dexscreener.com/tokens/v1/solana"
	globalCooldownSeconds = 100

	// --- Base Metric Filters ---
	minLiquidity = 30000.0
	minMarketCap = 50000.0
	maxMarketCap = 300000.0
	min5mVolume  = 1000.0
	min1hVolume  = 10000.0
	min5mTx      = 100
	min1hTx      = 500

	// --- Advanced Filter Constants ---
	// Filter A: Stagnation
	STAGNATION_GROWTH_FACTOR = 1.1 // Volume & TXNs must grow by at least 10%

	// Filter B: High Initial TXN, Low Growth
	HIGH_TXN_THRESHOLD         = 2000
	HIGH_TXN_LOW_GROWTH_FACTOR = 1.25 // TXNs must grow by at least 25% if initial TXNs were high

	// Filter C: Moderate Initial TXN, Very Low Growth
	MODERATE_TXN_LOWER_BOUND       = 700
	MODERATE_TXN_UPPER_BOUND       = 1999
	MODERATE_TXN_LOW_GROWTH_FACTOR = 1.35 // TXNs must grow by at least 35% if initial TXNs were moderate

	// Filter D: Simplified High 5m Volume to Liquidity Ratio with Low MC
	VOL_LIQ_IMBALANCE_RATIO_THRESHOLD = 6.0 // 5m Volume is > 6x Liquidity
	// The MC check for Filter D is `result.MarketCap < (minMarketCap * 1.5)`

	// Filter E: High Liquidity Sanity Check
	HIGH_LIQUIDITY_THRESHOLD_FOR_MC_CHECK = 100000.0 // Apply this check if Liq > $100k
	MIN_MC_TO_HIGH_LIQ_RATIO              = 1.15     // MC must be at least 115% of Liq if Liq is high

	// --- NEW: Filter G Constants (Buy/Sell Imbalance) ---
	MIN_TX_FOR_BS_RATIO_CHECK    = 50   // Minimum total transactions in the period to perform B/S ratio check
	BS_RATIO_IMBALANCE_THRESHOLD = 0.85 // Flag if >85% are buys OR >85% are sells
)

var (
	cooldownMutex sync.RWMutex
	coolDownUntil time.Time
)

type Pair struct {
	ChainID       string             `json:"chainId"`
	DexID         string             `json:"dexId"`
	URL           string             `json:"url"`
	PairAddress   string             `json:"pairAddress"`
	BaseToken     Token              `json:"baseToken"`
	QuoteToken    Token              `json:"quoteToken"`
	PriceNative   string             `json:"priceNative"`
	PriceUsd      string             `json:"priceUsd"`
	Transactions  map[string]TxData  `json:"txns"` // Contains Buys and Sells
	Volume        map[string]float64 `json:"volume"`
	PriceChange   map[string]float64 `json:"priceChange"`
	Liquidity     *Liquidity         `json:"liquidity"`
	FDV           float64            `json:"fdv"`
	MarketCap     float64            `json:"marketCap"`
	PairCreatedAt int64              `json:"pairCreatedAt"`
	Info          *TokenInfo         `json:"info"`
}

type WebsiteInfo struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type SocialInfo struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type TokenInfo struct {
	ImageURL  string        `json:"imageUrl"`
	Header    string        `json:"header"`
	OpenGraph string        `json:"openGraph"`
	Websites  []WebsiteInfo `json:"websites"`
	Socials   []SocialInfo  `json:"socials"`
}

type Token struct {
	Address string `json:"address"`
	Name    string `json:"name"`
	Symbol  string `json:"symbol"`
}

type Liquidity struct {
	Usd   float64 `json:"usd"`
	Base  float64 `json:"base"`
	Quote float64 `json:"quote"`
}

type TxData struct { // This struct from DexScreener contains Buys and Sells
	Buys  int `json:"buys"`
	Sells int `json:"sells"`
}

type ValidationResult struct {
	IsValid                bool
	PairAddress            string
	TokenName              string
	TokenSymbol            string
	LiquidityUSD           float64
	MarketCap              float64
	Volume5m               float64
	Volume1h               float64
	Txns5m                 int // Total 5m transactions (buys + sells)
	Txns1h                 int // Total 1h transactions (buys + sells)
	Txns5mBuys             int // Specific 5m buys
	Txns5mSells            int // Specific 5m sells
	Txns1hBuys             int // Specific 1h buys
	Txns1hSells            int // Specific 1h sells
	FailReasons            []string
	WebsiteURL             string
	TwitterURL             string
	TelegramURL            string
	OtherSocials           map[string]string
	ImageURL               string
	PairCreatedAtTimestamp int64
}

func IsTokenValid(tokenCA string, appLogger *logger.Logger) (*ValidationResult, error) {
	const maxRetries = 3
	baseRetryWait := 1 * time.Second
	var lastErr error
	tokenField := zap.String("tokenCA", tokenCA)

	cooldownMutex.RLock()
	currentCoolDownUntil := coolDownUntil
	cooldownMutex.RUnlock()

	if !currentCoolDownUntil.IsZero() && time.Now().Before(currentCoolDownUntil) {
		waitDuration := time.Until(currentCoolDownUntil)
		appLogger.Info("DexScreener global cooldown active, waiting.", zap.Duration("waitDuration", waitDuration.Round(time.Second)), tokenField)
		time.Sleep(waitDuration)
		appLogger.Info("DexScreener global cooldown finished, proceeding.", tokenField)
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		attemptField := zap.Int("attempt", attempt+1)
		waitCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		err := dexScreenerLimiter.Wait(waitCtx)
		cancel()
		if err != nil {
			appLogger.Error("DexScreener internal rate limiter wait error", tokenField, zap.Error(err))
			return nil, fmt.Errorf("internal rate limiter error for %s: %w", tokenCA, err)
		}

		appLogger.Debug("Checking DexScreener validity", attemptField, tokenField)
		urlAPI := fmt.Sprintf("%s/%s", dexScreenerAPI, tokenCA)
		client := &http.Client{Timeout: 10 * time.Second}
		req, _ := http.NewRequestWithContext(context.Background(), "GET", urlAPI, nil)

		resp, err := client.Do(req)
		if err != nil {
			appLogger.Warn("DexScreener API GET request failed", attemptField, tokenField, zap.Error(err))
			lastErr = fmt.Errorf("API request failed for %s: %w", tokenCA, err)
			if attempt < maxRetries-1 {
				time.Sleep(baseRetryWait * time.Duration(math.Pow(2, float64(attempt))))
			}
			continue
		}

		statusCode := resp.StatusCode
		statusField := zap.Int("status", statusCode)
		bodyBytes, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()

		if statusCode == http.StatusOK {
			var responseData []Pair
			err = json.Unmarshal(bodyBytes, &responseData)
			if err != nil {
				appLogger.Error("DexScreener JSON Parsing Failed", tokenField, zap.Error(err), zap.ByteString("rawResponse", bodyBytes))
				return nil, fmt.Errorf("JSON parsing failed for %s: %w", tokenCA, err)
			}
			if len(responseData) == 0 {
				appLogger.Info("Token found but has no trading pairs on DexScreener.", tokenField)
				return &ValidationResult{IsValid: false, FailReasons: []string{"No trading pairs found"}}, nil
			}

			pair := responseData[0]
			pairField := zap.String("pairAddress", pair.PairAddress)
			result := &ValidationResult{
				PairAddress:            pair.PairAddress,
				PairCreatedAtTimestamp: pair.PairCreatedAt,
				FailReasons:            []string{},
				OtherSocials:           make(map[string]string),
				TokenName:              pair.BaseToken.Name,
				TokenSymbol:            pair.BaseToken.Symbol,
			}

			if pair.Liquidity != nil {
				result.LiquidityUSD = pair.Liquidity.Usd
			}
			result.MarketCap = pair.FDV
			if pair.MarketCap > 0 {
				result.MarketCap = pair.MarketCap
			}
			if vol5m, ok := pair.Volume["m5"]; ok {
				result.Volume5m = vol5m
			}
			if vol1h, ok := pair.Volume["h1"]; ok {
				result.Volume1h = vol1h
			}
			// Populate Buy/Sell counts
			if txData5m, ok := pair.Transactions["m5"]; ok {
				result.Txns5mBuys = txData5m.Buys
				result.Txns5mSells = txData5m.Sells
				result.Txns5m = txData5m.Buys + txData5m.Sells
			}
			if txData1h, ok := pair.Transactions["h1"]; ok {
				result.Txns1hBuys = txData1h.Buys
				result.Txns1hSells = txData1h.Sells
				result.Txns1h = txData1h.Buys + txData1h.Sells
			}

			if pair.Info != nil {
				result.ImageURL = pair.Info.ImageURL
				if len(pair.Info.Websites) > 0 {
					result.WebsiteURL = pair.Info.Websites[0].URL
				}
				for _, social := range pair.Info.Socials {
					switch strings.ToLower(social.Type) {
					case "twitter":
						result.TwitterURL = social.URL
					case "telegram":
						result.TelegramURL = social.URL
					default:
						result.OtherSocials[strings.Title(social.Type)] = social.URL
					}
				}
			}
			appLogger.Debug("DexScreener data fetched for validation", tokenField, pairField,
				zap.Float64("liqUSD", result.LiquidityUSD), zap.Float64("mc", result.MarketCap),
				zap.Float64("vol5m", result.Volume5m), zap.Float64("vol1h", result.Volume1h),
				zap.Int("tx5mTotal", result.Txns5m), zap.Int("tx5mBuys", result.Txns5mBuys), zap.Int("tx5mSells", result.Txns5mSells),
				zap.Int("tx1hTotal", result.Txns1h), zap.Int("tx1hBuys", result.Txns1hBuys), zap.Int("tx1hSells", result.Txns1hSells))

			meetsCriteria := true

			if result.LiquidityUSD <= 0 {
				meetsCriteria = false
				result.FailReasons = append(result.FailReasons, "Liquidity is zero or negative")
			}
			if result.Txns5m > 0 && result.Txns5m == result.Txns1h {
				meetsCriteria = false
				result.FailReasons = append(result.FailReasons, "5m TXNs and 1h TXNs are identical and > 0")
			}
			if result.Volume5m > 0 && result.Volume5m == result.Volume1h {
				meetsCriteria = false
				result.FailReasons = append(result.FailReasons, "5m Volume and 1h Volume are identical and > 0")
			}

			if meetsCriteria {
				if result.LiquidityUSD < minLiquidity {
					meetsCriteria = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Liquidity %.0f < %.0f", result.LiquidityUSD, minLiquidity))
				}
				if result.MarketCap < minMarketCap {
					meetsCriteria = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("MarketCap %.0f < %.0f", result.MarketCap, minMarketCap))
				}
				if result.MarketCap > maxMarketCap {
					meetsCriteria = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("MarketCap %.0f > %.0f", result.MarketCap, maxMarketCap))
				}
				if result.Volume5m < min5mVolume {
					meetsCriteria = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Vol(5m) %.0f < %.0f", result.Volume5m, min5mVolume))
				}
				if result.Volume1h < min1hVolume {
					meetsCriteria = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Vol(1h) %.0f < %.0f", result.Volume1h, min1hVolume))
				}
				if result.Txns5m < min5mTx {
					meetsCriteria = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Tx(5m) %d < %d", result.Txns5m, min5mTx))
				}
				if result.Txns1h < min1hTx {
					meetsCriteria = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Tx(1h) %d < %d", result.Txns1h, min1hTx))
				}
			}

			if meetsCriteria {
				volCheckStagnation := result.Volume1h < result.Volume5m*STAGNATION_GROWTH_FACTOR
				txnCheckStagnation := float64(result.Txns1h) < float64(result.Txns5m)*STAGNATION_GROWTH_FACTOR
				if volCheckStagnation && txnCheckStagnation {
					if !(result.Volume5m == 0 && result.Txns5m == 0) {
						meetsCriteria = false
						result.FailReasons = append(result.FailReasons, fmt.Sprintf("Stagnation: Vol(1h)<Vol(5m)*%.1f AND Tx(1h)<Tx(5m)*%.1f", STAGNATION_GROWTH_FACTOR, STAGNATION_GROWTH_FACTOR))
					}
				}

				if meetsCriteria && (result.Txns5m > HIGH_TXN_THRESHOLD) && (float64(result.Txns1h) < float64(result.Txns5m)*HIGH_TXN_LOW_GROWTH_FACTOR) {
					meetsCriteria = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("High initial TXN with low growth: Tx(5m)>%d AND Tx(1h)<Tx(5m)*%.2f", HIGH_TXN_THRESHOLD, HIGH_TXN_LOW_GROWTH_FACTOR))
				}

				if meetsCriteria && (result.Txns5m >= MODERATE_TXN_LOWER_BOUND && result.Txns5m <= MODERATE_TXN_UPPER_BOUND) && (float64(result.Txns1h) < float64(result.Txns5m)*MODERATE_TXN_LOW_GROWTH_FACTOR) {
					meetsCriteria = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Moderate initial TXN with very low growth: Tx(5m) in [%d,%d] AND Tx(1h)<Tx(5m)*%.2f", MODERATE_TXN_LOWER_BOUND, MODERATE_TXN_UPPER_BOUND, MODERATE_TXN_LOW_GROWTH_FACTOR))
				}

				if meetsCriteria && result.LiquidityUSD > 0 {
					volToLiqRatio := result.Volume5m / result.LiquidityUSD
					if volToLiqRatio > VOL_LIQ_IMBALANCE_RATIO_THRESHOLD && result.MarketCap < (minMarketCap*1.5) {
						meetsCriteria = false
						result.FailReasons = append(result.FailReasons, fmt.Sprintf("High 5m Vol/Liq ratio (%.2fx) with low MC (%.0f, threshold < %.0f)", volToLiqRatio, result.MarketCap, minMarketCap*1.5))
					}
				}

				if meetsCriteria && result.LiquidityUSD > HIGH_LIQUIDITY_THRESHOLD_FOR_MC_CHECK {
					if result.MarketCap < (result.LiquidityUSD * MIN_MC_TO_HIGH_LIQ_RATIO) {
						meetsCriteria = false
						result.FailReasons = append(result.FailReasons, fmt.Sprintf("High liquidity ($%.0f > $%.0f) with insufficient MC uplift (MC $%.0f < Liq $%.0f * %.2f)", result.LiquidityUSD, HIGH_LIQUIDITY_THRESHOLD_FOR_MC_CHECK, result.MarketCap, result.LiquidityUSD, MIN_MC_TO_HIGH_LIQ_RATIO))
					}
				}

				// Filter G: Buy/Sell Imbalance
				if meetsCriteria && result.Txns5m >= MIN_TX_FOR_BS_RATIO_CHECK {
					if result.Txns5m > 0 { // Avoid division by zero
						buyRatio5m := float64(result.Txns5mBuys) / float64(result.Txns5m)
						// sellRatio5m := float64(result.Txns5mSells) / float64(result.Txns5m) // Not strictly needed if checking buys then remaining must be sells
						if buyRatio5m > BS_RATIO_IMBALANCE_THRESHOLD {
							meetsCriteria = false
							result.FailReasons = append(result.FailReasons, fmt.Sprintf("5m Buy Imbalance: %.0f%% Buys (Threshold >%.0f%%)", buyRatio5m*100, BS_RATIO_IMBALANCE_THRESHOLD*100))
						} else if (1.0 - buyRatio5m) > BS_RATIO_IMBALANCE_THRESHOLD { // Check sell ratio (1 - buyRatio)
							meetsCriteria = false
							result.FailReasons = append(result.FailReasons, fmt.Sprintf("5m Sell Imbalance: %.0f%% Sells (Threshold >%.0f%%)", (1.0-buyRatio5m)*100, BS_RATIO_IMBALANCE_THRESHOLD*100))
						}
					}
				}
				if meetsCriteria && result.Txns1h >= MIN_TX_FOR_BS_RATIO_CHECK {
					if result.Txns1h > 0 { // Avoid division by zero
						buyRatio1h := float64(result.Txns1hBuys) / float64(result.Txns1h)
						// sellRatio1h := float64(result.Txns1hSells) / float64(result.Txns1h)
						if buyRatio1h > BS_RATIO_IMBALANCE_THRESHOLD {
							meetsCriteria = false
							result.FailReasons = append(result.FailReasons, fmt.Sprintf("1h Buy Imbalance: %.0f%% Buys (Threshold >%.0f%%)", buyRatio1h*100, BS_RATIO_IMBALANCE_THRESHOLD*100))
						} else if (1.0 - buyRatio1h) > BS_RATIO_IMBALANCE_THRESHOLD { // Check sell ratio
							meetsCriteria = false
							result.FailReasons = append(result.FailReasons, fmt.Sprintf("1h Sell Imbalance: %.0f%% Sells (Threshold >%.0f%%)", (1.0-buyRatio1h)*100, BS_RATIO_IMBALANCE_THRESHOLD*100))
						}
					}
				}
			}

			result.IsValid = meetsCriteria
			if result.IsValid {
				appLogger.Debug("Token passed all validation criteria.", tokenField)
			} else {
				appLogger.Info("Token failed validation criteria.", tokenField, zap.Strings("reasons", result.FailReasons))
			}
			return result, nil
		}

		lastErr = fmt.Errorf("API request failed with status: %d", statusCode)
		if statusCode == http.StatusTooManyRequests {
			lastErr = ErrRateLimited
			retryAfterHeader := resp.Header.Get("Retry-After")
			retryAfterSeconds := 0
			if secs, errConv := strconv.Atoi(retryAfterHeader); errConv == nil && secs > 0 {
				retryAfterSeconds = secs
			} else {
				retryAfterSeconds = int(math.Pow(2, float64(attempt))) + 1
			}
			maxWait := 60
			if retryAfterSeconds > maxWait {
				retryAfterSeconds = maxWait
			}
			appLogger.Warn("DexScreener rate limit hit", attemptField, tokenField, statusField, zap.Int("retryAfter", retryAfterSeconds))
			if attempt < maxRetries-1 {
				time.Sleep(time.Duration(retryAfterSeconds) * time.Second)
			}
			continue
		} else if statusCode == http.StatusNotFound {
			appLogger.Info("Token not found on DexScreener", tokenField, statusField)
			return &ValidationResult{IsValid: false, FailReasons: []string{"Token not found on DexScreener"}}, nil
		} else {
			errorMsgPart := fmt.Sprintf("DexScreener API non-OK status: %d", statusCode)
			if readErr != nil {
				errorMsgPart += fmt.Sprintf(". Failed to read response body: %v", readErr)
			} else {
				errorMsgPart += fmt.Sprintf(". Body: %s", string(bodyBytes))
			}
			appLogger.Warn(errorMsgPart, attemptField, tokenField, statusField)
			lastErr = fmt.Errorf(errorMsgPart)
			if attempt < maxRetries-1 {
				time.Sleep(baseRetryWait * time.Duration(math.Pow(2, float64(attempt))))
			}
			continue
		}
	}

	appLogger.Error("Failed to get valid DexScreener response after retries", tokenField, zap.Int("attempts", maxRetries), zap.Error(lastErr))
	if errors.Is(lastErr, ErrRateLimited) {
		cooldownMutex.Lock()
		now := time.Now()
		currentCoolDownEnd := coolDownUntil
		if currentCoolDownEnd.IsZero() || now.After(currentCoolDownEnd) {
			coolDownUntil = now.Add(time.Duration(globalCooldownSeconds) * time.Second)
			appLogger.Warn("Persistent DexScreener rate limit hit. Activating global cooldown.", tokenField, zap.Int("cooldownSeconds", globalCooldownSeconds), zap.Error(lastErr))
		} else {
			appLogger.Info("Persistent DexScreener rate limit hit, but global cooldown already active.", tokenField, zap.Time("coolDownUntil", currentCoolDownEnd))
		}
		cooldownMutex.Unlock()
	}
	return nil, lastErr
}
