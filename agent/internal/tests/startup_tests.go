// agent/internal/tests/startup_tests.go
package tests

import (
	"bytes"
	"ca-scraper/shared/env" // Import shared env
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	// "os" // Use env package instead
	"time"
	// Remove godotenv from here if LoadEnv in main handles it
	// "github.com/joho/godotenv"
)

// RunStartupTests orchestrates all startup checks after server start attempt.
func RunStartupTests() {
	log.Println("--- Running Startup Tests ---")

	// No need to load .env here if main.go already did and env.LoadEnv populated vars
	// if err := godotenv.Load(".env"); err != nil {
	// 	log.Println("Info: .env file not found or error loading:", err)
	// } else {
	// 	log.Println(".env file successfully loaded.")
	// }

	// Use variables loaded by env.LoadEnv in main.go
	webhookURL := env.WebhookURL
	if webhookURL == "" {
		log.Println("CRITICAL: WEBHOOK_LISTENER_URL_DEV not set in environment. Startup tests cannot proceed meaningfully.")
		// Decide if you want to exit or try a default (default less useful here)
		// For now, we log and let it potentially fail later.
		webhookURL = "http://localhost:5555/api/v1/webhook" // Default might not be reachable
	}
	log.Printf("Using Webhook URL for tests: %s", webhookURL)

	// --- Server Readiness Probe ---
	initialDelay := 5 * time.Second // Keep initial grace period
	log.Printf("Waiting for initial %v server startup grace period...", initialDelay)
	time.Sleep(initialDelay)

	log.Println("Probing server readiness...")
	serverReady := false
	maxRetries := 15                 // Number of retries
	retryInterval := 3 * time.Second // Interval between retries
	probeTimeout := 5 * time.Second  // Timeout for each individual probe request

	// Use a distinct client for probes with shorter timeout
	probeClient := &http.Client{Timeout: probeTimeout}

	// Use a reliable health check endpoint if available, otherwise fallback
	healthURL := webhookURL // Use webhook URL as default probe target with GET
	// If you have a dedicated /health endpoint:
	// healthURL = strings.Replace(webhookURL, "/webhook", "/health", 1)

	for i := 0; i < maxRetries; i++ {
		log.Printf("Attempt %d/%d: Pinging %s...", i+1, maxRetries, healthURL)
		req, err := http.NewRequest("GET", healthURL, nil) // Use GET for probing
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

		// Check if status code is OK (2xx)
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			log.Println(" Server is up!")
			serverReady = true
			resp.Body.Close() // Close body on success
			break
		} else {
			log.Printf(" Server responded with status %s. Not ready yet...", resp.Status)
			resp.Body.Close() // Close body on non-success too
			time.Sleep(retryInterval)
		}
	} // End probe loop

	if !serverReady {
		log.Println("FATAL: Server did not become ready after probing. Aborting further tests.")
		// Consider os.Exit(1) here if server readiness is absolutely critical
		return // Exit RunStartupTests
	}
	// --- End Server Readiness Probe ---

	// --- Run Specific Tests AFTER Server is Ready ---

	log.Println("Running Initial Simple Webhook Test (like old TestWebhookOnStartup)...")
	initialTestPassed := testSimpleWebhookPost(webhookURL) // New function call

	log.Println("Running More Realistic Webhook Test (like old testWebhook)...")
	realisticTestPassed := testRealisticWebhookPost(webhookURL) // Renamed function call

	log.Println("Testing Helius RPC API...")
	heliusTestPassed := testHeliusAPI() // Renamed function call

	// --- Report Results ---
	// Decide which tests are critical for notification
	allTestsPassed := initialTestPassed && realisticTestPassed && heliusTestPassed

	if allTestsPassed {
		log.Println("✅ All Startup Tests Passed: Notifying Telegram.")
		// Use the centralized notifications package if integrated, otherwise keep direct send
		sendTelegram("✅ All startup tests passed successfully.") // More specific message
	} else {
		log.Println("❌ One or more startup tests failed.")
		// Optionally send a failure notification
		// sendTelegram("❌ Warning: One or more startup tests failed. Check logs.")
	}

	log.Println("--- Startup Tests Complete ---")
}

