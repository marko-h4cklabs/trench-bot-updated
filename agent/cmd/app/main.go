package main

import (
	"ca-scraper/agent/internal/bot"
	"ca-scraper/agent/internal/handlers"
	"ca-scraper/agent/internal/services"
	"ca-scraper/shared/config"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

func startHeartbeat(appLogger *logger.Logger) {
	go func() {
		ticker := time.NewTicker(8 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			appLogger.Info("Heartbeat: Program running...")
		}
	}()
}

func main() {
	defer func() {
		if r := recover(); r != nil {
			log.Panicf("FATAL PANIC RECOVERY: %v", r)
		}
	}()

	if err := env.LoadEnv(); err != nil {
		log.Fatalf("FATAL: Failed to load environment variables: %v", err)
	}
	log.Println("INFO: Environment variables loaded via shared/env.")

	log.Println("INFO: Initializing application logger.")
	appEnv := "production"
	logLevel := "info"
	enableTelegramLogging := env.TelegramBotToken != "" && env.TelegramGroupID != 0
	loggerCfg := logger.Config{
		Level:          logLevel,
		Environment:    appEnv,
		EnableTelegram: enableTelegramLogging,
	}
	appLogger, err := logger.NewLogger(loggerCfg)
	if err != nil {
		log.Fatalf("FATAL: Failed to initialize logger: %v", err)
	}
	appLogger.Info("Application logger initialized successfully.")

	log.Println("INFO: Initializing Telegram notifications...")
	if err := notifications.InitTelegramBot(); err != nil {
		log.Printf("WARN: Failed to initialize Telegram Bot, proceeding without Telegram features: %v", err)
	} else {
		log.Println("INFO: Telegram notifications initialized (if enabled and configured).")
	}

	appLogger.Info("Loading application configuration...")
	cfg, errCfg := config.LoadConfig("agent/config.yaml")
	if errCfg != nil {
		appLogger.Fatal("Failed to load agent/config.yaml", zap.Error(errCfg))
	}
	config.SetGlobalConfig(cfg)
	appLogger.Info("Application configuration loaded.")

	appLogger.Info("Initializing Telegram Bot command listener...")
	if err := bot.InitializeBot(appLogger, nil); err != nil {
		appLogger.Error("Failed to initialize Telegram Bot listener", zap.Error(err))
	} else {
		appLogger.Info("Telegram Bot command listener initialized.")
	}

	appLogger.Info("Setting up Helius webhooks for graduation...")
	graduationWebhookURL := env.WebhookURL
	if graduationWebhookURL != "" {
		appLogger.Info("Attempting to set up Graduation webhook subscription with Helius", zap.String("yourReceivingWebhookURL", graduationWebhookURL))
		if err := services.SetupGraduationWebhook(graduationWebhookURL, appLogger); err != nil {
			appLogger.Error("Failed to set up Graduation webhook subscription", zap.Error(err))
		} else {
			found, checkErr := services.CheckExistingHeliusWebhook(graduationWebhookURL, appLogger)
			if checkErr != nil {
				appLogger.Warn("Failed to check existing Graduation webhook status after setup", zap.Error(checkErr))
			} else if !found {
				appLogger.Error("Graduation webhook verification failed after setup attempt.")
			} else {
				appLogger.Info("Graduation webhook existence confirmed.")
			}
		}
	} else {
		appLogger.Warn("Your Webhook URL (env.WebhookURL) not set, skipping Graduation webhook subscription setup with Helius.")
	}

	appLogger.Info("Setting up web server...")
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"*"}
	corsConfig.AllowMethods = []string{"GET", "POST", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization", "X-Verification-Secret"}
	router.Use(cors.New(corsConfig))
	appLogger.Info("CORS middleware configured.")

	handlers.RegisterRoutes(router, appLogger)
	handlers.RegisterAPIRoutes(router, appLogger, nil)
	appLogger.Info("Web server and API routes registered.")

	appLogger.Info("Starting background services...")
	go services.CheckTokenProgress(appLogger)
	appLogger.Info("Background services started.")

	go func() {
		serverAddr := ":" + env.Port
		appLogger.Info("Starting web server", zap.String("address", serverAddr))
		if err := router.Run(serverAddr); err != nil {
			appLogger.Fatal("Could not start web server.", zap.Error(err))
		}
	}()

	appLogger.Info("Running startup tests...")
	appLogger.Info("Startup tests completed (or skipped).")

	appLogger.Info("Starting heartbeat monitor.")
	startHeartbeat(appLogger)

	if notifications.GetBotInstance() != nil {
		appLogger.Info("Starting Telegram Bot message listener...")
		go bot.StartListening(context.Background())

		// ✅ Send startup message
		// Removed `err :=` because SendBotCallMessage does not return an error.
		notifications.SendBotCallMessage("📡 Scanning started...", map[string]string{
			"thread_id": fmt.Sprintf("%d", env.BotCallsThreadID),
		})
		// If you need to handle potential errors from coreSendMessageWithRetry,
		// you would need to modify SendBotCallMessage to return that error.
		// For now, based on its current definition, it doesn't.
	} else {
		appLogger.Warn("Telegram Bot listener not started because bot initialization failed or was skipped.")
	}

	appLogger.Info("Application startup complete. Waiting for events...")
	select {}
}