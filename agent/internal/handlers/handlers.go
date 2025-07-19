package handlers

import (
	"bytes"
	"ca-scraper/agent/database"
	"ca-scraper/agent/internal/services"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mymmrac/telego"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type MarkVerifiedRequest struct {
	UserID int64 `json:"userId" binding:"required"`
}

func RegisterRoutes(router *gin.Engine, appLogger *logger.Logger) {
	router.GET("/", func(c *gin.Context) {
		appLogger.Info("Root endpoint accessed")
		c.JSON(http.StatusOK, gin.H{"message": "API is running. Scanner active!"})
	})
}

func RegisterAPIRoutes(router *gin.Engine, appLogger *logger.Logger, db *gorm.DB) {
	apiGroup := router.Group("/api/v1")
	{
		apiGroup.GET("/health", func(c *gin.Context) {
			appLogger.Info("API Health endpoint called")
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "API Service is running"})
		})

		apiGroup.GET("/testHelius", func(c *gin.Context) {
			appLogger.Info("/api/v1/testHelius endpoint called")
			TestHeliusConnection(c, appLogger)
		})

		apiGroup.POST("/webhook", func(c *gin.Context) {
			requestID := zap.String("requestID", generateRequestID())
			appLogger.Info("POST /api/v1/webhook (Helius) endpoint received request", requestID)

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

			err = services.HandleWebhook(body, appLogger)
			if err != nil {
				appLogger.Error("Error processing webhook payload in service", zap.Error(err), requestID)
				c.JSON(http.StatusOK, gin.H{"message": "Webhook received, but processing encountered an error"})
				return
			}

			c.JSON(http.StatusOK, gin.H{"message": "Webhook received and processing initiated"})
		})

		apiGroup.POST("/mark-verified", handleMarkVerified(appLogger, notifications.GetBotInstance()))
	}
	appLogger.Info("API routes registered under /api/v1")
}

func handleMarkVerified(appLogger *logger.Logger, botInstance *telego.Bot) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req MarkVerifiedRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			appLogger.Warn("Invalid mark-verified request", zap.Error(err), zap.String("remoteAddr", c.RemoteIP()))
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request body"})
			return
		}

		appLogger.Info("Marking user as verified", zap.Int64("userId", req.UserID))
		if err := database.MarkUserAsVerified(nil, req.UserID); err != nil {
			appLogger.Error("Failed to mark user as verified", zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Database update failed"})
			return
		}

		appLogger.Info("User marked as verified", zap.Int64("userId", req.UserID))
		c.JSON(http.StatusOK, gin.H{"success": true})
	}
}

func TestHeliusConnection(c *gin.Context, appLogger *logger.Logger) {
	appLogger.Info("Executing Helius connection test (via CheckExistingHeliusWebhook)...")
	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		appLogger.Error("Helius API Key (HELIUS_API_KEY) is missing from env. Cannot perform webhook listing test.")
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Server configuration error: Helius API Key missing."})
		return
	}
	_, err := services.CheckExistingHeliusWebhook("http://dummy-helius-check.invalid", appLogger)
	if err != nil {
		appLogger.Error("Helius connection test failed.", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": fmt.Sprintf("Helius connection test failed: %s", err.Error())})
		return
	}
	appLogger.Info("Helius connection test successful.")
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Helius connection test successful."})
}

func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
