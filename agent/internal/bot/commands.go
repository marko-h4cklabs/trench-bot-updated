package bot

import (
	"ca-scraper/shared/env" // Ensure env package is imported
	"ca-scraper/shared/notifications"
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/mymmrac/telego"
	"go.uber.org/zap"
	// Your other necessary imports...
	// "ca-scraper/agent/database" // If needed for verification checks
)

// Assume appLogger and dbInstance are accessible package-level variables

func getThreadIDFromUpdate(update telego.Update) int {
	var threadID int // Default to 0
	if update.Message != nil {
		if update.Message.MessageThreadID != 0 {
			return update.Message.MessageThreadID
		}
		if update.Message.ReplyToMessage != nil && update.Message.ReplyToMessage.MessageThreadID != 0 {
			return update.Message.ReplyToMessage.MessageThreadID
		}
	}
	return threadID
}

func HandleCommand(update telego.Update, command, args string) {
	if update.Message == nil {
		return
	}
	chatID := telego.ChatID{ID: update.Message.Chat.ID}
	userID := update.Message.From.ID
	threadID := getThreadIDFromUpdate(update)

	if appLogger == nil {
		log.Println("ERROR: appLogger is nil in HandleCommand")
		_ = SendReply(chatID, threadID, "An internal error occurred. Please notify an administrator.")
		return
	}
	logFields := []interface{}{
		"command", command, "args", args, "chatID", chatID.ID, "threadID", threadID,
		"user", update.Message.From.Username, "userID", userID,
	}
	appLogger.Zap().Infow("Processing command", logFields...)

	restrictedCommands := map[string]bool{
		"whitelist":    true,
		"walletupdate": true,
		"walletdelete": true,
	}

	if restrictedCommands[command] {
		appLogger.Debug("Checking user verification status", zap.Int64("userID", userID))
		// *** Replace with your actual database call using dbInstance ***
		// Example: isVerified, err := database.IsUserVerified(dbInstance, userID)
		isVerified := false // Placeholder
		var err error = nil // Placeholder

		if err != nil {
			appLogger.Error("Database error checking user verification status", zap.Error(err), zap.Int64("userID", userID))
			_ = SendReply(chatID, threadID, "An error occurred while checking your access status. Please try again later.")
			return
		}

		if !isVerified {
			appLogger.Info("Access denied: User not verified", zap.Int64("userID", userID), zap.String("command", command))
			replyMsg := fmt.Sprintf("Access Denied. Please use the `/verify` command to connect your wallet and verify you hold at least %d Trench Demon NFTs.", env.NFTMinimumHolding)
			_ = SendReply(chatID, threadID, replyMsg)
			return
		}
		appLogger.Info("Access granted (verified user)", zap.Int64("userID", userID), zap.String("command", command))
	}

	switch command {
	case "verify":
		handleVerifyCommand(chatID, threadID) // Uses the updated function below
	case "whitelist":
		handleWhitelistCommand(chatID, threadID, args)
	case "walletupdate":
		handleWalletUpdateCommand(chatID, threadID, args)
	case "walletdelete":
		handleWalletDeleteCommand(chatID, threadID, args)
	case "start", "help":
		handleHelpCommand(chatID, threadID)
	default:
		appLogger.Zap().Warnw("Unknown command received", logFields...)
		rawMessage := fmt.Sprintf("Unknown command: /%s", command)
		_ = SendReply(chatID, threadID, rawMessage)
	}
}

