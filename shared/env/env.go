package env

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

var (
	HeliusAPIKey            string
	WebhookSecret           string
	WebhookURL              string
	PumpFunAuthority        string
	RaydiumAccountAddresses string
	HeliusAuthHeader        string
	Port                    string
	TrenchDemonCollection   string
	RaydiumAccounts         []string

	TelegramBotToken   string
	TelegramGroupID    int64
	SystemLogsThreadID int
)

func LoadEnv() error {
	HeliusAPIKey = os.Getenv("HELIUS_API_KEY")
	WebhookSecret = os.Getenv("WEBHOOK_SECRET")
	WebhookURL = os.Getenv("WEBHOOK_LISTENER_URL_DEV")
	PumpFunAuthority = os.Getenv("PUMPFUN_AUTHORITY_ADDRESS")
	RaydiumAccountAddresses = os.Getenv("RAYDIUM_ACCOUNT_ADDRESSES")
	HeliusAuthHeader = os.Getenv("HELIUS_AUTH_HEADER")
	Port = os.Getenv("PORT")
	TrenchDemonCollection = os.Getenv("TRENCH_DEMON_COLLECTION")
	TelegramBotToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	telegramGroupIDStr := os.Getenv("TELEGRAM_GROUP_ID")
	systemLogsThreadIDStr := os.Getenv("SYSTEM_LOGS_THREAD_ID")

	errorList := []string{}
	if HeliusAPIKey == "" {
		errorList = append(errorList, "missing required env var: HELIUS_API_KEY")
	}
	if WebhookSecret == "" {
		log.Println("WARN: missing env var: WEBHOOK_SECRET (needed for webhook creation/auth)")
	}
	if WebhookURL == "" {
		errorList = append(errorList, "missing required env var: WEBHOOK_LISTENER_URL_DEV")
	}
	if TrenchDemonCollection == "" {
		log.Println("WARN: missing env var: TRENCH_DEMON_COLLECTION (needed for NFT check)")
	}
	if TelegramBotToken == "" {
		errorList = append(errorList, "missing required env var: TELEGRAM_BOT_TOKEN")
	}
	if telegramGroupIDStr == "" {
		errorList = append(errorList, "missing required env var: TELEGRAM_GROUP_ID")
	} else {
		var err error
		TelegramGroupID, err = strconv.ParseInt(telegramGroupIDStr, 10, 64)
		if err != nil {
			errorList = append(errorList, fmt.Sprintf("invalid TELEGRAM_GROUP_ID (must be integer): %v", err))
		}
	}

	if systemLogsThreadIDStr != "" {
		var err error
		SystemLogsThreadID, err = strconv.Atoi(systemLogsThreadIDStr)
		if err != nil {
			log.Printf("WARN: invalid SYSTEM_LOGS_THREAD_ID (must be integer), defaulting to 0: %v", err)
			SystemLogsThreadID = 0
		}
	} else {
		SystemLogsThreadID = 0
	}

	if len(errorList) > 0 {
		return fmt.Errorf("environment variable errors: %s", strings.Join(errorList, "; "))
	}

	if Port == "" {
		Port = "5555"
		log.Println("PORT not set, using default:", Port)
	}

	RaydiumAccounts = []string{}
	if RaydiumAccountAddresses != "" {
		for _, addr := range strings.Split(RaydiumAccountAddresses, ",") {
			trimmedAddr := strings.TrimSpace(addr)
			if trimmedAddr != "" {
				RaydiumAccounts = append(RaydiumAccounts, trimmedAddr)
			}
		}
	}

	log.Println("Environment variables loaded into env package.")
	return nil
}
