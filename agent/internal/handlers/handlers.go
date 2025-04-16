package handlers

import (
	"bytes"
	"ca-scraper/agent/internal/services"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func RegisterRoutes(router *gin.Engine, appLogger *logger.Logger) {

	router.GET("/", func(c *gin.Context) {
		appLogger.Info("Root endpoint accessed")
		c.JSON(http.StatusOK, gin.H{"message": "API is running. Scanner active!"})
	})

	api := router.Group("/api/v1")
	{
		api.GET("/health", func(c *gin.Context) {
			appLogger.Info("Health endpoint called")
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Service is running"})
		})

		api.GET("/testHelius", func(c *gin.Context) {
			appLogger.Info("/testHelius endpoint called")
			TestHeliusConnection(c, appLogger)
		})

		api.GET("/webhook", func(c *gin.Context) {
			appLogger.Info("GET request to /webhook received")
			c.JSON(http.StatusOK, gin.H{"message": "Webhook endpoint ready. Use POST to send events."})
		})

		api.POST("/webhook", func(c *gin.Context) {
			requestID := zap.String("requestID", generateRequestID())
			appLogger.Info("POST /webhook (Graduation) endpoint received request", requestID)

			expectedAuthHeader := env.HeliusAuthHeader
			if expectedAuthHeader != "" {
				receivedAuthHeader := c.GetHeader("Authorization")
				if receivedAuthHeader == "" {
					appLogger.Warn("Webhook request missing Authorization header.", requestID)
					c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
					return
				}
				if receivedAuthHeader != expectedAuthHeader {
					appLogger.Error("Unauthorized Webhook Request - Header mismatch.", requestID)
					c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
					return
				}
				appLogger.Info("Webhook authorized successfully.", requestID)
			} else {
				appLogger.Warn("No HELIUS_AUTH_HEADER configured. Accepting webhook without Authorization check.", requestID)
			}

			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				appLogger.Error("Failed to read webhook payload", zap.Error(err), requestID)
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload"})
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

			appLogger.Info("Webhook Payload Received", zap.Int("size", len(body)), requestID)
			appLogger.Debug("Webhook Payload", zap.ByteString("payload", body), requestID)

			services.HandleWebhook(body, appLogger)

			c.JSON(http.StatusOK, gin.H{"message": "Webhook received and queued for graduation processing"})
		})
	}
}

func TestHeliusConnection(c *gin.Context, appLogger *logger.Logger) {
	appLogger.Info("Executing Helius connection test...")

	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		appLogger.Error("Helius API Key (HELIUS_API_KEY) is missing from env. Cannot perform test.")
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "Server configuration error: Helius API Key missing.",
		})
		return
	}

	_, err := services.CheckExistingHeliusWebhook("http://dummy-helius-check.invalid", appLogger)

	if err != nil {
		appLogger.Error("Helius connection test failed.", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "Helius connection test failed.",
		})
		return
	}

	appLogger.Info("Helius connection test successful (authentication check passed).")
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "Helius connection test successful (authentication check passed).",
	})
}

func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
