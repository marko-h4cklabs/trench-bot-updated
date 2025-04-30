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
	telegramMessagesPerSecond = 15.0 // Adjusted rate based on previous logs
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
		return nil // Return nil, not necessarily an error for the app to stop
	}
	if parsedGroupID == 0 {
		log.Println("WARN: TELEGRAM_GROUP_ID missing or invalid. Telegram notifications disabled.")
		isInitialized = false
		bot = nil
		return nil // Return nil
	}

	defaultGroupID = parsedGroupID

	log.Println("INFO: Initializing Telegram bot (Telego)...")
	var err error
	// Initialize bot with default logger that prints errors
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
		// Decide if this is fatal, usually it is if the token is wrong
		return fmt.Errorf("failed to verify bot token with GetMe (Telego): %w", err)
	}

	// Initialize rate limiter
	telegramLimiter = rate.NewLimiter(rate.Limit(telegramMessagesPerSecond), telegramBurstLimit)
	isInitialized = true
	log.Printf("INFO: Telegram bot (Telego) initialized successfully for @%s", botUser.Username)
	log.Printf("INFO: Target Telegram Group ID: %d", defaultGroupID)
	log.Printf("INFO: External Telegram rate limiter initialized (Limit: %.2f/s, Burst: %d)", telegramMessagesPerSecond, telegramBurstLimit)
	// Log thread IDs if they are used
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
	// No need to log here, the caller should handle nil
	return bot
}

