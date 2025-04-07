package handlers

import (
	"bytes"
	"ca-scraper/agent/internal/services"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func RegisterRoutes(router *gin.Engine, log *zap.SugaredLogger, appLogger *logger.Logger) {
	router.GET("/", func(c *gin.Context) {
		log.Info("Root endpoint accessed")
		c.JSON(http.StatusOK, gin.H{"message": "API is running. Scanner active!"})
	})

	api := router.Group("/api/v1")
	{
		api.GET("/health", func(c *gin.Context) {
			log.Info("Health endpoint called")
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Service is running"})
		})

		api.GET("/testHelius", func(c *gin.Context) {
			log.Info("/testHelius endpoint called")
			TestHeliusConnection(c, appLogger)
		})

		api.GET("/webhook", func(c *gin.Context) {
			log.Info("GET request to /webhook received")
			c.JSON(http.StatusOK, gin.H{"message": "Webhook endpoint ready. Use POST to send events."})
		})

		api.POST("/webhook", func(c *gin.Context) {
			log.Info("POST /webhook endpoint received request")

			expectedAuthHeader := env.HeliusAuthHeader
			if expectedAuthHeader != "" {
				receivedAuthHeader := c.GetHeader("Authorization")
				if receivedAuthHeader == "" {
					log.Warn("Webhook request missing Authorization header.")
					c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing Authorization header"})
					return
				}
				if receivedAuthHeader != expectedAuthHeader {
					log.Error("Unauthorized Webhook Request - Header mismatch.")
					c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
					return
				}
				log.Info("Webhook authorized successfully.")
			} else {
				log.Warn("No HELIUS_AUTH_HEADER configured in env. Accepting webhook without Authorization check.")
			}

			body, err := io.ReadAll(c.Request.Body)
			if err != nil {
				log.Error("Failed to read webhook payload", zap.Error(err))
				c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid payload"})
				return
			}
			c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

			log.Info("Webhook Payload Received", zap.Int("size", len(body)))
			log.Debug("Webhook Payload", zap.String("payload", string(body)))

			services.HandleWebhook(body, appLogger)

			c.JSON(http.StatusOK, gin.H{"message": "Webhook received and queued for processing"})
		})

	}
}

func TestHeliusConnection(c *gin.Context, log *logger.Logger) {
	log.Info("Executing Helius connection test...")

	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		log.Error("Helius API Key (HELIUS_API_KEY) is missing from env. Cannot perform test.")
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": "Server configuration error: Helius API Key missing.",
		})
		return
	}

	_, err := services.CheckExistingHeliusWebhook("http://dummy-helius-check.invalid")

	if err != nil {
		log.Error("Helius connection test failed.", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{
			"status":  "error",
			"message": fmt.Sprintf("Helius connection test failed: %v", err),
		})
		return
	}

	log.Info("Helius connection test successful (authentication check passed).")
	c.JSON(http.StatusOK, gin.H{
		"status":  "ok",
		"message": "Helius connection test successful (authentication check passed).",
	})
}
