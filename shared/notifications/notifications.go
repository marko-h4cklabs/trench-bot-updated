package notifications

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

var bot *tgbotapi.BotAPI
var botToken string
var groupID int64

func InitTelegramBot() error {
	botToken = os.Getenv("TELEGRAM_BOT_TOKEN")
	groupIDStr := os.Getenv("TELEGRAM_GROUP_ID")

	if botToken == "" {
		return fmt.Errorf("TELEGRAM_BOT_TOKEN environment variable is missing")
	}
	if groupIDStr == "" {
		return fmt.Errorf("TELEGRAM_GROUP_ID environment variable is missing")
	}

	var err error
	groupID, err = strconv.ParseInt(groupIDStr, 10, 64)
	if err != nil {
		return fmt.Errorf("failed to parse TELEGRAM_GROUP_ID: %v", err)
	}

	// Initialize the bot API FIRST
	bot, err = tgbotapi.NewBotAPI(botToken)
	if err != nil {
		// Bot initialization failed, set bot back to nil just in case
		bot = nil
		return fmt.Errorf("failed to initialize Telegram bot API: %v", err)
	}

	// Bot is potentially initialized, you can optionally verify with GetMe
	userInfo, err := bot.GetMe()
	if err != nil {
		bot = nil // Set back to nil if verification fails
		return fmt.Errorf("failed to verify bot token with GetMe: %v", err)
	}
	log.Printf(" Telegram bot initialized successfully for @%s", userInfo.UserName)

	// NOW send the test message, as 'bot' is confirmed non-nil
	SendTelegramMessage(fmt.Sprintf("✅ Bot connected successfully (@%s). Ready to send notifications.", userInfo.UserName))

	return nil // Successful initialization
}

// SendTelegramMessage remains the same, but will now work during init
func SendTelegramMessage(message string) {
	if bot == nil {
		log.Println(" Error: Telegram bot is nil. Cannot send message.")
		return
	}

	msg := tgbotapi.NewMessage(groupID, message)
	// Consider setting ParseMode globally or per message type if needed
	// msg.ParseMode = tgbotapi.ModeMarkdown // Example: Enable Markdown

	// Simple retry logic (consider exponential backoff for production)
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		_, err := bot.Send(msg)
		if err == nil {
			log.Printf(" Telegram message sent successfully to group %d.", groupID)
			return // Success
		}

		// Log specific Telegram errors if available
		if tgErr, ok := err.(*tgbotapi.Error); ok {
			log.Printf(" Failed to send Telegram message (Attempt %d/%d): API Error %d - %s", i+1, maxRetries, tgErr.Code, tgErr.Message)
			// Check for specific retryable errors if necessary (e.g., 429 Too Many Requests)
			if tgErr.Code == 429 {
				retryAfter := tgErr.RetryAfter
				if retryAfter <= 0 {
					retryAfter = 2 // Default delay
				}
				log.Printf(" Rate limit hit (429). Retrying after %d seconds...", retryAfter)
				time.Sleep(time.Duration(retryAfter) * time.Second)
				continue // Continue to next retry attempt
			}
		} else {
			// General network or other error
			log.Printf(" Failed to send Telegram message (Attempt %d/%d): %v", i+1, maxRetries, err)
		}

		// Optional: Delay before retrying non-rate-limit errors
		if i < maxRetries-1 {
			time.Sleep(1 * time.Second)
		}
	}

	log.Printf(" Error: Telegram message failed to send after %d retries.", maxRetries)
}

func LogToTelegram(message string) {
	SendTelegramMessage(message)
}

func LogTokenPair(pairAddress, url, baseToken, quoteToken string, liquidity, volume float64, buys, sells int, tokenAge string) {
	message := fmt.Sprintf(
		`🚀 *Token Pair Found!* 🚀
📜 *Token Address:* [%s](%s)
🔀 *Pair:* %s / %s
💧 *Liquidity:* $%.2f
💰 *5-Min Volume:* $%.2f
🛒 *Buys:* %d, *Sells:* %d
🕒 *Token Age:* %s`,
		pairAddress, url, baseToken, quoteToken, liquidity, volume, buys, sells, tokenAge,
	)

	SendTelegramMessage(message)
}
