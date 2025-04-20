package logger

import (
	"ca-scraper/shared/env"
	"ca-scraper/shared/notifications" // Ensure this is the correct import path
	"fmt"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger struct {
	ZapLogger      *zap.SugaredLogger
	atomicLevel    zap.AtomicLevel
	enableTelegram bool
}

// --- Existing Config, NewLogger, GetLogger, Zap, SetLevel should remain unchanged ---

type Config struct {
	Level          string
	Environment    string
	EnableTelegram bool
}

var globalLogger *Logger // Keep global instance if used

func NewLogger(cfg Config) (*Logger, error) {
	logLevel := zap.InfoLevel
	switch strings.ToLower(cfg.Level) {
	case "debug":
		logLevel = zap.DebugLevel
	case "info":
		logLevel = zap.InfoLevel
	case "warn", "warning":
		logLevel = zap.WarnLevel
	case "error":
		logLevel = zap.ErrorLevel
	case "fatal":
		logLevel = zap.FatalLevel
	default:
		fmt.Printf("WARN: Invalid log level '%s' specified, defaulting to INFO\n", cfg.Level)
	}

	atomicLevel := zap.NewAtomicLevelAt(logLevel)

	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	encoderConfig.TimeKey = "timestamp"
	encoderConfig.LevelKey = "severity"
	encoderConfig.MessageKey = "message"
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	consoleEncoder := zapcore.NewConsoleEncoder(encoderConfig)

	core := zapcore.NewCore(
		consoleEncoder,
		zapcore.Lock(os.Stdout),
		atomicLevel,
	)

	// AddCallerSkip(1) so caller shows function calling logger methods, not logger methods themselves
	zapLogger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
	sugaredLogger := zapLogger.Sugar()

	// Assign to global if your structure relies on it
	globalLogger = &Logger{
		ZapLogger:      sugaredLogger,
		atomicLevel:    atomicLevel,
		enableTelegram: cfg.EnableTelegram,
	}

	// Optional: Log initialization success
	globalLogger.ZapLogger.Infof("Logger initialized. Level: %s, Telegram Enabled: %t", logLevel.String(), cfg.EnableTelegram)
	if cfg.EnableTelegram && env.TelegramGroupID == 0 { // Assuming env is accessible or passed in
		globalLogger.ZapLogger.Warnf("Telegram logging enabled but TELEGRAM_GROUP_ID is not set.")
	}

	return globalLogger, nil // Return the instance
}

// GetLogger remains if needed for global access pattern
func GetLogger() *Logger {
	if globalLogger == nil {
		fmt.Println("FATAL: Global logger requested before initialization.")
		// Consider initializing a default logger here or returning an error/panicking
		os.Exit(1) // Or handle more gracefully
	}
	return globalLogger
}

func (l *Logger) Zap() *zap.SugaredLogger {
	return l.ZapLogger
}

// Formats key-values WITHOUT escaping them here
func formatKeyValuesForTelegram(keysAndValues ...interface{}) string {
	if len(keysAndValues)%2 != 0 {
		keysAndValues = append(keysAndValues, "INVALID_ARGS") // Handle odd number of args
	}
	if len(keysAndValues) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(" |") // Start with separator

	for i := 0; i < len(keysAndValues); i += 2 {
		keyStr := fmt.Sprintf("%v", keysAndValues[i])
		var valStr string
		// Handle errors gracefully if passed as values
		if err, ok := keysAndValues[i+1].(error); ok {
			valStr = err.Error()
		} else {
			valStr = fmt.Sprintf("%v", keysAndValues[i+1])
		}

		// Use raw key/value, keep backticks for value formatting
		// Escaping will happen in notifications.coreSendMessageWithRetry
		sb.WriteString(fmt.Sprintf(" %s=`%s`", keyStr, valStr))
	}
	return sb.String()
}

func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Debugw(msg, keysAndValues...)
	// No Telegram for Debug level
}

func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Infow(msg, keysAndValues...)
	// No Telegram for Info level by default, unless specifically needed
}

func (l *Logger) Warn(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Warnw(msg, keysAndValues...)
	if l.enableTelegram {
		// Format raw key-values
		formattedKeyValues := formatKeyValuesForTelegram(keysAndValues...)
		// Construct the raw message with intended Markdown
		rawFormattedMsg := fmt.Sprintf("🟡 *WARN:* %s%s", msg, formattedKeyValues)
		// Send raw message to notification handler
		notifications.SendTelegramMessage(rawFormattedMsg)
	}
}

