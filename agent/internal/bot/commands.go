package bot

import (
	"ca-scraper/shared/notifications" // Ensure correct import path
	"encoding/json"
	"errors"
	"fmt"
	"log" // Keep standard log for errors if appLogger isn't guaranteed
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	// Add logger import if appLogger is used here, e.g.:
	// "ca-scraper/shared/logger"
)

// Keep appLogger variable if it's initialized and passed correctly from bot.go
// var appLogger *logger.Logger

// --- getThreadIDFromUpdate (Keep Existing Logic) ---
func getThreadIDFromUpdate(update tgbotapi.Update) int {
	message := update.Message
	if message == nil {
		return 0
	}

	threadID := 0

	// Check ReplyToMessage first (keep JSON parsing logic as provided)
	if message.ReplyToMessage != nil {
		replyJSON, err := json.Marshal(message.ReplyToMessage)
		if errMarshalReply := err; errMarshalReply == nil {
			var replyMap map[string]interface{}
			if errUnmarshalReply := json.Unmarshal(replyJSON, &replyMap); errUnmarshalReply == nil {
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

	// Check main Message if not found in reply (keep JSON parsing logic as provided)
	if threadID == 0 {
		messageJSON, err := json.Marshal(message)
		if errMarshalMsg := err; errMarshalMsg == nil {
			var messageMap map[string]interface{}
			if errUnmarshalMsg := json.Unmarshal(messageJSON, &messageMap); errUnmarshalMsg == nil {
				if threadIDVal, ok := messageMap["message_thread_id"]; ok {
					if tidFloat, ok := threadIDVal.(float64); ok {
						threadID = int(tidFloat)
					}
				}
			}
		}
	}

	return threadID
}

// --- HandleCommand (Keep Existing Logic, Ensure appLogger is valid) ---
func HandleCommand(update tgbotapi.Update, command, args string) {
	if update.Message == nil {
		return // Ignore updates without messages
	}
	chatID := update.Message.Chat.ID
	threadID := getThreadIDFromUpdate(update)

	// Make sure appLogger is available here (likely set in bot.go's InitializeBot)
	if appLogger == nil {
		log.Println("ERROR: appLogger is nil in HandleCommand")
		// Optionally send a basic reply indicating an internal error
		// SendReply(chatID, threadID, "Internal error processing command.")
		return
	}

	// Keep detailed logging
	appLogger.Zap().Infow("Processing command",
		"command", command,
		"args", args,
		"chatID", chatID,
		"determinedThreadID", threadID,
		"user", update.Message.From.UserName,
		"userID", update.Message.From.ID,
	)

	// Route command, passing raw text to handlers/SendReply
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
		// Prepare raw message
		rawMessage := fmt.Sprintf("Unknown command: /%s", command)
		// Send raw message
		SendReply(chatID, threadID, rawMessage)
	}
}

// --- Command Handlers (Pass RAW text to SendReply) ---

func handleWhitelistCommand(chatID int64, threadID int, args string) {
	wallet := strings.TrimSpace(args)
	if wallet == "" {
		// Send raw usage message
		SendReply(chatID, threadID, "Usage: /whitelist {wallet-address}")
		return
	}

	// Keep logging as needed
	appLogger.Zap().Infow("Whitelist command received (DB disabled)", "wallet", wallet, "chatID", chatID, "threadID", threadID)
	// Prepare raw reply message with Markdown
	rawMessage := fmt.Sprintf("Received whitelist command for: `%s`", wallet)
	// Send raw message
	SendReply(chatID, threadID, rawMessage)
}

func handleWalletUpdateCommand(chatID int64, threadID int, args string) {
	parts := strings.Fields(args)
	if len(parts) != 2 {
		// Send raw usage message
		SendReply(chatID, threadID, "Usage: /walletupdate {current-wallet} {new-wallet}")
		return
	}
	currWallet := parts[0]
	newWallet := parts[1]

	// Keep logging as needed
	appLogger.Zap().Infow("Wallet update command received (DB disabled)", "oldWallet", currWallet, "newWallet", newWallet, "chatID", chatID, "threadID", threadID)
	// Prepare raw reply message with Markdown
	rawMessage := fmt.Sprintf("Received wallet update command: `%s` -> `%s`", currWallet, newWallet)
	// Send raw message
	SendReply(chatID, threadID, rawMessage)
}

func handleWalletDeleteCommand(chatID int64, threadID int, args string) {
	wallet := strings.TrimSpace(args)
	if wallet == "" {
		// Send raw usage message
		SendReply(chatID, threadID, "Usage: /walletdelete {wallet-address}")
		return
	}

	// Keep logging as needed
	appLogger.Zap().Infow("Wallet delete command received (DB disabled)", "wallet", wallet, "chatID", chatID, "threadID", threadID)
	// Prepare raw reply message with Markdown
	rawMessage := fmt.Sprintf("Received wallet delete command for: `%s`", wallet)
	// Send raw message
	SendReply(chatID, threadID, rawMessage)
}

func handleHelpCommand(chatID int64, threadID int) {
	// Prepare raw help text with intended Markdown
	helpText := `*Available commands:*
*/whitelist {wallet}* - _(DB Disabled)_
*/walletupdate {current} {new}* - _(DB Disabled)_
*/walletdelete {wallet}* - _(DB Disabled)_
*/help* - Show this help`
	// Send raw message
	SendReply(chatID, threadID, helpText)
}

// --- SendReply function (Accepts RAW text, Escapes ONCE before sending) ---
func SendReply(chatID int64, threadID int, rawText string) {
	theBot := notifications.GetBotInstance()
	if theBot == nil {
		log.Println("ERROR: Cannot send reply, bot instance (tgbotapi) is nil.")
		return
	}

	// --- Escape the raw text ONCE here ---
	escapedText := notifications.EscapeMarkdownV2(rawText)
	// ------------------------------------

	params := make(map[string]string)
	params["chat_id"] = fmt.Sprintf("%d", chatID)
	params["text"] = escapedText                   // Use the escaped text
	params["parse_mode"] = tgbotapi.ModeMarkdownV2 // Mode is essential
	if threadID != 0 {
		params["message_thread_id"] = fmt.Sprintf("%d", threadID)
	}

	_, err := theBot.MakeRequest("sendMessage", params)
	if err != nil {
		// Keep detailed error logging, logging the ORIGINAL raw text is helpful
		if appLogger != nil {
			// Pass rawText to log context for easier debugging
			logArgs := []interface{}{"chatID", chatID, "threadID", threadID, "error", err, "originalText", rawText}
			var tgErr *tgbotapi.Error
			if errors.As(err, &tgErr) {
				logArgs = append(logArgs, "apiErrorCode", tgErr.Code, "apiErrorDesc", tgErr.Message)
			}
			appLogger.Zap().Errorw("Failed to send reply via MakeRequest", logArgs...)
		} else {
			// Fallback to standard logger if appLogger isn't available
			log.Printf("ERROR: Failed to send tgbotapi reply via MakeRequest to chat %d (thread %d): %v. Original Text: %s", chatID, threadID, err, rawText)
		}
	}
}

// Assume appLogger is initialized and accessible in this package, typically via bot.go
// var appLogger *logger.Logger // Declaration if needed, initialization happens elsewhere
