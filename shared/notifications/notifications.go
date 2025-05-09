// FILE: agent/shared/notifications/notifications.go
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
	if env.BotCallsThreadID != 0 {
		log.Printf("INFO: Bot Calls Thread ID: %d", env.BotCallsThreadID)
	}
	if env.TrackingThreadID != 0 {
		log.Printf("INFO: Tracking Thread ID: %d", env.TrackingThreadID)
	}
	return nil
}

func GetBotInstance() *telego.Bot {
	initMutex.Lock()
	defer initMutex.Unlock()
	return bot
}

func EscapeMarkdownV2(s string) string {
	charsToEscape := []string{"_", "*", "[", "]", "(", ")", "~", ">", "#", "+", "-", "=", "{", "}", ".", "!"}
	var builder strings.Builder
	builder.Grow(len(s) + 20)
	for _, r := range s {
		char := string(r)
		shouldEscape := false
		if char == "`" || char == "|" {
			shouldEscape = false
		} else {
			for _, esc := range charsToEscape {
				if char == esc {
					shouldEscape = true
					break
				}
			}
		}
		if shouldEscape {
			builder.WriteRune('\\')
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// --- START OF UPDATED FUNCTION ---
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

		logPayload := fmt.Sprintf("Payload: %s", escapedTextOrCaption)
		if len(logPayload) > 1000 {
			logPayload = logPayload[:1000] + "..."
		}
		log.Printf("DEBUG: Attempting Send API Call %s (Attempt %d/%d). %s", logCtx, attempt+1, maxRetries, logPayload)

		if isPhoto {
			params := &telego.SendPhotoParams{ChatID: telego.ChatID{ID: chatID}, Photo: telego.InputFile{URL: photoURL}, Caption: escapedTextOrCaption, ParseMode: telego.ModeMarkdownV2, MessageThreadID: messageThreadID, ReplyMarkup: replyMarkup}
			sentMsg, currentErr = localBot.SendPhoto(ctx, params)
		} else {
			params := &telego.SendMessageParams{ChatID: telego.ChatID{ID: chatID}, Text: escapedTextOrCaption, ParseMode: telego.ModeMarkdownV2, MessageThreadID: messageThreadID, ReplyMarkup: replyMarkup}
			sentMsg, currentErr = localBot.SendMessage(ctx, params)
		}
		cancel() // cancel() should be called regardless of error to free resources

		// ***** ADDED/MODIFIED LOGGING BLOCK from previous suggestion - KEEP THIS *****
		if currentErr != nil {
			log.Printf("ERROR: Telegram API call failed %s (Attempt %d/%d). Error: %v. Request Data: (photo=%t, photoURL=%s, caption_snippet=%.100s...)",
				logCtx, attempt+1, maxRetries, currentErr, isPhoto, photoURL, escapedTextOrCaption)
			var apiErr *telegoapi.Error
			if errors.As(currentErr, &apiErr) {
				log.Printf("ERROR_DETAILS: API Error Code: %d, Description: %s, Parameters: %+v %s",
					apiErr.ErrorCode, apiErr.Description, apiErr.Parameters, logCtx)
			}
		} else if sentMsg == nil || sentMsg.MessageID == 0 {
			log.Printf("WARN: Telegram API call seemingly succeeded (no error) but no valid message returned %s (Attempt %d/%d). Msg: %+v", logCtx, attempt+1, maxRetries, sentMsg)
			if currentErr == nil {
				currentErr = errors.New("no message returned from Telegram API after successful-looking call")
			}
		}
		// ***** END OF ADDED LOGGING BLOCK *****

		if currentErr == nil && sentMsg != nil && sentMsg.MessageID != 0 {
			log.Printf("INFO: Telegram message sent successfully %s (MsgID: %d)", logCtx, sentMsg.MessageID)
			return nil // Success!
		}

		// This was already assigned above, ensure it reflects the latest error if one was created for nil msg
		lastErr = currentErr

		// --- Retry / Fallback Logic ---
		shouldRetry := false
		specificRetryAfter := 0
		if currentErr != nil {
			var apiErr *telegoapi.Error
			if errors.As(currentErr, &apiErr) {
				if apiErr.ErrorCode == 429 && apiErr.Parameters != nil {
					specificRetryAfter = apiErr.Parameters.RetryAfter
					shouldRetry = true
				} else if apiErr.ErrorCode == 400 {
					nonRetryableSubstrings := []string{"thread not found", "chat not found", "wrong type of chat", "message text is empty", "can't parse entities", "Text buttons are unallowed"}
					isNonRetryable := false
					for _, sub := range nonRetryableSubstrings {
						if strings.Contains(apiErr.Description, sub) {
							isNonRetryable = true
							break
						}
					}

					// ***** THIS IS THE CORRECTED PART *****
					photoErrorSubstrings := []string{
						"wrong file identifier", "Wrong remote file ID specified",
						"can't download file", "failed to get HTTP URL content",
						"PHOTO_INVALID_DIMENSIONS", "Photo dimensions are too small",
						"IMAGE_PROCESS_FAILED",
						"wrong type of the web page content", // <<< ADDED THIS LINE
					}
					// ***** END OF CORRECTED PART *****
					isKnownPhotoError := false
					if isPhoto {
						for _, sub := range photoErrorSubstrings {
							if strings.Contains(apiErr.Description, sub) {
								isKnownPhotoError = true
								break
							}
						}
					}

					if isNonRetryable {
						shouldRetry = false
					} else if isKnownPhotoError {
						log.Printf("INFO: Known photo error detected (%s). Attempting fallback to text. %s", apiErr.Description, logCtx)
						fallbackErr := coreSendMessageWithRetry(chatID, messageThreadID, rawTextOrCaption, false, "", replyMarkup) // Pass replyMarkup for buttons
						if fallbackErr != nil {
							log.Printf("ERROR: Fallback to text ALSO FAILED. Original Error: %v. Fallback Error: %v. %s", currentErr, fallbackErr, logCtx)
							lastErr = currentErr // Keep original error
							shouldRetry = false
						} else {
							log.Printf("INFO: Fallback to text succeeded. %s", logCtx)
							return nil // Fallback worked
						}
					} else {
						// For other 400 errors not in nonRetryableSubstrings or photoErrorSubstrings
						log.Printf("WARN: Unhandled 400 Bad Request: %s. Not retrying. %s", apiErr.Description, logCtx)
						shouldRetry = false
					}
				} else if apiErr.ErrorCode == 403 || apiErr.ErrorCode == 401 || apiErr.ErrorCode == 404 {
					shouldRetry = false
				} else { // For 5xx errors or other API errors
					shouldRetry = true
				}
			} else { // Network errors, etc.
				shouldRetry = true
			}
		} else { // currentErr was nil, but message sending didn't fully succeed (e.g., sentMsg was nil or MsgID was 0)
			shouldRetry = true
		}

		if !shouldRetry || attempt >= maxRetries-1 {
			if shouldRetry && attempt >= maxRetries-1 {
				log.Printf("ERROR: Max retries (%d) reached. Aborting. %s", maxRetries, logCtx)
			} else if !shouldRetry {
				log.Printf("ERROR: Non-retryable error encountered or fallback attempted and failed. Aborting retry loop. %s", logCtx)
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
		log.Printf("INFO: Retrying failed send in %v... %s", waitDuration, logCtx)
		time.Sleep(waitDuration)
	}

	if lastErr != nil {
		log.Printf("ERROR: Telegram message FAILED after all attempts/fallbacks. Final Error: %v. %s", lastErr, logCtx)
	}
	return lastErr
}

// --- END OF UPDATED FUNCTION ---

func SendTelegramMessage(message string) {
	_ = coreSendMessageWithRetry(defaultGroupID, 0, message, false, "", nil)
}

func SendBotCallMessage(message string, buttons ...map[string]string) {
	threadID := env.BotCallsThreadID
	if threadID == 0 {
		log.Println("WARN: Bot Calls thread ID not set, sending to main group.")
		threadID = 0
	}

	var replyMarkup telego.ReplyMarkup
	if len(buttons) > 0 && len(buttons[0]) > 0 {
		var rows [][]telego.InlineKeyboardButton
		var row []telego.InlineKeyboardButton
		for label, urlValue := range buttons[0] {
			btn := telego.InlineKeyboardButton{
				Text: label,
				URL:  urlValue,
			}
			row = append(row, btn)
		}
		if len(row) > 0 {
			rows = append(rows, row)
			replyMarkup = &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
		}
	}

	_ = coreSendMessageWithRetry(defaultGroupID, threadID, message, false, "", replyMarkup)
}

func SendBotCallPhotoMessage(photoURL string, caption string, buttons ...map[string]string) {
	threadID := env.BotCallsThreadID
	if threadID == 0 {
		log.Println("WARN: Bot Calls thread ID not set, sending caption as text to main group.")
		// Call SendBotCallMessage for fallback, which handles buttons correctly
		SendBotCallMessage(caption, buttons...)
		return
	}
	parsedURL, urlErr := url.ParseRequestURI(photoURL)
	if urlErr != nil || !(parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
		log.Printf("ERROR: Invalid photo URL format: %s - %v. Falling back to sending caption as text message.", photoURL, urlErr)
		// Call SendBotCallMessage for fallback, which handles buttons correctly
		SendBotCallMessage(caption, buttons...)
		return
	}

	var replyMarkup telego.ReplyMarkup
	if len(buttons) > 0 && len(buttons[0]) > 0 {
		var rows [][]telego.InlineKeyboardButton
		var row []telego.InlineKeyboardButton
		for label, urlValue := range buttons[0] {
			btn := telego.InlineKeyboardButton{
				Text: label,
				URL:  urlValue,
			}
			row = append(row, btn)
		}
		if len(row) > 0 {
			rows = append(rows, row)
			replyMarkup = &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
		}
	}

	_ = coreSendMessageWithRetry(defaultGroupID, threadID, caption, true, photoURL, replyMarkup)
}

func SendTrackingUpdateMessage(message string) {
	threadID := env.TrackingThreadID
	if threadID == 0 {
		log.Println("WARN: Tracking thread ID not set, sending to main group.")
		threadID = 0
	}
	_ = coreSendMessageWithRetry(defaultGroupID, threadID, message, false, "", nil)
}

func SendVerificationSuccessMessage(userID int64, groupLink string) error {
	if groupLink == "" {
		log.Println("ERROR: Target group link empty for verification success msg.")
		return errors.New("target group link empty")
	}
	escapedGroupLink := groupLink
	messageText := fmt.Sprintf("✅ Verification Successful! You now have access.\n\nJoin the group here: [Click to Join](%s)", escapedGroupLink)
	localBot := GetBotInstance()
	if localBot == nil {
		log.Printf("ERROR: Bot nil for verification success msg to user %d.", userID)
		return errors.New("bot instance nil.")
	}

	err := coreSendMessageWithRetry(userID, 0, messageText, false, "", nil)
	if err != nil {
		log.Printf("ERROR: Failed to send verification success msg to user %d: %v", userID, err)
	} else {
		log.Printf("INFO: Verification success message sent to user %d.", userID)
	}
	return err
}
