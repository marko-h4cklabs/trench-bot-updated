package logger

import (
	"ca-scraper/shared/env" // Keep env import if needed by NewLogger check
	// "ca-scraper/shared/notifications" // Can likely remove if formatKeyValues is removed/unused
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Logger struct {
	ZapLogger      *zap.SugaredLogger
	atomicLevel    zap.AtomicLevel
	enableTelegram bool // Keep flag, might be useful for other checks later
}

type Config struct {
	Level          string
	Environment    string
	EnableTelegram bool
}

var globalLogger *Logger

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

	zapLogger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
	sugaredLogger := zapLogger.Sugar()

	globalLogger = &Logger{
		ZapLogger:      sugaredLogger,
		atomicLevel:    atomicLevel,
		enableTelegram: cfg.EnableTelegram,
	}

	globalLogger.ZapLogger.Infof("Logger initialized. Level: %s, Telegram Explicit Notifications Enabled: %t", logLevel.String(), cfg.EnableTelegram)
	if cfg.EnableTelegram && env.TelegramGroupID == 0 {
		globalLogger.ZapLogger.Warnf("Telegram notifications enabled but TELEGRAM_GROUP_ID is not set.")
	}

	return globalLogger, nil
}

func GetLogger() *Logger {
	if globalLogger == nil {
		fmt.Println("FATAL: Global logger requested before initialization.")
		os.Exit(1)
	}
	return globalLogger
}

func (l *Logger) Zap() *zap.SugaredLogger {
	return l.ZapLogger
}

func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Debugw(msg, keysAndValues...)
}

func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Infow(msg, keysAndValues...)
}

func (l *Logger) Warn(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Warnw(msg, keysAndValues...)
	// REMOVED TELEGRAM SENDING BLOCK
}

func (l *Logger) Error(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Errorw(msg, keysAndValues...)
	// REMOVED TELEGRAM SENDING BLOCK
}

func (l *Logger) Fatal(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Errorw("FATAL ERROR", append(keysAndValues, "fatal_message", msg)...) // Log as error first
	// REMOVED TELEGRAM SENDING BLOCK & DELAY
	l.ZapLogger.Fatalw(msg, keysAndValues...) // This logs and calls os.Exit(1)
}

func (l *Logger) LogToScanner(level zapcore.Level, msg string, keysAndValues ...interface{}) {
	zapFunc := l.ZapLogger.Infow // Default to Info
	switch level {
	case zapcore.DebugLevel:
		zapFunc = l.ZapLogger.Debugw
	case zapcore.WarnLevel:
		zapFunc = l.ZapLogger.Warnw
	case zapcore.ErrorLevel:
		zapFunc = l.ZapLogger.Errorw
	case zapcore.FatalLevel:
		// Log as error, then handle fatal exit if needed
		l.ZapLogger.Errorw("FATAL SCANNER EVENT", append(keysAndValues, "fatal_message", msg)...)
		l.ZapLogger.Fatalw(msg, keysAndValues...) // Trigger exit after local log
		return                                    // Exit happens in Fatalw
	}
	// Log locally only
	zapFunc(msg, keysAndValues...)
	// REMOVED TELEGRAM SENDING BLOCK
}

func (l *Logger) SetLevel(level string) {
	logLevel := zap.InfoLevel
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
