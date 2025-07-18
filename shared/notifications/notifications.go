package notifications

import (
	"ca-scraper/shared/env"
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"
	"github.com/mymmrac/telego/telegoapi"
	"golang.org/x/time/rate"
)

var (
	bot             *telego.Bot
	telegramLimiter *rate.Limiter
	initMutex       sync.Mutex
	isInitialized   bool
	defaultGroupID  int64
)

const (
	telegramMessagesPerSecond = 15.0
	telegramBurstLimit        = 20
	maxRetries                = 3
	baseRetryWait             = 1 * time.Second
	maxRetryWait              = 60 * time.Second
)

func InitTelegramBot() error {
	initMutex.Lock()
	defer initMutex.Unlock()

	if isInitialized {
		log.Println("INFO: Telegram bot (Telego) already initialized.")
		return nil
	}
	botToken := env.TelegramBotToken
	parsedGroupID := env.TelegramGroupID

	if botToken == "" {
		log.Println("WARN: TELEGRAM_BOT_TOKEN missing. Telegram notifications disabled.")
		isInitialized = false
		bot = nil
		return nil
	}
	if parsedGroupID == 0 {
		log.Println("warn: TELEGRAM_GROUP_ID missing or invalid (0). Telegram notifications to primary group disabled...")
	}
	defaultGroupID = parsedGroupID

	log.Println("INFO: Initializing Telegram bot (Telego)...")
	var err error
	bot, err = telego.NewBot(botToken, telego.WithDefaultDebugLogger())
	if err != nil {
		log.Printf("ERROR: Failed to initialize Telego bot: %v\n", err)
		bot = nil
		isInitialized = false
		return fmt.Errorf("failed to initialize Telego bot: %w", err)
	}

	log.Println("INFO: Verifying bot token with Telegram API (GetMe via Telego)...")
	botUser, err := bot.GetMe(context.Background())
	if err != nil {
		log.Printf("ERROR: Failed to verify bot token with GetMe (Telego): %v\n", err)
		bot = nil
		isInitialized = false
		return fmt.Errorf("failed to verify bot token with GetMe (Telego): %w", err)
	}

	telegramLimiter = rate.NewLimiter(rate.Limit(telegramMessagesPerSecond), telegramBurstLimit)
	isInitialized = true
	log.Printf("INFO: Telegram bot (Telego) initialized successfully for @%s", botUser.Username)
	if defaultGroupID != 0 {
		log.Printf("INFO: Primary Target Telegram Group ID: %d", defaultGroupID)
	}
	log.Printf("INFO: Rate limiter initialized (Limit: %.2f/s, Burst: %d)", telegramMessagesPerSecond, telegramBurstLimit)

	if env.BotCallsThreadID != 0 {
		log.Printf("INFO: Primary Bot Calls Thread ID: %d", env.BotCallsThreadID)
	}
	return nil
}

func GetBotInstance() *telego.Bot {
	initMutex.Lock()
	defer initMutex.Unlock()
	return bot
}

