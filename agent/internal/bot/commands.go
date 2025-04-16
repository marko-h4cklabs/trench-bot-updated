package bot

import (
	"ca-scraper/agent/database"
	"ca-scraper/agent/internal/models"
	"ca-scraper/shared/notifications"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func getThreadIDFromUpdate(update tgbotapi.Update) int {
	threadID := 0

	if update.Message != nil && update.Message.ReplyToMessage != nil {
		replyJSON, err := json.Marshal(update.Message.ReplyToMessage)
		if err == nil {
			var replyMap map[string]interface{}
			if json.Unmarshal(replyJSON, &replyMap) == nil {
				if threadIDVal, ok := replyMap["message_thread_id"]; ok {
					if tidFloat, ok := threadIDVal.(float64); ok {
						threadID = int(tidFloat)
						if threadID != 0 {
							return threadID
						}
					}
				}
			}
		}
	}

	if update.Message != nil {
		messageJSON, err := json.Marshal(update.Message)
		if err == nil {
			var messageMap map[string]interface{}
			if json.Unmarshal(messageJSON, &messageMap) == nil {
				isTopic := false
				if isTopicVal, ok := messageMap["is_topic_message"]; ok {
					if isTopicBool, ok := isTopicVal.(bool); ok {
						isTopic = isTopicBool
					}
				}

				if threadIDVal, ok := messageMap["message_thread_id"]; ok {
					if tidFloat, ok := threadIDVal.(float64); ok {
						threadID = int(tidFloat)
						if threadID != 0 {
							return threadID
						}
					}
				}

				if isTopic && threadID == 0 {
					if msgIDVal, ok := messageMap["message_id"]; ok {
						if msgIDFloat, ok := msgIDVal.(float64); ok {
							threadID = int(msgIDFloat)
							return threadID
						}
					}
				}
			}
		}
	}

	return threadID
}

func HandleCommand(update tgbotapi.Update, command, args string) {
	if update.Message == nil {
		return
	}
	chatID := update.Message.Chat.ID

	threadID := getThreadIDFromUpdate(update)

	if appLogger == nil {
		log.Println("ERROR: appLogger is nil in HandleCommand")
		return
	}
	appLogger.Zap().Infow("Processing command",
		"command", command,
		"args", args,
		"chatID", chatID,
		"determinedThreadID", threadID,
		"user", update.Message.From.UserName,
		"userID", update.Message.From.ID,
	)

	switch command {
	case "whitelist":
		handleWhitelistCommand(chatID, threadID, args)
	case "walletupdate":
		handleWalletUpdateCommand(chatID, threadID, args)
	case "walletdelete":
		handleWalletDeleteCommand(chatID, threadID, args)
	case "start", "help":
		handleHelpCommand(chatID, threadID)
	default:
		appLogger.Zap().Warnw("Unknown command received", "command", command, "chatID", chatID, "threadID", threadID)
		SendReply(chatID, threadID, fmt.Sprintf("Unknown command: /%s", notifications.EscapeMarkdownV2(command)))
	}
}

func handleWhitelistCommand(chatID int64, threadID int, args string) {
	wallet := strings.TrimSpace(args)
	if wallet == "" {
		SendReply(chatID, threadID, "Usage: `/whitelist {wallet\\-address}`")
		return
	}
	var user models.User
	result := database.DB.Where("wallet_id = ?", wallet).First(&user)
	if result.Error != nil && result.Error.Error() != "record not found" {
		SendReply(chatID, threadID, "Database error checking wallet.")
		return
	}
	if result.RowsAffected > 0 {
		SendReply(chatID, threadID, fmt.Sprintf("Wallet `%s` is already whitelisted\\!", notifications.EscapeMarkdownV2(wallet)))
		return
	}
	newUser := models.User{WalletID: wallet, NFTStatus: false}
	if err := database.DB.Create(&newUser).Error; err != nil {
		SendReply(chatID, threadID, "Error adding wallet to whitelist.")
		return
	}
	SendReply(chatID, threadID, fmt.Sprintf("Wallet `%s` has been whitelisted\\!", notifications.EscapeMarkdownV2(wallet)))
}

func handleWalletUpdateCommand(chatID int64, threadID int, args string) {
	parts := strings.Fields(args)
	if len(parts) != 2 {
		SendReply(chatID, threadID, "Usage: `/walletupdate {current-wallet} {new-wallet}`")
		return
	}
	currWallet := parts[0]
	newWallet := parts[1]

	var user models.User
	result := database.DB.Where("wallet_id = ?", currWallet).First(&user)
	if result.Error != nil || result.RowsAffected == 0 {
		SendReply(chatID, threadID, "Current wallet not found or database error.")
		return
	}

	var check models.User
	if err := database.DB.Where("wallet_id = ?", newWallet).First(&check).Error; err == nil && check.ID != user.ID {
		SendReply(chatID, threadID, "New wallet is already used.")
		return
	}

	user.WalletID = newWallet
	if err := database.DB.Save(&user).Error; err != nil {
		SendReply(chatID, threadID, "Error updating wallet.")
		return
	}
	SendReply(chatID, threadID, fmt.Sprintf("Wallet `%s` updated to `%s`\\!", notifications.EscapeMarkdownV2(currWallet), notifications.EscapeMarkdownV2(newWallet)))
}

func handleWalletDeleteCommand(chatID int64, threadID int, args string) {
	wallet := strings.TrimSpace(args)
	if wallet == "" {
		SendReply(chatID, threadID, "Usage: `/walletdelete {wallet}`")
		return
	}
	result := database.DB.Where("wallet_id = ?", wallet).Delete(&models.User{})
	if result.Error != nil {
		SendReply(chatID, threadID, "Error deleting wallet.")
		return
	}
	if result.RowsAffected == 0 {
		SendReply(chatID, threadID, "Wallet not found.")
		return
	}
	SendReply(chatID, threadID, fmt.Sprintf("Wallet `%s` removed.`", notifications.EscapeMarkdownV2(wallet)))
}

func handleHelpCommand(chatID int64, threadID int) {
	helpText := `*Available commands:*
/whitelist {wallet} - Add wallet to whitelist
/walletupdate {current} {new} - Update wallet
/walletdelete {wallet} - Remove wallet
/help - Show this help`
	SendReply(chatID, threadID, helpText)
}

func SendReply(chatID int64, threadID int, text string) {
	theBot := notifications.GetBotInstance()
	if theBot == nil {
		log.Println("ERROR: Cannot send reply, bot instance (tgbotapi) is nil.")
		return
	}

	params := make(map[string]string)
	params["chat_id"] = fmt.Sprintf("%d", chatID)
	params["text"] = text
	params["parse_mode"] = tgbotapi.ModeMarkdownV2
	if threadID != 0 {
		params["message_thread_id"] = fmt.Sprintf("%d", threadID)
	}

	_, err := theBot.MakeRequest("sendMessage", params)
	if err != nil {
		if appLogger != nil {
			logArgs := []interface{}{"chatID", chatID, "threadID", threadID, "error", err}
			var tgErr *tgbotapi.Error
			if errors.As(err, &tgErr) {
				logArgs = append(logArgs, "apiErrorCode", tgErr.Code, "apiErrorDesc", tgErr.Message)
			}
			appLogger.Zap().Errorw("Failed to send reply via MakeRequest", logArgs...)
		} else {
			log.Printf("ERROR: Failed to send tgbotapi reply via MakeRequest to chat %d (thread %d): %v", chatID, threadID, err)
		}
	}
}
