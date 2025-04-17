package bot

import (
	// Remove database and model imports
	// "ca-scraper/agent/database"       // REMOVED
	// "ca-scraper/agent/internal/models" // REMOVED
	"ca-scraper/shared/notifications" // Keep notifications
	"encoding/json"

	// Keep necessary imports
	"errors"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	// Add logger import if appLogger comes from elsewhere or make sure it's correctly initialized
)

// --- getThreadIDFromUpdate (Keep As Is, assuming it doesn't use DB/Models) ---
func getThreadIDFromUpdate(update tgbotapi.Update) int {
	message := update.Message
	if message == nil {
		// Cannot determine thread ID without a message context in this implementation
		return 0
	}

	threadID := 0 // Default to 0 (main chat / no specific topic)

	// --- Check ReplyToMessage first using JSON ---
	if message.ReplyToMessage != nil {
		replyJSON, err := json.Marshal(message.ReplyToMessage)
		// Use a temporary variable inside the scope to avoid shadowing 'err' below
		if errMarshalReply := err; errMarshalReply == nil {
			var replyMap map[string]interface{}
			if errUnmarshalReply := json.Unmarshal(replyJSON, &replyMap); errUnmarshalReply == nil {
				// Look for "message_thread_id" in the reply's JSON structure
				if threadIDVal, ok := replyMap["message_thread_id"]; ok {
					if tidFloat, ok := threadIDVal.(float64); ok {
						threadID = int(tidFloat)
						// If found a non-zero thread ID in the reply, return it immediately
						if threadID != 0 {
							return threadID
						}
					}
				}
			} else {
				// Optional: Log error unmarshalling reply JSON
				// log.Printf("WARN: Failed to unmarshal ReplyToMessage JSON: %v", errUnmarshalReply)
			}
		} else {
			// Optional: Log error marshalling reply
			// log.Printf("WARN: Failed to marshal ReplyToMessage: %v", errMarshalReply)
		}
	}

	// --- If not found in reply, check the main Message using JSON ---
	// Only proceed if threadID is still 0
	if threadID == 0 {
		messageJSON, err := json.Marshal(message)
		if errMarshalMsg := err; errMarshalMsg == nil {
			var messageMap map[string]interface{}
			if errUnmarshalMsg := json.Unmarshal(messageJSON, &messageMap); errUnmarshalMsg == nil {
				// Look for "message_thread_id" in the main message's JSON structure
				if threadIDVal, ok := messageMap["message_thread_id"]; ok {
					if tidFloat, ok := threadIDVal.(float64); ok {
						threadID = int(tidFloat)
						// If found a non-zero thread ID here, return it
						// (This variable 'threadID' will be returned at the end anyway)
						// No need for 'return threadID' here, just assign it.
					}
				}

				// --- Safely ignore the 'is_topic_message' and 'message_id' fallback ---
				// This fallback logic is often unreliable and was causing the unused variable error.
				// It's better to rely solely on the explicit 'message_thread_id' field.

			} else {
				// Optional: Log error unmarshalling message JSON
				// log.Printf("WARN: Failed to unmarshal Message JSON: %v", errUnmarshalMsg)
			}
		} else {
			// Optional: Log error marshalling message
			// log.Printf("WARN: Failed to marshal Message: %v", errMarshalMsg)
		}
	}

	// Return the found threadID (will be 0 if not found in reply or main message JSON)
	return threadID
}

// --- HandleCommand (Adjust switch cases) ---
func HandleCommand(update tgbotapi.Update, command, args string) {
	if update.Message == nil {
		return
	}
	chatID := update.Message.Chat.ID
	threadID := getThreadIDFromUpdate(update) // Use the updated function

	if appLogger == nil { // Ensure appLogger is initialized correctly somewhere
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
		// Call the modified handler
		handleWhitelistCommand(chatID, threadID, args)
	case "walletupdate":
		// Call the modified handler
		handleWalletUpdateCommand(chatID, threadID, args)
	case "walletdelete":
		// Call the modified handler
		handleWalletDeleteCommand(chatID, threadID, args)
	case "start", "help":
		// Call the modified handler
		handleHelpCommand(chatID, threadID)
	default:
		appLogger.Zap().Warnw("Unknown command received", "command", command, "chatID", chatID, "threadID", threadID)
		rawMessage := fmt.Sprintf("Unknown command: /%s", command)
		SendReply(chatID, threadID, rawMessage)
	}
}

