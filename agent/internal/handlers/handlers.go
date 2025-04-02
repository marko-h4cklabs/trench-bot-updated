package handlers

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"ca-scraper/agent/internal/services"
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
)

func TestWebhookOnStartup() {
	if err := godotenv.Load(".env"); err != nil {
		println(".env file NOT found in the current directory.")
	} else {
		println(".env file successfully loaded.")
	}

	webhookURL := os.Getenv("WEBHOOK_LISTENER_URL_DEV")
	if webhookURL == "" {
		println(" WEBHOOK_LISTENER_URL_DEV is not set. Falling back to localhost.")
		webhookURL = "http://localhost:5555/api/v1/webhook"
	} else {
		println(" Using Webhook URL:", webhookURL)
	}
	payload := `{
		"transaction": {
			"signature": "test123",
			"events": {
				"swap": {
					"tokenOutputs": [{
						"mint": "HqqnXZ8S76rY3GnXgHR9LpbLEKNfxCZASWydNHydpump"
					}]
				}
			}
		}
	}`

	req, err := http.NewRequest("POST", webhookURL, bytes.NewBuffer([]byte(payload)))
	if err != nil {
		log.Printf("Failed to create webhook test request: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", os.Getenv("WEBHOOK_SECRET"))

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Webhook test request failed: %v", err)
		return
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Printf("Test Webhook Response: %s", string(body))
}

func RegisterRoutes(router *gin.Engine, log *zap.SugaredLogger, appLogger *logger.Logger) {
	router.GET("/", func(c *gin.Context) {
		log.Info("Root endpoint accessed")
		c.JSON(http.StatusOK, gin.H{"message": "API is running. Raydium scanner active!"})
	})

	api := router.Group("/api/v1")
	{
		api.GET("/health", func(c *gin.Context) {
			log.Info("Health endpoint called")
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Service is running"})
		})

		api.POST("/messages", func(c *gin.Context) {
			log.Info("Messages endpoint called")
			if err := notifications.InitTelegramBot(); err != nil {
				log.Errorf("Failed to initialize bot: %v", err)
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to initialize bot"})
				return
			}
			c.JSON(http.StatusOK, gin.H{"message": "Telegram bot initialized successfully"})
		})

		api.GET("/testHelius", func(c *gin.Context) {
			log.Info("Helius test endpoint called")
			TestHeliusConnection(c)
		})

		// NEW GET handler to prevent 404s on GET /webhook
		api.GET("/webhook", func(c *gin.Context) {
			log.Info("GET request to /webhook received (likely health check or bot)")
			c.JSON(http.StatusOK, gin.H{"message": "Webhook endpoint ready. Use POST to send events."})
		})

		// Existing POST webhook handler
		api.POST("/webhook", func(c *gin.Context) {
			log.Info("Webhook endpoint received request")

			for key, values := range c.Request.Header {
				for _, value := range values {
					log.Infof("Header: %s = %s", key, value)
				}
			}

			receivedAuthHeader := c.GetHeader("Authorization")
			expectedAuthHeader := os.Getenv("WEBHOOK_SECRET")

			if expectedAuthHeader == "" {
				log.Warn("Missing WEBHOOK_SECRET in environment variables.")
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Server misconfiguration: missing webhook secret"})
				return
			}
			if receivedAuthHeader == "" {
				log.Warn("No Authorization header in webhook request.")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
				return
			}
			if receivedAuthHeader != expectedAuthHeader {
				log.Errorf("Unauthorized Webhook Request - Expected: %s, Received: %s", expectedAuthHeader, receivedAuthHeader)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
				return
			}

			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				log.Error("Failed to read webhook payload")
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload"})
				return
			}

			log.Infof("Webhook Payload: %s", string(body))

			var parsedData interface{}
			if err := json.Unmarshal(body, &parsedData); err != nil {
				log.Errorf("JSON parsing error: %v | Raw Payload: %s", err, string(body))
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format"})
				return
			}

			switch data := parsedData.(type) {
			case []interface{}:
				log.Infof("Received %d transactions in webhook", len(data))

				if len(data) > 0 {
					firstTx, _ := data[0].(map[string]interface{})
					signature, _ := firstTx["signature"].(string)
					log.Infof("First transaction signature: %s", signature)
				}

				services.HandleWebhook(body, receivedAuthHeader, appLogger)

			case map[string]interface{}:
				log.Info("Received single transaction")

				signature, _ := data["signature"].(string)
				if signature != "" {
					log.Infof("Transaction Signature: %s", signature)
				}

				services.HandleWebhook(body, receivedAuthHeader, appLogger)

			default:
				log.Error("Unexpected JSON format received")
				c.JSON(http.StatusBadRequest, gin.H{"error": "Unexpected JSON format"})
				return
			}

			c.JSON(http.StatusOK, gin.H{"message": "Webhook processed successfully"})
		})
	}
}
