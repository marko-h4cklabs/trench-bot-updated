package main

import (
	"os"
	"strings"
	"time"

	"ca-scraper/agent/internal/bot"
	"ca-scraper/agent/internal/handlers"
	"ca-scraper/agent/internal/services"
	"ca-scraper/agent/internal/tests"
	"ca-scraper/shared/config"
	"ca-scraper/shared/logger"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
)

func startHeartbeat(appLogger *logger.Logger) {
	go func() {
		for {
			appLogger.Info("Program is running... waiting for transactions.")
			time.Sleep(8 * time.Minute)
		}
	}()
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			panicMsg := "Program crashed with panic: " + r.(string)
			println(panicMsg)
		}
	}()

	if err := godotenv.Load(".env"); err != nil {
		println(".env file NOT found in the current directory.")
	} else {
		println(".env file successfully loaded.")
	}

	appLogger, err := logger.NewLogger("production", true)
	if err != nil {
		println(" Failed to initialize logger:", err.Error())
		return
	}
	println(" Logger initialized successfully.")

	port := os.Getenv("PORT")
	if port == "" {
		port = "5555"
		appLogger.Info(" PORT not set in .env, using default port 5555")
	}

	apiKey := os.Getenv("HELIUS_API_KEY")
	webhookSecret := os.Getenv("WEBHOOK_SECRET")
	webhookURL := os.Getenv("WEBHOOK_LISTENER_URL_DEV")
	pumpFunAuthority := os.Getenv("PUMPFUN_AUTHORITY_ADDRESS")
	addresses := os.Getenv("RAYDIUM_ACCOUNT_ADDRESSES")

	if apiKey == "" || webhookSecret == "" || webhookURL == "" {
		appLogger.Error(" ERROR: Missing one or more required environment variables (HELIUS_API_KEY, WEBHOOK_SECRET, or WEBHOOK_LISTENER_URL_DEV). Check .env file.")
		return
	}

	addressList := strings.Split(addresses, ",")
	if pumpFunAuthority != "" {
		addressList = append(addressList, pumpFunAuthority)
		println(" Added Pump.fun Authority Address: " + pumpFunAuthority)
	} else {
		appLogger.Info(" Warning: PUMPFUN_AUTHORITY_ADDRESS is missing. Pump.fun graduations may not be tracked.")
	}

	println(" Final Address List for Webhook: " + strings.Join(addressList, ", "))

	cfg, err := config.LoadConfig("agent/config.yaml")
	if err != nil {
		appLogger.Error(" Failed to load configuration: " + err.Error())
		return
	}
	config.SetGlobalConfig(cfg)

	println("Initializing Telegram Bot...")
	if err := bot.InitializeBot(appLogger); err != nil {
		appLogger.Error(" Failed to initialize Telegram Bot: " + err.Error())
		return
	}

	println(" Checking and creating Webhook (if necessary)...")
	if !services.CreateHeliusWebhook(webhookURL) {
		appLogger.Error(" Webhook creation failed")
		return
	}

	found, _ := services.CheckExistingHeliusWebhook(apiKey, webhookURL)
	if !found {
		appLogger.Error(" Webhook verification failed")
		return
	}

	println("Running Webhook Startup Test...")
	handlers.TestWebhookOnStartup()

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	router.Use(cors.Default())

	handlers.RegisterRoutes(router, appLogger.ZapLogger, appLogger)

	go func() {
		println(" Server running on http://localhost:" + port)
		if err := router.Run(":" + port); err != nil {
			appLogger.Error(" Could not start server: " + err.Error())
		}
	}()

	services.TestWebhookWithAuth()
	tests.RunStartupTests()

	startHeartbeat(appLogger)

	select {}
}
