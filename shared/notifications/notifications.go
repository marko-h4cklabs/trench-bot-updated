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
	telegramMessagesPerSecond = 0.8 // Slightly conservative limit
	telegramBurstLimit        = 2
	maxRetries                = 3
	baseRetryWait             = 1 * time.Second
)

// --- InitTelegramBot (unchanged) ---
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

	// Log thread IDs for clarity
	log.Printf("INFO: System Logs Thread ID: %d", env.SystemLogsThreadID)
	log.Printf("INFO: Scanner Logs Thread ID: %d", env.ScannerLogsThreadID)
	log.Printf("INFO: Potential CA Thread ID: %d", env.PotentialCAThreadID)
	log.Printf("INFO: Bot Calls Thread ID: %d", env.BotCallsThreadID)
	log.Printf("INFO: Tracking Thread ID: %d", env.TrackingThreadID) // Log the new one

	return nil
}

// --- GetBotInstance (unchanged) ---
func GetBotInstance() *tgbotapi.BotAPI {
	initMutex.Lock()
	defer initMutex.Unlock()
	if !isInitialized || bot == nil {
		log.Println("WARN: GetBotInstance called but bot is not properly initialized.")
		return nil
	}
	return bot
}

// --- Existing Send functions (SendTelegramMessage, SendSystemLogMessage, etc.) unchanged ---
func SendTelegramMessage(message string) { // Example, others are similar
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
	// Escape the entire message right before sending
	escapedMessage := EscapeMarkdownV2(message)
	coreSendMessageWithRetry(defaultGroupID, env.BotCallsThreadID, escapedMessage, false, "")
}

func SendBotCallPhotoMessage(photoURL string, caption string) {
	if env.BotCallsThreadID == 0 {
		log.Println("WARN: Attempted to send photo to Bot Calls topic, but BOT_CALLS_THREAD_ID is not set.")
		return
	}

	// *** THIS IS THE CRITICAL LINE ***
	// Escape the entire caption right before sending or falling back
	escapedCaption := EscapeMarkdownV2(caption)

	// Basic URL validation
	if _, err := url.ParseRequestURI(photoURL); err != nil {
		log.Printf("ERROR: Invalid photo URL provided for Bot Call: %s - %v. Falling back to sending caption as text.", photoURL, err)
		// Fallback uses the *escaped* caption
		coreSendMessageWithRetry(defaultGroupID, env.BotCallsThreadID, escapedCaption, false, "")
		return
	}
	// Send photo uses the *escaped* caption
	coreSendMessageWithRetry(defaultGroupID, env.BotCallsThreadID, escapedCaption, true, photoURL)
}

// *** NEW: Function to send messages to the Tracking topic ***
func SendTrackingUpdateMessage(message string) {
	if env.TrackingThreadID == 0 {
		log.Println("WARN: Attempted to send to Tracking topic, but TRACKING_THREAD_ID is not set.")
		return
	}
	// Escape the entire message right before sending
	escapedMessage := EscapeMarkdownV2(message)
	coreSendMessageWithRetry(defaultGroupID, env.TrackingThreadID, escapedMessage, false, "")
}

