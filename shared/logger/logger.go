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
	} else {
		zapCfg = zap.NewDevelopmentConfig()
		zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}
	zapCfg.DisableCaller = false

	if cfg.Environment == "production" {
		zapCfg.Development = false
		zapCfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
		zapCfg.DisableStacktrace = false
	} else {
		zapCfg.Development = true
		zapCfg.Level = zap.NewAtomicLevelAt(zap.DebugLevel)
		zapCfg.DisableStacktrace = true
	}

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

func formatKeyValuesSimple(keysAndValues ...interface{}) string {
	if len(keysAndValues) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(" |")
	for i := 0; i < len(keysAndValues); i += 2 {
		keyStr := fmt.Sprintf("%v", keysAndValues[i])
		valStr := "MISSING_VALUE"
		if i+1 < len(keysAndValues) {
			if err, ok := keysAndValues[i+1].(error); ok {
				valStr = err.Error()
			} else {
				valStr = fmt.Sprintf("%v", keysAndValues[i+1])
			}
		}
		sb.WriteString(fmt.Sprintf(" %s=`%s`", keyStr, notifications.EscapeMarkdownV2(valStr)))
	}
	return sb.String()
}

func (l *Logger) Info(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Infow(msg, keysAndValues...)
}

func (l *Logger) Warn(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Warnw(msg, keysAndValues...)
	if l.enableTelegram {
		formattedMsg := fmt.Sprintf(" *WARN:* %s%s", notifications.EscapeMarkdownV2(msg), formatKeyValuesSimple(keysAndValues...))
		notifications.SendSystemLogMessage(formattedMsg)
	}
}

func (l *Logger) Error(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Errorw(msg, keysAndValues...)
	if l.enableTelegram {
		formattedMsg := fmt.Sprintf("‼ *ERROR:* %s%s", notifications.EscapeMarkdownV2(msg), formatKeyValuesSimple(keysAndValues...))
		notifications.SendSystemLogMessage(formattedMsg)
	}
}

func (l *Logger) Debug(msg string, keysAndValues ...interface{}) {
	l.ZapLogger.Debugw(msg, keysAndValues...)
}

func (l *Logger) Fatal(msg string, keysAndValues ...interface{}) {
	if l.enableTelegram {
		formattedMsg := fmt.Sprintf(" *FATAL:* %s%s", notifications.EscapeMarkdownV2(msg), formatKeyValuesSimple(keysAndValues...))
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
		prefix := "[Scanner] "
		switch level {
		case zapcore.InfoLevel:
			prefix += "ℹ INFO: "
		case zapcore.WarnLevel:
			prefix += " *WARN:* "
		case zapcore.ErrorLevel:
			prefix += "‼ *ERROR:* "
		case zapcore.FatalLevel:
			prefix += " *FATAL:* "
			formattedMsg := fmt.Sprintf("%s%s%s", prefix, notifications.EscapeMarkdownV2(msg), formatKeyValuesSimple(keysAndValues...))
			notifications.SendScannerLogMessage(formattedMsg)
			time.Sleep(1 * time.Second)
			return
		}

		formattedMsg := fmt.Sprintf("%s%s%s", prefix, notifications.EscapeMarkdownV2(msg), formatKeyValuesSimple(keysAndValues...))
		notifications.SendScannerLogMessage(formattedMsg)
	}
}

func (l *Logger) Zap() *zap.SugaredLogger {
	return l.ZapLogger
}
