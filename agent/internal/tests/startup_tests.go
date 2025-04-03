package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/joho/godotenv"
)

func RunStartupTests() {
	log.Println("--- Running Startup Tests ---")

	if err := godotenv.Load(".env"); err != nil {
		log.Println("Info: .env file not found or error loading:", err)
	} else {
		log.Println(".env file successfully loaded.")
	}

	webhookURL := os.Getenv("WEBHOOK_LISTENER_URL_DEV")
	if webhookURL == "" {
		log.Println("Warning: WEBHOOK_LISTENER_URL_DEV not set, defaulting to localhost for tests. This might not work in deployed environments.")
		webhookURL = "http://localhost:5555/api/v1/webhook"
	}
	log.Printf("Using Webhook URL for tests: %s", webhookURL)

	initialDelay := 5 * time.Second
	log.Printf("Waiting for initial %v server startup grace period...", initialDelay)
	time.Sleep(initialDelay)

	log.Println("Probing server readiness...")
	serverReady := false
	maxRetries := 15
	for i := 0; i < maxRetries; i++ {
		healthURL := os.Getenv("HEALTH_CHECK_URL")
		if healthURL == "" {
			healthURL = webhookURL
		}

		log.Printf("Attempt %d/%d: Pinging %s...", i+1, maxRetries, healthURL)
		if testAPI(healthURL, "GET", nil, nil) {
			log.Println("Webhook Server is up!")
			serverReady = true
			break
		}
		log.Println("Server not ready yet...")
		time.Sleep(3 * time.Second)
	}

	if !serverReady {
		log.Println("Warning: Server did not become ready after probing. Proceeding with tests anyway...")
	}

	log.Println("Running Webhook Test...")
	testPassed := testWebhook(webhookURL)

	log.Println("Testing Helius RPC API...")
	testHeliusAPI()

	if testPassed {
		log.Println("✅ Startup Test Passed: Notifying Telegram.")
		sendTelegram("✅ All startup tests passed. Start scanning the markets.")
	} else {
		log.Println("❌ Webhook test failed.")
	}

	log.Println("--- Startup Tests Complete ---")
}

func testWebhook(webhookURL string) bool {
	webhookSecret := os.Getenv("WEBHOOK_SECRET")

	sampleTx := map[string]interface{}{
		"signature": "test-transaction-123",
		"events": map[string]interface{}{
			"swap": map[string]interface{}{
				"tokenOutputs": []interface{}{
					map[string]interface{}{
						"mint": "21AErpiB8uSb94oQKRcwuHqyHF93njAxBSbdUrpupump",
					},
				},
			},
		},
	}

	jsonBody, err := json.Marshal(sampleTx)
	if err != nil {
		log.Printf("Failed to marshal test payload: %v", err)
		return false
	}

	headers := map[string]string{
		"Authorization": webhookSecret,
		"Content-Type":  "application/json",
	}

	if testAPI(webhookURL, "POST", jsonBody, headers) {
		log.Println("Webhook test request successful!")
		return true
	}
	log.Println("Webhook test request failed!")
	return false
}

func testAPI(url, method string, payload []byte, headers map[string]string) bool {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("Error creating request for %s: %v", url, err)
		return false
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Failed to connect to %s: %v", url, err)
		return false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("%s [%s] - Response: %s", method, url, string(body))

	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func testHeliusAPI() bool {
	apiKey := os.Getenv("HELIUS_API_KEY")
	if apiKey == "" {
		log.Println("Skipping Helius API test due to missing API key")
		return false
	}

	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", apiKey)
	payload := `{"jsonrpc":"2.0","id":1,"method":"getBlockHeight","params":[]}`

	resp, err := http.Post(url, "application/json", bytes.NewBuffer([]byte(payload)))
	if err != nil {
		log.Printf("Failed to connect to Helius API: %v", err)
		return false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("Helius API Response: %s", string(body))

	return resp.StatusCode == 200
}

func sendTelegram(message string) {
	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	groupID := os.Getenv("TELEGRAM_GROUP_ID")

	if botToken == "" || groupID == "" {
		log.Println("Telegram credentials missing. Skipping notification.")
		return
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)
	payload := fmt.Sprintf(`{"chat_id": "%s", "text": "%s"}`, groupID, message)

	resp, err := http.Post(url, "application/json", bytes.NewBuffer([]byte(payload)))
	if err != nil {
		log.Printf("Failed to send Telegram message: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Println("✅ Telegram notification sent.")
	} else {
		log.Printf("❌ Failed to send Telegram message. Status: %d", resp.StatusCode)
	}
}
