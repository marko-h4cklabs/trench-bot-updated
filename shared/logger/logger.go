package logger

import (
	"ca-scraper/shared/notifications"
	"fmt"
	"log"
	"strings"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Config struct {
	Environment         string
	EnableTelegram      bool
	SystemLogsThreadID  int
	ScannerLogsThreadID int
}

type Logger struct {
	ZapLogger           *zap.SugaredLogger
	enableTelegram      bool
	systemLogsThreadID  int
	scannerLogsThreadID int
}

func NewLogger(cfg Config) (*Logger, error) {
	var zapCoreLogger *zap.Logger
	var err error
	var zapCfg zap.Config

	if cfg.Environment == "production" {
		zapCfg = zap.NewProductionConfig()
		zapCfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		zapCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
		zapCfg.Development = false
		zapCfg.DisableStacktrace = false
	} else {
		zapCfg = zap.NewDevelopmentConfig()
		zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
		zapCfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
		zapCfg.Development = true
		zapCfg.DisableStacktrace = true
	}
	zapCfg.DisableCaller = false

	zapCoreLogger, err = zapCfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		log.Printf("FATAL: Failed to initialize zap logger: %v\n", err)
		return nil, fmt.Errorf("failed to initialize zap logger: %w", err)
	}
	zapSugaredLogger := zapCoreLogger.Sugar()

	if cfg.EnableTelegram {
		zapSugaredLogger.Infof("Logger initialized. Telegram logging integration ENABLED (System Thread: %d, Scanner Thread: %d)",
			cfg.SystemLogsThreadID, cfg.ScannerLogsThreadID)
	} else {
		zapSugaredLogger.Info("Logger initialized. Telegram logging integration DISABLED.")
	}

	return &Logger{
		ZapLogger:           zapSugaredLogger,
		enableTelegram:      cfg.EnableTelegram,
		systemLogsThreadID:  cfg.SystemLogsThreadID,
		scannerLogsThreadID: cfg.ScannerLogsThreadID,
	}, nil
}

func formatAndEscapeKeyValues(keysAndValues ...interface{}) string {
	if len(keysAndValues)%2 != 0 {
		keysAndValues = append(keysAndValues, "INVALID_ARGS")
	}
	if len(keysAndValues) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString(" |")

	for i := 0; i < len(keysAndValues); i += 2 {
		keyStr := fmt.Sprintf("%v", keysAndValues[i])
		var valStr string
		if err, ok := keysAndValues[i+1].(error); ok {
			valStr = err.Error()
		} else {
			valStr = fmt.Sprintf("%v", keysAndValues[i+1])
		}

		escapedKey := notifications.EscapeMarkdownV2(keyStr)
		escapedValue := notifications.EscapeMarkdownV2(valStr)
		sb.WriteString(fmt.Sprintf(" %s=`%s`", escapedKey, escapedValue))
	}
	return sb.String()
}

func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Infow(msg, keysAndValues...)
}

func (l *Logger) Warn(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Warnw(msg, keysAndValues...)
	if l.enableTelegram && l.systemLogsThreadID != 0 {
		escapedMsg := notifications.EscapeMarkdownV2(msg)
		formattedKeyValues := formatAndEscapeKeyValues(keysAndValues...)
		formattedMsg := fmt.Sprintf("🟡 *WARN:* %s%s", escapedMsg, formattedKeyValues)
		notifications.SendSystemLogMessage(formattedMsg)
	}
}

func (l *Logger) Error(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Errorw(msg, keysAndValues...)
	if l.enableTelegram && l.systemLogsThreadID != 0 {
		escapedMsg := notifications.EscapeMarkdownV2(msg)
		formattedKeyValues := formatAndEscapeKeyValues(keysAndValues...)
		formattedMsg := fmt.Sprintf("🔴 *ERROR:* %s%s", escapedMsg, formattedKeyValues)
		notifications.SendSystemLogMessage(formattedMsg)
	}
}

func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Debugw(msg, keysAndValues...)
}

func (l *Logger) Fatal(msg string, keysAndValues ...interface{}) {
	if l.enableTelegram && l.systemLogsThreadID != 0 {
		escapedMsg := notifications.EscapeMarkdownV2(msg)
		formattedKeyValues := formatAndEscapeKeyValues(keysAndValues...)
		formattedMsg := fmt.Sprintf("💀 *FATAL:* %s%s", escapedMsg, formattedKeyValues)
		notifications.SendSystemLogMessage(formattedMsg)
		time.Sleep(1 * time.Second)
	}
	l.ZapLogger.Fatalw(msg, keysAndValues...)
}

func (l *Logger) LogToScanner(level zapcore.Level, msg string, keysAndValues ...interface{}) {
	zapFunc := l.ZapLogger.Infow
	switch level {
	case zapcore.DebugLevel:
		zapFunc = l.ZapLogger.Debugw
	case zapcore.WarnLevel:
		zapFunc = l.ZapLogger.Warnw
	case zapcore.ErrorLevel:
		zapFunc = l.ZapLogger.Errorw
	case zapcore.FatalLevel:
		zapFunc = l.ZapLogger.Fatalw
	}

	zapFunc(msg, keysAndValues...)

	if l.enableTelegram && l.scannerLogsThreadID != 0 && level >= zapcore.InfoLevel {
		escapedMsg := notifications.EscapeMarkdownV2(msg)
		formattedKeyValues := formatAndEscapeKeyValues(keysAndValues...)
		var prefix string

		switch level {
		case zapcore.InfoLevel:
			prefix = "ℹ️ [Scanner] INFO: "
		case zapcore.WarnLevel:
			prefix = "🟡 [Scanner] *WARN:* "
		case zapcore.ErrorLevel:
			prefix = "🔴 [Scanner] *ERROR:* "
		case zapcore.FatalLevel:
			prefix = "💀 [Scanner] *FATAL:* "
			formattedMsg := fmt.Sprintf("%s%s%s", prefix, escapedMsg, formattedKeyValues)
			notifications.SendScannerLogMessage(formattedMsg)
			time.Sleep(1 * time.Second)
			return
		default:
			prefix = "[Scanner] "
		}

		formattedMsg := fmt.Sprintf("%s%s%s", prefix, escapedMsg, formattedKeyValues)
		notifications.SendScannerLogMessage(formattedMsg)
	}
}

func (l *Logger) Zap() *zap.SugaredLogger {
	return l.ZapLogger
}
