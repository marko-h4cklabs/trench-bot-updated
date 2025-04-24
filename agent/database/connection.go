package database

import (
	"log"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func ConnectToDatabase(dsn string) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Printf("ERROR: Failed to connect to the database using DSN: %v", err)
		return nil, err
	}
	log.Println("INFO: Database connection successful.")
	return db, nil
}
