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

// InitTelegramBot unchanged...
func InitTelegramBot() error {
	// ... (same as previous correct version) ...
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

// GetBotInstance unchanged...
func GetBotInstance() *telego.Bot {
	initMutex.Lock()
	defer initMutex.Unlock()
	return bot
}

// EscapeMarkdownV2 escapes characters for Telegram MarkdownV2 parse mode.
// --- CORRECTED VERSION: Preserves backticks ` ` ---
// This function WILL be called by graduate.go now.
func EscapeMarkdownV2(s string) string {
	charsToEscape := []string{"_", "*", "[", "]", "(", ")", "~" /*"`",*/, ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"} // Backtick removed, hyphen/dot/etc included
	var builder strings.Builder
	builder.Grow(len(s) + 20)
	for _, r := range s {
		char := string(r)
		shouldEscape := false
		if char == "`" {
			shouldEscape = false
		} else { // Do NOT escape backticks
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

// coreSendMessageWithRetry handles the sending logic with rate limiting and retries.
// --- CORRECTED VERSION: Removed internal escaping ---
func coreSendMessageWithRetry(chatID int64, messageThreadID int, rawTextOrCaption string, isPhoto bool, photoURL string) error {
	localBot := GetBotInstance()
	if localBot == nil {
		log.Printf("WARN: coreSendMessageWithRetry: Bot not initialized (ChatID: %d).", chatID)
		return errors.New("telego bot not initialized")
	}

	// === FIX: REMOVE the internal escaping call ===
	// Send the already formatted/escaped string received from the caller (graduate.go)
	textOrCaptionToSend := rawTextOrCaption
	// ============================================

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

		if isPhoto {
			params := &telego.SendPhotoParams{ChatID: telego.ChatID{ID: chatID}, Photo: telego.InputFile{URL: photoURL}, Caption: textOrCaptionToSend, ParseMode: telego.ModeMarkdownV2, MessageThreadID: messageThreadID} // Send UNESCAPED textOrCaptionToSend
			log.Printf("DEBUG: Attempting SendPhoto %s (Attempt %d/%d)", logCtx, attempt+1, maxRetries)
			sentMsg, currentErr = localBot.SendPhoto(ctx, params)
		} else {
			params := &telego.SendMessageParams{ChatID: telego.ChatID{ID: chatID}, Text: textOrCaptionToSend, ParseMode: telego.ModeMarkdownV2, MessageThreadID: messageThreadID} // Send UNESCAPED textOrCaptionToSend
			log.Printf("DEBUG: Attempting SendMessage %s (Attempt %d/%d)", logCtx, attempt+1, maxRetries)
			sentMsg, currentErr = localBot.SendMessage(ctx, params)
		}
		cancel()

		// Handle Result
		if currentErr == nil && sentMsg != nil && sentMsg.MessageID != 0 {
			log.Printf("INFO: Telegram message sent successfully %s (MsgID: %d)", logCtx, sentMsg.MessageID)
			return nil
		}

		// Error Handling & Retry Logic (robust version - unchanged from last provided)
		lastErr = currentErr
		shouldRetry := false
		specificRetryAfter := 0
		if currentErr != nil {
			var apiErr *telegoapi.Error
			if errors.As(currentErr, &apiErr) {
				log.Printf("WARN: Telegram API Error (Attempt %d/%d): Code=%d, Desc=%s %s", attempt+1, maxRetries, apiErr.ErrorCode, apiErr.Description, logCtx)
				if apiErr.ErrorCode == 429 && apiErr.Parameters != nil {
					specificRetryAfter = apiErr.Parameters.RetryAfter
					shouldRetry = true
					log.Printf("INFO: Rate limit hit, retry after %d s %s", specificRetryAfter, logCtx)
				} else if apiErr.ErrorCode == 400 {
					if strings.Contains(apiErr.Description, "can't parse entities") {
						log.Printf("ERROR: MarkdownV2 parsing error: %s. Aborting retries. Check caller escaping. %s", apiErr.Description, logCtx)
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
							log.Printf("ERROR: Non-retryable API error 400: %s. Aborting. %s", apiErr.Description, logCtx)
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
								shouldRetry = false
								log.Printf("WARN: Photo-specific error 400: %s. Will attempt fallback. %s", apiErr.Description, logCtx)
							} else {
								shouldRetry = false
								log.Printf("WARN: Uncategorized API error 400: %s. Aborting retry. %s", apiErr.Description, logCtx)
							}
						}
					}
				} else if apiErr.ErrorCode == 403 || apiErr.ErrorCode == 401 || apiErr.ErrorCode == 404 {
					shouldRetry = false
					log.Printf("ERROR: Non-retryable API error %d: %s. Aborting. %s", apiErr.ErrorCode, apiErr.Description, logCtx)
				} else {
					shouldRetry = true
					log.Printf("INFO: Retrying potentially temporary API error %d: %s %s", apiErr.ErrorCode, apiErr.Description, logCtx)
				}
			} else {
				log.Printf("WARN: Failed Send (Attempt %d/%d): Network/Other error: %v %s", attempt+1, maxRetries, currentErr, logCtx)
				shouldRetry = true
			}
		} else {
			log.Printf("WARN: Send attempt %d/%d ok but no message ID. Retrying. %s", attempt+1, maxRetries, logCtx)
			lastErr = errors.New("send ok but message ID missing")
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
		log.Printf("ERROR: Telegram message FAILED after %d retries. Last Error: %v. %s", maxRetries, lastErr, logCtx)
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
					return coreSendMessageWithRetry(chatID, messageThreadID, rawTextOrCaption, false, "")
				}
			}
		}
	}
	return lastErr
}

// --- Public Send Functions --- (Unchanged from previous correct version)
func SendTelegramMessage(message string) {
	_ = coreSendMessageWithRetry(defaultGroupID, 0, message, false, "")
}
func SendBotCallMessage(message string) {
	threadID := env.BotCallsThreadID
	if threadID == 0 {
		log.Println("WARN: Bot Calls thread ID not set, sending to main group.")
		threadID = 0
	}
	_ = coreSendMessageWithRetry(defaultGroupID, threadID, message, false, "")
}
func SendBotCallPhotoMessage(photoURL string, caption string) {
	threadID := env.BotCallsThreadID
	if threadID == 0 {
		log.Println("WARN: Bot Calls thread ID not set, sending caption as text to main group.")
		_ = coreSendMessageWithRetry(defaultGroupID, 0, caption, false, "")
		return
	}
	parsedURL, urlErr := url.ParseRequestURI(photoURL)
	if urlErr != nil || !(parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
		log.Printf("ERROR: Invalid photo URL format: %s - %v. Falling back to text.", photoURL, urlErr)
		_ = coreSendMessageWithRetry(defaultGroupID, threadID, caption, false, "")
		return
	}
	_ = coreSendMessageWithRetry(defaultGroupID, threadID, caption, true, photoURL)
}
func SendTrackingUpdateMessage(message string) {
	threadID := env.TrackingThreadID
	if threadID == 0 {
		log.Println("WARN: Tracking thread ID not set, sending to main group.")
		threadID = 0
	}
	_ = coreSendMessageWithRetry(defaultGroupID, threadID, message, false, "")
}
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
		return errors.New("bot instance nil")
	}
	err := coreSendMessageWithRetry(userID, 0, messageText, false, "")
	if err != nil {
		log.Printf("ERROR: Failed to send verification success msg to user %d: %v", userID, err)
	} else {
		log.Printf("INFO: Verification success message sent to user %d.", userID)
	}
	return err
}
