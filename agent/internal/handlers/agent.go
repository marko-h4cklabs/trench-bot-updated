package handlers

import (
	"bytes"
	"ca-scraper/agent/database"
	"ca-scraper/agent/internal/models"
	"ca-scraper/agent/internal/services"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/gin-gonic/gin"
)

func HandleWhitelistAPI(c *gin.Context) {
	var payload struct {
		Wallet string `json:"wallet"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	wallet := strings.TrimSpace(payload.Wallet)
	if wallet == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Wallet address is required"})
		return
	}

	var user models.User
	result := database.DB.Where("wallet_id = ?", wallet).First(&user)
	if result.RowsAffected > 0 {
		c.JSON(http.StatusConflict, gin.H{"message": "Wallet is already whitelisted"})
		return
	}

	newUser := models.User{WalletID: wallet, NFTStatus: false}
	if err := database.DB.Create(&newUser).Error; err != nil {
		log.Printf("❌ Failed to add wallet to database: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to whitelist wallet"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "✅ Wallet whitelisted successfully"})
}

func HandleWalletUpdateAPI(c *gin.Context) {
	var payload struct {
		CurrentWallet string `json:"currentWallet"`
		NewWallet     string `json:"newWallet"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	currentWallet := strings.TrimSpace(payload.CurrentWallet)
	newWallet := strings.TrimSpace(payload.NewWallet)

	var user models.User
	result := database.DB.Where("wallet_id = ?", currentWallet).First(&user)
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Current wallet not found"})
		return
	}

	user.WalletID = newWallet
	if err := database.DB.Save(&user).Error; err != nil {
		log.Printf(" Failed to update wallet: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update wallet"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "✅ Wallet updated successfully"})
}

func HandleWalletDeleteAPI(c *gin.Context) {
	var payload struct {
		Wallet string `json:"wallet"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	wallet := strings.TrimSpace(payload.Wallet)

	var user models.User
	result := database.DB.Where("wallet_id = ?", wallet).First(&user)
	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Wallet not found"})
		return
	}

	if err := database.DB.Delete(&user).Error; err != nil {
		log.Printf(" Failed to delete wallet: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete wallet"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "✅ Wallet deleted successfully"})
}

func HandleCheckWalletAPI(c *gin.Context) {
	var payload struct {
		Wallet string `json:"wallet"`
	}
	if err := c.BindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	wallet := strings.TrimSpace(payload.Wallet)
	heliusAPIKey := os.Getenv("HELIUS_API_KEY")
	trenchDemonCollection := os.Getenv("TRENCH_DEMON_COLLECTION")

	if heliusAPIKey == "" || trenchDemonCollection == "" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Server configuration error: Missing API keys"})
		log.Println(" Missing HELIUS_API_KEY or TRENCH_DEMON_COLLECTION in environment variables")
		return
	}

	ownsNFT, err := services.CheckTokenOwnership(heliusAPIKey, wallet, trenchDemonCollection)
	if err != nil {
		log.Printf(" Error checking wallet ownership: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check wallet ownership"})
		return
	}

	status := " does not hold"
	if ownsNFT {
		status = " holds"
	}

	c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("Wallet %s %s a Trench Demon NFT", wallet, status)})
}

func TestHeliusConnection(c *gin.Context) {
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "getHealth",
	}
	requestBody, _ := json.Marshal(payload)

	url := fmt.Sprintf("https://mainnet.helius-rpc.com/?api-key=%s", os.Getenv("HELIUS_API_KEY"))
	resp, err := http.Post(url, "application/json", bytes.NewBuffer(requestBody))
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to connect to Helius API"})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Helius API returned an error"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": " Helius connection successful"})
}

func HealthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": " OK"})
}

func AnalyseHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": " Analysis complete"})
}

func ListenHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": " Listening for updates"})
}

func FilterHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"message": " Filter applied"})
}
