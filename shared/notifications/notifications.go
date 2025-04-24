package notifications

import (
	"ca-scraper/shared/env"
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url" // Keep for photo URL validation
	"strings"
	"sync"
	"time"

	"github.com/mymmrac/telego"           // Import telego
	"github.com/mymmrac/telego/telegoapi" // Import telegoapi for error checking
	"golang.org/x/time/rate"              // Keep rate limiter
)

var (
	bot             *telego.Bot // Use Telego type
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

// InitTelegramBot using Telego
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
		log.Println("WARN: TELEGRAM_GROUP_ID missing or invalid. Telegram notifications disabled.")
		isInitialized = false
		bot = nil
		return nil
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
	log.Printf("INFO: Target Telegram Group ID: %d", defaultGroupID)
	log.Printf("INFO: External Telegram rate limiter initialized (Limit: %.2f/s, Burst: %d)", telegramMessagesPerSecond, telegramBurstLimit)
	log.Printf("INFO: Bot Calls Thread ID: %d", env.BotCallsThreadID)
	log.Printf("INFO: Tracking Thread ID: %d", env.TrackingThreadID)

	return nil
}

// GetBotInstance returns *telego.Bot
func GetBotInstance() *telego.Bot {
	initMutex.Lock()
	defer initMutex.Unlock()
	if !isInitialized || bot == nil {
		log.Println("WARN: GetBotInstance called but bot (Telego) is not properly initialized.")
		return nil
	}
	return bot
}

// EscapeMarkdownV2 remains the same
func EscapeMarkdownV2(s string) string {
	charsToEscape := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	temp := s
	for _, char := range charsToEscape {
		temp = strings.ReplaceAll(temp, char, "\\"+char)
	}
	return temp
}

