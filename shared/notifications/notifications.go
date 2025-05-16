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
	defaultGroupID  int64 // This will store env.TelegramGroupID
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
	parsedGroupID := env.TelegramGroupID // Use the loaded env variable

	if botToken == "" {
		log.Println("WARN: TELEGRAM_BOT_TOKEN missing. Telegram notifications disabled.")
		isInitialized = false
		bot = nil
		return nil // Explicitly return nil if notifications are just disabled
	}
	if parsedGroupID == 0 {
		log.Println("WARN: TELEGRAM_GROUP_ID missing or invalid (0). Telegram notifications to primary group disabled.")
		// Continue initialization if token is present, but defaultGroupID will be 0
		// Specific functions will need to check if defaultGroupID is valid before sending.
	}
	defaultGroupID = parsedGroupID // Store the loaded primary group ID

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
	if env.TrackingThreadID != 0 {
		log.Printf("INFO: Tracking Thread ID: %d", env.TrackingThreadID)
	}
	if env.SecondaryBotCallsChatID != 0 {
		log.Printf("INFO: Secondary Bot Calls Target Chat ID: %d", env.SecondaryBotCallsChatID)
		log.Printf("INFO: Secondary Bot Calls Target Thread ID: %d", env.SecondaryBotCallsThreadID)
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
		if charStr == "`" { // Preserve backticks for manual code formatting
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
		} // Avoid overly long log lines
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
			var apiErr *telegoapi.Error
			if errors.As(currentErr, &apiErr) {
				log.Printf("ERROR_DETAILS: API Error Code: %d, Description: %s, Parameters: %+v %s", apiErr.ErrorCode, apiErr.Description, apiErr.Parameters, logCtx)
			}
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
					photoErrorSubstrings := []string{"wrong file identifier", "Wrong remote file ID specified", "can't download file", "failed to get HTTP URL content", "PHOTO_INVALID_DIMENSIONS", "Photo dimensions are too small", "IMAGE_PROCESS_FAILED", "wrong type of the web page content"}
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
						log.Printf("INFO: Known photo error detected (%s). Attempting fallback to text for this call. %s", apiErr.Description, logCtx)
						fallbackErr := coreSendMessageWithRetry(chatID, messageThreadID, rawTextOrCaption, false, "", replyMarkup)
						if fallbackErr != nil {
							log.Printf("ERROR: Fallback to text ALSO FAILED for this call. Original Error: %v. Fallback Error: %v. %s", currentErr, fallbackErr, logCtx)
							lastErr = currentErr
							shouldRetry = false
						} else {
							log.Printf("INFO: Fallback to text succeeded for this call. %s", logCtx)
							return nil
						}
					} else {
						log.Printf("WARN: Unhandled 400 Bad Request: %s. Not retrying this attempt. %s", apiErr.Description, logCtx)
						shouldRetry = false
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

		if !shouldRetry || attempt >= maxRetries-1 {
			if shouldRetry && attempt >= maxRetries-1 {
				log.Printf("ERROR: Max retries (%d) reached. Aborting send for this call. %s", maxRetries, logCtx)
			} else if !shouldRetry {
				log.Printf("ERROR: Non-retryable error or fallback failed for this call. Aborting retry loop. %s", logCtx)
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

func buildReplyMarkup(buttons ...map[string]string) telego.ReplyMarkup {
	if len(buttons) > 0 && len(buttons[0]) > 0 {
		var rows [][]telego.InlineKeyboardButton
		var currentRow []telego.InlineKeyboardButton
		// Simple layout: put all buttons in one row. Adjust if more complex layout needed.
		for label, urlValue := range buttons[0] {
			currentRow = append(currentRow, telego.InlineKeyboardButton{Text: label, URL: urlValue})
		}
		if len(currentRow) > 0 {
			rows = append(rows, currentRow)
			return &telego.InlineKeyboardMarkup{InlineKeyboard: rows}
		}
	}
	return nil
}

func SendBotCallMessage(message string, buttons ...map[string]string) {
	replyMarkup := buildReplyMarkup(buttons...)

	// Send to primary destination
	if defaultGroupID != 0 {
		primaryThreadID := env.BotCallsThreadID
		if primaryThreadID == 0 {
			log.Println("WARN: Primary Bot Calls thread ID not set, sending to main topic of primary group.")
		}
		log.Printf("INFO: Sending Bot Call message to primary destination. ChatID: %d, ThreadID: %d", defaultGroupID, primaryThreadID)
		_ = coreSendMessageWithRetry(defaultGroupID, primaryThreadID, message, false, "", replyMarkup)
	} else {
		log.Println("WARN: Primary Telegram Group ID (defaultGroupID) not set. Cannot send primary bot call message.")
	}

	// Send to secondary destination
	if env.SecondaryBotCallsChatID != 0 {
		secondaryThreadID := env.SecondaryBotCallsThreadID
		log.Printf("INFO: Sending duplicate Bot Call message to secondary destination. ChatID: %d, ThreadID: %d", env.SecondaryBotCallsChatID, secondaryThreadID)
		_ = coreSendMessageWithRetry(env.SecondaryBotCallsChatID, secondaryThreadID, message, false, "", replyMarkup)
	}
}

// Helper to send text specifically to secondary, used by SendBotCallPhotoMessage fallback
func sendBotCallTextMessageToSecondary(caption string, buttons ...map[string]string) {
	if env.SecondaryBotCallsChatID == 0 {
		log.Println("WARN: sendBotCallTextMessageToSecondary: SecondaryBotCallsChatID is 0. Skipping.")
		return
	}
	secondaryThreadID := env.SecondaryBotCallsThreadID
	replyMarkup := buildReplyMarkup(buttons...)
	log.Printf("INFO: Sending Bot Call (as text due to photo issue) to secondary destination. ChatID: %d, ThreadID: %d", env.SecondaryBotCallsChatID, secondaryThreadID)
	_ = coreSendMessageWithRetry(env.SecondaryBotCallsChatID, secondaryThreadID, caption, false, "", replyMarkup)
}

func SendBotCallPhotoMessage(photoURL string, caption string, buttons ...map[string]string) {
	replyMarkup := buildReplyMarkup(buttons...)
	photoValid := true

	if photoURL == "" {
		photoValid = false
		log.Printf("ERROR: SendBotCallPhotoMessage: photoURL is empty. Will send as text to all configured destinations.")
	} else {
		parsedURL, urlErr := url.ParseRequestURI(photoURL)
		if urlErr != nil || !(parsedURL.Scheme == "http" || parsedURL.Scheme == "https") {
			photoValid = false
			log.Printf("ERROR: SendBotCallPhotoMessage: Invalid photo URL format: %s - %v. Will send as text to all configured destinations.", photoURL, urlErr)
		}
	}

	if !photoValid {
		// If photo URL itself is invalid, send text to both primary and secondary.
		SendBotCallMessage(caption, buttons...) // This handles both primary and secondary text sends
		return
	}

	// Attempt to send photo to primary destination
	if defaultGroupID != 0 {
		primaryThreadID := env.BotCallsThreadID
		if primaryThreadID == 0 {
			log.Println("WARN: Primary Bot Calls thread ID not set for photo, sending to main topic of primary group.")
		}

		log.Printf("INFO: Attempting to send Bot Call photo message to primary destination. ChatID: %d, ThreadID: %d", defaultGroupID, primaryThreadID)
		// coreSendMessageWithRetry will attempt fallback to text for this specific call if photo send fails
		errPrimary := coreSendMessageWithRetry(defaultGroupID, primaryThreadID, caption, true, photoURL, replyMarkup)
		if errPrimary != nil {
			log.Printf("WARN: Sending photo to primary destination (ChatID: %d) ultimately failed (after retries/fallbacks). Error: %v", defaultGroupID, errPrimary)
		}
	} else {
		log.Println("WARN: Primary Telegram Group ID (defaultGroupID) not set. Cannot send primary bot call photo message.")
	}

	// Attempt to send photo to secondary destination
	if env.SecondaryBotCallsChatID != 0 {
		secondaryThreadID := env.SecondaryBotCallsThreadID
		log.Printf("INFO: Attempting to send duplicate Bot Call photo message to secondary destination. ChatID: %d, ThreadID: %d", env.SecondaryBotCallsChatID, secondaryThreadID)
		// coreSendMessageWithRetry will attempt fallback to text for this specific call if photo send fails
		errSecondary := coreSendMessageWithRetry(env.SecondaryBotCallsChatID, secondaryThreadID, caption, true, photoURL, replyMarkup)
		if errSecondary != nil {
			log.Printf("WARN: Sending photo to secondary destination (ChatID: %d) ultimately failed (after retries/fallbacks). Error: %v", env.SecondaryBotCallsChatID, errSecondary)
		}
	}
}

func SendTrackingUpdateMessage(message string) {
	if defaultGroupID == 0 {
		log.Println("WARN: SendTrackingUpdateMessage: defaultGroupID is 0, cannot send message.")
		return
	}
	threadID := env.TrackingThreadID
	if threadID == 0 {
		log.Println("WARN: Tracking thread ID not set, sending to main topic of primary group.")
	}
	_ = coreSendMessageWithRetry(defaultGroupID, threadID, message, false, "", nil)
}

func SendVerificationSuccessMessage(userID int64, groupLink string) error {
	if groupLink == "" {
		log.Println("ERROR: Target group link empty for verification success msg.")
		return errors.New("target group link empty")
	}
	messageText := fmt.Sprintf("✅ Verification Successful\\! You now have access\\.\n\nJoin the group here: [%s](%s)", EscapeMarkdownV2("Click to Join"), groupLink) // Group link itself is not escaped
	localBot := GetBotInstance()
	if localBot == nil {
		log.Printf("ERROR: Bot nil for verification success msg to user %d.", userID)
		return errors.New("bot instance nil.")
	}
	err := coreSendMessageWithRetry(userID, 0, messageText, false, "", nil) // DMs don't use threads
	if err != nil {
		log.Printf("ERROR: Failed to send verification success msg to user %d: %v", userID, err)
	} else {
		log.Printf("INFO: Verification success message sent to user %d.", userID)
	}
	return err
}
