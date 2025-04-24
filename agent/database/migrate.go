// In database/migrate.go
package database

import (
	// Remove "ca-scraper/agent/internal/models" if not needed elsewhere in this file
	"database/sql" // Need database/sql for migrate library source
	"errors"
	"log"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres" // Import postgres driver for migrate
	_ "github.com/golang-migrate/migrate/v4/source/file"     // Import file source driver for migrate
	_ "github.com/lib/pq"                                    // Import the PostgreSQL driver for database/sql
)

// Removed global var DB *gorm.DB

// MigrateDatabase runs SQL migrations from the specified directory.
func MigrateDatabase(dsn string) {
	log.Println("Connecting to database with database/sql for migrations...")
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to connect to database for migration: %v", err)
	}
	defer func() {
		if cerr := db.Close(); cerr != nil {
			log.Printf("WARN: Error closing migration db connection: %v", cerr)
		}
	}()

	// Ping DB to ensure connection is valid
	if err = db.Ping(); err != nil {
		log.Fatalf("Failed to ping database for migration: %v", err)
	}
	log.Println("Database connection established for migration.")

	log.Println("Initializing migration driver...")
	// Configure the migrate instance to use the PostgreSQL driver
	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		log.Fatalf("Failed to create postgres migration driver instance: %v", err)
	}

	// Point migrate to the directory containing your SQL files
	// Use "file://" prefix. Adjust the path relative to where your app runs.
	// If running from project root, 'agent/database/migrations' should work.
	migrationPath := "file://agent/database/migrations"
	log.Printf("Looking for migrations in: %s", migrationPath)

	m, err := migrate.NewWithDatabaseInstance(
		migrationPath,
		"postgres", // Specify the database type name used by the driver
		driver,
	)
	if err != nil {
		log.Fatalf("Failed to initialize migrate instance: %v", err)
	}

	log.Println("Running database migrations up...")
	// Apply all pending "up" migrations
	err = m.Up()

	// Check the result
	if err != nil {
		// migrate.ErrNoChange means migrations were already up-to-date
		if errors.Is(err, migrate.ErrNoChange) {
			log.Println("Database schema is already up to date. No changes applied.")
		} else {
			// Report any other migration error
			log.Fatalf("Failed to apply database migrations: %v", err)
		}
	} else {
		log.Println("Database migrations applied successfully.")
	}

	// Log the current migration version (optional)
	version, dirty, vErr := m.Version()
	if vErr != nil {
		log.Printf("WARN: Could not get migration version after applying: %v", vErr)
	} else {
		log.Printf("Current migration version: %d, Dirty: %t", version, dirty)
		if dirty {
			log.Println("WARN: Migration state is dirty. This might indicate a previously failed migration.")
		}
	}
}
