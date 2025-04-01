package tests

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
)

func RunStartupTests() {
	println(" Waiting for the server to start before running tests...")

	if err := godotenv.Load(".env"); err != nil {
		println(".env file NOT found in the current directory.")
	} else {
		println(".env file successfully loaded.")
	}

	webhookURL := os.Getenv("WEBHOOK_LISTENER_URL_DEV")
	if webhookURL == "" {
		webhookURL = "http://localhost:5555/api/v1/webhook"
	}
	println(" Using Webhook URL for tests: %s", webhookURL)

	for i := 0; i < 10; i++ {
		if testAPI(webhookURL, "GET", nil, nil) {
			println(" Webhook Server is up, running tests now!")
			break
		}
		println(" Waiting for Webhook Server to be ready...")
		time.Sleep(1 * time.Second)
	}

	println(" Running Webhook Test...")

	testWebhook(webhookURL)
}

func testWebhook(webhookURL string) {
	webhookSecret := os.Getenv("WEBHOOK_SECRET")

	payload := []byte(`{"events": {"swap": {"tokenOutputs": [{"mint": "9gyfbPVwwZx4y1hotNSLcqXCQNpNqqz6ZRvo8yTLpump"}]}}, "signature": "test-transaction-123"}`)

	headers := map[string]string{
		"Authorization": webhookSecret,
		"Content-Type":  "application/json",
	}

	if testAPI(webhookURL, "POST", payload, headers) {
		println(" Webhook test request successful!")
	} else {
		println(" Webhook test request failed!")
	}
}

func testLocalAPIs(webhookURL string) {
	webhookSecret := os.Getenv("WEBHOOK_SECRET")

	endpoints := []struct {
		path    string
		method  string
		payload []byte
		headers map[string]string
	}{
		{"/api/v1/health", "GET", nil, nil},
		{webhookURL, "POST", []byte(`{"transaction": {"type": "manual_webhook", "amount": 100}, "instructions": []}`),
			map[string]string{"Authorization": webhookSecret, "Content-Type": "application/json"}},
	}

	for _, ep := range endpoints {
		testAPI(ep.path, ep.method, ep.payload, ep.headers)
	}
}

func testAPI(url, method string, payload []byte, headers map[string]string) bool {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf(" Error creating request for %s: %v", url, err)
		return false
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf(" Failed to connect to %s: %v", url, err)
		return false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf(" %s [%s] - Response: %s", method, url, string(body))

	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func testHeliusAPI() bool {
	apiKey := os.Getenv("HELIUS_API_KEY")
	if apiKey == "" {
		println(" Skipping Helius API test due to missing API key")
		return false
	}

	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", apiKey)
	payload := `{"jsonrpc":"2.0","id":1,"method":"getBlockHeight","params":[]}`

	resp, err := http.Post(url, "application/json", bytes.NewBuffer([]byte(payload)))
	if err != nil {
		println(" Failed to connect to Helius API: %v", err)
		return false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	println(" Helius API Response: %s", string(body))

	return resp.StatusCode == 200
}
