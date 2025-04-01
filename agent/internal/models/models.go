package models

import "time"

// User represents an authenticated user
type User struct {
	ID        uint      `gorm:"primaryKey"`      // Auto-increment primary key
	WalletID  string    `gorm:"unique;not null"` // Wallet ID for authentication
	NFTStatus bool      `gorm:"not null"`        // Indicates if the user holds the required NFT
	CreatedAt time.Time `gorm:"autoCreateTime"`  // Creation timestamp
	UpdatedAt time.Time `gorm:"autoUpdateTime"`  // Update timestamp
}

// BuyBotData represents the data collected from buy bots
type BuyBotData struct {
	ID          uint      `gorm:"primaryKey"`     // Auto-increment primary key
	Contract    string    `gorm:"not null"`       // Contract address
	Volume      float64   `gorm:"not null"`       // Transaction volume
	Buys        int       `gorm:"not null"`       // Number of buy transactions
	Sells       int       `gorm:"not null"`       // Number of sell transactions
	CollectedAt time.Time `gorm:"autoCreateTime"` // Timestamp of data collection
	Verified    bool      `gorm:"default:false"`  // Indicates if the contract is verified
}

// Filter represents the filters for data processing
type Filter struct {
	ID          uint      `gorm:"primaryKey"`     // Auto-increment primary key
	Name        string    `gorm:"not null"`       // Filter name
	Description string    `gorm:"not null"`       // Filter description
	Criteria    string    `gorm:"type:jsonb"`     // JSON representation of filter criteria
	CreatedAt   time.Time `gorm:"autoCreateTime"` // Creation timestamp
	UpdatedAt   time.Time `gorm:"autoUpdateTime"` // Update timestamp
}
