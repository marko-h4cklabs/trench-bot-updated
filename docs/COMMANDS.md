# Telegram Bot CA Scraper

## Overview
This Telegram bot is designed to manage wallet whitelisting and verify wallet ownership of Trench Demon NFTs using the Solana blockchain and the Helius API. It provides several commands for administrators to manage wallet-related operations.

---

## Commands

### `/whitelist {wallet-address}`
- **Description**: Adds a wallet address to the whitelist.
- **Usage**:
  ```
  /whitelist {wallet-address}
  ```
- **Behavior**:
  - Checks if the wallet already exists in the database.
  - If not, it adds the wallet to the database with `NFTStatus` set to `false`.
  - Sends a confirmation message upon success.

---

### `/walletupdate {current-wallet-address} {updated-wallet-address}`
- **Description**: Updates an existing wallet address in the whitelist.
- **Usage**:
  ```
  /walletupdate {current-wallet-address} {updated-wallet-address}
  ```
- **Behavior**:
  - Checks if the `current-wallet-address` exists in the database.
  - Updates the wallet address to `updated-wallet-address`.
  - Sends a confirmation message upon success.

---

### `/walletdelete {wallet-address}`
- **Description**: Removes a wallet address from the whitelist.
- **Usage**:
  ```
  /walletdelete {wallet-address}
  ```
- **Behavior**:
  - Checks if the wallet exists in the database.
  - Removes the wallet from the database.
  - Sends a confirmation message upon success.

---

### `/checkwallet {wallet-address}`
- **Description**: Checks if a wallet holds a Trench Demon NFT.
- **Usage**:
  ```
  /checkwallet {wallet-address}
  ```
- **Behavior**:
  - Verifies the wallet ownership of a Trench Demon NFT using the Solana blockchain and the Helius API.
  - Updates the `NFTStatus` of the wallet in the database.
  - Sends a message indicating whether the wallet holds the NFT.
  - If the wallet holds the NFT, it provides a link to view the wallet on the Solana Explorer.

---

## API Routes

### `GET /api/v1/health`
- **Description**: Health check endpoint.
- **Response**:
  ```json
  { "message": "OK" }
  ```

---

### `GET /api/v1/testHelius`
- **Description**: Tests the Helius API connection.
- **Response**:
  - On success:
    ```json
    { "message": "Helius connection successful" }
    ```
  - On failure:
    ```json
    { "error": "Helius API returned an error", "statusCode": <status_code> }
    ```

---

### `POST /api/v1/messages`
- **Description**: Starts the Telegram bot and begins listening for updates.
- **Response**:
  ```json
  { "message": "Telegram bot started successfully" }
  ```

---

## Environment Variables
Make sure to configure the following environment variables in your `.env` file:

- **Telegram Bot Configuration**:
  ```
  TELEGRAM_BOT_TOKEN=your_bot_token
  TELEGRAM_GROUP_ID=your_group_id
  ```

- **Helius API Configuration**:
  ```
  HELIUS_API_KEY=your_helius_api_key
  ```

- **Pre-Approved Wallets**:
  ```
  WALLET_1=pre_approved_wallet_1
  WALLET_2=pre_approved_wallet_2
  ```

- **PostgreSQL Configuration**:
  ```
  LOCAL_DATABASE_NAME=trench_db
  LOCAL_DATABASE_USER=julian
  LOCAL_DATABASE_PASSWORD=trench
  LOCAL_DATABASE_HOST=localhost
  LOCAL_DATABASE_PORT=5432
  LOCAL_POSTGRES_DSN=postgres://julian:trench@localhost:5432/trench_db?sslmode=disable
  ```

---

## Database
The following tables are used:
1. **`users`**:
   - **Columns**:
     - `id`: Primary Key
     - `wallet_id`: Wallet address
     - `nft_status`: Boolean indicating if the wallet owns a Trench Demon NFT
     - `created_at`, `updated_at`: Timestamps
2. **`buy_bot_data`**:
   - Used for data collected from buy bots.
3. **`filters`**:
   - Stores filter configurations for processing.

---

## Notes
- Ensure that the `trenchDemonMintAddress` constant is updated with the correct mint address for the Trench Demon NFT.
- Use the `/api/v1/testHelius` route to verify that the Helius API key is working correctly before running the `/checkwallet` command.

