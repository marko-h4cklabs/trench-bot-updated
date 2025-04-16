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

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/time/rate"
)

var (
	bot             *tgbotapi.BotAPI
	telegramLimiter *rate.Limiter
	initMutex       sync.Mutex
	isInitialized   bool
	defaultGroupID  int64
)

const (
	telegramMessagesPerSecond = 0.8
	telegramBurstLimit        = 2
	maxRetries                = 3
	baseRetryWait             = 1 * time.Second
)

func InitTelegramBot() error {
	initMutex.Lock()
	defer initMutex.Unlock()

	if isInitialized {
		log.Println("INFO: Telegram bot (go-telegram-bot-api) already initialized.")
		return nil
	}

	botToken := env.TelegramBotToken
	parsedGroupID := env.TelegramGroupID

	if botToken == "" {
		log.Println("WARN: TELEGRAM_BOT_TOKEN missing. Telegram notifications disabled.")
		isInitialized = false
		bot = nil
		telegramLimiter = nil
		return nil
	}
	if parsedGroupID == 0 {
		log.Println("WARN: TELEGRAM_GROUP_ID missing or invalid. Telegram notifications disabled.")
		isInitialized = false
		bot = nil
		telegramLimiter = nil
		return nil
	}

	defaultGroupID = parsedGroupID

	log.Println("INFO: Initializing Telegram bot API (go-telegram-bot-api/v5)...")
	var err error
	bot, err = tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Printf("ERROR: Failed to initialize Telegram bot API: %v\n", err)
		bot = nil
		isInitialized = false
		return fmt.Errorf("failed to initialize Telegram bot API: %w", err)
	}

	log.Println("INFO: Verifying bot token with Telegram API (GetMe)...")
	userInfo, err := bot.GetMe()
	if err != nil {
		log.Printf("ERROR: Failed to verify bot token with GetMe API call: %v\n", err)
		bot = nil
		isInitialized = false
		return fmt.Errorf("failed to verify bot token with GetMe API call: %w", err)
	}

	telegramLimiter = rate.NewLimiter(rate.Limit(telegramMessagesPerSecond), telegramBurstLimit)
	isInitialized = true
	log.Printf("INFO: Telegram bot (go-telegram-bot-api) initialized successfully for @%s", userInfo.UserName)
	log.Printf("INFO: Target Telegram Group ID: %d", defaultGroupID)
	log.Printf("INFO: Telegram rate limiter initialized (Limit: %.2f/s, Burst: %d)", telegramMessagesPerSecond, telegramBurstLimit)

	log.Printf("INFO: System Logs Thread ID: %d", env.SystemLogsThreadID)
	log.Printf("INFO: Scanner Logs Thread ID: %d", env.ScannerLogsThreadID)
	log.Printf("INFO: Potential CA Thread ID: %d", env.PotentialCAThreadID)
	log.Printf("INFO: Bot Calls Thread ID: %d", env.BotCallsThreadID)
	log.Printf("INFO: Tracking Thread ID: %d", env.TrackingThreadID)

	return nil
}

func GetBotInstance() *tgbotapi.BotAPI {
	initMutex.Lock()
	defer initMutex.Unlock()
	if !isInitialized || bot == nil {
		log.Println("WARN: GetBotInstance called but bot is not properly initialized.")
		return nil
	}
	return bot
}

func SendTelegramMessage(message string) {
	coreSendMessageWithRetry(defaultGroupID, env.SystemLogsThreadID, message, false, "")
}

func SendSystemLogMessage(message string) {
	coreSendMessageWithRetry(defaultGroupID, env.SystemLogsThreadID, message, false, "")
}

func SendScannerLogMessage(message string) {
	coreSendMessageWithRetry(defaultGroupID, env.ScannerLogsThreadID, message, false, "")
}

func SendPotentialCAMessage(message string) {
	coreSendMessageWithRetry(defaultGroupID, env.PotentialCAThreadID, message, false, "")
}

