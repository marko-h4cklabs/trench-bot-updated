package utils

import (
	"log"
	"os"
)

// GetDatabaseDSN returns the appropriate DSN based on the current environment
func GetDatabaseDSN() string {
	appEnv := GetEnv("APP_ENV", "development")

	var dsn string
	switch appEnv {
	case "production":
		dsn = os.Getenv("PROD_POSTGRES_DSN")
	case "test":
		dsn = os.Getenv("TEST_POSTGRES_DSN")
	default: // development
		dsn = os.Getenv("LOCAL_POSTGRES_DSN")
	}

	log.Printf("Resolved DSN for environment '%s': %s", appEnv, dsn)

	if dsn == "" {
		log.Fatalf("Database DSN is not configured for environment: %s", appEnv)
	}

	return dsn
}

// GetKnowledgeGraphAgentURL returns the Knowledge Graph Agent URL based on the current environment
func GetKnowledgeGraphAgentURL() string {
	appEnv := GetEnv("APP_ENV", "development")

	var url string
	switch appEnv {
	case "production":
		url = os.Getenv("PROD_KNOWLEDGEGRAPH_AGENT_URL")
	case "test":
		url = os.Getenv("TEST_KNOWLEDGEGRAPH_AGENT_URL")
	default: // development
		url = os.Getenv("LOCAL_KNOWLEDGEGRAPH_AGENT_URL")
	}

	log.Printf("Resolved Knowledge Graph Agent URL for environment '%s': %s", appEnv, url)

	if url == "" {
		log.Fatalf("Knowledge Graph Agent URL is not configured for environment: %s", appEnv)
	}

	return url
}

// GetEnv fetches environment variables with a fallback default
func GetEnv(key string, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// GetEnvOrPanic fetches an environment variable and panics if not set
func GetEnvOrPanic(key string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	log.Fatalf("Environment variable '%s' is not set and is required.", key)
	return ""
}

// GetEnvOrDefault fetches environment variables with a default value
func GetEnvOrDefault(key string, defaultValue string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return defaultValue
}

// GetWebhookListenerURL returns the appropriate webhook listener URL based on the current environment
func GetWebhookListenerURL() string {
	appEnv := GetEnv("APP_ENV", "development")

	var webhookURL string
	switch appEnv {
	case "production":
		webhookURL = os.Getenv("WEBHOOK_LISTENER_URL_PROD")
	case "test":
		webhookURL = os.Getenv("WEBHOOK_LISTENER_URL_DEV") // Use dev URL for tests as fallback
	default: // development
		webhookURL = os.Getenv("WEBHOOK_LISTENER_URL_DEV")
	}

	log.Printf("Resolved Webhook Listener URL for environment '%s': %s", appEnv, webhookURL)

	if webhookURL == "" {
		log.Fatalf("Webhook Listener URL is not configured for environment: %s", appEnv)
	}

	return webhookURL
}