// --- coreSendMessageWithRetry (unchanged) ---
// This function handles the actual sending, rate limiting, and retries.
func coreSendMessageWithRetry(chatID int64, messageThreadID int, textOrCaption string, isPhoto bool, photoURL string) {
	// ... (existing code remains the same) ...
	initMutex.Lock()
	localIsInitialized := isInitialized
	localBot := bot
	initMutex.Unlock()

	if !localIsInitialized || localBot == nil {
		// Avoid spamming logs if Telegram is intentionally disabled
		if env.TelegramBotToken != "" && env.TelegramGroupID != 0 {
			log.Printf("WARN: Attempted to send Telegram message (ChatID: %d, ThreadID: %d) but bot is not initialized.", chatID, messageThreadID)
		}
		return
	}

	// Rate Limiter Check
	if telegramLimiter == nil {
		// Should not happen if Init is correct, but safeguard
		log.Println("ERROR: Telegram rate limiter is nil! Sending without limit check.")
	} else {
		// Wait for the limiter
		ctxWait, cancelWait := context.WithTimeout(context.Background(), 30*time.Second) // 30s max wait for rate limit
		err := telegramLimiter.Wait(ctxWait)
		cancelWait()
		if err != nil {
			// Log if waiting failed (e.g., context deadline exceeded)
			log.Printf("ERROR: Telegram rate limiter wait error for chat %d, thread %d: %v. Proceeding cautiously...", chatID, messageThreadID, err)
			// Don't return, try sending anyway, but be aware limit might be exceeded
		}
	}

	var lastErr error
	logCtx := fmt.Sprintf("[ChatID: %d]", chatID)
	if messageThreadID != 0 {
		logCtx = fmt.Sprintf("%s[ThreadID: %d]", logCtx, messageThreadID)
	} else {
		logCtx = fmt.Sprintf("%s[ThreadID: 0 (Main)]", logCtx) // Indicate sending to main group if thread ID is 0
	}

	// Prepare parameters for the API call
	params := make(map[string]string)
	params["chat_id"] = fmt.Sprintf("%d", chatID)
	params["parse_mode"] = tgbotapi.ModeMarkdownV2 // Use MarkdownV2 for formatting

	// Add message_thread_id ONLY if it's non-zero
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

	// Retry logic
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Make the API request using MakeRequest for flexibility
		_, err := localBot.MakeRequest(apiMethod, params)

		if err == nil {
			// Success!
			// log.Printf("DEBUG: Telegram message sent successfully. %s", logCtx) // Optional debug log
			return // Exit the retry loop
		}

		// Store the error for potential logging after retries fail
		lastErr = err

		// Analyze the error
		var tgErr *tgbotapi.Error
		isAPIErr := errors.As(err, &tgErr)
		shouldRetry := true     // Assume retry unless specific error says otherwise
		specificRetryAfter := 0 // Seconds suggested by API 429 error

		if isAPIErr {
			// It's a Telegram API error
			log.Printf("ERROR: Failed Telegram send (Attempt %d/%d): API Err %d - %s %s",
				attempt+1, maxRetries, tgErr.Code, tgErr.Message, logCtx)

			if tgErr.Code == 429 { // Too Many Requests
				specificRetryAfter = tgErr.RetryAfter // Use API's suggested wait time
				// If API doesn't provide RetryAfter, use exponential backoff
				if specificRetryAfter <= 0 {
					specificRetryAfter = int(math.Pow(2, float64(attempt+1))) // Exponential backoff (2, 4, 8 seconds...)
				}
				// Cap max wait time to avoid excessively long waits
				maxWait := 60
				if specificRetryAfter > maxWait {
					specificRetryAfter = maxWait
				}
				log.Printf("INFO: Telegram API rate limit hit (%d). Retrying after %d seconds... %s", tgErr.Code, specificRetryAfter, logCtx)
			} else if tgErr.Code == 400 { // Bad Request - check for non-retryable specifics
				// List of common 400 errors that are unlikely to be fixed by retrying
				nonRetryable400s := []string{
					"thread not found",
					"can't parse entities", // Markdown issue
					"chat not found",
					"wrong file identifier",          // Bad photo URL/ID
					"Wrong remote file ID specified", // Bad photo URL/ID
					"can't download file",            // Issue fetching photo URL
					"failed to get HTTP URL content", // Issue fetching photo URL
					"PHOTO_INVALID_DIMENSIONS",       // Photo issue
					"wrong type of chat",             // Sending to a user instead of group?
				}
				isNonRetryable400 := false
				for _, substring := range nonRetryable400s {
					if strings.Contains(tgErr.Message, substring) {
						isNonRetryable400 = true
						break
					}
				}

				if isNonRetryable400 {
					shouldRetry = false
					log.Printf("WARN: Non-retryable Telegram API error 400: %s. Aborting retries. %s", tgErr.Message, logCtx)

					// Special handling for photo errors: try sending caption as text
					if isPhoto && (strings.Contains(tgErr.Message, "failed to get HTTP URL content") ||
						strings.Contains(tgErr.Message, "wrong file identifier") ||
						strings.Contains(tgErr.Message, "Wrong remote file ID specified") ||
						strings.Contains(tgErr.Message, "can't download file")) {
						log.Printf("INFO: Falling back to sending caption as text message due to photo URL/fetch error. %s", logCtx)
						// Recursive call WITHOUT photo flag - BE CAREFUL with recursion depth, but should be fine here.
						coreSendMessageWithRetry(chatID, messageThreadID, textOrCaption, false, "")
						return // Stop processing the original photo message
					}
				} else {
					// Other 400 errors might be temporary, log but maybe don't retry? Or retry once?
					// Let's default to not retrying generic 400s unless known to be transient.
					shouldRetry = false
					log.Printf("WARN: Potentially non-retryable Telegram API error 400: %s. Aborting retry. %s", tgErr.Message, logCtx)
				}
			} else if tgErr.Code == 403 || tgErr.Code == 401 || tgErr.Code == 404 { // Forbidden, Unauthorized, Not Found
				// These usually indicate permission issues, bad bot token, or wrong chat ID - not temporary
				shouldRetry = false
				log.Printf("WARN: Non-retryable Telegram API error %d: %s. Aborting retries. %s", tgErr.Code, tgErr.Message, logCtx)
			}
			// Other API errors might be temporary network issues, default to retry

		} else {
			// Not a Telegram API error (e.g., network timeout, DNS issue)
			log.Printf("ERROR: Failed Telegram send (Attempt %d/%d): Network/Other error: %v %s",
				attempt+1, maxRetries, err, logCtx)
			shouldRetry = true // Network errors are worth retrying
		}

		// Check if we should stop retrying
		if !shouldRetry || attempt >= maxRetries-1 {
			if shouldRetry && attempt >= maxRetries-1 { // Log if max retries hit for a retryable error
				log.Printf("WARN: Max retries (%d) reached for Telegram send. Aborting. %s", maxRetries, logCtx)
			}
			break // Exit the retry loop
		}

		// Calculate wait duration
		var waitDuration time.Duration
		if specificRetryAfter > 0 {
			waitDuration = time.Duration(specificRetryAfter) * time.Second
		} else {
			// Exponential backoff for general retries
			waitDuration = baseRetryWait * time.Duration(math.Pow(2, float64(attempt)))
		}

		log.Printf("INFO: Retrying failed Telegram send in %v... %s", waitDuration, logCtx)
		time.Sleep(waitDuration) // Wait before the next attempt
	} // End retry loop

	// Log final failure only if an error persisted after all retries
	if lastErr != nil {
		log.Printf("ERROR: Telegram message FAILED to send after %d retries. Last Error: %v. %s", maxRetries, lastErr, logCtx)
		// Final fallback for photo errors if all retries failed
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

// --- EscapeMarkdownV2 (unchanged) ---
func EscapeMarkdownV2(s string) string {
	// Characters to escape in Telegram MarkdownV2
	charsToEscape := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"} // Make sure '!' is here
	temp := s
	for _, char := range charsToEscape {
		temp = strings.ReplaceAll(temp, char, "\\"+char)
	}
	return temp
}
