package services

import (
	"log"
	"sync"
)

// WebhookCache stores webhook ID to avoid redundant API calls
type WebhookCache struct {
	sync.Mutex
	WebhookID string
	Exists    bool
}

// LogHealth logs the /health API activity
func LogHealth() {
	log.Println("Health API called")
}

// LogAnalyse logs the /analyse API activity
func LogAnalyse() {
	log.Println("Analyse API called")
}

// LogListen logs the /listen API activity
func LogListen() {
	log.Println("Listen API called")
}

// LogFilter logs the /filter API activity
func LogFilter() {
	log.Println("Filter API called")
}
