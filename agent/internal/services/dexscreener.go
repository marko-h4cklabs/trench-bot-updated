package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"golang.org/x/time/rate"
)

var dexScreenerLimiter = rate.NewLimiter(rate.Limit(4.66), 5)

const (
	dexScreenerAPI = "https://api.dexscreener.com/tokens/v1/solana"

	minLiquidity = 40000.0
	minMarketCap = 20000.0
	maxMarketCap = 300000.0
	min5mVolume  = 1000.0
	min1hVolume  = 10000.0
	min5mTx      = 80
	min1hTx      = 350
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

func IsTokenValid(tokenCA string) (*ValidationResult, error) {
	if err := dexScreenerLimiter.Wait(context.Background()); err != nil {
		log.Printf("ERROR: DexScreener rate limiter wait error for %s: %v", tokenCA, err)
		return nil, fmt.Errorf("rate limiter error for %s: %w", tokenCA, err)
	}

	log.Printf("Checking token validity on DexScreener: %s", tokenCA)

	url := fmt.Sprintf("%s/%s", dexScreenerAPI, tokenCA)
	client := &http.Client{Timeout: 10 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		log.Printf("ERROR: DexScreener API GET request failed for %s: %v", tokenCA, err)
		return nil, fmt.Errorf("DexScreener API request failed for %s: %w", tokenCA, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		log.Printf("WARN: Rate limit hit checking DexScreener for %s (Status: %d)", tokenCA, resp.StatusCode)
		return nil, fmt.Errorf("rate limit exceeded (429)")
	} else if resp.StatusCode == http.StatusNotFound {
		log.Printf("INFO: Token %s not found on DexScreener (Status: %d)", tokenCA, resp.StatusCode)
		return &ValidationResult{IsValid: false, FailReasons: []string{"Token not found on DexScreener"}}, nil
	} else if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		errorMsg := fmt.Sprintf("DexScreener API request failed for %s with status: %s", tokenCA, resp.Status)
		bodyStr := ""
		if readErr == nil && len(bodyBytes) > 0 {
			bodyStr = string(bodyBytes)
			errorMsg += fmt.Sprintf(". Body: %s", bodyStr)
		} else if readErr != nil {
			errorMsg += fmt.Sprintf(". Failed to read response body: %v", readErr)
		}
		log.Printf("ERROR: DexScreener API non-OK status for %s: %s", tokenCA, errorMsg)
		return nil, fmt.Errorf("API request failed with status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("ERROR: Error reading DexScreener API response body for %s: %v", tokenCA, err)
		return nil, fmt.Errorf("error reading API response for %s: %w", tokenCA, err)
	}

	var responseData []Pair
	err = json.Unmarshal(body, &responseData)
	if err != nil {
		log.Printf("ERROR: DexScreener JSON Parsing Failed for %s: %v \nRaw Response: %s", tokenCA, err, string(body))
		return nil, fmt.Errorf("JSON parsing failed for %s: %w", tokenCA, err)
	}

	if len(responseData) == 0 {
		log.Printf("INFO: Token %s found but has no trading pairs on DexScreener.", tokenCA)
		return &ValidationResult{IsValid: false, FailReasons: []string{"No trading pairs found"}}, nil
	}

	pair := responseData[0]
	result := &ValidationResult{
		PairAddress:  pair.PairAddress,
		FailReasons:  []string{},
		OtherSocials: make(map[string]string),
	}

	if pair.Liquidity != nil {
		result.LiquidityUSD = pair.Liquidity.Usd
	} else {
		log.Printf("Warning: Liquidity data missing for token %s, pair %s. Treating as 0.", tokenCA, pair.PairAddress)
		result.LiquidityUSD = 0.0
	}

	result.MarketCap = pair.FDV
	if pair.MarketCap > 0 {
		result.MarketCap = pair.MarketCap
	}

	volume5m, ok5mVol := pair.Volume["m5"]
	if !ok5mVol {
		log.Printf("Warning: Volume data for 'm5' missing for token %s, pair %s. Treating as 0.", tokenCA, pair.PairAddress)
		result.Volume5m = 0
	} else {
		result.Volume5m = volume5m
	}

	volume1h, ok1hVol := pair.Volume["h1"]
	if !ok1hVol {
		log.Printf("Warning: Volume data for 'h1' missing for token %s, pair %s. Treating as 0.", tokenCA, pair.PairAddress)
		result.Volume1h = 0
	} else {
		result.Volume1h = volume1h
	}

	if txData5m, ok := pair.Transactions["m5"]; ok {
		result.Txns5m = txData5m.Buys + txData5m.Sells
	} else {
		log.Printf("Warning: Transaction data for 'm5' missing for token %s, pair %s. Treating as 0.", tokenCA, pair.PairAddress)
		result.Txns5m = 0
	}

	if txData1h, ok := pair.Transactions["h1"]; ok {
		result.Txns1h = txData1h.Buys + txData1h.Sells
	} else {
		log.Printf("Warning: Transaction data for 'h1' missing for token %s, pair %s. Treating as 0.", tokenCA, pair.PairAddress)
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

	log.Printf("INFO: DexScreener Data fetched for %s (Pair: %s) - Liq: %.2f, MC: %.2f, Vol(5m): %.2f, Vol(1h): %.2f, Tx(5m): %d, Tx(1h): %d, Website: %s, Twitter: %s, Telegram: %s, Image: %s",
		tokenCA, result.PairAddress, result.LiquidityUSD, result.MarketCap, result.Volume5m, result.Volume1h, result.Txns5m, result.Txns1h, result.WebsiteURL, result.TwitterURL, result.TelegramURL, result.ImageURL)

	meetsCriteria := true
	failReasons := []string{}

	if result.LiquidityUSD < minLiquidity {
		meetsCriteria = false
		reason := fmt.Sprintf("Liquidity %.2f < %.2f", result.LiquidityUSD, minLiquidity)
		failReasons = append(failReasons, reason)
		log.Printf("INFO: Criteria fail for %s: %s", tokenCA, reason)
	}
	if result.MarketCap < minMarketCap {
		meetsCriteria = false
		reason := fmt.Sprintf("MarketCap %.2f < %.2f", result.MarketCap, minMarketCap)
		failReasons = append(failReasons, reason)
		log.Printf("INFO: Criteria fail for %s: %s", tokenCA, reason)
	}
	if result.MarketCap > maxMarketCap {
		meetsCriteria = false
		reason := fmt.Sprintf("MarketCap %.2f > %.2f", result.MarketCap, maxMarketCap)
		failReasons = append(failReasons, reason)
		log.Printf("INFO: Criteria fail for %s: %s", tokenCA, reason)
	}
	if result.Volume5m < min5mVolume {
		meetsCriteria = false
		reason := fmt.Sprintf("Vol(5m) %.2f < %.2f", result.Volume5m, min5mVolume)
		failReasons = append(failReasons, reason)
		log.Printf("INFO: Criteria fail for %s: %s", tokenCA, reason)
	}
	if result.Volume1h < min1hVolume {
		meetsCriteria = false
		reason := fmt.Sprintf("Vol(1h) %.2f < %.2f", result.Volume1h, min1hVolume)
		failReasons = append(failReasons, reason)
		log.Printf("INFO: Criteria fail for %s: %s", tokenCA, reason)
	}
	if result.Txns5m < min5mTx {
		meetsCriteria = false
		reason := fmt.Sprintf("Tx(5m) %d < %d", result.Txns5m, min5mTx)
		failReasons = append(failReasons, reason)
		log.Printf("INFO: Criteria fail for %s: %s", tokenCA, reason)
	}

	if result.Txns1h < min1hTx {
		meetsCriteria = false
		reason := fmt.Sprintf("Tx(1h) %d < %d", result.Txns1h, min1hTx)
		failReasons = append(failReasons, reason)
		log.Printf("INFO: Criteria fail for %s: %s", tokenCA, reason)
	}
	result.IsValid = meetsCriteria
	result.FailReasons = failReasons

	if meetsCriteria {
		log.Printf("INFO: Token %s meets DexScreener criteria!", tokenCA)
	} else {
		log.Printf("INFO: Token %s did not meet DexScreener criteria. Reasons: %s", tokenCA, strings.Join(failReasons, "; "))
	}

	return result, nil
}
