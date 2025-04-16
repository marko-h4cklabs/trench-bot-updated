package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
)

type TelegramConfig struct {
	BotToken              string
	GroupID               int64
	SystemLogsThreadID    int
	VolumeScannerThreadID int
	ScannerLogsThreadID   int
}

var telegramConfig TelegramConfig

func InitializeTelegram() error {
	var err error

	telegramConfig.BotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	groupIDStr := os.Getenv("TELEGRAM_GROUP_ID")
	systemLogsThreadIDStr := os.Getenv("SYSTEM_LOGS_THREAD_ID")
	volumeScannerThreadIDStr := os.Getenv("VOLUME_SCANNER_THREAD_ID")
	scannerLogsThreadIDStr := os.Getenv("SCANNER_LOGS_THREAD_ID")

	telegramConfig.GroupID, err = strconv.ParseInt(groupIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse TELEGRAM_GROUP_ID: %v", err)
	}

	telegramConfig.SystemLogsThreadID, err = strconv.Atoi(systemLogsThreadIDStr)
	if err != nil {
		return fmt.Errorf("failed to parse SYSTEM_LOGS_THREAD_ID: %v", err)
	}

	telegramConfig.VolumeScannerThreadID, err = strconv.Atoi(volumeScannerThreadIDStr)
	if err != nil {
		return fmt.Errorf("failed to parse VOLUME_SCANNER_THREAD_ID: %v", err)
	}

	telegramConfig.ScannerLogsThreadID, err = strconv.Atoi(scannerLogsThreadIDStr)
	if err != nil {
		return fmt.Errorf("failed to parse SCANNER_LOGS_THREAD_ID: %v", err)
	}

	log.Println("Telegram initialized successfully.")
	return nil
}

type SendMessagePayload struct {
	ChatID          int64  `json:"chat_id"`
	Text            string `json:"text"`
	MessageThreadID int    `json:"message_thread_id,omitempty"`
}

func SendTelegramMessage(payload SendMessagePayload) error {
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", telegramConfig.BotToken)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %v", err)
	}

	resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to send HTTP request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram API responded with status code: %d", resp.StatusCode)
	}

	return nil
}