// --- Command Handlers (Remove DB Logic) ---

func handleWhitelistCommand(chatID int64, threadID int, args string) {
	wallet := strings.TrimSpace(args)
	if wallet == "" {
		// Raw message, SendReply handles escaping
		SendReply(chatID, threadID, "Usage: /whitelist {wallet-address}")
		return
	}

	// Simulate success or indicate DB is disabled
	appLogger.Zap().Infow("Whitelist command received (DB disabled)", "wallet", wallet, "chatID", chatID, "threadID", threadID)
	// rawMessage := fmt.Sprintf("Wallet `%s` would be whitelisted (DB disabled).", wallet)
	rawMessage := fmt.Sprintf("Received whitelist command for: `%s`", wallet) // Simpler reply
	SendReply(chatID, threadID, rawMessage)
}

func handleWalletUpdateCommand(chatID int64, threadID int, args string) {
	parts := strings.Fields(args)
	if len(parts) != 2 {
		SendReply(chatID, threadID, "Usage: /walletupdate {current-wallet} {new-wallet}")
		return
	}
	currWallet := parts[0]
	newWallet := parts[1]

	// Simulate success or indicate DB is disabled
	appLogger.Zap().Infow("Wallet update command received (DB disabled)", "oldWallet", currWallet, "newWallet", newWallet, "chatID", chatID, "threadID", threadID)
	// rawMessage := fmt.Sprintf("Wallet `%s` would be updated to `%s` (DB disabled).", currWallet, newWallet)
	rawMessage := fmt.Sprintf("Received wallet update command: `%s` -> `%s`", currWallet, newWallet) // Simpler reply
	SendReply(chatID, threadID, rawMessage)
}

func handleWalletDeleteCommand(chatID int64, threadID int, args string) {
	wallet := strings.TrimSpace(args)
	if wallet == "" {
		SendReply(chatID, threadID, "Usage: /walletdelete {wallet-address}")
		return
	}

	// Simulate success or indicate DB is disabled
	appLogger.Zap().Infow("Wallet delete command received (DB disabled)", "wallet", wallet, "chatID", chatID, "threadID", threadID)
	// rawMessage := fmt.Sprintf("Wallet `%s` would be removed (DB disabled).", wallet)
	rawMessage := fmt.Sprintf("Received wallet delete command for: `%s`", wallet) // Simpler reply
	SendReply(chatID, threadID, rawMessage)
}

func handleHelpCommand(chatID int64, threadID int) {
	// Updated help text to reflect disabled commands or remove them
	helpText := `*Available commands:*
*/whitelist {wallet}* - _(DB Disabled)_
*/walletupdate {current} {new}* - _(DB Disabled)_
*/walletdelete {wallet}* - _(DB Disabled)_
*/help* - Show this help`
	// SendReply will escape the *, -, etc.
	SendReply(chatID, threadID, helpText)
}

// --- SendReply function (Keep As Is - Requires notifications package) ---
func SendReply(chatID int64, threadID int, text string) {
	theBot := notifications.GetBotInstance()
	if theBot == nil {
		log.Println("ERROR: Cannot send reply, bot instance (tgbotapi) is nil.")
		return
	}

	// Escape the *entire* text string before sending
	escapedText := notifications.EscapeMarkdownV2(text)

	params := make(map[string]string)
	params["chat_id"] = fmt.Sprintf("%d", chatID)
	params["text"] = escapedText // Use the escaped text
	params["parse_mode"] = tgbotapi.ModeMarkdownV2
	if threadID != 0 {
		params["message_thread_id"] = fmt.Sprintf("%d", threadID)
	}

	_, err := theBot.MakeRequest("sendMessage", params)
	if err != nil {
		// Keep existing detailed error logging
		if appLogger != nil {
			logArgs := []interface{}{"chatID", chatID, "threadID", threadID, "error", err, "originalText", text} // Log original text for debug
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
