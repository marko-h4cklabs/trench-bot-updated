package services

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strconv"
	"time"
)

func FetchSOLPrice() (float64, error) {
	price, err := fetchFromCoinGecko()
	if err == nil {
		log.Printf("✅ Fetched SOL Price from CoinGecko: $%.2f", price)
		return price, nil
	}

	log.Println("⚠️ CoinGecko API failed. Switching to Binance API...")
	price, err = fetchFromBinance()
	if err == nil {
		log.Printf("✅ Fetched SOL Price from Binance: $%.2f", price)
	}
	return price, err
}

func fetchFromCoinGecko() (float64, error) {
	url := "https://api.coingecko.com/api/v3/simple/price?ids=solana&vs_currencies=usd"
	return fetchWithRetries(url, "solana", "usd")
}

func fetchFromBinance() (float64, error) {
	url := "https://api.binance.com/api/v3/ticker/price?symbol=SOLUSDT"
	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(url)
	if err != nil {
		return 0, fmt.Errorf("❌ Failed to fetch from Binance: %v", err)
	}
	defer resp.Body.Close()

	var result map[string]string
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("❌ Failed to read Binance response: %v", err)
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return 0, fmt.Errorf("❌ Failed to parse Binance JSON: %v", err)
	}

	priceStr, exists := result["price"]
	if !exists {
		return 0, fmt.Errorf("❌ SOL price not found in Binance response")
	}

	price, err := strconv.ParseFloat(priceStr, 64)
	if err != nil {
		return 0, fmt.Errorf("❌ Failed to convert Binance price: %v", err)
	}

	return price, nil
}

func fetchWithRetries(url, mainKey, subKey string) (float64, error) {
	maxRetries := 3
	client := &http.Client{Timeout: 5 * time.Second}

	var data map[string]map[string]float64
	var err error

	for i := 0; i < maxRetries; i++ {
		resp, err := client.Get(url)
		if err != nil {
			log.Printf("⚠️ Attempt %d: API call failed: %v", i+1, err)
			time.Sleep(time.Duration(math.Pow(2, float64(i))) * time.Second)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			log.Printf("⏳ API rate limit reached. Retrying after 10 seconds...")
			time.Sleep(10 * time.Second)
			continue
		}

		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			log.Printf("⚠️ Attempt %d: Failed to read response: %v", i+1, err)
			time.Sleep(time.Duration(math.Pow(2, float64(i))) * time.Second)
			continue
		}

		if err := json.Unmarshal(body, &data); err != nil {
			log.Printf("⚠️ Attempt %d: Failed to parse JSON: %v", i+1, err)
			time.Sleep(time.Duration(math.Pow(2, float64(i))) * time.Second)
			continue
		}

		if price, exists := data[mainKey][subKey]; exists {
			log.Printf("✅ SOL Price Retrieved: $%.2f", price)
			return price, nil
		}

		log.Printf("⚠️ Attempt %d: Price not found in response", i+1)
		time.Sleep(time.Duration(math.Pow(2, float64(i))) * time.Second)
	}

	return 0, fmt.Errorf("❌ API call failed after %d retries: %v", maxRetries, err)
}