func SendBotCallMessage(message string) {
	if env.BotCallsThreadID == 0 {
		log.Println("WARN: Attempted to send to Bot Calls topic, but BOT_CALLS_THREAD_ID is not set.")
		return
	}
	coreSendMessageWithRetry(defaultGroupID, env.BotCallsThreadID, message, false, "")
}

func SendBotCallPhotoMessage(photoURL string, caption string) {
	if env.BotCallsThreadID == 0 {
		log.Println("WARN: Attempted to send photo to Bot Calls topic, but BOT_CALLS_THREAD_ID is not set.")
		return
	}
	if _, err := url.ParseRequestURI(photoURL); err != nil {
		log.Printf("ERROR: Invalid photo URL provided for Bot Call: %s - %v. Falling back to sending caption as text.", photoURL, err)
		coreSendMessageWithRetry(defaultGroupID, env.BotCallsThreadID, caption, false, "")
		return
	}
	coreSendMessageWithRetry(defaultGroupID, env.BotCallsThreadID, caption, true, photoURL)
}

func SendTrackingUpdateMessage(message string) {
	if env.TrackingThreadID == 0 {
		log.Println("WARN: Attempted to send to Tracking topic, but TRACKING_THREAD_ID is not set.")
		return
	}
	coreSendMessageWithRetry(defaultGroupID, env.TrackingThreadID, message, false, "")
}

