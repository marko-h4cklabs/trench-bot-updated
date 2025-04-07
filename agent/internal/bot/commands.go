package bot

import (
	"ca-scraper/agent/database"
	"ca-scraper/agent/internal/models"
	"fmt"
	"log"

	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

func HandleCommand(update tgbotapi.Update) {
	command := update.Message.Command()
	args := update.Message.CommandArguments()

	if appLogger == nil {
		log.Printf("ERROR: appLogger not initialized in bot package when handling command '%s'", command)
		return
	}

	appLogger.Info("Processing command",
		zap.String("command", command),
		zap.String("args", args),
		zap.Int64("ChatID", update.Message.Chat.ID),
		zap.String("User", update.Message.From.UserName),
	)

	switch command {
	case "whitelist":
		handleWhitelistCommand(update, args)
	case "walletupdate":
		handleWalletUpdateCommand(update, args)
	case "walletdelete":
		handleWalletDeleteCommand(update, args)
	case "start", "help":
		handleHelpCommand(update)
	default:
		appLogger.Warn("Unknown command received", zap.String("command", command))
		SendReply(update.Message.Chat.ID, fmt.Sprintf(" Unknown command: /%s", command))
	}
}

func handleWhitelistCommand(update tgbotapi.Update, args string) {
	wallet := strings.TrimSpace(args)
	if wallet == "" {
		SendReply(update.Message.Chat.ID, "Usage: /whitelist {wallet-address}")
		appLogger.Warn("Whitelist command failed: no wallet address provided")
		return
	}

	var user models.User
	result := database.DB.Where("wallet_id = ?", wallet).First(&user)
	if result.RowsAffected > 0 {
		SendReply(update.Message.Chat.ID, fmt.Sprintf("Wallet `%s` is already whitelisted!", wallet))
		appLogger.Info("Whitelist command failed: wallet already whitelisted", zap.String("wallet", wallet))
		return
	}

	newUser := models.User{WalletID: wallet, NFTStatus: false}
	if err := database.DB.Create(&newUser).Error; err != nil {
		SendReply(update.Message.Chat.ID, "An error occurred while whitelisting the wallet.")
		appLogger.Error("Whitelist command failed: error adding wallet to DB", zap.String("wallet", wallet), zap.Error(err))
		return
	}

	SendReply(update.Message.Chat.ID, fmt.Sprintf("Wallet `%s` has been whitelisted!", wallet))
	appLogger.Info("Wallet successfully whitelisted", zap.String("wallet", wallet))
}

func handleWalletUpdateCommand(update tgbotapi.Update, args string) {
	parts := strings.Fields(args)
	if len(parts) != 2 {
		SendReply(update.Message.Chat.ID, "Usage: /walletupdate {current-wallet-address} {new-wallet-address}")
		return
	}

	currentWallet := parts[0]
	updatedWallet := parts[1]

	var user models.User
	result := database.DB.Where("wallet_id = ?", currentWallet).First(&user)
	if result.RowsAffected == 0 {
		SendReply(update.Message.Chat.ID, fmt.Sprintf("Wallet `%s` is not in the whitelist.", currentWallet))
		appLogger.Warn("Wallet update failed: current wallet not found", zap.String("currentWallet", currentWallet))
		return
	}

	user.WalletID = updatedWallet
	if err := database.DB.Save(&user).Error; err != nil {
		appLogger.Error("Wallet update failed: error saving updated wallet", zap.String("currentWallet", currentWallet), zap.String("newWallet", updatedWallet), zap.Error(err))
		SendReply(update.Message.Chat.ID, " An error occurred while updating the wallet.")
		return
	}

	SendReply(update.Message.Chat.ID, fmt.Sprintf("Wallet `%s` has been updated to `%s`!", currentWallet, updatedWallet))
	appLogger.Info("Wallet successfully updated", zap.String("currentWallet", currentWallet), zap.String("newWallet", updatedWallet))
}

func handleWalletDeleteCommand(update tgbotapi.Update, args string) {
	wallet := strings.TrimSpace(args)
	if wallet == "" {
		SendReply(update.Message.Chat.ID, " Usage: /walletdelete {wallet-address}")
		return
	}

	result := database.DB.Where("wallet_id = ?", wallet).Delete(&models.User{})
	if result.Error != nil {
		appLogger.Error("Wallet delete failed: error deleting wallet", zap.String("wallet", wallet), zap.Error(result.Error))
		SendReply(update.Message.Chat.ID, "An error occurred while deleting the wallet.")
		return
	}

	if result.RowsAffected == 0 {
		SendReply(update.Message.Chat.ID, fmt.Sprintf("Wallet `%s` is not in the whitelist.", wallet))
		appLogger.Warn("Wallet delete failed: wallet not found", zap.String("wallet", wallet))
		return
	}

	SendReply(update.Message.Chat.ID, fmt.Sprintf("Wallet `%s` has been removed from the whitelist!", wallet))
	appLogger.Info("Wallet successfully deleted", zap.String("wallet", wallet))
}

func handleHelpCommand(update tgbotapi.Update) {
	helpText := `Available commands:
/whitelist {wallet} - Add wallet to whitelist.
/walletupdate {current} {new} - Update whitelisted wallet.
/walletdelete {wallet} - Remove wallet from whitelist.
/checkwallet {wallet} - Check if wallet holds Trench Demon NFT.
/help - Show this help message.`
	SendReply(update.Message.Chat.ID, helpText)
}

func SendReply(chatID int64, text string) {
	if bot == nil {
		log.Println("ERROR: Cannot send reply, bot is not initialized.")
		if appLogger != nil {
			appLogger.Error("Cannot send reply, bot is not initialized.")
		}
		return
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown

	if _, err := bot.Send(msg); err != nil {
		if appLogger != nil {
			appLogger.Error("Failed to send reply message", zap.Error(err), zap.Int64("chatID", chatID))
		} else {
			log.Printf("ERROR: Failed to send reply: %v", err)
		}
	}
}
