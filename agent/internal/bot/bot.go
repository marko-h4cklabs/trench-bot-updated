package bot

import (
	"ca-scraper/shared/logger"
	"ca-scraper/shared/notifications" // Now provides *telego.Bot
	"context"
	"fmt"
	"log"
	"os" // Import os for signal handling
	"os/signal"
	"strings"
	"syscall" // Import syscall for signal handling
	"time"    // Import time for potential delays

	"github.com/mymmrac/telego" // Use telego
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var appLogger *logger.Logger
var botInstance *telego.Bot // Correct type
var dbInstance *gorm.DB

// InitializeBot uses *telego.Bot (Unchanged)
// InitializeBot uses *telego.Bot
func InitializeBot(logInstance *logger.Logger, db *gorm.DB) error {
	if logInstance == nil {
		log.Println("FATAL ERROR: InitializeBot requires a non-nil logger instance")
		return fmt.Errorf("logger instance provided to InitializeBot is nil")
	}
	appLogger = logInstance

	if db == nil {
		appLogger.Warn("No database instance passed to InitializeBot. Continuing without DB features.")
	} else {
		dbInstance = db
	}

	botInstance = notifications.GetBotInstance()
	if botInstance == nil {
		appLogger.Error("Could not retrieve initialized Telegram bot (*telego.Bot) instance from notifications package. Bot commands may not function.")
	}

	appLogger.Info("Telegram bot interaction services initialized (using Telego).")
	return nil
}


// Modified StartListening for Telego Long Polling and context cancellation
func StartListening(ctx context.Context) { // Main context for overall shutdown signal
	if appLogger == nil {
		log.Println("ERROR: Logger not initialized for bot listener. Cannot start.")
		return
	}
	if botInstance == nil {
		appLogger.Warn("Telego Bot instance not available. Cannot start command listener.")
		return
	}
	if dbInstance == nil {
		appLogger.Error("Database instance not available. Commands requiring database access will fail.")
	}

	appLogger.Info("Starting bot message/command listener (using Telego Long Polling)...")

	// --- Context for controlling the Long Polling loop ---
	// We derive a cancellable context from the main context.
	// This allows us to stop polling specifically without cancelling the entire application context.
	pollingCtx, stopPolling := context.WithCancel(ctx)
	defer stopPolling() // Ensure stopPolling is called when StartListening exits

	// Optional: Parameters for long polling
	pollParams := &telego.GetUpdatesParams{
		Timeout: 60,
		Limit:   100,
	}

	// --- CORRECTED: Call UpdatesViaLongPolling with context ---
	// Pass the cancellable context 'pollingCtx'
	updatesChan, _ := botInstance.UpdatesViaLongPolling(pollingCtx, pollParams)
	appLogger.Info("Listening for Telegram commands and messages via Telego...")

	// --- Graceful Shutdown Handling ---
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)

	// Goroutine to process updates
	go func() {
		defer appLogger.Info("Update processing goroutine finished.")
		for {
			select {
			case update, ok := <-updatesChan:
				if !ok {
					appLogger.Info("Telego updates channel closed, exiting processor.")
					return // Exit goroutine if channel is closed
				}

				// --- Process Update ---
				if update.Message == nil {
					continue
				}
				if !strings.HasPrefix(update.Message.Text, "/") {
					continue
				}

				parts := strings.Fields(update.Message.Text)
				if len(parts) == 0 {
					continue
				}
				command := ""
				args := ""
				commandPart := parts[0]
				if strings.HasPrefix(commandPart, "/") {
					commandWithMaybeUsername := strings.TrimPrefix(commandPart, "/")
					commandParts := strings.Split(commandWithMaybeUsername, "@")
					command = commandParts[0]
					if len(parts) > 1 {
						args = strings.Join(parts[1:], " ")
					}
				} else {
					continue
				}

				// Launch command handling
				go HandleCommand(update, command, args)
				// --------------------

			// Check if the specific polling context was cancelled (e.g., by OS signal handler below)
			case <-pollingCtx.Done():
				appLogger.Info("Polling context cancelled. Stopping Telego update processing goroutine.")
				return // Exit goroutine
			}
		}
	}() // End update processing goroutine

	// Wait for OS signal OR main context cancellation
	select {
	case sig := <-signals:
		appLogger.Info("Received OS signal, shutting down listener...", zap.String("signal", sig.String()))
		// --- CORRECTED: Stop polling by cancelling the context ---
		stopPolling()
		// ------------------------------------------------------
		// Wait a moment allows the goroutine and library internals to potentially clean up
		time.Sleep(1 * time.Second)
	case <-ctx.Done():
		appLogger.Info("Main context cancelled externally. Stopping listener...")
		// --- CORRECTED: Stop polling by cancelling the context ---
		stopPolling()
		// ------------------------------------------------------
		time.Sleep(1 * time.Second)
	}

	appLogger.Info("Telego listener shutdown requested.")
}
