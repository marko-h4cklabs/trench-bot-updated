package database

import (
	"ca-scraper/agent/internal/models"
	"database/sql"
	"log"
	"os"

	_ "github.com/lib/pq"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

var DB *gorm.DB

// MigrateDatabase handles database migrations using GORM's AutoMigrate and raw SQL as a fallbac
func MigrateDatabase(dsn string) {
	// Determine environment
	env := os.Getenv("APP_ENV")
	log.Printf("Running migrations for environment: %s", env)

	// Use GORM to connect to the database
	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to the database with GORM: %v", err)
	}

	// Run GORM migrations for the models
	log.Println("Running GORM migrations...")
	err = DB.AutoMigrate(&models.User{}, &models.BuyBotData{}, &models.Filter{})
	if err != nil {
		log.Fatalf("Failed to perform GORM migrations: %v", err)
	}
	log.Println("GORM migrations executed successfully.")

	// Use raw SQL migrations as a safety fallback
	dbSQL, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to the database with SQL: %v", err)
	}
	defer dbSQL.Close()

	executeSQLMigrations(dbSQL, env)
}

// executeSQLMigrations performs raw SQL migrations as a fallback
func executeSQLMigrations(db *sql.DB, env string) {
	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
            id SERIAL PRIMARY KEY,
            wallet_id TEXT UNIQUE NOT NULL,
            nft_status BOOLEAN NOT NULL,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );`,
		`CREATE TABLE IF NOT EXISTS buy_bot_data (
            id SERIAL PRIMARY KEY,
            contract TEXT NOT NULL,
            volume FLOAT NOT NULL,
            buys INT NOT NULL,
            sells INT NOT NULL,
            collected_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            verified BOOLEAN DEFAULT FALSE
        );`,
		`CREATE TABLE IF NOT EXISTS filters (
            id SERIAL PRIMARY KEY,
            name TEXT NOT NULL,
            description TEXT NOT NULL,
            criteria JSONB NOT NULL,
            created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
            updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
        );`,
	}

	// Apply environment-specific constraints
	if env == "production" || env == "staging" {
		queries = append(queries,
			`ALTER TABLE buy_bot_data ADD CONSTRAINT verified_default CHECK (verified IN (true, false));`,
		)
	}

	// Execute each query
	for _, query := range queries {
		_, err := db.Exec(query)
		if err != nil {
			log.Fatalf("Failed to execute query: %s, error: %v", query, err)
		}
	}
	log.Println("Raw SQL migrations executed successfully.")
}