// --- UPDATED handleVerifyCommand ---
func handleVerifyCommand(chatID telego.ChatID, threadID int) {
	// Use the Mini App URL loaded from the environment
	if env.MiniAppURL == "" {
		// This check is a safeguard; LoadEnv should have caught this if required=true
		log.Println("ERROR: MINI_APP_URL environment variable is not set or empty in handleVerifyCommand!")
		if appLogger != nil {
			appLogger.Error("MINI_APP_URL environment variable is missing or empty")
		}
		_ = SendReply(chatID, threadID, "Verification service is currently unavailable (configuration error). Please contact an admin.")
		return
	}

	webApp := &telego.WebAppInfo{URL: env.MiniAppURL} // Use the loaded env var
	button := telego.InlineKeyboardButton{
		Text:   "🔒 Connect Wallet & Verify NFTs",
		WebApp: webApp,
	}
	keyboard := &telego.InlineKeyboardMarkup{
		InlineKeyboard: [][]telego.InlineKeyboardButton{{button}},
	}

	text := "Click the button below to connect your Solana wallet and verify your Trench Demon NFT holdings:"

	msgParams := &telego.SendMessageParams{
		ChatID:      chatID,
		Text:        text,
		ReplyMarkup: keyboard,
	}

	if threadID != 0 {
		msgParams.MessageThreadID = threadID
	}

	theBot := notifications.GetBotInstance()
	if theBot == nil {
		log.Println("ERROR: Bot instance nil in handleVerifyCommand")
		_ = SendReply(chatID, threadID, "Error: Could not initialize verification process. Please contact an admin.")
		return
	}

	_, err := theBot.SendMessage(context.Background(), msgParams)
	if err != nil {
		log.Printf("ERROR sending verify command reply with button: %v", err)
		if appLogger != nil {
			appLogger.Error("Failed to send verify command reply", zap.Error(err), zap.Int64("chatID", chatID.ID))
		}
		_ = SendReply(chatID, threadID, "Could not display the verification button. Please try using the `/verify` command again later.")
	} else {
		if appLogger != nil {
			appLogger.Info("Verify prompt with button sent successfully", zap.Int64("chatID", chatID.ID), zap.String("url", env.MiniAppURL))
		}
	}
}

func handleWhitelistCommand(chatID telego.ChatID, threadID int, args string) {
	wallet := strings.TrimSpace(args)
	if wallet == "" {
		_ = SendReply(chatID, threadID, "Usage: /whitelist {wallet-address}")
		return
	}
	appLogger.Zap().Infow("Whitelist command execution", "wallet", wallet, "chatID", chatID.ID, "threadID", threadID)
	rawMessage := fmt.Sprintf("Received whitelist command for: `%s` (DB Action Placeholder)", wallet)
	_ = SendReply(chatID, threadID, rawMessage)
}

func handleWalletUpdateCommand(chatID telego.ChatID, threadID int, args string) {
	parts := strings.Fields(args)
	if len(parts) != 2 {
		_ = SendReply(chatID, threadID, "Usage: /walletupdate {current-wallet} {new-wallet}")
		return
	}
	currWallet := parts[0]
	newWallet := parts[1]
	appLogger.Zap().Infow("Wallet update command execution", "oldWallet", currWallet, "newWallet", newWallet, "chatID", chatID.ID, "threadID", threadID)
	rawMessage := fmt.Sprintf("Received wallet update command: `%s` -> `%s` (DB Action Placeholder)", currWallet, newWallet)
	_ = SendReply(chatID, threadID, rawMessage)
}

func handleWalletDeleteCommand(chatID telego.ChatID, threadID int, args string) {
	wallet := strings.TrimSpace(args)
	if wallet == "" {
		_ = SendReply(chatID, threadID, "Usage: /walletdelete {wallet-address}")
		return
	}
	appLogger.Zap().Infow("Wallet delete command execution", "wallet", wallet, "chatID", chatID.ID, "threadID", threadID)
	rawMessage := fmt.Sprintf("Received wallet delete command for: `%s` (DB Action Placeholder)", wallet)
	_ = SendReply(chatID, threadID, rawMessage)
}

func handleHelpCommand(chatID telego.ChatID, threadID int) {
	helpText := `*Available commands:*
*/verify* - Connect wallet & verify NFT holdings for access
*/whitelist {wallet}* - _(Requires Verification)_
*/walletupdate {current} {new}* - _(Requires Verification)_
*/walletdelete {wallet}* - _(Requires Verification)_
*/help* - Show this help`
	_ = SendReply(chatID, threadID, helpText)
}

func SendReply(chatID telego.ChatID, threadID int, rawText string) error {
	theBot := notifications.GetBotInstance()
	if theBot == nil {
		log.Println("ERROR: Cannot send reply, bot instance (telego) is nil.")
		return errors.New("bot instance is nil")
	}

	escapedText := notifications.EscapeMarkdownV2(rawText)

	params := &telego.SendMessageParams{
		ChatID:    chatID,
		Text:      escapedText,
		ParseMode: telego.ModeMarkdownV2,
	}
	if threadID != 0 {
		params.MessageThreadID = threadID
	}

	_, err := theBot.SendMessage(context.Background(), params)

	if err != nil {
		log.Printf("ERROR: Failed to send Telego reply to chat %d (thread %d): %v. Original Text: %s", chatID.ID, threadID, err, rawText)
		if appLogger != nil {
			logArgs := []interface{}{"chatID", chatID.ID, "threadID", threadID, "error", err, "originalText", rawText}
			appLogger.Zap().Errorw("Failed to send reply via Telego SendMessage", logArgs...)
		}
	}
	return err
}
