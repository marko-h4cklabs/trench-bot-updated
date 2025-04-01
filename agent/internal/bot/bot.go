package bot

import (
	"fmt"
	"log"
	"os"
	"strconv"

	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/joho/godotenv"
)

var bot *tgbotapi.BotAPI
var botToken string
var groupID int64
var systemLogsThreadID int
var appLogger *logger.Logger

func InitializeBot(log *logger.Logger) error {
	appLogger = log
	var err error

	if err := godotenv.Load(".env"); err != nil {
		println(".env file NOT found in the current directory.")
	} else {
		println(".env file successfully loaded.")
	}

	botToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	if botToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN is missing in .env or system environment variables")
	}

	groupIDStr := os.Getenv("TELEGRAM_GROUP_ID")
	if groupIDStr == "" {
		return fmt.Errorf("TELEGRAM_GROUP_ID is missing in .env or system environment variables")
	}

	groupID, err = strconv.ParseInt(groupIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("Failed to parse TELEGRAM_GROUP_ID: %v", err)
	}

	systemLogsThreadIDStr := os.Getenv("SYSTEM_LOGS_THREAD_ID")
	if systemLogsThreadIDStr != "" {
		systemLogsThreadID, err = strconv.Atoi(systemLogsThreadIDStr)
		if err != nil {
			return fmt.Errorf("Failed to parse SYSTEM_LOGS_THREAD_ID: %v", err)
		}
	}

	err = notifications.InitTelegramBot()
	if err != nil {
		return fmt.Errorf("Failed to initialize Telegram bot: %v", err)
	}

	println("Telegram bot initialized successfully.")

	return nil
}

func StartListening() {
	if bot == nil {
		log.Println(" Telegram bot not initialized. Skipping message listening.")
		return
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			appLogger.Info(fmt.Sprintf(" Received message: %s", update.Message.Text))

			if update.Message.IsCommand() {
				command := update.Message.Command()
				args := update.Message.CommandArguments()
				appLogger.Info(fmt.Sprintf(" Command '%s' called with args: %s", command, args))

				HandleCommand(update)
			}
		}
	}
}
