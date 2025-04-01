package handlers

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
)

type Logger struct {
	ZapLogger      *zap.SugaredLogger
	BotToken       string
	GroupID        int64
	SystemLogsID   int
	EnableTelegram bool
}

func NewLogger(environment string, enableTelegram bool) (*Logger, error) {
	if err := godotenv.Load(".env"); err != nil {
		println(".env file NOT found in the current directory.")
	} else {
		println(".env file successfully loaded.")

		fmt.Println("🔹 TELEGRAM_BOT_TOKEN:", os.Getenv("TELEGRAM_BOT_TOKEN"))
		fmt.Println("🔹 TELEGRAM_GROUP_ID:", os.Getenv("TELEGRAM_GROUP_ID"))
		fmt.Println("🔹 SYSTEM_LOGS_THREAD_ID:", os.Getenv("SYSTEM_LOGS_THREAD_ID"))
	}

	var zapLogger *zap.Logger
	if environment == "production" {
		zapLogger, _ = zap.NewProduction()
	} else {
		zapLogger, _ = zap.NewDevelopment()
	}

	botToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	groupIDStr := os.Getenv("TELEGRAM_GROUP_ID")
	systemLogsIDStr := os.Getenv("SYSTEM_LOGS_THREAD_ID")

	if botToken == "" {
		fmt.Println("Missing TELEGRAM_BOT_TOKEN, Telegram logging disabled.")
		enableTelegram = false
	}

	var groupID int64
	var systemLogsID int
	var err error

	if enableTelegram {
		if groupIDStr == "" || systemLogsIDStr == "" {
			fmt.Println("TELEGRAM_GROUP_ID or SYSTEM_LOGS_THREAD_ID missing. Disabling Telegram logs.")
			enableTelegram = false
		} else {
			groupID, err = strconv.ParseInt(groupIDStr, 10, 64)
			if err != nil {
				fmt.Printf("Failed to parse TELEGRAM_GROUP_ID: %v\n", err)
				enableTelegram = false
			}

			systemLogsID, err = strconv.Atoi(systemLogsIDStr)
			if err != nil {
				fmt.Printf("Failed to parse SYSTEM_LOGS_THREAD_ID: %v\n", err)
				enableTelegram = false
			}
		}
	}

	return &Logger{
		ZapLogger:      zapLogger.Sugar(),
		BotToken:       botToken,
		GroupID:        groupID,
		SystemLogsID:   systemLogsID,
		EnableTelegram: enableTelegram,
	}, nil
}

func LogToTelegram(message string) {
	telegramToken := os.Getenv("TELEGRAM_BOT_TOKEN")
	chatID := os.Getenv("TELEGRAM_GROUP_ID")

	if telegramToken == "" || chatID == "" {
		log.Println(" Telegram credentials are missing, skipping notification.")
		return
	}

	time.Sleep(2 * time.Second)

	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramToken)
	data := url.Values{}
	data.Set("chat_id", chatID)
	data.Set("text", message)
	data.Set("parse_mode", "Markdown")

	resp, err := http.Post(apiURL, "application/x-www-form-urlencoded", strings.NewReader(data.Encode()))
	if err != nil {
		log.Printf("Failed to send message to Telegram: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == 429 {
		log.Println(" Telegram API rate limit reached. Slowing down messages...")
		time.Sleep(10 * time.Second)
	}
}
