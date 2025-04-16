package services

import (
	"log"
	"sync"
)

type WebhookCache struct {
	sync.Mutex
	WebhookID string
	Exists    bool
}

func LogHealth() {
	log.Println("Health API called")
}

func LogAnalyse() {
	log.Println("Analyse API called")
}

func LogListen() {
	log.Println("Listen API called")
}

func LogFilter() {
	log.Println("Filter API called")
}
