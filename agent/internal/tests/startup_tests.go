package tests

import (
	"bytes"
	"ca-scraper/shared/env"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"time"
)

func RunStartupTests() {
	log.Println("--- Running Startup Tests ---")
	webhookURL := env.WebhookURL
	if webhookURL == "" {
		log.Println("CRITICAL: WEBHOOK_LISTENER_URL_DEV not set in environment. Startup tests cannot proceed meaningfully.")

		webhookURL = "http://localhost:5555/api/v1/webhook"
	}
	log.Printf("Using Webhook URL for tests: %s", webhookURL)

	initialDelay := 5 * time.Second
	log.Printf("Waiting for initial %v server startup grace period...", initialDelay)
	time.Sleep(initialDelay)

	log.Println("Probing server readiness...")
	serverReady := false
	maxRetries := 15
	retryInterval := 3 * time.Second
	probeTimeout := 5 * time.Second

	probeClient := &http.Client{Timeout: probeTimeout}

	healthURL := webhookURL

	for i := 0; i < maxRetries; i++ {
		log.Printf("Attempt %d/%d: Pinging %s...", i+1, maxRetries, healthURL)
		req, err := http.NewRequest("GET", healthURL, nil)
		if err != nil {
			log.Printf(" Probe Error (creating request): %v", err)
			time.Sleep(retryInterval)
			continue
		}

		resp, err := probeClient.Do(req)
		if err != nil {
			log.Printf(" Probe Error (connecting): %v", err)
			log.Println(" Server not ready yet...")
			time.Sleep(retryInterval)
			continue
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Println(" Server is up!")
			serverReady = true
			resp.Body.Close()
			break
		} else {
			log.Printf(" Server responded with status %s. Not ready yet...", resp.Status)
			resp.Body.Close()
			time.Sleep(retryInterval)
		}
	}

	if !serverReady {
		log.Println("FATAL: Server did not become ready after probing. Aborting further tests.")
		return
	}
	log.Println("Running Initial Simple Webhook Test (like old TestWebhookOnStartup)...")
	initialTestPassed := testSimpleWebhookPost(webhookURL)

	log.Println("Running More Realistic Webhook Test (like old testWebhook)...")
	realisticTestPassed := testRealisticWebhookPost(webhookURL)

	log.Println("Testing Helius RPC API...")
	heliusTestPassed := testHeliusAPI()

	allTestsPassed := initialTestPassed && realisticTestPassed && heliusTestPassed

	if allTestsPassed {
		log.Println("All Startup Tests Passed: Notifying Telegram.")
		sendTelegram("All startup tests passed successfully.")
	} else {
		log.Println(" One or more startup tests failed.")
	}

	log.Println("--- Startup Tests Complete ---")
}

func testSimpleWebhookPost(webhookURL string) bool {
	log.Printf(" -> Sending simple POST test to: %s", webhookURL)
	authHeader := env.HeliusAuthHeader
	payloadArray := `[
		{
			"description": "Startup Test Event (Simple)",
			"timestamp": ` + fmt.Sprintf("%d", time.Now().Unix()) + `,
			"type": "TRANSFER",
			"source": "SYSTEM_TEST_SIMPLE",
			"signature": "startup-test-sig-simple-` + fmt.Sprintf("%d", time.Now().UnixNano()) + `",
			"tokenTransfers": [{"mint": "HqqnXZ8S76rY3GnXgHR9LpbLEKNfxCZASWydNHydtest"}]
		}
	]`
	payloadBytes := []byte(payloadArray)

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if authHeader != "" {
		headers["Authorization"] = authHeader
		log.Printf("    -> with Authorization header.")
	}

	client := &http.Client{Timeout: 20 * time.Second}

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer(payloadBytes))
	if err != nil {
		log.Printf("    -> ERROR creating simple test request: %v", err)
		return false
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("    -> ERROR sending simple test request: %v", err)
		return false
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("    -> Simple Test Response Status: %s", resp.Status)
	log.Printf("    -> Simple Test Response Body: %s", string(body))

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Println("    -> Simple Webhook Test successful!")
		return true
	} else {
		log.Printf("    -> WARN: Simple Webhook Test received non-OK status: %s", resp.Status)
		return false
	}
}

