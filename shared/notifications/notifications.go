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
	"github.com/mymmrac/telego/telegoutil"
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

// InitTelegramBot initializes the Telegram bot and rate limiter.
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

// GetBotInstance returns the initialized bot instance or nil.
func GetBotInstance() *telego.Bot {
	initMutex.Lock()
	defer initMutex.Unlock()
	return bot
}

// EscapeMarkdownV2 escapes characters for Telegram MarkdownV2 parse mode.
// --- CORRECTED VERSION: Preserves backticks ` AND pipe | ---
func EscapeMarkdownV2(s string) string {
	charsToEscape := []string{"_", "*", "[", "]", "(", ")", "~" /*"`",*/ /*"|",*/, ">", "#", "+", "-", "=" /*"|",*/, "{", "}", ".", "!"} // Backtick and pipe removed/commented
	var builder strings.Builder
	builder.Grow(len(s) + 20) // Preallocate buffer slightly larger
	for _, r := range s {
		char := string(r)
		shouldEscape := false
		if char == "`" || char == "|" {
			shouldEscape = false
		} else { // Do NOT escape backticks OR pipes
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

// coreSendMessageWithRetry handles sending logic with rate limiting and retries.
// --- MODIFIED: Added optional replyMarkup parameter ---
func coreSendMessageWithRetry(chatID int64, messageThreadID int, rawTextOrCaption string, isPhoto bool, photoURL string, replyMarkup telego.ReplyMarkup) error { // Added replyMarkup
	localBot := GetBotInstance()
	if localBot == nil {
		log.Printf("WARN: coreSendMessageWithRetry: Bot not initialized (ChatID: %d).", chatID)
		return errors.New("telego bot not initialized")
	}

	// Apply the corrected escaping function (preserves backticks AND pipes, escapes others)
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
		// Rate Limiter
		if telegramLimiter != nil {
			ctxWait, cancelWait := context.WithTimeout(context.Background(), 30*time.Second)
			waitErr := telegramLimiter.Wait(ctxWait)
			cancelWait()
			if waitErr != nil {
				log.Printf("WARN: Telegram rate limiter wait error %s: %v. Proceeding cautiously...", logCtx, waitErr)
			}
		}

		// Send Attempt
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		var currentErr error
		var sentMsg *telego.Message

		// Log before sending
		logPayload := fmt.Sprintf("Payload: %s", escapedTextOrCaption)
		if len(logPayload) > 1000 {
			logPayload = logPayload[:1000] + "..."
		}
		log.Printf("DEBUG: Attempting Send API Call %s (Attempt %d/%d). %s", logCtx, attempt+1, maxRetries, logPayload)

		if isPhoto {
			params := &telego.SendPhotoParams{ChatID: telego.ChatID{ID: chatID}, Photo: telego.InputFile{URL: photoURL}, Caption: escapedTextOrCaption, ParseMode: telego.ModeMarkdownV2, MessageThreadID: messageThreadID, ReplyMarkup: replyMarkup} // Pass replyMarkup
			sentMsg, currentErr = localBot.SendPhoto(ctx, params)
		} else {
			params := &telego.SendMessageParams{ChatID: telego.ChatID{ID: chatID}, Text: escapedTextOrCaption, ParseMode: telego.ModeMarkdownV2, MessageThreadID: messageThreadID, ReplyMarkup: replyMarkup} // Pass replyMarkup
			sentMsg, currentErr = localBot.SendMessage(ctx, params)
		}
		cancel()

		// Handle Result
		if currentErr == nil && sentMsg != nil && sentMsg.MessageID != 0 {
			log.Printf("INFO: Telegram message sent successfully %s (MsgID: %d)", logCtx, sentMsg.MessageID)
			return nil
		}

		// Log error after sending attempt
		if currentErr != nil {
			log.Printf("ERROR: Send API Call FAILED %s (Attempt %d/%d). Error: %v", logCtx, attempt+1, maxRetries, currentErr)
		} else {
			log.Printf("WARN: Send API Call SUCCEEDED according to error status but MsgID is 0 %s (Attempt %d/%d).", logCtx, attempt+1, maxRetries)
		}

		// Error Handling & Retry Logic (robust version - unchanged)
		lastErr = currentErr
		shouldRetry := false
		specificRetryAfter := 0
		if currentErr != nil {
			var apiErr *telegoapi.Error
			if errors.As(currentErr, &apiErr) {
				if apiErr.ErrorCode == 429 && apiErr.Parameters != nil {
					specificRetryAfter = apiErr.Parameters.RetryAfter
					shouldRetry = true
				} else if apiErr.ErrorCode == 400 {
					if strings.Contains(apiErr.Description, "can't parse entities") {
						shouldRetry = false
					} else {
						nonRetryableSubstrings := []string{"thread not found", "chat not found", "wrong type of chat", "message text is empty"}
						photoErrorSubstrings := []string{"wrong file identifier", "Wrong remote file ID specified", "can't download file", "failed to get HTTP URL content", "PHOTO_INVALID_DIMENSIONS", "Photo dimensions are too small", "IMAGE_PROCESS_FAILED"}
						isNonRetryable := false
						for _, sub := range nonRetryableSubstrings {
							if strings.Contains(apiErr.Description, sub) {
								isNonRetryable = true
								break
							}
						}
						if isNonRetryable {
							shouldRetry = false
						} else {
							isKnownPhotoError := false
							if isPhoto {
								for _, sub := range photoErrorSubstrings {
									if strings.Contains(apiErr.Description, sub) {
										isKnownPhotoError = true
										break
									}
								}
							}
							if isKnownPhotoError {
								shouldRetry = false /* Fallback happens after loop */
							} else {
								shouldRetry = false
							}
						}
					}
				} else if apiErr.ErrorCode == 403 || apiErr.ErrorCode == 401 || apiErr.ErrorCode == 404 {
					shouldRetry = false
				} else {
					shouldRetry = true
				}
			} else {
				shouldRetry = true
			}
		} else {
			shouldRetry = true
		}

		// Exit or Wait
		if !shouldRetry || attempt >= maxRetries-1 {
			if shouldRetry {
				log.Printf("ERROR: Max retries (%d) reached. Aborting. %s", maxRetries, logCtx)
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
	} // End retry loop

	// Final Check and Fallback
	if lastErr != nil {
		log.Printf("ERROR: Telegram message FAILED after %d retries. Final Error: %v. %s", maxRetries, lastErr, logCtx)
		if isPhoto {
			var lastApiErr *telegoapi.Error
			photoErrorSubstrings := []string{"wrong file identifier", "Wrong remote file ID specified", "can't download file", "failed to get HTTP URL content", "PHOTO_INVALID_DIMENSIONS", "Photo dimensions are too small", "IMAGE_PROCESS_FAILED"}
			if errors.As(lastErr, &lastApiErr) && lastApiErr.ErrorCode == 400 {
				isKnownPhotoError := false
				for _, sub := range photoErrorSubstrings {
					if strings.Contains(lastApiErr.Description, sub) {
						isKnownPhotoError = true
						break
					}
				}
				if isKnownPhotoError {
					log.Printf("INFO: Final error indicates photo issue. Falling back to text. %s", logCtx)
					return coreSendMessageWithRetry(chatID, messageThreadID, rawTextOrCaption, false, "", nil)
				}
			} // Pass nil for markup on fallback
		}
	}
	return lastErr
}

// --- Public Send Functions ---

// SendTelegramMessage sends a standard text message to the default group.
func SendTelegramMessage(message string) {
	_ = coreSendMessageWithRetry(defaultGroupID, 0, message, false, "", nil) // Pass nil for markup
}

// SendBotCallMessage sends a text message, potentially with buttons, to the Bot Calls topic thread.
func SendBotCallMessage(message string, buttons ...map[string]string) { // Use variadic args for optional buttons
	threadID := env.BotCallsThreadID
	if threadID == 0 {
		log.Println("WARN: Bot Calls thread ID not set, sending to main group.")
		threadID = 0
	}

	var replyMarkup *telego.InlineKeyboardMarkup
	if len(buttons) > 0 && len(buttons[0]) > 0 {
		// Build inline buttons if provided
		var rows [][]telego.InlineKeyboardButton
		var row []telego.InlineKeyboardButton
		// Assume only one map is passed, put all its buttons on one row
		for label, url := range buttons[0] {
			btn := telegoutil.InlineKeyboardButton(url).WithText(label) // URL button
			row = append(row, btn)
		}
		if len(row) > 0 {
			rows = append(rows, row)
			replyMarkup = &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
		}
	}

	_ = coreSendMessageWithRetry(defaultGroupID, threadID, message, false, "", replyMarkup) // Pass potential markup
}

// SendBotCallPhotoMessage sends a photo with caption, potentially with buttons, to the Bot Calls topic thread.
func SendBotCallPhotoMessage(photoURL string, caption string, buttons ...map[string]string) { // Use variadic args for optional buttons
	threadID := env.BotCallsThreadID
	if threadID == 0 {
		log.Println("WARN: Bot Calls thread ID not set, sending caption as text to main group.")
		_ = coreSendMessageWithRetry(defaultGroupID, 0, caption, false, "", nil) // Fallback to text in main group, no buttons
		return
	}
	parsedURL, urlErr := url.ParseRequestURI(photoURL)
	if urlErr != nil || !(parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
		log.Printf("ERROR: Invalid photo URL format: %s - %v. Falling back to sending caption as text message.", photoURL, urlErr)
		// Fallback to text, but try to include buttons if provided
		SendBotCallMessage(caption, buttons...) // Call the text version which handles buttons
		return
	}

	var replyMarkup *telego.InlineKeyboardMarkup
	if len(buttons) > 0 && len(buttons[0]) > 0 {
		// Build inline buttons if provided
		var rows [][]telego.InlineKeyboardButton
		var row []telego.InlineKeyboardButton
		// Assume only one map is passed, put all its buttons on one row
		for label, url := range buttons[0] {
			btn := telegoutil.InlineKeyboardButton(url).WithText(label) // URL button
			row = append(row, btn)
		}
		if len(row) > 0 {
			rows = append(rows, row)
			replyMarkup = &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
		}
	}

	_ = coreSendMessageWithRetry(defaultGroupID, threadID, caption, true, photoURL, replyMarkup) // Pass potential markup
}

// SendTrackingUpdateMessage sends a text message to the Tracking topic thread.
func SendTrackingUpdateMessage(message string) { // No buttons needed for tracking usually
	threadID := env.TrackingThreadID
	if threadID == 0 {
		log.Println("WARN: Tracking thread ID not set, sending to main group.")
		threadID = 0
	}
	_ = coreSendMessageWithRetry(defaultGroupID, threadID, message, false, "", nil) // Pass nil for markup
}

// SendVerificationSuccessMessage sends a direct message to a user upon successful verification.
func SendVerificationSuccessMessage(userID int64, groupLink string) error {
	if groupLink == "" {
		log.Println("ERROR: Target group link empty for verification success msg.")
		return errors.New("target group link empty")
	}
	escapedGroupLink := groupLink
	messageText := fmt.Sprintf("✅ Verification Successful\\! You now have access\\.\n\nJoin the group here: [Click to Join](%s)", escapedGroupLink)
	localBot := GetBotInstance()
	if localBot == nil {
		log.Printf("ERROR: Bot nil for verification success msg to user %d.", userID)
		return errors.New("bot instance nil.")
	}
	// Send directly to user, no buttons needed here usually
	err := coreSendMessageWithRetry(userID, 0, messageText, false, "", nil) // Pass nil for markup
	if err != nil {
		log.Printf("ERROR: Failed to send verification success msg to user %d: %v", userID, err)
	} else {
		log.Printf("INFO: Verification success message sent to user %d.", userID)
	}
	return err
}