func (l *Logger) Error(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Errorw(msg, keysAndValues...)
	if l.enableTelegram {
		// Format raw key-values
		formattedKeyValues := formatKeyValuesForTelegram(keysAndValues...)
		// Construct the raw message with intended Markdown
		rawFormattedMsg := fmt.Sprintf("🔴 *ERROR:* %s%s", msg, formattedKeyValues)
		// Send raw message to notification handler
		notifications.SendTelegramMessage(rawFormattedMsg)
	}
}

func (l *Logger) Fatal(msg string, keysAndValues ...interface{}) {
	// Log locally first
	l.ZapLogger.Errorw(msg, keysAndValues...) // Log as Error before fatal

	if l.enableTelegram {
		// Format raw key-values
		formattedKeyValues := formatKeyValuesForTelegram(keysAndValues...)
		// Construct the raw message with intended Markdown
		rawFormattedMsg := fmt.Sprintf("💀 *FATAL:* %s%s", msg, formattedKeyValues)
		// Send raw message to notification handler
		notifications.SendTelegramMessage(rawFormattedMsg)
		// Give Telegram a moment to send before exiting
		time.Sleep(1 * time.Second) // Consider making this configurable or removing if not needed
	}
	// Use Fatalw which logs and then calls os.Exit(1)
	l.ZapLogger.Fatalw(msg, keysAndValues...)
}

// LogToScanner - kept distinct if it has specific meaning, but sends via SendTelegramMessage
func (l *Logger) LogToScanner(level zapcore.Level, msg string, keysAndValues ...interface{}) {
	// Log locally using appropriate Zap level
	zapFunc := l.ZapLogger.Infow // Default to Info
	switch level {
	case zapcore.DebugLevel:
		zapFunc = l.ZapLogger.Debugw
	case zapcore.WarnLevel:
		zapFunc = l.ZapLogger.Warnw
	case zapcore.ErrorLevel:
		zapFunc = l.ZapLogger.Errorw
	case zapcore.FatalLevel:
		// Use Errorw for logging, then handle Fatal separately if needed
		zapFunc = l.ZapLogger.Errorw
	}
	zapFunc(msg, keysAndValues...)

	// Send to Telegram if enabled and level is Info or higher
	if l.enableTelegram && level >= zapcore.InfoLevel {
		// Format raw key-values
		formattedKeyValues := formatKeyValuesForTelegram(keysAndValues...)
		var prefix string
		// Use raw Markdown in prefixes
		switch level {
		case zapcore.InfoLevel:
			prefix = "ℹ️ [Scanner] INFO: "
		case zapcore.WarnLevel:
			prefix = "🟡 [Scanner] *WARN:* "
		case zapcore.ErrorLevel:
			prefix = "🔴 [Scanner] *ERROR:* "
		case zapcore.FatalLevel:
			prefix = "💀 [Scanner] *FATAL:* "
		default:
			prefix = "[Scanner] " // Should not happen if check is >= InfoLevel
		}

		// Construct the raw message with intended Markdown
		rawFormattedMsg := fmt.Sprintf("%s%s%s", prefix, msg, formattedKeyValues)
		// Send raw message via the standard notification handler
		notifications.SendTelegramMessage(rawFormattedMsg)

		// Handle Fatal level specifically after logging/notifying
		if level == zapcore.FatalLevel {
			time.Sleep(1 * time.Second)               // Optional delay
			l.ZapLogger.Fatalw(msg, keysAndValues...) // Trigger exit
		}
	}
}

func (l *Logger) SetLevel(level string) {
	logLevel := zap.InfoLevel // Default
	switch strings.ToLower(level) {
	case "debug":
		logLevel = zap.DebugLevel
	case "info":
		logLevel = zap.InfoLevel
	case "warn", "warning":
		logLevel = zap.WarnLevel
	case "error":
		logLevel = zap.ErrorLevel
	default:
		l.ZapLogger.Warnf("Invalid log level '%s' provided to SetLevel, level unchanged.", level)
		return
	}
	l.atomicLevel.SetLevel(logLevel)
	l.ZapLogger.Infof("Logger level changed to: %s", logLevel.String())
}
