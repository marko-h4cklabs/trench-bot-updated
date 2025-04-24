-- Migration 000002: Revert add_user_verification
DROP TRIGGER IF EXISTS set_timestamp ON users;
DROP FUNCTION IF EXISTS trigger_set_timestamp();
DROP TABLE IF EXISTS users;
-- Or, if just adding the column:
-- ALTER TABLE users DROP COLUMN IF EXISTS is_nft_verified;