// In database/user_store.go

package database

import (
	"ca-scraper/agent/internal/models" // Use your actual path
	"errors"
	"log" // Use standard log or inject logger

	"gorm.io/gorm"
)

// IsUserVerified checks the database for the user's verification status.
// It returns true if verified, false otherwise. Handles errors.
// Creates the user record with default false if not found.
func IsUserVerified(db *gorm.DB, userID int64) (bool, error) {
	var user models.User

	// Find the user by TelegramUserID. If not found, GORM returns ErrRecordNotFound.
	// Use FirstOrCreate to handle users who might not exist yet.
	// We want to create them with the default IsNftVerified = false.
	result := db.Where(&models.User{TelegramUserID: userID}).
		Attrs(&models.User{IsNftVerified: false}). // Set default attributes if creating
		FirstOrCreate(&user)

	if result.Error != nil {
		// Don't treat RecordNotFound as an error here, as FirstOrCreate handles it.
		// Log other potential database errors.
		if !errors.Is(result.Error, gorm.ErrRecordNotFound) { // Though FirstOrCreate shouldn't return this error directly
			log.Printf("ERROR: Database error checking user %d: %v", userID, result.Error)
			return false, result.Error // Return the actual DB error
		}
		// If somehow FirstOrCreate failed without returning an error but didn't create/find... unlikely.
	}

	// Log the status found or created
	log.Printf("INFO: Verification status for user %d: %t (Records affected: %d)", userID, user.IsNftVerified, result.RowsAffected)

	// Return the status found or defaulted (false)
	return user.IsNftVerified, nil
}

// MarkUserAsVerified updates the user's status to verified in the database.
// Creates the user record if it doesn't exist.
func MarkUserAsVerified(db *gorm.DB, userID int64) error {
	// Use FirstOrCreate to ensure the user exists, then Update.
	// This prevents errors if trying to update a non-existent user.
	user := models.User{TelegramUserID: userID}

	// Ensure user exists (creates with default false if needed)
	// Using .Where and .Assign to update specific fields even if record exists.
	result := db.Where(&models.User{TelegramUserID: userID}).
		Assign(&models.User{IsNftVerified: true}). // Set fields to update/assign
		FirstOrCreate(&user)                       // Find or Create based on Where condition

	if result.Error != nil {
		log.Printf("ERROR: Database error marking user %d as verified: %v", userID, result.Error)
		return result.Error
	}

	if result.RowsAffected > 0 {
		log.Printf("INFO: User %d marked as verified successfully.", userID)
	} else {
		// This might happen if FirstOrCreate found the user and Assign didn't change anything
		// (i.e., they were already verified). This is not necessarily an error.
		log.Printf("INFO: User %d verification status unchanged (possibly already verified).", userID)
	}

	return nil
}
