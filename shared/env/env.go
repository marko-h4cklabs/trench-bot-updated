package env

import (
	"log"
	"os"
	"strconv"

	"github.com/joho/godotenv"
)

var (
	TelegramBotToken string
	TelegramGroupID  int64
	BotCallsThreadID int
	TrackingThreadID int

	HeliusAPIKey     string
	WebhookURL       string
	WebhookSecret    string
	HeliusAuthHeader string
	PumpFunAuthority string

	RaydiumWebhookURL       string
	RaydiumAccountAddresses string

	Port string

	NFTCollectionAddress string
	NFTMinimumHolding    int

	PGHOST       string
	PGPORT       string
	PGUSER       string
	PGPASSWORD   string
	PGDATABASE   string
	DATABASE_URL string

	LOCAL_DATABASE_HOST     string
	LOCAL_DATABASE_PORT     string
	LOCAL_DATABASE_USER     string
	LOCAL_DATABASE_PASSWORD string
	LOCAL_DATABASE_NAME     string
)

func loadEnvVariable(key string, isRequired bool) string {
	value := os.Getenv(key)
	if isRequired && value == "" {
		log.Fatalf("FATAL: Environment variable %s is required but not set.", key)
	}
	isHidden := key == "TELEGRAM_BOT_TOKEN" || key == "HELIUS_API_KEY" || key == "WEBHOOK_SECRET" || key == "LOCAL_DATABASE_PASSWORD" || key == "PGPASSWORD" || key == "DATABASE_URL"
	if value == "" {
		if !isRequired {
			log.Printf("INFO: Environment variable %s is not set.", key)
		}
	} else if isHidden {
		log.Printf("INFO: Loaded %s (value hidden)", key)
	} else {
		log.Printf("INFO: Loaded %s = %s", key, value)
	}
	return value
}

func loadIntEnv(key string, required bool, isGroupID bool) int {
	strValue := loadEnvVariable(key, required)
	if strValue == "" {
		if !required {
			log.Printf("INFO: Optional integer environment variable %s is missing, defaulting to 0.", key)
			return 0
		}
		log.Fatalf("FATAL: Required integer environment variable %s is missing after load.", key)
		return 0
	}
	if isGroupID {
		id, err := strconv.ParseInt(strValue, 10, 64)
		if err != nil {
			log.Fatalf("FATAL: Failed to parse int64 environment variable %s='%s': %v", key, strValue, err)
		}
		if id > 2147483647 || id < -2147483648 {
			log.Fatalf("FATAL: Group ID %s='%d' is out of standard int range.", key, id)
		}
		return int(id)
	}
	id, err := strconv.Atoi(strValue)
	if err != nil {
		log.Fatalf("FATAL: Failed to parse integer environment variable %s='%s': %v", key, strValue, err)
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
	WebhookURL = os.Getenv("WEBHOOK_LISTENER_URL_PROD")
	if WebhookURL == "" {
		log.Println("INFO: WEBHOOK_LISTENER_URL_PROD not set, trying WEBHOOK_LISTENER_URL_DEV.")
		WebhookURL = loadEnvVariable("WEBHOOK_LISTENER_URL_DEV", true)
	} else {
		log.Println("INFO: Using WEBHOOK_LISTENER_URL_PROD.")
	}
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
	BotCallsThreadID = loadIntEnv("BOT_CALLS_THREAD_ID", false, false)
	TrackingThreadID = loadIntEnv("TRACKING_THREAD_ID", false, false)
	NFTCollectionAddress = loadEnvVariable("NFT_COLLECTION_ADDRESS", true)
	nftMinHoldingStr := loadEnvVariable("NFT_MINIMUM_HOLDING", false)
	if nftMinHoldingStr == "" {
		NFTMinimumHolding = 3
		log.Printf("INFO: NFT_MINIMUM_HOLDING not set, defaulting to %d", NFTMinimumHolding)
	} else {
		var parseErr error
		NFTMinimumHolding, parseErr = strconv.Atoi(nftMinHoldingStr)
		if parseErr != nil || NFTMinimumHolding <= 0 {
			log.Printf("WARN: Invalid value '%s' for NFT_MINIMUM_HOLDING. Defaulting to 3. Error: %v", nftMinHoldingStr, parseErr)
			NFTMinimumHolding = 3
		}
		log.Printf("INFO: NFT Minimum Holding required for verification: %d", NFTMinimumHolding)
	}

	PGHOST = loadEnvVariable("PGHOST", false)
	PGPORT = loadEnvVariable("PGPORT", false)
	PGUSER = loadEnvVariable("PGUSER", false)
	PGPASSWORD = loadEnvVariable("PGPASSWORD", false)
	PGDATABASE = loadEnvVariable("PGDATABASE", false)
	DATABASE_URL = loadEnvVariable("DATABASE_URL", false)

	LOCAL_DATABASE_HOST = loadEnvVariable("LOCAL_DATABASE_HOST", true)
	LOCAL_DATABASE_PORT = loadEnvVariable("LOCAL_DATABASE_PORT", true)
	LOCAL_DATABASE_USER = loadEnvVariable("LOCAL_DATABASE_USER", true)
	LOCAL_DATABASE_PASSWORD = loadEnvVariable("LOCAL_DATABASE_PASSWORD", true)
	LOCAL_DATABASE_NAME = loadEnvVariable("LOCAL_DATABASE_NAME", true)

	if TelegramBotToken != "" && TelegramGroupID == 0 {
		log.Println("WARN: TELEGRAM_BOT_TOKEN is set, but TELEGRAM_GROUP_ID is missing, invalid, or zero.")
	}
	if WebhookURL == "" {
		log.Println("WARN: Webhook URL is not set. Webhooks will not function.")
	}
	if TelegramBotToken != "" && BotCallsThreadID == 0 {
		log.Println("WARN: BOT_CALLS_THREAD_ID is missing or invalid (0). Validated token calls will not be sent to the specific topic.")
	}
	if TelegramBotToken != "" && TrackingThreadID == 0 {
		log.Println("WARN: TRACKING_THREAD_ID is missing or invalid (0). Tracking updates will not be sent to the specific topic.")
	}

	log.Println("INFO: Environment variables loading process complete.")
	return nil
}