// EscapeMarkdownV2 escapes characters for Telegram MarkdownV2 parse mode.
func EscapeMarkdownV2(s string) string {
	// Characters listed by Telegram for escaping in MarkdownV2
	charsToEscape := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	var builder strings.Builder
	builder.Grow(len(s) + 10) // Preallocate slightly larger buffer
	for _, r := range s {
		char := string(r)
		shouldEscape := false
		for _, esc := range charsToEscape {
			if char == esc {
				shouldEscape = true
				break
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
// It now correctly handles photo fallback on specific errors.
func coreSendMessageWithRetry(chatID int64, messageThreadID int, rawTextOrCaption string, isPhoto bool, photoURL string) error {
	localBot := GetBotInstance()
	if localBot == nil {
		log.Printf("WARN: coreSendMessageWithRetry: Bot (Telego) not initialized (ChatID: %d).", chatID)
		return errors.New("telego bot not initialized")
	}

	// Send raw text - backticks for copy/paste don't need escaping in MarkdownV2.
	// If you add other formatting like *bold*, you might need to escape selectively or use the EscapeMarkdownV2 function.
	escapedTextOrCaption := rawTextOrCaption

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
		// --- Rate Limiter ---
		if telegramLimiter != nil {
			ctxWait, cancelWait := context.WithTimeout(context.Background(), 30*time.Second)
			waitErr := telegramLimiter.Wait(ctxWait)
			cancelWait()
			if waitErr != nil {
				log.Printf("WARN: Telegram rate limiter wait error %s: %v. Proceeding cautiously...", logCtx, waitErr)
			}
		}

		// --- Send Attempt ---
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		var currentErr error
		var sentMsg *telego.Message

		if isPhoto {
			params := &telego.SendPhotoParams{
				ChatID:          telego.ChatID{ID: chatID},
				Photo:           telego.InputFile{URL: photoURL},
				Caption:         escapedTextOrCaption,
				ParseMode:       telego.ModeMarkdownV2, // Use MarkdownV2 for caption
				MessageThreadID: messageThreadID,
			}
			log.Printf("DEBUG: Attempting SendPhoto %s (Attempt %d/%d)", logCtx, attempt+1, maxRetries)
			sentMsg, currentErr = localBot.SendPhoto(ctx, params)
		} else {
			params := &telego.SendMessageParams{
				ChatID:          telego.ChatID{ID: chatID},
				Text:            escapedTextOrCaption,
				ParseMode:       telego.ModeMarkdownV2, // Use MarkdownV2 for backticks etc.
				MessageThreadID: messageThreadID,
				// DisableWebPagePreview: true, // Optional
			}
			log.Printf("DEBUG: Attempting SendMessage %s (Attempt %d/%d)", logCtx, attempt+1, maxRetries)
			sentMsg, currentErr = localBot.SendMessage(ctx, params)
		}
		cancel()

		// --- Handle Result ---
		if currentErr == nil && sentMsg != nil && sentMsg.MessageID != 0 {
			log.Printf("INFO: Telegram message sent successfully %s (MsgID: %d)", logCtx, sentMsg.MessageID)
			return nil // Success
		}

		// --- Error Handling & Retry Logic ---
		lastErr = currentErr
		shouldRetry := false
		specificRetryAfter := 0
		// Removed unused variable: isPhotoError := false

		if currentErr != nil {
			var apiErr *telegoapi.Error
			if errors.As(currentErr, &apiErr) {
				log.Printf("WARN: Telegram API Error (Attempt %d/%d): Code=%d, Desc=%s %s",
					attempt+1, maxRetries, apiErr.ErrorCode, apiErr.Description, logCtx)

				if apiErr.ErrorCode == 429 && apiErr.Parameters != nil {
					specificRetryAfter = apiErr.Parameters.RetryAfter
					shouldRetry = true
					log.Printf("INFO: Rate limit hit, will retry after %d seconds %s", specificRetryAfter, logCtx)
				} else if apiErr.ErrorCode == 400 {
					nonRetryableSubstrings := []string{
						"thread not found", "can't parse entities", "chat not found",
						"wrong type of chat", "message text is empty",
					}
					photoErrorSubstrings := []string{
						"wrong file identifier", "Wrong remote file ID specified", "can't download file",
						"failed to get HTTP URL content", "PHOTO_INVALID_DIMENSIONS", "Photo dimensions are too small",
						"IMAGE_PROCESS_FAILED",
					}
					isNonRetryable := false
					for _, sub := range nonRetryableSubstrings {
						if strings.Contains(apiErr.Description, sub) {
							isNonRetryable = true
							break
						}
					}

					if isNonRetryable {
						shouldRetry = false
						log.Printf("ERROR: Non-retryable Telegram API error 400: %s. Aborting. %s", apiErr.Description, logCtx)
					} else {
						isKnownPhotoError := false
						if isPhoto { // Only check photo errors if we were sending a photo
							for _, sub := range photoErrorSubstrings {
								if strings.Contains(apiErr.Description, sub) {
									isKnownPhotoError = true
									break
								}
							}
						}

						if isKnownPhotoError { // implies isPhoto was true
							shouldRetry = false // Don't retry photo send if it's a known photo error
							log.Printf("WARN: Photo-specific error 400: %s. Will attempt fallback to text after retries. %s", apiErr.Description, logCtx)
							// Fallback logic happens *after* the loop based on lastErr
						} else {
							// Other 400 errors - might be temporary, markdown issue, etc. Don't retry.
							shouldRetry = false
							log.Printf("WARN: Uncategorized/potentially non-retryable API error 400: %s. Aborting retry. %s", apiErr.Description, logCtx)
						}
					}
				} else if apiErr.ErrorCode == 403 || apiErr.ErrorCode == 401 || apiErr.ErrorCode == 404 {
					shouldRetry = false
					log.Printf("ERROR: Non-retryable Telegram API error %d: %s. Aborting. %s", apiErr.ErrorCode, apiErr.Description, logCtx)
				} else { // Other API errors (e.g., 5xx) -> Retry
					shouldRetry = true
					log.Printf("INFO: Retrying potentially temporary Telegram API error %d: %s %s", apiErr.ErrorCode, apiErr.Description, logCtx)
				}
			} else { // Network or other non-API error
				log.Printf("WARN: Failed Telegram send (Attempt %d/%d): Network/Other error: %v %s",
					attempt+1, maxRetries, currentErr, logCtx)
				shouldRetry = true
			}
		} else {
			log.Printf("WARN: Telegram send attempt %d/%d seemed to succeed but returned no message ID. Retrying cautiously. %s", attempt+1, maxRetries, logCtx)
			lastErr = errors.New("send succeeded according to API but message ID missing")
			shouldRetry = true
		}

		// --- Exit or Wait for Retry ---
		if !shouldRetry || attempt >= maxRetries-1 {
			if shouldRetry && attempt >= maxRetries-1 {
				log.Printf("ERROR: Max retries (%d) reached for Telegram send. Aborting. %s", maxRetries, logCtx)
			}
			break // Exit loop
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

	// --- Final Check and Fallback ---
	if lastErr != nil {
		log.Printf("ERROR: Telegram message FAILED to send after %d retries. Last Error: %v. %s", maxRetries, lastErr, logCtx)
		// Check if the original attempt was a photo AND the last error indicates a photo-specific issue
		if isPhoto {
			var lastApiErr *telegoapi.Error
			photoErrorSubstrings := []string{
				"wrong file identifier", "Wrong remote file ID specified", "can't download file",
				"failed to get HTTP URL content", "PHOTO_INVALID_DIMENSIONS", "Photo dimensions are too small",
				"IMAGE_PROCESS_FAILED",
			}
			if errors.As(lastErr, &lastApiErr) && lastApiErr.ErrorCode == 400 {
				isKnownPhotoError := false
				for _, sub := range photoErrorSubstrings {
					if strings.Contains(lastApiErr.Description, sub) {
						isKnownPhotoError = true
						break
					}
				}
				if isKnownPhotoError {
					log.Printf("INFO: Final error indicates photo issue. Falling back to sending caption as text message. %s", logCtx)
					// Recursive call - send as text. NO retry loop needed here as this is the fallback.
					return coreSendMessageWithRetry(chatID, messageThreadID, rawTextOrCaption, false, "")
				}
			}
		}
	}

	return lastErr // Return the last error encountered, or nil if successful
}

// SendTelegramMessage sends a standard text message to the default group.
func SendTelegramMessage(message string) {
	// Discard error for simple fire-and-forget logging
	_ = coreSendMessageWithRetry(defaultGroupID, 0, message, false, "")
}

// SendBotCallMessage sends a text message to the Bot Calls topic thread.
func SendBotCallMessage(message string) {
	threadID := env.BotCallsThreadID
	if threadID == 0 {
		log.Println("WARN: Attempted to send to Bot Calls topic, but BOT_CALLS_THREAD_ID is not set. Sending to main group chat.")
		// threadID remains 0 to send to main group
	}
	_ = coreSendMessageWithRetry(defaultGroupID, threadID, message, false, "")
}

// SendBotCallPhotoMessage sends a photo with caption to the Bot Calls topic thread.
func SendBotCallPhotoMessage(photoURL string, caption string) {
	threadID := env.BotCallsThreadID
	if threadID == 0 {
		log.Println("WARN: Attempted to send photo to Bot Calls topic, but BOT_CALLS_THREAD_ID is not set. Sending caption as text to main group chat.")
		_ = coreSendMessageWithRetry(defaultGroupID, 0, caption, false, "") // Fallback to text in main group
		return
	}
	// Basic URL validation before attempting send
	parsedURL, urlErr := url.ParseRequestURI(photoURL)
	if urlErr != nil || !(parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
		log.Printf("ERROR: Invalid photo URL format: %s - %v. Falling back to sending caption as text message.", photoURL, urlErr)
		_ = coreSendMessageWithRetry(defaultGroupID, threadID, caption, false, "") // Send caption as text instead to the correct thread
		return
	}
	_ = coreSendMessageWithRetry(defaultGroupID, threadID, caption, true, photoURL)
}

// SendTrackingUpdateMessage sends a text message to the Tracking topic thread.
func SendTrackingUpdateMessage(message string) {
	threadID := env.TrackingThreadID
	if threadID == 0 {
		log.Println("WARN: Attempted to send to Tracking topic, but TRACKING_THREAD_ID is not set. Sending to main group chat.")
		// threadID remains 0 to send to main group
	}
	_ = coreSendMessageWithRetry(defaultGroupID, threadID, message, false, "")
}

// SendVerificationSuccessMessage sends a direct message to a user upon successful verification.
func SendVerificationSuccessMessage(userID int64, groupLink string) error {
	if groupLink == "" {
		log.Println("ERROR: Attempted to send success message, but target group link (TARGET_GROUP_LINK) is empty.")
		return errors.New("target group link is empty")
	}

	// Escape the link text *and* the special characters in the surrounding text
	// Note: Links themselves usually don't need escaping in MarkdownV2 `[text](url)`
	escapedGroupLink := groupLink // URL itself doesn't need escaping
	// Ensure ! and . are escaped for MarkdownV2
	messageText := fmt.Sprintf("✅ Verification Successful\\! You now have access\\.\n\nJoin the group here: [Click to Join](%s)", escapedGroupLink)

	localBot := GetBotInstance()
	if localBot == nil {
		log.Printf("ERROR: Cannot send verification success message to user %d: Bot instance is nil.", userID)
		return errors.New("bot instance is nil")
	}

	// Send directly to the user (ChatID = userID, no thread ID)
	err := coreSendMessageWithRetry(userID, 0, messageText, false, "")

	if err != nil {
		log.Printf("ERROR: Failed to send verification success message to user %d: %v", userID, err)
	} else {
		log.Printf("INFO: Verification success message sent to user %d.", userID)
	}
	return err
}
