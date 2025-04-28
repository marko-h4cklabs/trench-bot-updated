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

	minLiquidity = 30000.0
	minMarketCap = 50000.0
	min5mVolume  = 1000.0
	min1hVolume  = 10000.0
	min5mTx      = 100
	min1hTx      = 500

	maxLiquidity = 55000.0  // NEW
	maxMarketCap = 300000.0 // Existing upper bound for market cap
	max5mVolume  = 50000.0  // NEW
	max1hVolume  = 200000.0 // NEW
	max5mTx      = 400      // NEW
	max1hTx      = 1200     // NEW
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
	Transactions  map[string]TxData  `json:"txns"`
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

type TxData struct {
	Buys  int `json:"buys"`
	Sells int `json:"sells"`
}

type ValidationResult struct {
	IsValid                bool
	PairAddress            string
	LiquidityUSD           float64
	MarketCap              float64
	Volume5m               float64
	Volume1h               float64
	Txns5m                 int
	Txns1h                 int
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
		appLogger.Info("DexScreener global cooldown active, waiting.",
			zap.Duration("waitDuration", waitDuration.Round(time.Second)),
			tokenField)
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
		url := fmt.Sprintf("%s/%s", dexScreenerAPI, tokenCA)
		client := &http.Client{Timeout: 10 * time.Second}
		req, _ := http.NewRequestWithContext(context.Background(), "GET", url, nil)

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
			}

			if pair.Liquidity != nil {
				result.LiquidityUSD = pair.Liquidity.Usd
			} else {
				appLogger.Warn("Liquidity data missing, treating as 0.", tokenField, pairField)
				result.LiquidityUSD = 0.0
			}

			result.MarketCap = pair.FDV
			if pair.MarketCap > 0 {
				result.MarketCap = pair.MarketCap
			}

			volume5m, ok5mVol := pair.Volume["m5"]
			if !ok5mVol {
				appLogger.Warn("Volume data missing", tokenField, pairField, zap.String("period", "m5"))
				result.Volume5m = 0
			} else {
				result.Volume5m = volume5m
			}

			volume1h, ok1hVol := pair.Volume["h1"]
			if !ok1hVol {
				appLogger.Warn("Volume data missing", tokenField, pairField, zap.String("period", "h1"))
				result.Volume1h = 0
			} else {
				result.Volume1h = volume1h
			}

			if txData5m, ok := pair.Transactions["m5"]; ok {
				result.Txns5m = txData5m.Buys + txData5m.Sells
			} else {
				appLogger.Warn("Transaction data missing", tokenField, pairField, zap.String("period", "m5"))
				result.Txns5m = 0
			}

			if txData1h, ok := pair.Transactions["h1"]; ok {
				result.Txns1h = txData1h.Buys + txData1h.Sells
			} else {
				appLogger.Warn("Transaction data missing", tokenField, pairField, zap.String("period", "h1"))
				result.Txns1h = 0
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

			appLogger.Debug("DexScreener data fetched", tokenField, pairField,
				zap.Float64("liqUSD", result.LiquidityUSD),
				zap.Float64("mc", result.MarketCap),
				zap.Float64("vol5m", result.Volume5m),
				zap.Float64("vol1h", result.Volume1h),
				zap.Int("tx5m", result.Txns5m),
				zap.Int("tx1h", result.Txns1h),
				zap.Bool("hasWebsite", result.WebsiteURL != ""),
				zap.Bool("hasTwitter", result.TwitterURL != ""),
				zap.Bool("hasTelegram", result.TelegramURL != ""),
				zap.Bool("hasImage", result.ImageURL != ""),
				zap.Int64("pairCreated", result.PairCreatedAtTimestamp),
			)

			meetsCriteria := true
			failReasons := []string{}

			// --- Minimum Checks ---
			if result.LiquidityUSD < minLiquidity {
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("Liquidity %.0f < %.0f", result.LiquidityUSD, minLiquidity))
			}
			if result.MarketCap < minMarketCap {
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("MarketCap %.0f < %.0f", result.MarketCap, minMarketCap))
			}
			if result.Volume5m < min5mVolume {
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("Vol(5m) %.0f < %.0f", result.Volume5m, min5mVolume))
			}
			if result.Volume1h < min1hVolume {
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("Vol(1h) %.0f < %.0f", result.Volume1h, min1hVolume))
			}
			if result.Txns5m < min5mTx {
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("Tx(5m) %d < %d", result.Txns5m, min5mTx))
			}
			if result.Txns1h < min1hTx {
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("Tx(1h) %d < %d", result.Txns1h, min1hTx))
			}

			// --- Maximum Checks ---
			if result.MarketCap > maxMarketCap { // Existing check, kept for clarity
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("MarketCap %.0f > %.0f", result.MarketCap, maxMarketCap))
			}
			if result.LiquidityUSD > maxLiquidity { // NEW Check
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("Liquidity %.0f > %.0f", result.LiquidityUSD, maxLiquidity))
			}
			if result.Volume5m > max5mVolume { // NEW Check
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("Vol(5m) %.0f > %.0f", result.Volume5m, max5mVolume))
			}
			if result.Volume1h > max1hVolume { // NEW Check
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("Vol(1h) %.0f > %.0f", result.Volume1h, max1hVolume))
			}
			if result.Txns5m > max5mTx { // NEW Check
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("Tx(5m) %d > %d", result.Txns5m, max5mTx))
			}
			if result.Txns1h > max1hTx { // NEW Check
				meetsCriteria = false
				failReasons = append(failReasons, fmt.Sprintf("Tx(1h) %d > %d", result.Txns1h, max1hTx))
			}

			result.IsValid = meetsCriteria
			result.FailReasons = failReasons

			if meetsCriteria {
				appLogger.Debug("Token meets criteria (including max limits)", tokenField)
			} else {
				appLogger.Debug("Token did not meet criteria (min/max checks)", tokenField, zap.Strings("reasons", failReasons))
			}

			return result, nil
		}

		lastErr = fmt.Errorf("API request failed with status: %d", statusCode)

		if statusCode == http.StatusTooManyRequests {
			lastErr = ErrRateLimited
			retryAfterHeader := resp.Header.Get("Retry-After")
			retryAfterSeconds := 0
			if secs, err := strconv.Atoi(retryAfterHeader); err == nil && secs > 0 {
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
			errorMsg := fmt.Sprintf("DexScreener API non-OK status: %d", statusCode)
			if readErr != nil {
				errorMsg += fmt.Sprintf(". Failed to read response body: %v", readErr)
			}
			appLogger.Warn(errorMsg, attemptField, tokenField, statusField, zap.ByteString("body", bodyBytes))
			lastErr = fmt.Errorf(errorMsg)
			if attempt < maxRetries-1 {
				time.Sleep(baseRetryWait * time.Duration(math.Pow(2, float64(attempt))))
			}
			continue
		}
	}

	appLogger.Error("Failed to get valid DexScreener response after retries",
		tokenField,
		zap.Int("attempts", maxRetries),
		zap.Error(lastErr))

	if errors.Is(lastErr, ErrRateLimited) {
		cooldownMutex.Lock()
		now := time.Now()
		currentCoolDownEnd := coolDownUntil
		if currentCoolDownEnd.IsZero() || now.After(currentCoolDownEnd) {
			coolDownUntil = now.Add(time.Duration(globalCooldownSeconds) * time.Second)
			appLogger.Warn("Persistent DexScreener rate limit hit. Activating global cooldown.",
				tokenField,
				zap.Int("cooldownSeconds", globalCooldownSeconds),
				zap.Error(lastErr))
		} else {
			appLogger.Info("Persistent DexScreener rate limit hit, but global cooldown already active.",
				tokenField,
				zap.Time("coolDownUntil", currentCoolDownEnd))
		}
		cooldownMutex.Unlock()
	}

	return nil, lastErr
}
