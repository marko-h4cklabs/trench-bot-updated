package main

import (
	"fmt"
	"strings"
	"time"

	"ca-scraper/agent/internal/bot"
	"ca-scraper/agent/internal/handlers"
	"ca-scraper/agent/internal/services"
	"ca-scraper/agent/internal/tests"
	"ca-scraper/shared/config"
	"ca-scraper/shared/env"
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
			panicMsg := fmt.Sprintf("Program crashed with panic: %v", r)
			println(panicMsg)
		}
	}()

	if err := godotenv.Load(".env"); err != nil {
		println(".env file NOT found in the current directory.")
	} else {
		println(".env file successfully loaded.")
	}

	if err := env.LoadEnv(); err != nil {
		println("Failed to load environment variables:", err.Error())
		return
	}

	appLogger, err := logger.NewLogger("production", true)
	if err != nil {
		println("Failed to initialize logger:", err.Error())
		return
	}
	println("Logger initialized successfully.")

	addressList := strings.Split(env.RaydiumAccountAddresses, ",")
	if env.PumpFunAuthority != "" {
		addressList = append(addressList, env.PumpFunAuthority)
		println("Added Pump.fun Authority Address:", env.PumpFunAuthority)
	} else {
		appLogger.Info("PUMPFUN_AUTHORITY_ADDRESS is missing. Pump.fun graduations may not be tracked.")
	}
	println("Final Address List for Webhook:", strings.Join(addressList, ", "))

	cfg, err := config.LoadConfig("agent/config.yaml")
	if err != nil {
		appLogger.Error("Failed to load configuration: " + err.Error())
		return
	}
	config.SetGlobalConfig(cfg)

	println("Initializing Telegram Bot...")
	if err := bot.InitializeBot(appLogger); err != nil {
		appLogger.Error("Failed to initialize Telegram Bot: " + err.Error())
		return
	}

	println("Checking and creating Webhook (if necessary)...")
	if !services.CreateHeliusWebhook(env.WebhookURL) {
		appLogger.Error("Webhook creation failed")
		return
	}

	found, err := services.CheckExistingHeliusWebhook(env.WebhookURL)
	if err != nil {
		appLogger.Warn("Failed to check existing webhook status", "error", err)
	} else if !found {
		appLogger.Error("Webhook verification failed")
		return
	} else {
		appLogger.Info("Webhook existence confirmed.")
	}

	println("Running Webhook Startup Test...")
	handlers.TestWebhookOnStartup()

	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()
	router.Use(cors.Default())

	handlers.RegisterRoutes(router, appLogger.ZapLogger, appLogger)

	go func() {
		println("Server running on http://localhost:" + env.Port)
		if err := router.Run(":" + env.Port); err != nil {
			appLogger.Error("Could not start server: " + err.Error())
		}
	}()

	services.TestWebhookWithAuth()
	tests.RunStartupTests()

	startHeartbeat(appLogger)

	select {}
}
