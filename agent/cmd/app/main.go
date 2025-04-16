package main

import (
	"ca-scraper/agent/internal/bot"
	"ca-scraper/agent/internal/handlers"
	"ca-scraper/agent/internal/services"
	"ca-scraper/agent/internal/tests"
	"ca-scraper/shared/config"
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications"
	"log"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
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

	if err := godotenv.Load(".env"); err != nil {
		log.Println("INFO: .env file not found or failed to load.")
	} else {
		log.Println("INFO: .env file loaded successfully.")
	}

	if err := env.LoadEnv(); err != nil {
		log.Fatalf("FATAL: Failed to load environment variables: %v", err)
	}
	log.Println("INFO: Environment variables loaded via shared/env.")

	log.Println("INFO: Initializing Telegram notifications...")
	if err := notifications.InitTelegramBot(); err != nil {
		log.Printf("WARN: Failed to initialize Telegram Bot, proceeding without Telegram features: %v", err)
	} else {
		log.Println("INFO: Telegram notifications initialized (if enabled and configured).")
	}

	log.Println("INFO: Initializing application logger...")
	appEnv := "production"
	enableTelegramLogging := env.TelegramBotToken != "" && env.TelegramGroupID != 0

	loggerCfg := logger.Config{
		Environment:         appEnv,
		EnableTelegram:      enableTelegramLogging,
		SystemLogsThreadID:  env.SystemLogsThreadID,
		ScannerLogsThreadID: env.ScannerLogsThreadID,
	}
	appLogger, err := logger.NewLogger(loggerCfg)
	if err != nil {
		log.Fatalf("FATAL: Failed to initialize logger: %v", err)
	}
	appLogger.Info("Application logger initialized successfully.")

	appLogger.Info("Loading application configuration...")
	cfg, err := config.LoadConfig("agent/config.yaml")
	if err != nil {
		appLogger.Fatal("Failed to load agent/config.yaml", zap.Error(err))
	}
	config.SetGlobalConfig(cfg)
	appLogger.Info("Application configuration loaded.")

	appLogger.Info("Initializing Telegram Bot command listener...")
	if err := bot.InitializeBot(appLogger); err != nil {
		appLogger.Error("Failed to initialize Telegram Bot listener", zap.Error(err))
	} else {
		appLogger.Info("Telegram Bot command listener initialized.")
	}

	appLogger.Info("Setting up Helius webhooks...")
	graduationWebhookURL := env.WebhookURL
	if graduationWebhookURL != "" {
		appLogger.Info("Attempting to set up Graduation webhook", zap.String("url", graduationWebhookURL))
		if err := services.SetupGraduationWebhook(graduationWebhookURL, appLogger); err != nil {
			appLogger.Error("Failed to set up Graduation webhook", zap.Error(err))
		} else {
			found, checkErr := services.CheckExistingHeliusWebhook(graduationWebhookURL, appLogger)
			if checkErr != nil {
				appLogger.Warn("Failed to check existing Graduation webhook status", zap.Error(checkErr))
			} else if !found {
				appLogger.Error("Graduation webhook verification failed after setup attempt.")
			} else {
				appLogger.Info("Graduation webhook existence confirmed.")
			}
		}
	} else {
		appLogger.Warn("WEBHOOK_LISTENER_URL_DEV/PROD not set, skipping Graduation webhook setup.")
	}
	appLogger.Info("Setting up web server routes...")
	gin.SetMode(gin.ReleaseMode)
	router := gin.Default()

	corsConfig := cors.DefaultConfig()
	corsConfig.AllowOrigins = []string{"*"}
	corsConfig.AllowMethods = []string{"GET", "POST", "OPTIONS"}
	corsConfig.AllowHeaders = []string{"Origin", "Content-Length", "Content-Type", "Authorization"}
	router.Use(cors.New(corsConfig))

	handlers.RegisterRoutes(router, appLogger)
	appLogger.Info("Web server routes registered.")

	appLogger.Info("Starting background services...")
	go services.CheckTokenProgress(appLogger)
	go services.ValidateAndNotifyCachedSwaps(appLogger)
	go services.CleanSwapCachePeriodically(appLogger)
	appLogger.Info("Background services started.")

	go func() {
		serverAddr := ":" + env.Port
		appLogger.Info("Starting web server", zap.String("address", serverAddr))
		if err := router.Run(serverAddr); err != nil {
			appLogger.Fatal("Could not start web server", zap.Error(err))
		}
	}()

	appLogger.Info("Running startup tests...")
	tests.RunStartupTests(appLogger)
	appLogger.Info("Startup tests completed.")

	appLogger.Info("Starting heartbeat monitor.")
	startHeartbeat(appLogger)

	appLogger.Info("Application startup complete. Waiting for events...")
	select {}
}
