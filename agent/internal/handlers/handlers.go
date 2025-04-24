package handlers

import (
	"bytes"
	"ca-scraper/agent/database" // *** Import your ACTUAL database package ***
	"ca-scraper/agent/internal/services"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"fmt"
	"io" // Using standard log for placeholder DB functions - remove when using real DB
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm" // <-- Import gorm
)

// Registers the webhook routes (kept separate as in main.go example)
// Optionally accept DB if webhook handlers need it
func RegisterRoutes(router *gin.Engine, appLogger *logger.Logger /*, db *gorm.DB */) {

	router.GET("/", func(c *gin.Context) {
		appLogger.Info("Root endpoint accessed")
		c.JSON(http.StatusOK, gin.H{"message": "API is running. Scanner active!"})
	})

	webhookGroup := router.Group("/webhook")
	{
		webhookGroup.GET("/", func(c *gin.Context) {
			appLogger.Info("GET request to /webhook received")
			c.JSON(http.StatusOK, gin.H{"message": "Webhook endpoint ready. Use POST to send events."})
		})

		webhookGroup.POST("/helius", func(c *gin.Context) { // Assuming this is the correct path
			requestID := zap.String("requestID", generateRequestID())
			appLogger.Info("POST /webhook/helius (Graduation) endpoint received request", requestID)

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
				appLogger.Error("Error processing graduation webhook payload", zap.Error(err), requestID)
				c.JSON(http.StatusOK, gin.H{"message": "Webhook received, but processing encountered an error"})
				return
			}

			c.JSON(http.StatusOK, gin.H{"message": "Webhook received and queued for graduation processing"})
		})

		webhookGroup.POST("/test", func(c *gin.Context) {
			appLogger.Info("Received Test Webhook Request")
			c.JSON(http.StatusOK, gin.H{"message": "Test endpoint hit"})
		})
	}
}

// --- MODIFIED: Function signature now accepts db *gorm.DB ---
func RegisterAPIRoutes(router *gin.Engine, appLogger *logger.Logger, db *gorm.DB) {
	apiGroup := router.Group("/api/v1")
	{
		apiGroup.GET("/health", func(c *gin.Context) {
			appLogger.Info("API Health endpoint called")
			c.JSON(http.StatusOK, gin.H{"status": "ok", "message": "API Service is running"})
		})

		// Pass db instance to the handler factory
		apiGroup.POST("/verify-nft", handleVerifyNFT(appLogger, db))

		apiGroup.GET("/testHelius", func(c *gin.Context) {
			appLogger.Info("/api/v1/testHelius endpoint called")
			TestHeliusConnection(c, appLogger)
		})
	}
	appLogger.Info("API routes registered under /api/v1")
}

// Request structure remains the same
type VerifyNFTRequest struct {
	TelegramUserID int64  `json:"telegramUserId" binding:"required"`
	WalletAddress  string `json:"walletAddress" binding:"required"`
	// InitData string `json:"initData"`
}

// --- MODIFIED: Handler factory now accepts db *gorm.DB ---
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
			// *** Replace Placeholder with actual DB call using the passed 'db' instance ***
			err := database.MarkUserAsVerified(db, req.TelegramUserID) // Use your actual function
			if err != nil {
				appLogger.Error("Failed to mark user as verified in DB", zap.Error(err), userIdField)
				c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Verification status update failed"})
				return
			}
			// MarkUserAsVerified_Placeholder(req.TelegramUserID) // Remove placeholder

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

// TestHeliusConnection function remains the same
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

// generateRequestID function remains the same
func generateRequestID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// --- REMOVE Placeholder DB Functions from here ---
// // func MarkUserAsVerified_Placeholder(userID int64) { ... }
// // func IsUserVerified_Placeholder(userID int64) (bool, error) { ... }

// --- Optional: Server-side InitData validation ---
// // func validateTelegramInitData(initData string, botToken string) bool { ... }
