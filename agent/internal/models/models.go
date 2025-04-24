// In internal/models/models.go
package models

import (
	"time"

	"gorm.io/gorm"
)

// User represents a Telegram user interacting with the bot
type User struct {
	// Use Telegram User ID as the primary key
	TelegramUserID int64 `gorm:"primaryKey"`

	// Verification status flag
	IsNftVerified bool `gorm:"not null;default:false;column:is_nft_verified"` // Explicit column name

	// GORM standard fields (optional but good practice)
	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`

	// Optional: Store the last verified wallet if needed for other features
	// LastVerifiedWallet string `gorm:"size:44"` // Example: Solana addresses are typically 32-44 chars
}

// BuyBotData struct remains the same
type BuyBotData struct {
	ID          uint      `gorm:"primaryKey"`
	Contract    string    `gorm:"not null"`
	Volume      float64   `gorm:"not null"`
	Buys        int       `gorm:"not null"`
	Sells       int       `gorm:"not null"`
	CollectedAt time.Time `gorm:"autoCreateTime"`
	Verified    bool      `gorm:"default:false"`
}

// Filter struct remains the same
type Filter struct {
	ID          uint      `gorm:"primaryKey"`
	Name        string    `gorm:"not null"`
	Description string    `gorm:"not null"`
	Criteria    string    `gorm:"type:jsonb"` // Assuming criteria is stored as JSON string
	CreatedAt   time.Time `gorm:"autoCreateTime"`
	UpdatedAt   time.Time `gorm:"autoUpdateTime"`
}
