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

	HeliusAPIKey     string
	WebhookURL       string
	WebhookSecret    string
	HeliusAuthHeader string
	PumpFunAuthority string

	RaydiumAccountAddresses string
	Port                     string

	HeliusRPCURL string
	SolanaRPCURL string

	PotentialCAThreadID   int
	ScannerLogsThreadID   int
	SystemLogsThreadID    int
	TargetGroupLink       string
)

func loadEnvVariable(key string, isRequired bool) string {
	value := os.Getenv(key)
	if isRequired && value == "" {
		log.Fatalf("FATAL: Environment variable %s is required but not set.", key)
	}
	isHidden := key == "TELEGRAM_BOT_TOKEN" || key == "HELIUS_API_KEY" || key == "WEBHOOK_SECRET" || key == "HELIUS_AUTH_HEADER"
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
	TelegramGroupID = loadInt64Env("TELEGRAM_GROUP_ID", true)
	BotCallsThreadID = loadIntEnv("BOT_CALLS_THREAD_ID", false)

	HeliusAPIKey = loadEnvVariable("HELIUS_API_KEY", true)
	HeliusAuthHeader = loadEnvVariable("HELIUS_AUTH_HEADER", false)
	HeliusRPCURL = loadEnvVariable("HELIUS_RPC_URL", true)
	SolanaRPCURL = loadEnvVariable("SOLANA_RPC_URL", true)

	WebhookURL = os.Getenv("WEBHOOK_LISTENER_URL_PROD")
	if WebhookURL == "" {
		log.Println("INFO: WEBHOOK_LISTENER_URL_PROD not set, trying WEBHOOK_LISTENER_URL_DEV.")
		WebhookURL = loadEnvVariable("WEBHOOK_LISTENER_URL_DEV", true)
	} else {
		log.Println("INFO: Using WEBHOOK_LISTENER_URL_PROD.")
	}
	WebhookSecret = loadEnvVariable("WEBHOOK_SECRET", false)

	PumpFunAuthority = loadEnvVariable("PUMPFUN_AUTHORITY_ADDRESS", true)
	RaydiumAccountAddresses = loadEnvVariable("RAYDIUM_ACCOUNT_ADDRESSES", false)

	Port = loadEnvVariable("PORT", false)
	if Port == "" {
		Port = "8080"
		log.Printf("INFO: PORT not set, defaulting to %s", Port)
	}

	PotentialCAThreadID = loadIntEnv("POTENTIAL_CA_THREAD_ID", false)
	ScannerLogsThreadID = loadIntEnv("SCANNER_LOGS_THREAD_ID", false)
	SystemLogsThreadID = loadIntEnv("SYSTEM_LOGS_THREAD_ID", false)

	TargetGroupLink = loadEnvVariable("TARGET_GROUP_LINK", true)

	log.Println("INFO: Environment variables loading process complete.")
	return nil
}
