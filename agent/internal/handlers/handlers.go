package handlers

import (
	"bytes"
	"ca-scraper/agent/database"
	"ca-scraper/agent/internal/services"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Registers non-API specific routes (like root path)
func RegisterRoutes(router *gin.Engine, appLogger *logger.Logger) {

	router.GET("/", func(c *gin.Context) {
		appLogger.Info("Root endpoint accessed")
		c.JSON(http.StatusOK, gin.H{"message": "API is running. Scanner active!"})
	})

}

// Registers routes under the /api/v1 path prefix
func RegisterAPIRoutes(router *gin.Engine, appLogger *logger.Logger, db *gorm.DB) {
	apiGroup := router.Group("/api/v1")
	{
		apiGroup.GET("/health", func(c *gin.Context) {
			appLogger.Info("API Health endpoint called")
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "API Service is running"})
		})

		// Pass db instance to the handler factory for NFT verification
		apiGroup.POST("/verify-nft", handleVerifyNFT(appLogger, db))

		apiGroup.GET("/testHelius", func(c *gin.Context) {
			appLogger.Info("/api/v1/testHelius endpoint called")
			TestHeliusConnection(c, appLogger)
		})

		// --- ADDED HELIUS WEBHOOK REGISTRATION HERE ---
		apiGroup.POST("/webhook", func(c *gin.Context) { // <-- Path changed to /webhook (full path: /api/v1/webhook)
			requestID := zap.String("requestID", generateRequestID())
			// Log the correct path now
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
			// Important: Replace the request body so it can be read again if needed by subsequent middleware/handlers (though likely not needed here)
			c.Request.Body = io.NopCloser(bytes.NewBuffer(body))

			appLogger.Info("Webhook Payload Received", zap.Int("size", len(body)), requestID)
			// Use Debug level for potentially large payloads
			appLogger.Debug("Webhook Payload", zap.ByteString("payload", body), requestID)

			// Pass the body to the service handler
			err = services.HandleWebhook(body, appLogger) // Assuming HandleWebhook processes the raw body
			if err != nil {
				// Log the specific error from processing
				appLogger.Error("Error processing webhook payload in service", zap.Error(err), requestID)
				// Still return 200 OK to Helius so it doesn't retry constantly,
				// but indicate processing error in the message.
				c.JSON(http.StatusOK, gin.H{"message": "Webhook received, but processing encountered an error"})
				return
			}

			// Success message
			c.JSON(http.StatusOK, gin.H{"message": "Webhook received and processing initiated"})
		})
		// --- END HELIUS WEBHOOK REGISTRATION ---

	}
	appLogger.Info("API routes registered under /api/v1")
}

// --- handleVerifyNFT function remains the same ---
type VerifyNFTRequest struct {
	TelegramUserID int64  `json:"telegramUserId" binding:"required"`
	WalletAddress  string `json:"walletAddress" binding:"required"`
}

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

// --- TestHeliusConnection function remains the same ---
func TestHeliusConnection(c *gin.Context, appLogger *logger.Logger) {
	appLogger.Info("Executing Helius connection test...")
	apiKey := env.HeliusAPIKey
	if apiKey == "" {
		appLogger.Error("Helius API Key (HELIUS_API_KEY) is missing from env. Cannot perform test.")
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": "Server configuration error: Helius API Key missing."})
		return
	}
	_, err := services.CheckExistingHeliusWebhook("http://dummy-helius-check.invalid", appLogger)
	if err != nil {
		appLogger.Error("Helius connection test failed.", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"status": "error", "message": fmt.Sprintf("Helius connection test failed: %s", err.Error())})
		return
	}
	appLogger.Info("Helius connection test successful (authentication check passed).")
	c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "Helius connection test successful (authentication check passed)."})
}

// --- generateRequestID function remains the same ---
func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}
