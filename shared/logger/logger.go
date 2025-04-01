// File: shared/logger/logger.go
package logger

import (
	"fmt"
	"os"
	"strconv"

	// Assuming SendTelegramMessage is in the notifications package
	"ca-scraper/shared/notifications"

	"go.uber.org/zap"
)

// Logger struct manages logging with Zap and Telegram
type Logger struct {
	ZapLogger           *zap.SugaredLogger
	BotToken            string // Still needed if notifications package requires it implicitly or for other uses
	GroupID             int64
	SystemLogsID        int // Thread ID for general system logs
	ScannerLogsThreadID int // Thread ID for specific scanner logs (Raydium)
	EnableTelegram      bool
}

// NewLogger initializes the logger (assuming previous implementation is okay)
func NewLogger(environment string, enableTelegram bool) (*Logger, error) {
	var zapCoreLogger *zap.Logger
	var err error
	if environment == "production" {
		zapCoreLogger, err = zap.NewProduction()
	} else {
		zapCoreLogger, err = zap.NewDevelopment(zap.AddCallerSkip(1)) // Add CallerSkip for accurate line numbers in logs
	}
	if err != nil {
		return nil, fmt.Errorf("failed to initialize zap logger: %w", err)
	}
	zapSugaredLogger := zapCoreLogger.Sugar()

	var botToken string
	var groupID int64
	var systemLogsID, scannerLogsThreadID int = 0, 0

	if enableTelegram {
		botToken = os.Getenv("TELEGRAM_BOT_TOKEN")
		groupIDStr := os.Getenv("TELEGRAM_GROUP_ID")
		systemLogsIDStr := os.Getenv("SYSTEM_LOGS_THREAD_ID")
		scannerLogsThreadIDStr := os.Getenv("SCANNER_LOGS_THREAD_ID")

		if botToken == "" {
			zapSugaredLogger.Warn("TELEGRAM_BOT_TOKEN missing, disabling Telegram logging.")
			enableTelegram = false
		} else if groupIDStr == "" {
			zapSugaredLogger.Warn("TELEGRAM_GROUP_ID missing, disabling Telegram logging.")
			enableTelegram = false
		} else {
			groupID, err = strconv.ParseInt(groupIDStr, 10, 64)
			if err != nil {
				zapSugaredLogger.Errorf("Failed to parse TELEGRAM_GROUP_ID '%s', disabling Telegram logging: %v", groupIDStr, err)
				enableTelegram = false
			}

			if systemLogsIDStr != "" {
				systemLogsID, err = strconv.Atoi(systemLogsIDStr)
				if err != nil {
					zapSugaredLogger.Warnf("Failed to parse SYSTEM_LOGS_THREAD_ID '%s', using default (0): %v", systemLogsIDStr, err)
					systemLogsID = 0
				}
			} else {
				zapSugaredLogger.Info("SYSTEM_LOGS_THREAD_ID not set, using default (0 - main group).")
			}

			if scannerLogsThreadIDStr != "" {
				scannerLogsThreadID, err = strconv.Atoi(scannerLogsThreadIDStr)
				if err != nil {
					zapSugaredLogger.Warnf("Failed to parse SCANNER_LOGS_THREAD_ID '%s', using default (0): %v", scannerLogsThreadIDStr, err)
					scannerLogsThreadID = 0
				}
			} else {
				zapSugaredLogger.Info("SCANNER_LOGS_THREAD_ID not set, using default (0 - main group).")
			}
		}
	}

	return &Logger{
		ZapLogger:           zapSugaredLogger,
		BotToken:            botToken, // Keep storing it
		GroupID:             groupID,
		SystemLogsID:        systemLogsID,
		ScannerLogsThreadID: scannerLogsThreadID,
		EnableTelegram:      enableTelegram,
	}, nil
}

// logToTelegramInternal sends a log message string to Telegram.
// NOTE: This assumes notifications.SendTelegramMessage handles target chat/thread implicitly
//
//	based on how notifications.InitTelegramBot() was configured, OR that it only sends
//	to one predefined place. It also assumes it handles errors internally.
func (l *Logger) logToTelegramInternal(message string /* removed threadID */) {
	if !l.EnableTelegram {
		return
	}
	// Directly call the function with just the message string
	// We assume this function knows the bot token and chat ID from initialization
	notifications.SendTelegramMessage(message)
	// Since it doesn't return an error, we can't check for success here.
	// The notifications package itself should log errors if sending fails.
}

// LogToSystemLogs sends logs specifically to the System Logs destination (via internal func)
func (l *Logger) LogToSystemLogs(message string) {
	// If you need separate threads and SendTelegramMessage doesn't support it,
	// you might need a different approach or modify the notifications package.
	// For now, we send the same way regardless of logical destination.
	l.logToTelegramInternal(message)
}

// LogToRaydiumTracker sends logs specifically to the Scanner Logs destination (via internal func)
func (l *Logger) LogToRaydiumTracker(message string) {
	// Sending the same way as system logs for now.
	l.logToTelegramInternal(message)
}

// --- Corrected Logging Methods ---

func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Infow(msg, keysAndValues...)
	// Decide if you want Info logs in Telegram
	// l.LogToSystemLogs(msg) // Example: Send only base message string
}

func (l *Logger) Warn(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Warnw(msg, keysAndValues...)
	// Format a simple string for Telegram
	formattedMsg := fmt.Sprintf("⚠️ WARN: %s", msg)
	// Add fields to the string if needed for context
	// (This part can get complex, keep it simple for now)
	// for i := 0; i < len(keysAndValues); i+=2 { ... }
	l.LogToSystemLogs(formattedMsg)
}

func (l *Logger) Error(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Errorw(msg, keysAndValues...)
	// Format a simple string for Telegram, attempting to include the error text
	errMsgText := ""
	for i := 0; i < len(keysAndValues); i += 2 {
		// Check if the value is an error type
		if err, ok := keysAndValues[i+1].(error); ok {
			errMsgText = err.Error() // Get the error string
			break                    // Take the first error found
		}
	}
	formattedMsg := fmt.Sprintf("❗️ ERROR: %s", msg)
	if errMsgText != "" {
		formattedMsg = fmt.Sprintf("%s | Details: %s", formattedMsg, errMsgText)
	}
	l.LogToSystemLogs(formattedMsg)
}

func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Debugw(msg, keysAndValues...)
	// Debug logs usually don't go to Telegram
}

func (l *Logger) Fatal(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Fatalw(msg, keysAndValues...) // Use Fatalw for structured key-value pairs

	errMsgText := ""
	for i := 0; i < len(keysAndValues); i += 2 {
		if err, ok := keysAndValues[i+1].(error); ok {
			errMsgText = err.Error()
			break
		}
	}
	formattedMsg := fmt.Sprintf("☠️ FATAL: %s", msg)
	if errMsgText != "" {
		formattedMsg = fmt.Sprintf("%s | Details: %s", formattedMsg, errMsgText)
	}
	// Attempt to send to Telegram before Zap exits (best effort)
	l.logToTelegramInternal(formattedMsg)
	// Zap's Fatalw calls os.Exit(1) after logging internally
}

// ExposeZapLogger allows direct access to the underlying zap.SugaredLogger if needed.
func (l *Logger) ExposeZapLogger() *zap.SugaredLogger {
	return l.ZapLogger
}
