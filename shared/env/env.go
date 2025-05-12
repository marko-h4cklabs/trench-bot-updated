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

	HeliusRPCURL string
	MiniAppURL   string // Added for frontend URL

	PGHOST     string
	PGPORT     string
	PGUSER     string
	PGPASSWORD string
	PGDATABASE string

	DATABASE_URL string

	LOCAL_DATABASE_HOST     string
	LOCAL_DATABASE_PORT     string
	LOCAL_DATABASE_USER     string
	LOCAL_DATABASE_PASSWORD string
	LOCAL_DATABASE_NAME     string

	TargetGroupLink   string
	FrontendAPISecret string
)

func loadEnvVariable(key string, isRequired bool) string {
	value := os.Getenv(key)
	if isRequired && value == "" {
		log.Fatalf("FATAL: Environment variable %s is required but not set.", key)
	}
	isHidden := key == "TELEGRAM_BOT_TOKEN" || key == "HELIUS_API_KEY" || key == "WEBHOOK_SECRET" || key == "LOCAL_DATABASE_PASSWORD" || key == "PGPASSWORD" || key == "DATABASE_URL" || key == "FRONTEND_API_SECRET"
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

func loadIntEnv(key string, required bool) int {
	strValue := loadEnvVariable(key, required)
	if strValue == "" {
		if !required {
			log.Printf("INFO: Optional integer environment variable %s is missing, defaulting to 0.", key)
			return 0
		}
		log.Fatalf("FATAL: Required integer environment variable %s is missing after load.", key)
		return 0
	}
	id, err := strconv.Atoi(strValue)
	if err != nil {
		log.Fatalf("FATAL: Failed to parse integer environment variable %s='%s': %v", key, strValue, err)
	}
	return id
}

func loadInt64Env(key string, required bool) int64 {
	strValue := loadEnvVariable(key, required)
	if strValue == "" {
		if !required {
			log.Printf("INFO: Optional int64 environment variable %s is missing, defaulting to 0.", key)
			return 0
		}
		log.Fatalf("FATAL: Required int64 environment variable %s is missing after load.", key)
		return 0
	}
	id, err := strconv.ParseInt(strValue, 10, 64)
	if err != nil {
		log.Fatalf("FATAL: Failed to parse int64 environment variable %s='%s': %v", key, strValue, err)
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

	TelegramGroupID = loadInt64Env("TELEGRAM_GROUP_ID", true)
	BotCallsThreadID = loadIntEnv("BOT_CALLS_THREAD_ID", false)
	TrackingThreadID = loadIntEnv("TRACKING_THREAD_ID", false)

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

	MiniAppURL = loadEnvVariable("MINI_APP_URL", true)
	TargetGroupLink = loadEnvVariable("TARGET_GROUP_LINK", true)
	FrontendAPISecret = loadEnvVariable("FRONTEND_API_SECRET", false)

	DATABASE_URL = loadEnvVariable("DATABASE_URL", true)

	PGHOST = loadEnvVariable("PGHOST", false)
	PGPORT = loadEnvVariable("PGPORT", false)
	PGUSER = loadEnvVariable("PGUSER", false)
	PGPASSWORD = loadEnvVariable("PGPASSWORD", false)
	PGDATABASE = loadEnvVariable("PGDATABASE", false)

	LOCAL_DATABASE_HOST = loadEnvVariable("LOCAL_DATABASE_HOST", false)
	LOCAL_DATABASE_PORT = loadEnvVariable("LOCAL_DATABASE_PORT", false)
	LOCAL_DATABASE_USER = loadEnvVariable("LOCAL_DATABASE_USER", false)
	LOCAL_DATABASE_PASSWORD = loadEnvVariable("LOCAL_DATABASE_PASSWORD", false)
	LOCAL_DATABASE_NAME = loadEnvVariable("LOCAL_DATABASE_NAME", false)

	if DATABASE_URL == "" {
		log.Println("WARN: DATABASE_URL is not set. Connection logic might rely on PG* or LOCAL_* variables.")
	}
	if MiniAppURL == "" {
		log.Println("FATAL: MINI_APP_URL is required but was not loaded correctly.") // Made Fatal
	}
	if TargetGroupLink == "" {
		log.Println("FATAL: TARGET_GROUP_LINK is required but was not loaded correctly.") // Made Fatal
	}
	if FrontendAPISecret == "" {
		log.Println("WARN: FRONTEND_API_SECRET is not set. The verification callback endpoint will be unsecured.")
	}
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

	HeliusRPCURL = loadEnvVariable("HELIUS_RPC_URL", true)
	if HeliusRPCURL == "" {
	}
	log.Println("INFO: Environment variables loading process complete.")
	return nil
}