// testSimpleWebhookPost - Mimics the old TestWebhookOnStartup logic
func testSimpleWebhookPost(webhookURL string) bool {
	log.Printf(" -> Sending simple POST test to: %s", webhookURL)
	authHeader := env.HeliusAuthHeader // Get from shared env

	// Simple payload used in the old test
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

	// Use a longer timeout for this test, similar to the original attempt
	// Can use the shared testAPI function or a dedicated client here
	client := &http.Client{Timeout: 20 * time.Second} // Increased timeout

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

// testRealisticWebhookPost - The test previously named testWebhook
func testRealisticWebhookPost(webhookURL string) bool {
	log.Printf(" -> Sending realistic POST test to: %s", webhookURL)
	authHeader := env.HeliusAuthHeader // Get from shared env

	// Use the more realistic sampleTx payload
	sampleTx := map[string]interface{}{
		// Using a more realistic structure based on previous logs
		"description": "Startup Test Event (Realistic)",
		"type":        "SWAP",
		"source":      "SYSTEM_TEST_REALISTIC",
		"signature":   fmt.Sprintf("startup-test-sig-realistic-%d", time.Now().UnixNano()),
		"timestamp":   time.Now().Unix(),
		"tokenTransfers": []interface{}{
			map[string]interface{}{
				"mint": "21AErpiB8uSb94oQKRcwuHqyHF93njAxBSbdUrpupump", // Example
			},
		},
		"nativeTransfers": []interface{}{},
		"accountData":     []interface{}{},
		"events": map[string]interface{}{
			"swap": map[string]interface{}{
				"tokenOutputs": []interface{}{
					map[string]interface{}{
						"mint":        "21AErpiB8uSb94oQKRcwuHqyHF93njAxBSbdUrpupump",
						"tokenAmount": 1.0, // Example amount
					},
				},
			},
		},
	}

	jsonBody, err := json.Marshal([]map[string]interface{}{sampleTx}) // Send as array
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

	// Can use the shared testAPI function
	if testAPI(webhookURL, "POST", jsonBody, headers) {
		log.Println("    -> Realistic Webhook Test successful!")
		return true
	}
	log.Println("    -> Realistic Webhook Test failed!")
	return false
}

// testAPI is a generic helper for making API calls during tests
func testAPI(url, method string, payload []byte, headers map[string]string) bool {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(payload))
	if err != nil {
		log.Printf("    -> ERROR creating request for %s: %v", url, err)
		return false
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	// Use a reasonable default timeout for tests within this function
	client := &http.Client{Timeout: 15 * time.Second} // Consistent timeout for test calls
	resp, err := client.Do(req)
	if err != nil {
		// Log specific connection errors
		log.Printf("    -> ERROR connecting to %s (%s): %v", url, method, err)
		return false
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		log.Printf("    -> WARN reading response body from %s (%s): %v", url, method, readErr)
	}

	log.Printf("    -> %s [%s] - Status: %s, Resp: %s", method, url, resp.Status, string(body))

	// Check for 2xx success status
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// testHeliusAPI remains the same logic, just called from RunStartupTests
func testHeliusAPI() bool {
	log.Printf(" -> Testing Helius RPC API...")
	apiKey := env.HeliusAPIKey // Get from shared env
	if apiKey == "" {
		log.Println("    -> Skipping Helius API test due to missing API key")
		return false // Indicate test skipped/failed due to config
	}

	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", apiKey)
	// Simple request to check connectivity and key validity
	payload := `{"jsonrpc":"2.0","id":1,"method":"getBlockHeight"}`

	// Use the shared testAPI helper
	headers := map[string]string{"Content-Type": "application/json"}
	if testAPI(url, "POST", []byte(payload), headers) {
		log.Println("    -> Helius API test successful!")
		return true
	}
	log.Println("    -> Helius API test failed!")
	return false
}

// sendTelegram remains the same logic, just called from RunStartupTests
// Consider replacing this with calls to your shared `notifications` package eventually
func sendTelegram(message string) {
	// ... (keep existing implementation or switch to notifications package) ...
	botToken := env.TelegramBotToken
	groupIDStr := fmt.Sprintf("%d", env.TelegramGroupID) // Convert int64 group ID to string

	if botToken == "" || groupIDStr == "0" { // Check for 0 as invalid ID
		log.Println("Telegram credentials missing or invalid Group ID. Skipping notification.")
		return
	}

	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", botToken)

	// Create the request body correctly escaping the message
	requestBodyMap := map[string]interface{}{
		"chat_id": groupIDStr,
		"text":    message,
		// "parse_mode": "MarkdownV2", // Add if needed, ensure message is escaped
	}
	jsonBody, err := json.Marshal(requestBodyMap)
	if err != nil {
		log.Printf("Failed to marshal Telegram payload: %v", err)
		return
	}

	// Use the shared testAPI helper (or a dedicated http call)
	headers := map[string]string{"Content-Type": "application/json"}
	if testAPI(url, "POST", jsonBody, headers) {
		log.Println("Telegram notification sent via test helper.") // Log success differently if using testAPI
	} else {
		log.Println("Failed to send Telegram notification via test helper.")
	}

	// OR Direct HTTP Call (previous way)
	// resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonBody))
	// if err != nil {
	// 	log.Printf("Failed to send Telegram message: %v", err)
	// 	return
	// }
	// defer resp.Body.Close()
	//
	// if resp.StatusCode >= 200 && resp.StatusCode < 300 {
	// 	log.Println("Telegram notification sent.")
	// } else {
	//     body, _ := io.ReadAll(resp.Body)
	// 	log.Printf("Failed to send Telegram message. Status: %d, Body: %s", resp.StatusCode, string(body))
	// }
}
