package services

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

const (
	dexScreenerAPI = "https://api.dexscreener.com/tokens/v1/solana"

	minLiquidity = 40000.0
	minMarketCap = 50000.0
	maxMarketCap = 250000.0
	min5mVolume  = 1000.0
	min1hVolume  = 10000.0
	min5mTx      = 100
	min1hTx      = 500
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
	Liquidity     Liquidity          `json:"liquidity"`
	FDV           float64            `json:"fdv"`
	MarketCap     float64            `json:"marketCap"`
	PairCreatedAt int64              `json:"pairCreatedAt"`
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

func IsTokenValid(tokenCA string) (bool, error) {
	time.Sleep(500 * time.Millisecond)

	log.Printf("Checking token validity on DexScreener: %s", tokenCA)

	url := fmt.Sprintf("%s/%s", dexScreenerAPI, tokenCA)
	client := &http.Client{Timeout: 15 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return false, fmt.Errorf("DexScreener API request failed for %s: %v", tokenCA, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		log.Printf("Rate limit exceeded (429) checking %s. Consider adding delays or exponential backoff.", tokenCA)
		return false, fmt.Errorf("rate limit exceeded (429)")
	} else if resp.StatusCode == http.StatusNotFound {
		log.Printf("Token %s not found on DexScreener (404).", tokenCA)
		return false, nil
	} else if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Printf("DexScreener API request failed for %s with status: %s. Body: %s", tokenCA, resp.Status, string(bodyBytes))
		return false, fmt.Errorf("API request failed with status: %s", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("error reading DexScreener API response for %s: %v", tokenCA, err)
	}

	var pairs []Pair

	err = json.Unmarshal(body, &pairs)
	if err != nil {
		log.Printf("DexScreener JSON Parsing Failed for %s: %v \nRaw Response: %s", tokenCA, err, string(body))
		return false, fmt.Errorf("JSON parsing failed: %v", err)
	}

	if len(pairs) == 0 {
		log.Printf("Token %s found but has no available trading pairs returned by DexScreener.", tokenCA)
		return false, nil
	}

	pair := pairs[0]

	liquidityUSD := pair.Liquidity.Usd
	marketCap := pair.MarketCap

	volume5m, ok5mVol := pair.Volume["m5"]
	if !ok5mVol {
		log.Printf("Warning: Volume data for 'm5' missing for token %s, pair %s. Treating as 0.", tokenCA, pair.PairAddress)
	}
	volume1h, ok1hVol := pair.Volume["h1"]
	if !ok1hVol {
		log.Printf("Warning: Volume data for 'h1' missing for token %s, pair %s. Treating as 0.", tokenCA, pair.PairAddress)
	}

	txns5m := 0
	if txData5m, ok := pair.Transactions["m5"]; ok {
		txns5m = txData5m.Buys + txData5m.Sells
	} else {
		log.Printf("Warning: Transaction data for 'm5' missing for token %s, pair %s. Treating as 0.", tokenCA, pair.PairAddress)
	}

	txns1h := 0
	if txData1h, ok := pair.Transactions["h1"]; ok {
		txns1h = txData1h.Buys + txData1h.Sells
	} else {
		log.Printf("Warning: Transaction data for 'h1' missing for token %s, pair %s. Treating as 0.", tokenCA, pair.PairAddress)
	}

	log.Printf(" DexScreener Data for %s (Pair: %s) - Liquidity: %.2f, MarketCap: %.2f, Vol(5m): %.2f, Vol(1h): %.2f, Tx(5m): %d, Tx(1h): %d",
		tokenCA, pair.PairAddress, liquidityUSD, marketCap, volume5m, volume1h, txns5m, txns1h)

	meetsCriteria := liquidityUSD >= minLiquidity &&
		marketCap >= minMarketCap && marketCap <= maxMarketCap &&
		volume5m >= min5mVolume &&
		volume1h >= min1hVolume &&
		txns5m >= min5mTx &&
		txns1h >= min1hTx

	if meetsCriteria {
		log.Printf("Token %s meets DexScreener criteria!", tokenCA)
		return true, nil
	} else {
		log.Printf("Token %s did not meet DexScreener criteria.", tokenCA)
		if liquidityUSD < minLiquidity {
			log.Printf("   - FAILED: Liquidity %.2f < %.2f", liquidityUSD, minLiquidity)
		}
		if marketCap < minMarketCap {
			log.Printf("   - FAILED: MarketCap %.2f < %.2f", marketCap, minMarketCap)
		}
		if marketCap > maxMarketCap {
			log.Printf("   - FAILED: MarketCap %.2f > %.2f", marketCap, maxMarketCap)
		}
		if volume5m < min5mVolume {
			log.Printf("   - FAILED: Vol(5m) %.2f < %.2f", volume5m, min5mVolume)
		}
		if volume1h < min1hVolume {
			log.Printf("   - FAILED: Vol(1h) %.2f < %.2f", volume1h, min1hVolume)
		}
		if txns5m < min5mTx {
			log.Printf("   - FAILED: Tx(5m) %d < %d", txns5m, min5mTx)
		}
		if txns1h < min1hTx {
			log.Printf("   - FAILED: Tx(1h) %d < %d", txns1h, min1hTx)
		}
		return false, nil
	}
}
