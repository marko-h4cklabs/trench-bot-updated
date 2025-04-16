package bot

import (
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
	"context"
	"fmt"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var appLogger *logger.Logger
var botInstance *tgbotapi.BotAPI

func InitializeBot(logInstance *logger.Logger) error {
	if logInstance == nil {
		fmt.Println("FATAL ERROR: InitializeBot requires a non-nil logger instance")
		return fmt.Errorf("logger instance provided to InitializeBot is nil")
	}
	appLogger = logInstance
	botInstance = notifications.GetBotInstance()
	if botInstance == nil {
		appLogger.Error("Could not retrieve initialized Telegram bot instance from notifications package. Bot may not function.")
		return fmt.Errorf("failed to get tgbotapi bot instance")
	}
	appLogger.Info("Telegram bot interaction services initialized using go-telegram-bot-api/v5.")
	return nil
}

func StartListening(ctx context.Context) {
	if appLogger == nil {
		fmt.Println("ERROR: Logger not initialized for bot listener. Cannot start.")
		return
	}
	if botInstance == nil {
		appLogger.Warn("Bot API instance not available. Cannot start command listener.")
		return
	}
	appLogger.Info("Starting bot message/command listener (go-telegram-bot-api/v5)...")

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := botInstance.GetUpdatesChan(u)
	appLogger.Info("Listening for Telegram commands and messages...")

	for {
		select {
		case update := <-updates:
			if update.Message == nil || !update.Message.Chat.IsGroup() && !update.Message.Chat.IsSuperGroup() {
				continue
			}

			if !update.Message.IsCommand() {
				continue
			}

			chatID := update.Message.Chat.ID
			fromUser := update.Message.From.UserName
			fromUserID := update.Message.From.ID
			text := update.Message.Text

			appLogger.Zap().Debugw("Received command message",
				"chatID", chatID,
				"fromUser", fromUser,
				"fromUserID", fromUserID,
				"text", text,
			)

			command := update.Message.Command()
			args := update.Message.CommandArguments()
			go HandleCommand(update, command, args)

		case <-ctx.Done():
			appLogger.Info("Context cancelled. Stopping Telegram listener.")
			return
		}
	}
}
