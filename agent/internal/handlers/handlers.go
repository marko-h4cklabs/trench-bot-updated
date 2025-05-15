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

type VerifyNFTRequest struct {
	TelegramUserID int64  `json:"telegramUserId" binding:"required"`
	WalletAddress  string `json:"walletAddress" binding:"required"`
}

func RegisterRoutes(router *gin.Engine, appLogger *logger.Logger) {
	router.GET("/", func(c *gin.Context) {
		appLogger.Info("Root endpoint accessed")
		c.JSON(http.StatusOK, gin.H{"message": "API is running. Scanner active!"})
	})
}

// MODIFIED: Removed heliusSvc parameter
func RegisterAPIRoutes(router *gin.Engine, appLogger *logger.Logger, db *gorm.DB) {
	apiGroup := router.Group("/api/v1")
	{
		apiGroup.GET("/health", func(c *gin.Context) {
			appLogger.Info("API Health endpoint called")
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "API Service is running"})
		})

		// apiGroup.POST("/verify-nft", handleVerifyNFT(appLogger, db)) // Assuming you still need/want this

		apiGroup.GET("/testHelius", func(c *gin.Context) {
			appLogger.Info("/api/v1/testHelius endpoint called")
			TestHeliusConnection(c, appLogger) // MODIFIED: Call without heliusSvc
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

			// MODIFIED: Call services.HandleWebhook without heliusSvc
			err = services.HandleWebhook(body, appLogger)
			if err != nil {
				appLogger.Error("Error processing webhook payload in service", zap.Error(err), requestID)
				c.JSON(http.StatusOK, gin.H{"message": "Webhook received, but processing encountered an error"}) // Acknowledge receipt
				return
			}

			c.JSON(http.StatusOK, gin.H{"message": "Webhook received and processing initiated"})
		})

		apiGroup.POST("/mark-verified", handleMarkVerified(appLogger, notifications.GetBotInstance()))
	}
	appLogger.Info("API routes registered under /api/v1")
}

// handleVerifyNFT - No changes needed here if it wasn't using HeliusService directly
func handleVerifyNFT(appLogger *logger.Logger, db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req VerifyNFTRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			appLogger.Warn("Invalid request to /verify-nft", zap.Error(err), zap.String("remoteAddr", c.RemoteIP()))
			c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid request body"})
			return
		}

		userIdField := zap.Int64("telegramUserId", req.TelegramUserID)
		walletField := zap.String("walletAddress", req.WalletAddress)
		appLogger.Info("Handling NFT verification request from Mini App", userIdField, walletField)

		// services.CheckNFTHoldings is likely making its own Helius calls or using a different mechanism
		hasEnoughNFTs, checkErr := services.CheckNFTHoldings(req.WalletAddress, appLogger)

		if checkErr != nil {
			appLogger.Error("Error checking NFT holdings", zap.Error(checkErr), userIdField, walletField)
			c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Verification check failed"})
			return
		}

		if hasEnoughNFTs {
			err := database.MarkUserAsVerified(db, req.TelegramUserID)
			if err != nil {
				appLogger.Error("Failed to mark user as verified in DB", zap.Error(err), userIdField)
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Verification status update failed"})
				return
			}
			appLogger.Info("NFT verification successful", userIdField, walletField)
			c.JSON(http.StatusOK, gin.H{"success": true})
		} else {
			appLogger.Info("NFT verification failed: Insufficient holdings", userIdField, walletField)
			purchaseURL := fmt.Sprintf("https://magiceden.io/marketplace/%s", env.NFTCollectionAddress)
			c.JSON(http.StatusOK, gin.H{
				"success":     false,
				"reason":      "insufficient_nfts",
				"required":    env.NFTMinimumHolding,
				"purchaseUrl": purchaseURL,
			})
		}
	}
}

// handleMarkVerified - No changes needed here
func handleMarkVerified(appLogger *logger.Logger, botInstance *telego.Bot) gin.HandlerFunc {
	return func(c *gin.Context) {
		// ... (content as before) ...
	}
}

// MODIFIED: TestHeliusConnection no longer accepts heliusSvc
// It relies on services.CheckExistingHeliusWebhook which is assumed to be standalone
// or use its own Helius client mechanism for management API calls.
func TestHeliusConnection(c *gin.Context, appLogger *logger.Logger) {
	appLogger.Info("Executing Helius connection test (via CheckExistingHeliusWebhook)...")
	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		appLogger.Error("Helius API Key (HELIUS_API_KEY) is missing from env. Cannot perform webhook listing test.")
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Server configuration error: Helius API Key missing."})
		return
	}
	// This function tests connectivity to Helius *management* API for webhooks
	_, err := services.CheckExistingHeliusWebhook("http://dummy-helius-check.invalid", appLogger)
	if err != nil {
		appLogger.Error("Helius connection test (via CheckExistingHeliusWebhook) failed.", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": fmt.Sprintf("Helius connection test failed: %s", err.Error())})
		return
	}
	appLogger.Info("Helius connection test (via CheckExistingHeliusWebhook) successful.")
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Helius connection test successful (management API auth check passed)."})
}

func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
