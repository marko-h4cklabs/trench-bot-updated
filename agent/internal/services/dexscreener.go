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

	// --- REVERTED Base Metric Filters ---
	minLiquidity = 35000.0
	minMarketCap = 80000.0
	maxMarketCap = 400000.0 // Updated as per your request
	min5mVolume  = 1000.0
	min1hVolume  = 10000.0
	min5mTx      = 100
	min1hTx      = 500

	// --- Constants for Hard Filters in IsTokenValid ---
	MIN_TX_FOR_BS_RATIO_CHECK_HARD_FILTER    = 50   // Min total TXNs in period to check B/S ratio for hard filter
	BS_RATIO_IMBALANCE_THRESHOLD_HARD_FILTER = 0.90 // Flag as INVALID if >90% buys OR >90% sells
)

var (
	cooldownMutex sync.RWMutex
	coolDownUntil time.Time
)

// --- Structs (Complete as they should be) ---
type Pair struct {
	ChainID       string             `json:"chainId"`
	DexID         string             `json:"dexId"`
	URL           string             `json:"url"`
	PairAddress   string             `json:"pairAddress"`
	BaseToken     Token              `json:"baseToken"`
	QuoteToken    Token              `json:"quoteToken"`
	PriceNative   string             `json:"priceNative"`
	PriceUsd      string             `json:"priceUsd"`
	Transactions  map[string]TxData  `json:"txns"`
	Volume        map[string]float64 `json:"volume"`
	PriceChange   map[string]float64 `json:"priceChange"`
	Liquidity     *LiquidityStruct   `json:"liquidity"` // Renamed to LiquidityStruct to avoid conflict
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

type LiquidityStruct struct { // Renamed to avoid conflict with a potential local variable
	Usd   float64 `json:"usd"`
	Base  float64 `json:"base"`
	Quote float64 `json:"quote"`
}

type TxData struct {
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
	Txns5m                 int
	Txns1h                 int
	Txns5mBuys             int
	Txns5mSells            int
	Txns1hBuys             int
	Txns1hSells            int
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

		appLogger.Debug("Fetching DexScreener data for basic validation", attemptField, tokenField)
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
			appLogger.Debug("DexScreener data fetched", tokenField, zap.String("pairAddress", result.PairAddress),
				zap.Float64("liqUSD", result.LiquidityUSD), zap.Float64("mc", result.MarketCap),
				zap.Float64("vol5m", result.Volume5m), zap.Float64("vol1h", result.Volume1h),
				zap.Int("tx5mTotal", result.Txns5m), zap.Int("tx5mBuys", result.Txns5mBuys), zap.Int("tx5mSells", result.Txns5mSells),
				zap.Int("tx1hTotal", result.Txns1h), zap.Int("tx1hBuys", result.Txns1hBuys), zap.Int("tx1hSells", result.Txns1hSells))

			meetsMinimums := true

			// 1. Hard Early Checks
			if result.LiquidityUSD <= 0 {
				meetsMinimums = false
				result.FailReasons = append(result.FailReasons, "Liquidity is zero or negative")
			}
			// Extreme Buy/Sell Imbalance Hard Filter (90%)
			if meetsMinimums && result.Txns5m >= MIN_TX_FOR_BS_RATIO_CHECK_HARD_FILTER {
				if result.Txns5m > 0 { // Avoid division by zero
					buyRatio5m := float64(result.Txns5mBuys) / float64(result.Txns5m)
					if buyRatio5m > BS_RATIO_IMBALANCE_THRESHOLD_HARD_FILTER || (1.0-buyRatio5m) > BS_RATIO_IMBALANCE_THRESHOLD_HARD_FILTER {
						meetsMinimums = false
						result.FailReasons = append(result.FailReasons, fmt.Sprintf("Extreme 5m Buy/Sell Imbalance (>%.0f%%)", BS_RATIO_IMBALANCE_THRESHOLD_HARD_FILTER*100))
					}
				}
			}
			if meetsMinimums && result.Txns1h >= MIN_TX_FOR_BS_RATIO_CHECK_HARD_FILTER {
				if result.Txns1h > 0 { // Avoid division by zero
					buyRatio1h := float64(result.Txns1hBuys) / float64(result.Txns1h)
					if buyRatio1h > BS_RATIO_IMBALANCE_THRESHOLD_HARD_FILTER || (1.0-buyRatio1h) > BS_RATIO_IMBALANCE_THRESHOLD_HARD_FILTER {
						meetsMinimums = false
						result.FailReasons = append(result.FailReasons, fmt.Sprintf("Extreme 1h Buy/Sell Imbalance (>%.0f%%)", BS_RATIO_IMBALANCE_THRESHOLD_HARD_FILTER*100))
					}
				}
			}

			// 2. Base Min/Max Filters
			if meetsMinimums {
				if result.LiquidityUSD < minLiquidity {
					meetsMinimums = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Liquidity %.0f < %.0f", result.LiquidityUSD, minLiquidity))
				}
				if result.MarketCap < minMarketCap {
					meetsMinimums = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("MarketCap %.0f < %.0f", result.MarketCap, minMarketCap))
				}
				if result.MarketCap > maxMarketCap {
					meetsMinimums = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("MarketCap %.0f > %.0f", result.MarketCap, maxMarketCap))
				}
				if result.Volume5m < min5mVolume {
					meetsMinimums = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Vol(5m) %.0f < %.0f", result.Volume5m, min5mVolume))
				}
				if result.Volume1h < min1hVolume {
					meetsMinimums = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Vol(1h) %.0f < %.0f", result.Volume1h, min1hVolume))
				}
				if result.Txns5m < min5mTx {
					meetsMinimums = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Tx(5m) %d < %d", result.Txns5m, min5mTx))
				}
				if result.Txns1h < min1hTx {
					meetsMinimums = false
					result.FailReasons = append(result.FailReasons, fmt.Sprintf("Tx(1h) %d < %d", result.Txns1h, min1hTx))
				}
			}

			result.IsValid = meetsMinimums

			if !result.IsValid {
				appLogger.Info("Token failed basic validation criteria or hard rug checks.", tokenField, zap.Strings("reasons", result.FailReasons))
			} else {
				appLogger.Debug("Token passed basic validation. Advanced rating and bundling analysis will follow.", tokenField)
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
