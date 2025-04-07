package bot

import (
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
	"fmt"

	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"go.uber.org/zap"
)

var bot *tgbotapi.BotAPI
var appLogger *logger.Logger

func InitializeBot(logInstance *logger.Logger) error {
	appLogger = logInstance

	err := notifications.InitTelegramBot()
	if err != nil {
		appLogger.Error("Failed to initialize Telegram bot via notifications package", zap.Error(err))
		return fmt.Errorf("failed to initialize Telegram bot: %w", err)
	}
	bot = notifications.GetBotInstance()
	if bot == nil {
		err := fmt.Errorf("failed to get bot instance from notifications package after initialization")
		appLogger.Error(err.Error())
		return err
	}

	appLogger.Info("Telegram bot services initialized successfully for listening.")

	return nil
}

func StartListening() {
	if bot == nil {
		if appLogger != nil {
			appLogger.Error("Telegram bot not initialized. Cannot start listening.")
		} else {
			log.Println("ERROR: Telegram bot not initialized. Cannot start listening.")
		}
		return
	}
	if appLogger != nil {
		appLogger.Info("Starting Telegram bot message listener...")
	} else {
		log.Println("INFO: Starting Telegram bot message listener...")
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	if appLogger != nil {
		appLogger.Info("Listening for Telegram commands and messages...")
	} else {
		log.Println("INFO: Listening for Telegram commands and messages...")
	}

	for update := range updates {
		if update.Message != nil {
			if appLogger != nil {
				appLogger.Debug("Received message",
					zap.Int64("ChatID", update.Message.Chat.ID),
					zap.String("From", update.Message.From.UserName),
					zap.String("Text", update.Message.Text),
				)
			}

			if update.Message.IsCommand() {
				HandleCommand(update)
			}
		}
	}

	if appLogger != nil {
		appLogger.Info("Telegram bot update channel closed.")
	} else {
		log.Println("INFO: Telegram bot update channel closed.")
	}
}
