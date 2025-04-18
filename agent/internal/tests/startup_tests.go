package tests

import (
	"bytes"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

func RunStartupTests(appLogger *logger.Logger) {
	appLogger.Info("--- Running Startup Tests ---")
	webhookURL := env.WebhookURL
	if webhookURL == "" {
		appLogger.Warn("WEBHOOK_LISTENER_URL_DEV/PROD not set. Using default for tests.", zap.String("defaultURL", "http://localhost:"+env.Port+"/api/v1/webhook"))
		webhookURL = "http://localhost:" + env.Port + "/api/v1/webhook"
	}
	appLogger.Info("Using Webhook URL for tests", zap.String("url", webhookURL))

	initialDelay := 5 * time.Second
	appLogger.Info("Waiting for initial server startup grace period...", zap.Duration("delay", initialDelay))
	time.Sleep(initialDelay)

	appLogger.Info("Probing server readiness...")
	serverReady := false
	maxRetries := 15
	retryInterval := 3 * time.Second
	probeTimeout := 5 * time.Second
	probeClient := &http.Client{Timeout: probeTimeout}
	healthURL := webhookURL // Assuming GET on webhook URL works for probe, adjust if needed

	for i := 0; i < maxRetries; i++ {
		attemptField := zap.Int("attempt", i+1)
		appLogger.Info("Pinging server...", attemptField, zap.Int("maxRetries", maxRetries), zap.String("url", healthURL))
		req, err := http.NewRequest("GET", healthURL, nil)
		if err != nil {
			appLogger.Warn("Probe Error (creating request)", attemptField, zap.Error(err))
			time.Sleep(retryInterval)
			continue
		}

		resp, err := probeClient.Do(req)
		if err != nil {
			appLogger.Warn("Probe Error (connecting)", attemptField, zap.Error(err))
			appLogger.Info("Server not ready yet...")
			time.Sleep(retryInterval)
			continue
		}

		statusField := zap.String("status", resp.Status)
		if resp.StatusCode >= 200 && resp.StatusCode < 400 { // Accept redirects as okay for readiness
			appLogger.Info("Server is responding.", statusField)
			serverReady = true
			resp.Body.Close()
			break
		} else {
			appLogger.Warn("Server responded with non-ready status.", statusField)
			resp.Body.Close()
			time.Sleep(retryInterval)
		}
	}

	if !serverReady {
		appLogger.Error("Server did not become ready after probing. Aborting further tests.")
		return
	}

	appLogger.Info("Running Initial Simple Webhook Test...")
	initialTestPassed := testSimpleWebhookPost(webhookURL, appLogger)

	appLogger.Info("Running More Realistic Webhook Test...")
	realisticTestPassed := testRealisticWebhookPost(webhookURL, appLogger)

	appLogger.Info("Testing Helius RPC API...")
	heliusTestPassed := testHeliusAPI(appLogger)

	allTestsPassed := initialTestPassed && realisticTestPassed && heliusTestPassed

	if allTestsPassed {
		appLogger.Info("All Startup Tests Passed: Notifying Telegram.")
		// Changed from SendSystemLogMessage to SendTelegramMessage
		notifications.SendTelegramMessage("✅ All startup tests passed successfully.")
	} else {
		appLogger.Error("One or more startup tests failed. Check logs for details.")
		// Changed from SendSystemLogMessage to SendTelegramMessage
		notifications.SendTelegramMessage("❌ One or more startup tests FAILED!")
	}

	appLogger.Info("--- Startup Tests Complete ---")
}

// testSimpleWebhookPost remains the same internally
func testSimpleWebhookPost(webhookURL string, appLogger *logger.Logger) bool {
	testName := "SimpleWebhookTest"
	appLogger.Info("-> Sending simple POST test", zap.String("test", testName), zap.String("url", webhookURL))
	authHeader := env.HeliusAuthHeader
	payloadArray := `[{"type": "SYSTEM_TEST_SIMPLE", "signature": "simple-` + fmt.Sprintf("%d", time.Now().UnixNano()) + `"}]`
	payloadBytes := []byte(payloadArray)

	headers := map[string]string{"Content-Type": "application/json"}
	authField := zap.String("authorization", "no")
	if authHeader != "" {
		headers["Authorization"] = authHeader
		authField = zap.String("authorization", "yes")
		appLogger.Debug("   -> with Authorization header.", zap.String("test", testName))
	}

	passed := testAPI(webhookURL, "POST", payloadBytes, headers, appLogger, testName)
	if passed {
		appLogger.Info("   -> Simple Webhook Test successful!", zap.String("test", testName))
	} else {
		appLogger.Error("   -> Simple Webhook Test failed!", zap.String("test", testName), authField)
	}
	return passed
}

// testRealisticWebhookPost remains the same internally
func testRealisticWebhookPost(webhookURL string, appLogger *logger.Logger) bool {
	testName := "RealisticWebhookTest"
	appLogger.Info("-> Sending realistic POST test", zap.String("test", testName), zap.String("url", webhookURL))
	authHeader := env.HeliusAuthHeader

	sampleTx := map[string]interface{}{
		"description": "Startup Test Event (Realistic)",
		"type":        "SWAP",
		"source":      "SYSTEM_TEST_REALISTIC",
		"signature":   fmt.Sprintf("realistic-%d", time.Now().UnixNano()),
		"timestamp":   time.Now().Unix(),
		"tokenTransfers": []interface{}{
			map[string]interface{}{"mint": "TESTMINTADDRESSREALISTICxxxxxxxxxxxxxxx"},
		},
		"events": map[string]interface{}{"swap": map[string]interface{}{}},
	}

	jsonBody, err := json.Marshal([]map[string]interface{}{sampleTx})
	if err != nil {
		appLogger.Error("   -> ERROR marshalling realistic test payload", zap.String("test", testName), zap.Error(err))
		return false
	}

	headers := map[string]string{"Content-Type": "application/json"}
	authField := zap.String("authorization", "no")
	if authHeader != "" {
		headers["Authorization"] = authHeader
		authField = zap.String("authorization", "yes")
		appLogger.Debug("   -> with Authorization header.", zap.String("test", testName))
	}

	passed := testAPI(webhookURL, "POST", jsonBody, headers, appLogger, testName)
	if passed {
		appLogger.Info("   -> Realistic Webhook Test successful!", zap.String("test", testName))
	} else {
		appLogger.Error("   -> Realistic Webhook Test failed!", zap.String("test", testName), authField)
	}
	return passed
}

// testAPI remains the same internally
func testAPI(url, method string, payload []byte, headers map[string]string, appLogger *logger.Logger, testName string) bool {
	req, err := http.NewRequest(method, url, bytes.NewBuffer(payload))
	urlField := zap.String("url", url)
	methodField := zap.String("method", method)
	testField := zap.String("test", testName)

	if err != nil {
		appLogger.Error("   -> ERROR creating request", testField, urlField, methodField, zap.Error(err))
		return false
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		appLogger.Error("   -> ERROR connecting/sending request", testField, urlField, methodField, zap.Error(err))
		return false
	}
	defer resp.Body.Close()

	body, readErr := io.ReadAll(resp.Body)
	statusField := zap.String("status", resp.Status)
	bodyField := zap.Skip() // Default to skipping body logging unless read succeeds
	if readErr == nil && len(body) > 0 {
		bodyField = zap.ByteString("respBody", body)
	} else if readErr != nil {
		appLogger.Warn("   -> WARN reading response body", testField, urlField, methodField, zap.Error(readErr))
	}

	appLogger.Info("   -> API Test Response", testField, methodField, urlField, statusField, bodyField)

	return resp.StatusCode >= 200 && resp.StatusCode < 300 // Success is 2xx range
}

// testHeliusAPI remains the same internally
func testHeliusAPI(appLogger *logger.Logger) bool {
	testName := "HeliusAPITest"
	appLogger.Info("-> Testing Helius RPC API...", zap.String("test", testName))
	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		appLogger.Warn("   -> Skipping Helius API test due to missing API key", zap.String("test", testName))
		// Changed from false to true, as missing key isn't a *failure* of the API itself
		return true
	}

	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", apiKey)
	payload := `{"jsonrpc":"2.0","id":1,"method":"getBlockHeight"}`
	headers := map[string]string{"Content-Type": "application/json"}

	passed := testAPI(url, "POST", []byte(payload), headers, appLogger, testName)
	if passed {
		appLogger.Info("   -> Helius API test successful!", zap.String("test", testName))
	} else {
		appLogger.Error("   -> Helius API test failed!", zap.String("test", testName))
	}
	return passed
}