func coreSendMessageWithRetry(chatID int64, messageThreadID int, textOrCaption string, isPhoto bool, photoURL string) {
	initMutex.Lock()
	localIsInitialized := isInitialized
	localBot := bot
	initMutex.Unlock()

	if !localIsInitialized || localBot == nil {
		if env.TelegramBotToken != "" && env.TelegramGroupID != 0 {
			log.Printf("WARN: Attempted to send Telegram message (ChatID: %d, ThreadID: %d) but bot is not initialized.", chatID, messageThreadID)
		}
		return
	}

	if telegramLimiter == nil {
		log.Println("ERROR: Telegram rate limiter is nil! Sending without limit check.")
	} else {
		ctxWait, cancelWait := context.WithTimeout(context.Background(), 30*time.Second)
		err := telegramLimiter.Wait(ctxWait)
		cancelWait()
		if err != nil {
			log.Printf("ERROR: Telegram rate limiter wait error for chat %d, thread %d: %v. Proceeding cautiously...", chatID, messageThreadID, err)
		}
	}

	var lastErr error
	logCtx := fmt.Sprintf("[ChatID: %d]", chatID)
	if messageThreadID != 0 {
		logCtx = fmt.Sprintf("%s][ThreadID: %d]", logCtx, messageThreadID)
	} else {
		logCtx = fmt.Sprintf("%s[ThreadID: 0 (Main)]", logCtx)
	}

	params := make(map[string]string)
	params["chat_id"] = fmt.Sprintf("%d", chatID)
	params["parse_mode"] = tgbotapi.ModeMarkdownV2
	if messageThreadID != 0 {
		params["message_thread_id"] = fmt.Sprintf("%d", messageThreadID)
	}

	apiMethod := ""
	if isPhoto {
		logCtx = fmt.Sprintf("[Photo]%s", logCtx)
		apiMethod = "sendPhoto"
		params["photo"] = photoURL
		params["caption"] = textOrCaption
	} else {
		logCtx = fmt.Sprintf("[Text]%s", logCtx)
		apiMethod = "sendMessage"
		params["text"] = textOrCaption
	}
	for attempt := 0; attempt < maxRetries; attempt++ {
		_, err := localBot.MakeRequest(apiMethod, params)

		if err == nil {
			return
		}

		lastErr = err

		var tgErr *tgbotapi.Error
		isAPIErr := errors.As(err, &tgErr)
		shouldRetry := true
		specificRetryAfter := 0

		if isAPIErr {
			log.Printf("ERROR: Failed Telegram send (Attempt %d/%d): API Err %d - %s %s",
				attempt+1, maxRetries, tgErr.Code, tgErr.Message, logCtx)

			if tgErr.Code == 429 {
				specificRetryAfter = tgErr.RetryAfter
				if specificRetryAfter <= 0 {
					specificRetryAfter = int(math.Pow(2, float64(attempt+1)))
				}
				maxWait := 60
				if specificRetryAfter > maxWait {
					specificRetryAfter = maxWait
				}
				log.Printf("INFO: Telegram API rate limit hit (%d). Retrying after %d seconds... %s", tgErr.Code, specificRetryAfter, logCtx)

			} else if tgErr.Code == 400 {
				if strings.Contains(tgErr.Message, "thread not found") ||
					strings.Contains(tgErr.Message, "can't parse entities") ||
					strings.Contains(tgErr.Message, "chat not found") ||
					strings.Contains(tgErr.Message, "wrong file identifier") ||
					strings.Contains(tgErr.Message, "Wrong remote file ID specified") ||
					strings.Contains(tgErr.Message, "can't download file") ||
					strings.Contains(tgErr.Message, "failed to get HTTP URL content") ||
					strings.Contains(tgErr.Message, "PHOTO_INVALID_DIMENSIONS") ||
					strings.Contains(tgErr.Message, "wrong type of chat") {
					shouldRetry = false
					log.Printf("WARN: Non-retryable Telegram API error %d: %s. Aborting retries. %s", tgErr.Code, tgErr.Message, logCtx)

					if isPhoto && (strings.Contains(tgErr.Message, "failed to get HTTP URL content") ||
						strings.Contains(tgErr.Message, "wrong file identifier") ||
						strings.Contains(tgErr.Message, "Wrong remote file ID specified") ||
						strings.Contains(tgErr.Message, "can't download file")) {
						log.Printf("INFO: Falling back to sending caption as text message due to photo URL/fetch error. %s", logCtx)
						coreSendMessageWithRetry(chatID, messageThreadID, textOrCaption, false, "")
						return
					}
				} else {
					log.Printf("WARN: Potentially non-retryable Telegram API error 400: %s. Aborting retry. %s", tgErr.Message, logCtx)
					shouldRetry = false
				}
			} else if tgErr.Code == 403 || tgErr.Code == 401 || tgErr.Code == 404 {
				shouldRetry = false
				log.Printf("WARN: Non-retryable Telegram API error %d: %s. Aborting retries. %s", tgErr.Code, tgErr.Message, logCtx)
			}

		} else {
			log.Printf("ERROR: Failed Telegram send (Attempt %d/%d): Network/Other error: %v %s",
				attempt+1, maxRetries, err, logCtx)
			shouldRetry = true
		}

		if !shouldRetry || attempt >= maxRetries-1 {
			if shouldRetry && attempt >= maxRetries-1 {
				log.Printf("WARN: Max retries (%d) reached for Telegram send. Aborting. %s", maxRetries, logCtx)
			}
			break
		}

		var waitDuration time.Duration
		if specificRetryAfter > 0 {
			waitDuration = time.Duration(specificRetryAfter) * time.Second
		} else {
			waitDuration = baseRetryWait * time.Duration(math.Pow(2, float64(attempt)))
		}

		log.Printf("INFO: Retrying failed Telegram send in %v... %s", waitDuration, logCtx)
		time.Sleep(waitDuration)

	}
	if lastErr != nil {
		log.Printf("ERROR: Telegram message FAILED to send after %d retries. Last Error: %v. %s", maxRetries, lastErr, logCtx)
		if isPhoto {
			var lastTgErr *tgbotapi.Error
			if errors.As(lastErr, &lastTgErr) {
				if strings.Contains(lastTgErr.Message, "failed to get HTTP URL content") ||
					strings.Contains(lastTgErr.Message, "wrong file identifier") ||
					strings.Contains(lastTgErr.Message, "Wrong remote file ID specified") ||
					strings.Contains(lastTgErr.Message, "can't download file") {
					log.Printf("INFO: Falling back to sending caption as text message after final photo send failure. %s", logCtx)
					coreSendMessageWithRetry(chatID, messageThreadID, textOrCaption, false, "")
				}
			}
		}
	}
}

func EscapeMarkdownV2(s string) string {
	charsToEscape := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	temp := s
	for _, char := range charsToEscape {
		temp = strings.ReplaceAll(temp, char, "\\"+char)
	}
	return temp
}
