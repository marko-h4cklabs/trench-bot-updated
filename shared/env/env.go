package env

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

var (
	TelegramBotToken    string
	TelegramGroupID     int64
	SystemLogsThreadID  int
	ScannerLogsThreadID int
	PotentialCAThreadID int
	BotCallsThreadID    int
	TrackingThreadID    int

	HeliusAPIKey     string
	WebhookURL       string
	WebhookSecret    string
	HeliusAuthHeader string
	PumpFunAuthority string

	RaydiumWebhookURL       string
	RaydiumAccountAddresses string

	Port string
)

func loadEnvVariable(key string, isRequired bool) string {
	value := os.Getenv(key)
	if isRequired && value == "" {
		log.Fatalf("FATAL: Environment variable %s is required but not set.", key)
	}
	if value == "" {
		log.Printf("INFO: Environment variable %s is not set.", key)
	} else {
		if key != "TELEGRAM_BOT_TOKEN" && key != "HELIUS_API_KEY" && key != "WEBHOOK_SECRET" {
			log.Printf("INFO: Loaded %s = %s", key, value)
		} else {
			log.Printf("INFO: Loaded %s (value hidden)", key)
		}
	}
	return value
}

func loadIntEnv(key string, required bool, isGroupID bool) int {
	strValue := loadEnvVariable(key, required)
	if strValue == "" {
		if required {
			log.Fatalf("FATAL: Required integer environment variable %s is missing.", key)
		}
		return 0
	}
	if isGroupID {
		id, err := strconv.ParseInt(strValue, 10, 64)
		if err != nil {
			log.Fatalf("FATAL: Failed to parse integer environment variable %s: %v", key, err)
		}
		return int(id)
	}

	id, err := strconv.Atoi(strValue)
	if err != nil {
		log.Fatalf("FATAL: Failed to parse integer environment variable %s: %v", key, err)
	}
	return id
}

func LoadEnv() error {
	err := godotenv.Load()
	if err != nil {
		log.Println("INFO: .env file not found or error loading, relying on system environment variables.")
	} else {
		log.Println("INFO: .env file loaded successfully.")
	}

	TelegramBotToken = loadEnvVariable("TELEGRAM_BOT_TOKEN", false)
	HeliusAPIKey = loadEnvVariable("HELIUS_API_KEY", true)
	WebhookURL = loadEnvVariable("WEBHOOK_LISTENER_URL_PROD", true)
	WebhookSecret = loadEnvVariable("WEBHOOK_SECRET", false)
	HeliusAuthHeader = loadEnvVariable("HELIUS_AUTH_HEADER", false)
	PumpFunAuthority = loadEnvVariable("PUMPFUN_AUTHORITY_ADDRESS", true)

	RaydiumAccountAddresses = loadEnvVariable("RAYDIUM_ACCOUNT_ADDRESSES", false)

	Port = loadEnvVariable("PORT", false)
	if Port == "" {
		Port = "8080"
		log.Printf("INFO: PORT not set, defaulting to %s", Port)
	}

	TelegramGroupID = int64(loadIntEnv("TELEGRAM_GROUP_ID", true, true))
	SystemLogsThreadID = loadIntEnv("SYSTEM_LOGS_THREAD_ID", false, false)
	ScannerLogsThreadID = loadIntEnv("SCANNER_LOGS_THREAD_ID", false, false)
	PotentialCAThreadID = loadIntEnv("POTENTIAL_CA_THREAD_ID", false, false)

	BotCallsThreadID = loadIntEnv("BOT_CALLS_THREAD_ID", true, false)
	TrackingThreadID = loadIntEnv("TRACKING_THREAD_ID", true, false)

	if TelegramBotToken != "" && TelegramGroupID == 0 {
		log.Println("WARN: TELEGRAM_BOT_TOKEN is set, but TELEGRAM_GROUP_ID is missing or invalid.")
	}
	if WebhookURL == "" {
		log.Println("WARN: WEBHOOK_LISTENER_URL_PROD (or DEV) is not set. Webhooks will not function.")
	}

	if TelegramBotToken != "" && BotCallsThreadID == 0 {
		log.Println("WARN: BOT_CALLS_THREAD_ID is missing or invalid. Validated token calls will not be sent.")
	}
	if TelegramBotToken != "" && TrackingThreadID == 0 {
		log.Println("WARN: TRACKING_THREAD_ID is missing or invalid. Tracking updates will not be sent.")
	}

	log.Println("INFO: Environment variables loaded.")
	return nil
}