// Core sending function refactored for Telego, including retries
func coreSendMessageWithRetry(chatID int64, messageThreadID int, rawTextOrCaption string, isPhoto bool, photoURL string) error {
	localBot := GetBotInstance()
	if localBot == nil {
		log.Printf("WARN: coreSendMessageWithRetry: Bot (Telego) not initialized (ChatID: %d).", chatID)
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
	if isPhoto {
		logCtx = "[Photo]" + logCtx
	} else {
		logCtx = "[Text]" + logCtx
	}

	for attempt := 0; attempt < maxRetries; attempt++ {
		if telegramLimiter != nil {
			ctxWait, cancelWait := context.WithTimeout(context.Background(), 30*time.Second)
			waitErr := telegramLimiter.Wait(ctxWait)
			cancelWait()
			if waitErr != nil {
				log.Printf("ERROR: Telegram rate limiter wait error %s: %v. Proceeding cautiously...", logCtx, waitErr)
			}
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second) // Timeout for API call

		var currentErr error
		var sentMsg *telego.Message // To check success

		if isPhoto {
			params := &telego.SendPhotoParams{
				ChatID:          telego.ChatID{ID: chatID},
				Photo:           telego.InputFile{URL: photoURL},
				Caption:         escapedTextOrCaption,
				ParseMode:       telego.ModeMarkdownV2,
				MessageThreadID: messageThreadID,
			}
			sentMsg, currentErr = localBot.SendPhoto(ctx, params)
		} else {
			params := &telego.SendMessageParams{
				ChatID:          telego.ChatID{ID: chatID},
				Text:            escapedTextOrCaption,
				ParseMode:       telego.ModeMarkdownV2,
				MessageThreadID: messageThreadID,
			}
			sentMsg, currentErr = localBot.SendMessage(ctx, params)
		}
		cancel() // Cancel context after API call attempt

		if currentErr == nil && sentMsg != nil {
			return nil // Success!
		}

		// --- Error Handling & Retry Logic ---
		lastErr = currentErr // Store the last error encountered
		shouldRetry := false
		specificRetryAfter := 0

		if currentErr != nil {
			var apiErr *telegoapi.Error // Check if it's a Telego API error
			if errors.As(currentErr, &apiErr) {
				// --- Use apiErr.ErrorCode instead of apiErr.Code ---
				log.Printf("ERROR: Failed Telegram send (Attempt %d/%d): API Err %d - %s %s",
					attempt+1, maxRetries, apiErr.ErrorCode, apiErr.Description, logCtx)

				if apiErr.ErrorCode == 429 && apiErr.Parameters != nil { // Rate Limit
					specificRetryAfter = apiErr.Parameters.RetryAfter
					shouldRetry = true
				} else if apiErr.ErrorCode == 400 { // Bad Request
					nonRetryableSubstrings := []string{"thread not found", "can't parse entities", "chat not found", "wrong file identifier", "Wrong remote file ID specified", "can't download file", "failed to get HTTP URL content", "PHOTO_INVALID_DIMENSIONS", "wrong type of chat"}
					isNonRetryable := false
					for _, sub := range nonRetryableSubstrings {
						if strings.Contains(apiErr.Description, sub) {
							isNonRetryable = true
							break
						}
					}
					if isNonRetryable {
						shouldRetry = false
						log.Printf("WARN: Non-retryable Telegram API error 400: %s. Aborting retries. %s", apiErr.Description, logCtx)
						if isPhoto && (strings.Contains(apiErr.Description, "failed to get HTTP URL content") || strings.Contains(apiErr.Description, "wrong file identifier") || strings.Contains(apiErr.Description, "Wrong remote file ID specified") || strings.Contains(apiErr.Description, "can't download file")) {
							log.Printf("INFO: Falling back to sending caption as text message due to photo error. %s", logCtx)
							return coreSendMessageWithRetry(chatID, messageThreadID, rawTextOrCaption, false, "")
						}
					} else {
						shouldRetry = false
						log.Printf("WARN: Potentially non-retryable Telegram API error 400: %s. Aborting retry. %s", apiErr.Description, logCtx)
					}
				} else if apiErr.ErrorCode == 403 || apiErr.ErrorCode == 401 || apiErr.ErrorCode == 404 { // Auth/Permissions/Not Found
					shouldRetry = false
					log.Printf("WARN: Non-retryable Telegram API error %d: %s. Aborting retries. %s", apiErr.ErrorCode, apiErr.Description, logCtx)
				} else {
					shouldRetry = true
					log.Printf("INFO: Retrying potentially temporary Telegram API error %d: %s %s", apiErr.ErrorCode, apiErr.Description, logCtx)
				}
			} else { // Network errors or other non-API errors
				log.Printf("ERROR: Failed Telegram send (Attempt %d/%d): Network/Other error: %v %s",
					attempt+1, maxRetries, currentErr, logCtx)
				shouldRetry = true
			}
		} else {
			log.Printf("WARN: Telegram send attempt failed without error but didn't return message. %s", logCtx)
			lastErr = errors.New("send failed without specific error")
			shouldRetry = true
		}

		if !shouldRetry || attempt >= maxRetries-1 {
			if shouldRetry && attempt >= maxRetries-1 {
				log.Printf("WARN: Max retries (%d) reached for Telegram send. Aborting. %s", maxRetries, logCtx)
			}
			break
		}

		waitDuration := baseRetryWait * time.Duration(math.Pow(2, float64(attempt)))
		if specificRetryAfter > 0 {
			waitDuration = time.Duration(specificRetryAfter) * time.Second
		}
		if waitDuration > maxRetryWait {
			waitDuration = maxRetryWait
		}

		log.Printf("INFO: Retrying failed Telegram send in %v... %s", waitDuration, logCtx)
		time.Sleep(waitDuration)
	} // End retry loop

	if lastErr != nil {
		log.Printf("ERROR: Telegram message FAILED to send after %d retries. Last Error: %v. %s", maxRetries, lastErr, logCtx)
		if isPhoto {
			var lastApiErr *telegoapi.Error
			if errors.As(lastErr, &lastApiErr) {
				if strings.Contains(lastApiErr.Description, "failed to get HTTP URL content") || strings.Contains(lastApiErr.Description, "wrong file identifier") || strings.Contains(lastApiErr.Description, "Wrong remote file ID specified") || strings.Contains(lastApiErr.Description, "can't download file") {
					log.Printf("INFO: Falling back to sending caption as text message after final photo send failure. %s", logCtx)
					return coreSendMessageWithRetry(chatID, messageThreadID, rawTextOrCaption, false, "")
				}
			}
		}
	}

	return lastErr
}

// Public sending functions call the refactored coreSendMessageWithRetry
func SendTelegramMessage(message string) {
	_ = coreSendMessageWithRetry(defaultGroupID, 0, message, false, "")
}

func SendBotCallMessage(message string) {
	if env.BotCallsThreadID == 0 {
		log.Println("WARN: Attempted to send to Bot Calls topic, but BOT_CALLS_THREAD_ID is not set.")
		return
	}
	_ = coreSendMessageWithRetry(defaultGroupID, env.BotCallsThreadID, message, false, "")
}

func SendBotCallPhotoMessage(photoURL string, caption string) {
	if env.BotCallsThreadID == 0 {
		log.Println("WARN: Attempted to send photo to Bot Calls topic, but BOT_CALLS_THREAD_ID is not set.")
		return
	}
	if _, urlErr := url.ParseRequestURI(photoURL); urlErr != nil {
		log.Printf("ERROR: Invalid photo URL for Bot Call: %s - %v. Not attempting send.", photoURL, urlErr)
		return
	}
	_ = coreSendMessageWithRetry(defaultGroupID, env.BotCallsThreadID, caption, true, photoURL)
}

func SendTrackingUpdateMessage(message string) {
	if env.TrackingThreadID == 0 {
		log.Println("WARN: Attempted to send to Tracking topic, but TRACKING_THREAD_ID is not set.")
		return
	}
	_ = coreSendMessageWithRetry(defaultGroupID, env.TrackingThreadID, message, false, "")
}
