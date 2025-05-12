package main

import (
	"ca-scraper/agent/database"
	"ca-scraper/agent/internal/bot"
	"ca-scraper/agent/internal/handlers"
	"ca-scraper/agent/internal/services" // This imports the package, HeliusService is in there
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

	// --- Initialize Helius Service ---
	appLogger.Info("Initializing Helius Service...")
	// Ensure env.HeliusRPCURL is loaded from your .env by env.LoadEnv()
	if env.HeliusRPCURL == "" { // Check if HELIUS_RPC_URL is loaded
		appLogger.Fatal("HELIUS_RPC_URL not set in environment variables. This is required for HeliusService.")
	}
	heliusSvc, errHelius := services.NewHeliusService(appLogger) // NewHeliusService now only takes appLogger
	// **** CORRECTED: Check errHelius ****
	if errHelius != nil {
		appLogger.Fatal("Failed to initialize Helius Service", zap.Error(errHelius))
	}
	// **** END CORRECTION ****
	appLogger.Info("Helius Service initialized successfully.")
	// --- END Helius Service Init ---

	var dsn string
	if env.DATABASE_URL != "" {
		appLogger.Info("Using DATABASE_URL for database connection.")
		dsn = env.DATABASE_URL
	} else {
		appLogger.Warn("DATABASE_URL not set. Attempting to construct DSN from PG* or LOCAL_* variables.")
		dbHost := env.PGHOST
		dbPort := env.PGPORT
		dbUser := env.PGUSER
		dbPassword := env.PGPASSWORD
		dbName := env.PGDATABASE

		if dbHost == "" && env.LOCAL_DATABASE_HOST != "" {
			appLogger.Info("Falling back to LOCAL_DATABASE_HOST")
			dbHost = env.LOCAL_DATABASE_HOST
		}
		if dbPort == "" && env.LOCAL_DATABASE_PORT != "" {
			appLogger.Info("Falling back to LOCAL_DATABASE_PORT")
			dbPort = env.LOCAL_DATABASE_PORT
		}
		if dbUser == "" && env.LOCAL_DATABASE_USER != "" {
			appLogger.Info("Falling back to LOCAL_DATABASE_USER")
			dbUser = env.LOCAL_DATABASE_USER
		}
		if dbPassword == "" && env.LOCAL_DATABASE_PASSWORD != "" {
			appLogger.Info("Falling back to LOCAL_DATABASE_PASSWORD (value hidden)")
			dbPassword = env.LOCAL_DATABASE_PASSWORD
		}
		if dbName == "" && env.LOCAL_DATABASE_NAME != "" {
			appLogger.Info("Falling back to LOCAL_DATABASE_NAME")
			dbName = env.LOCAL_DATABASE_NAME
		}

		if dbHost == "" || dbPort == "" || dbUser == "" || dbName == "" {
			appLogger.Fatal("Essential database connection variables are missing (DATABASE_URL, PG*, LOCAL_*)")
		}

		dsn = fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable TimeZone=UTC",
			dbHost, dbUser, dbPassword, dbName, dbPort,
		)
		appLogger.Info("Constructed Database DSN using individual variables (password hidden)")
	}

	appLogger.Info("Connecting to database...")
	// Renamed 'err' from logger to 'errDb' to avoid conflict if 'err' is reused.
	db, errDb := database.ConnectToDatabase(dsn)
	if errDb != nil {
		appLogger.Fatal("Database connection failed", zap.Error(errDb))
	}
	appLogger.Info("Database connection established successfully.")

	appLogger.Info("Running database migrations...")
	database.MigrateDatabase(dsn) // Assuming MigrateDatabase takes dsn, adjust if it takes *gorm.DB
	appLogger.Info("Database migrations completed.")

	log.Println("INFO: Initializing Telegram notifications...")
	if err := notifications.InitTelegramBot(); err != nil {
		log.Printf("WARN: Failed to initialize Telegram Bot, proceeding without Telegram features: %v", err)
	} else {
		log.Println("INFO: Telegram notifications initialized (if enabled and configured).")
	}

	appLogger.Info("Loading application configuration...")
	// Renamed 'err' to 'errCfg'
	cfg, errCfg := config.LoadConfig("agent/config.yaml")
	if errCfg != nil {
		appLogger.Fatal("Failed to load agent/config.yaml", zap.Error(errCfg))
	}
	config.SetGlobalConfig(cfg)
	appLogger.Info("Application configuration loaded.")

	appLogger.Info("Initializing Telegram Bot command listener...")
	if err := bot.InitializeBot(appLogger, db); err != nil { // 'err' is shadowed here, which is fine
		appLogger.Error("Failed to initialize Telegram Bot listener", zap.Error(err))
	} else {
		appLogger.Info("Telegram Bot command listener initialized.")
	}

	appLogger.Info("Setting up Helius webhooks for graduation...")
	graduationWebhookURL := env.WebhookURL
	if graduationWebhookURL != "" {
		appLogger.Info("Attempting to set up Graduation webhook subscription with Helius", zap.String("yourReceivingWebhookURL", graduationWebhookURL))
		// If SetupGraduationWebhook or CheckExistingHeliusWebhook were refactored to use HeliusService,
		// you would pass 'heliusSvc' to them here.
		// For now, assuming they use their own client logic or global state for Helius management API calls.
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
	// Pass heliusSvc to RegisterAPIRoutes
	handlers.RegisterAPIRoutes(router, appLogger, db, heliusSvc)
	appLogger.Info("Web server and API routes registered.")

	appLogger.Info("Starting background services...")
	// If CheckTokenProgress needs heliusSvc (e.g., if IsTokenValid started using it, which it doesn't for DexScreener data),
	// you'd need to adapt how it's called or how heliusSvc is accessed.
	// Currently, CheckTokenProgress only calls IsTokenValid (DexScreener part).
	go services.CheckTokenProgress(appLogger)
	appLogger.Info("Background services started.")

	go func() {
		serverAddr := ":" + env.Port
		appLogger.Info("Starting web server", zap.String("address", serverAddr))
		if err := router.Run(serverAddr); err != nil { // 'err' is shadowed here
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
	} else {
		appLogger.Warn("Telegram Bot listener not started because bot initialization failed or was skipped.")
	}

	appLogger.Info("Application startup complete. Waiting for events...")
	select {}
}