func EscapeMarkdownV2(s string) string {
	charsToEscape := []string{"_", "*", "[", "]", "(", ")", "~", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	var builder strings.Builder
	builder.Grow(len(s) + 20)
	for _, r := range s {
		charStr := string(r)
		if charStr == "`" {
			builder.WriteRune(r)
			continue
		}
		needsEscaping := false
		for _, esc := range charsToEscape {
			if charStr == esc {
				needsEscaping = true
				break
			}
		}
		if needsEscaping {
			builder.WriteRune('\\')
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func coreSendMessageWithRetry(chatID int64, messageThreadID int, rawTextOrCaption string, isPhoto bool, photoURL string, replyMarkup telego.ReplyMarkup) error {
	localBot := GetBotInstance()
	if localBot == nil {
		log.Printf("WARN: coreSendMessageWithRetry: Bot not initialized (ChatID: %d).", chatID)
		return errors.New("telego bot not initialized")
	}
	escapedTextOrCaption := EscapeMarkdownV2(rawTextOrCaption)
	var lastErr error
	logCtx := fmt.Sprintf("[ChatID: %d]", chatID)
	if messageThreadID != 0 {
		logCtx = fmt.Sprintf("%s[ThreadID: %d]", logCtx, messageThreadID)
	} else {
		logCtx = fmt.Sprintf("%s[ThreadID: 0 (Main)]", logCtx)
	}
	contentType := "[Text]"
	if isPhoto {
		contentType = "[Photo]"
	}
	logCtx = contentType + logCtx

	for attempt := 0; attempt < maxRetries; attempt++ {
		if telegramLimiter != nil {
			ctxWait, cancelWait := context.WithTimeout(context.Background(), 30*time.Second)
			waitErr := telegramLimiter.Wait(ctxWait)
			cancelWait()
			if waitErr != nil {
				log.Printf("WARN: Telegram rate limiter wait error %s: %v. Proceeding cautiously...", logCtx, waitErr)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		var currentErr error
		var sentMsg *telego.Message
		logPayload := escapedTextOrCaption
		if len(logPayload) > 1000 {
			logPayload = logPayload[:1000] + "..."
		}
		log.Printf("DEBUG: Attempting Send API Call %s (Attempt %d/%d). Payload snippet: %s", logCtx, attempt+1, maxRetries, logPayload)

		if isPhoto {
			params := &telego.SendPhotoParams{ChatID: telego.ChatID{ID: chatID}, Photo: telego.InputFile{URL: photoURL}, Caption: escapedTextOrCaption, ParseMode: telego.ModeMarkdownV2, MessageThreadID: messageThreadID, ReplyMarkup: replyMarkup}
			sentMsg, currentErr = localBot.SendPhoto(ctx, params)
		} else {
			params := &telego.SendMessageParams{ChatID: telego.ChatID{ID: chatID}, Text: escapedTextOrCaption, ParseMode: telego.ModeMarkdownV2, MessageThreadID: messageThreadID, ReplyMarkup: replyMarkup}
			sentMsg, currentErr = localBot.SendMessage(ctx, params)
		}
		cancel()

		if currentErr != nil {
			log.Printf("ERROR: Telegram API call failed %s (Attempt %d/%d). Error: %v. Request Data: (photo=%t, photoURL=%s)", logCtx, attempt+1, maxRetries, currentErr, isPhoto, photoURL)
		} else if sentMsg == nil || sentMsg.MessageID == 0 {
			log.Printf("WARN: Telegram API call seemingly succeeded (no error) but no valid message returned %s (Attempt %d/%d). Msg: %+v", logCtx, attempt+1, maxRetries, sentMsg)
			if currentErr == nil {
				currentErr = errors.New("no message returned from Telegram API after successful-looking call")
			}
		}
		if currentErr == nil && sentMsg != nil && sentMsg.MessageID != 0 {
			log.Printf("INFO: Telegram message sent successfully %s (MsgID: %d)", logCtx, sentMsg.MessageID)
			return nil
		}
		lastErr = currentErr
		waitDuration := baseRetryWait * time.Duration(math.Pow(2, float64(attempt)))
		if waitDuration > maxRetryWait {
			waitDuration = maxRetryWait
		}
		log.Printf("INFO: Retrying failed send in %v... %s", waitDuration, logCtx)
		time.Sleep(waitDuration)
	}
	if lastErr != nil {
		log.Printf("ERROR: Telegram message FAILED after all attempts/fallbacks for this call. Final Error: %v. %s", lastErr, logCtx)
	}
	return lastErr
}

func SendTelegramMessage(message string) {
	if defaultGroupID == 0 {
		log.Println("WARN: SendTelegramMessage: defaultGroupID is 0, cannot send message.")
		return
	}
	_ = coreSendMessageWithRetry(defaultGroupID, 0, message, false, "", nil)
}
