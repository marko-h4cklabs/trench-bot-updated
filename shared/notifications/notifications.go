package notifications

import (
	"ca-scraper/shared/env"
	"context"
	"fmt"
	"log"
	"math"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"golang.org/x/time/rate"
)

var bot *tgbotapi.BotAPI
var isInitialized bool = false
var telegramLimiter *rate.Limiter

func InitTelegramBot() error {
	if isInitialized && bot != nil {
		log.Println("INFO: Telegram bot already initialized.")
		return nil
	}

	isInitialized = false
	bot = nil
	telegramLimiter = nil

	botToken := env.TelegramBotToken
	groupID := env.TelegramGroupID

	if botToken == "" {
		return fmt.Errorf("critical error: TELEGRAM_BOT_TOKEN missing from env configuration")
	}
	if groupID == 0 {
		return fmt.Errorf("critical error: TELEGRAM_GROUP_ID missing or invalid in env configuration")
	}
	log.Println("Initializing Telegram bot API...")
	var err error

	bot, err = tgbotapi.NewBotAPI(botToken)
	if err != nil {
		bot = nil
		return fmt.Errorf("failed to initialize Telegram bot API: %w", err)
	}
	log.Println("Verifying bot token with Telegram API (GetMe)...")
	userInfo, err := bot.GetMe()
	if err != nil {
		bot = nil
		return fmt.Errorf("failed to verify bot token with GetMe API call: %w", err)
	}
	isInitialized = true
	telegramLimiter = rate.NewLimiter(rate.Limit(0.2), 1)
	log.Printf("Telegram bot initialized successfully for @%s", userInfo.UserName)
	log.Printf("Telegram rate limiter initialized (1 msg / 5 sec)")

	escapedUsername := EscapeMarkdownV2(userInfo.UserName)
	startupMessageFormatted := fmt.Sprintf("Bot connected successfully \\(@%s\\)\\. Ready\\.", escapedUsername)
	SendSystemLogMessage(startupMessageFormatted)

	return nil
}

func GetBotInstance() *tgbotapi.BotAPI {
	if !isInitialized || bot == nil {
		log.Println("WARN: GetBotInstance called but bot is not initialized or initialization failed.")
	}
	return bot
}

func SendTelegramMessage(message string) {
	sendMessageWithRetry(env.TelegramGroupID, 0, message)
}

func SendSystemLogMessage(message string) {
	targetChatID := env.TelegramGroupID
	targetThreadID := env.SystemLogsThreadID

	sendMessageWithRetry(targetChatID, targetThreadID, message)
}

func LogToTelegram(message string) {
	SendSystemLogMessage(message)
}

func LogTokenPair(pairAddress, url, baseToken, quoteToken string, liquidity, volume float64, buys, sells int, tokenAge string) {
	message := fmt.Sprintf(
		` *Token Pair Found\!* 
 *Token Address:* [%s](%s)
 *Pair:* %s / %s
 *Liquidity:* \$%.2f
 *5\-Min Volume:* \$%.2f
 *Buys:* %d \|  *Sells:* %d
 *Token Age:* %s`,
		EscapeMarkdownV2(pairAddress), EscapeMarkdownV2(url), EscapeMarkdownV2(baseToken), EscapeMarkdownV2(quoteToken), liquidity, volume, buys, sells, EscapeMarkdownV2(tokenAge),
	)

	SendTelegramMessage(message)
}

func sendMessageWithRetry(chatID int64, messageThreadID int, text string) {
	if telegramLimiter == nil {
		log.Println("WARN: Telegram rate limiter not initialized! Sending without global limit check.")
	} else {
		log.Printf("DEBUG: Waiting for Telegram rate limiter token (ChatID: %d)...", chatID)
		if err := telegramLimiter.Wait(context.Background()); err != nil {
			log.Printf("ERROR: Telegram rate limiter wait error for chat %d: %v. Proceeding with send attempt...", chatID, err)
		} else {
			log.Printf("DEBUG: Telegram rate limiter token acquired (ChatID: %d). Proceeding with send.", chatID)
		}
	}
	if bot == nil {
		log.Println("ERROR: Cannot send message, Telegram bot is not initialized.")
		return
	}
	if chatID == 0 {
		log.Println("ERROR: Cannot send message, target chatID is 0.")
		return
	}

	logCtx := fmt.Sprintf("[ChatID: %d, ThreadID Attempted: %d]", chatID, messageThreadID)

	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdownV2

	if messageThreadID != 0 {
		log.Printf("WARN: MessageThreadID feature potentially unavailable. Sending to main chat %d instead of thread %d. %s", chatID, messageThreadID, logCtx)
	}

	maxRetries := 3
	var lastErr error
	for i := 0; i < maxRetries; i++ {
		_, err := bot.Send(msg)
		if err == nil {
			return
		}

		lastErr = err

		if tgErr, ok := err.(*tgbotapi.Error); ok {
			log.Printf("ERROR: Failed Telegram send (Attempt %d/%d): API Err %d - %s %s",
				i+1, maxRetries, tgErr.Code, tgErr.Message, logCtx)

			if tgErr.Code == 429 {
				retryAfter := tgErr.RetryAfter
				if retryAfter <= 0 {
					retryAfter = 1
				}
				log.Printf("INFO: Telegram API rate limit hit (429). Retrying after %d seconds... %s", retryAfter, logCtx)
				time.Sleep(time.Duration(retryAfter) * time.Second)
				continue
			}
			if tgErr.Code == 400 && strings.Contains(tgErr.Message, "message thread not found") {
				log.Printf("INFO: Ignoring 'message thread not found' error (MessageThreadID workaround active?). %s", logCtx)
			}
		} else {
			log.Printf("ERROR: Failed Telegram send (Attempt %d/%d): General Error %v %s",
				i+1, maxRetries, err, logCtx)
		}

		if i < maxRetries-1 {
			waitDuration := time.Duration(math.Pow(2, float64(i))) * time.Second
			if waitDuration < time.Second {
				waitDuration = time.Second
			}
			log.Printf("INFO: Retrying failed send in %v... %s", waitDuration, logCtx)
			time.Sleep(waitDuration)
		}
	}
	log.Printf("ERROR: Telegram message failed to send after %d retries. Last Error: %v. %s", maxRetries, lastErr, logCtx)
}

func EscapeMarkdownV2(s string) string {
	charsToEscape := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	temp := s
	for _, char := range charsToEscape {
		temp = strings.ReplaceAll(temp, char, "\\"+char)
	}
	return temp
}