func testRealisticWebhookPost(webhookURL string) bool {
	log.Printf(" -> Sending realistic POST test to: %s", webhookURL)
	authHeader := env.HeliusAuthHeader

	sampleTx := map[string]interface{}{
		"description": "Startup Test Event (Realistic)",
		"type":        "SWAP",
		"source":      "SYSTEM_TEST_REALISTIC",
		"signature":   fmt.Sprintf("startup-test-sig-realistic-%d", time.Now().UnixNano()),
		"timestamp":   time.Now().Unix(),
		"tokenTransfers": []interface{}{
			map[string]interface{}{
				"mint": "21AErpiB8uSb94oQKRcwuHqyHF93njAxBSbdUrpupump",
			},
		},
		"nativeTransfers": []interface{}{},
		"accountData":     []interface{}{},
		"events": map[string]interface{}{
			"swap": map[string]interface{}{
				"tokenOutputs": []interface{}{
					map[string]interface{}{
						"mint":        "21AErpiB8uSb94oQKRcwuHqyHF93njAxBSbdUrpupump",
						"tokenAmount": 1.0,
					},
				},
			},
		},
	}

	jsonBody, err := json.Marshal([]map[string]interface{}{sampleTx})
	if err != nil {
		log.Printf("    -> ERROR marshalling realistic test payload: %v", err)
		return false
	}

	headers := map[string]string{
		"Content-Type": "application/json",
	}
	if authHeader != "" {
		headers["Authorization"] = authHeader
		log.Printf("    -> with Authorization header.")
	}

	if testAPI(webhookURL, "POST", jsonBody, headers) {
		log.Println("    -> Realistic Webhook Test successful!")
		return true
	}
	log.Println("    -> Realistic Webhook Test failed!")
	return false
}

func testAPI(url, method string, payload []byte, headers map[string]string) bool {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("    -> ERROR creating request for %s: %v", url, err)
		return false
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("    -> ERROR connecting to %s (%s): %v", url, method, err)
		return false
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		log.Printf("    -> WARN reading response body from %s (%s): %v", url, method, readErr)
	}

	log.Printf("    -> %s [%s] - Status: %s, Resp: %s", method, url, resp.Status, string(body))

	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func testHeliusAPI() bool {
	log.Printf(" -> Testing Helius RPC API...")
	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		log.Println("    -> Skipping Helius API test due to missing API key")
		return false
	}

	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", apiKey)
	payload := `{"jsonrpc":"2.0","id":1,"method":"getBlockHeight"}`

	headers := map[string]string{"Content-Type": "application/json"}
	if testAPI(url, "POST", []byte(payload), headers) {
		log.Println("    -> Helius API test successful!")
		return true
	}
	log.Println("    -> Helius API test failed!")
	return false
}

func sendTelegram(message string) {
	botToken := env.TelegramBotToken
	groupIDStr := fmt.Sprintf("%d", env.TelegramGroupID)

	if botToken == "" || groupIDStr == "0" {
		log.Println("Telegram credentials missing or invalid Group ID. Skipping notification.")
		return
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)

	requestBodyMap := map[string]interface{}{
		"chat_id": groupIDStr,
		"text":    message,
	}
	jsonBody, err := json.Marshal(requestBodyMap)
	if err != nil {
		log.Printf("Failed to marshal Telegram payload: %v", err)
		return
	}

	headers := map[string]string{"Content-Type": "application/json"}
	if testAPI(url, "POST", jsonBody, headers) {
		log.Println("Telegram notification sent via test helper.")
	} else {
		log.Println("Failed to send Telegram notification via test helper.")
	}

}
