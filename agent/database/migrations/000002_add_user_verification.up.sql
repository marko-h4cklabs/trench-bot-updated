-- Migration 000002: Add user table with NFT verification status

-- Drop existing users table if it was created with the wrong schema before (use with caution!)
-- DROP TABLE IF EXISTS users;

CREATE TABLE IF NOT EXISTS users (
    telegram_user_id BIGINT PRIMARY KEY, -- Use Telegram ID as primary key
    is_nft_verified BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
    -- Add other user fields here if needed later (e.g., last_verified_wallet TEXT)
);

-- Add index for potential lookups if needed (optional)
-- CREATE INDEX IF NOT EXISTS idx_users_telegram_id ON users(telegram_user_id);

-- If the table might already exist from GORM but LACKS the new column:
-- ALTER TABLE users
-- ADD COLUMN IF NOT EXISTS is_nft_verified BOOLEAN NOT NULL DEFAULT false;

-- If the table might already exist and needs the PRIMARY KEY changed (more complex, might require dropping constraints first):
-- ALTER TABLE users DROP CONSTRAINT IF EXISTS users_pkey; -- Might fail if dependencies exist
-- ALTER TABLE users ADD PRIMARY KEY (telegram_user_id);
-- ALTER TABLE users ALTER COLUMN telegram_user_id SET NOT NULL; -- Ensure it's NOT NULL
-- ALTER TABLE users DROP COLUMN IF EXISTS id; -- If removing the old serial ID
-- ALTER TABLE users DROP COLUMN IF EXISTS wallet_id; -- If removing wallet_id
-- ALTER TABLE users DROP COLUMN IF EXISTS nft_status; -- If renaming nft_status

-- Ensure updated_at trigger function exists (common practice in Postgres)
CREATE OR REPLACE FUNCTION trigger_set_timestamp()
RETURNS TRIGGER AS $$
BEGIN
  NEW.updated_at = NOW();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Apply the trigger to the users table
DROP TRIGGER IF EXISTS set_timestamp ON users; -- Drop existing trigger first
CREATE TRIGGER set_timestamp
BEFORE UPDATE ON users
FOR EACH ROW
EXECUTE PROCEDURE trigger_set_timestamp();