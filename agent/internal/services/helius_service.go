package services

import (
	"ca-scraper/shared/env"
	"ca-scraper/shared/logger"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/rpc"
	"go.uber.org/zap"
)

// HeliusService provides methods to interact with Helius RPC.
type HeliusService struct {
	rpcClient *rpc.Client
	appLogger *logger.Logger
}

func NewHeliusService(appLogger *logger.Logger) (*HeliusService, error) {
	rpcURL := env.HeliusRPCURL
	if rpcURL == "" {
		return nil, fmt.Errorf("HELIUS_RPC_URL environment variable not set")
	}
	client := rpc.New(rpcURL)
	_, err := client.GetHealth(context.Background())
	if err != nil {
		appLogger.Error("Failed to connect to Helius RPC during initialization", zap.String("url", sanitizeURL(rpcURL)), zap.Error(err))
		return nil, fmt.Errorf("failed to connect to Helius RPC at %s: %w", sanitizeURL(rpcURL), err)
	}
	appLogger.Info("Helius RPC client initialized successfully", zap.String("url", sanitizeURL(rpcURL)))
	return &HeliusService{rpcClient: client, appLogger: appLogger}, nil
}

func sanitizeURL(rawURL string) string {
	if idx := strings.Index(rawURL, "api-key="); idx != -1 {
		return rawURL[:idx+len("api-key=")] + "HIDDEN_FOR_LOGS"
	}
	return rawURL
}

func (hs *HeliusService) GetTokenSupply(mintAddressStr string) (uint64, error) {
	hs.appLogger.Debug("HeliusService: Attempting GetTokenSupply", zap.String("mint", mintAddressStr))
	mintPubKey, err := solana.PublicKeyFromBase58(mintAddressStr)
	if err != nil {
		hs.appLogger.Error("HeliusService: Invalid mint address for GetTokenSupply", zap.String("mint", mintAddressStr), zap.Error(err))
		return 0, fmt.Errorf("invalid mint address '%s': %w", mintAddressStr, err)
	}

	out, err := hs.rpcClient.GetTokenSupply(context.Background(), mintPubKey, rpc.CommitmentFinalized)
	if err != nil {
		hs.appLogger.Error("HeliusService: RPC GetTokenSupply call failed", zap.String("mint", mintAddressStr), zap.Error(err))
		return 0, fmt.Errorf("GetTokenSupply for %s failed: %w", mintAddressStr, err)
	}
	// Log the raw output for debugging
	hs.appLogger.Debug("HeliusService: GetTokenSupply RPC response", zap.String("mint", mintAddressStr), zap.Any("rpcOutput", out))
	if out == nil || out.Value == nil {
		hs.appLogger.Warn("HeliusService: GetTokenSupply returned nil or nil value", zap.String("mint", mintAddressStr))
		return 0, fmt.Errorf("GetTokenSupply for %s returned nil value", mintAddressStr)
	}

	supply, err := strconv.ParseUint(out.Value.Amount, 10, 64)
	if err != nil {
		hs.appLogger.Error("HeliusService: Failed to parse token supply amount", zap.String("mint", mintAddressStr), zap.String("amountStr", out.Value.Amount), zap.Error(err))
		return 0, fmt.Errorf("parsing token supply for %s failed: %w", mintAddressStr, err)
	}
	hs.appLogger.Info("HeliusService: Successfully fetched token supply", zap.String("mint", mintAddressStr), zap.Uint64("supply", supply))
	return supply, nil
}

func (hs *HeliusService) GetLPTokensInPair(lpMintAddressStr string, targetTokenMintStr string) (uint64, error) {
	hs.appLogger.Debug("GetLPTokensInPair: Starting", zap.String("lpMint", lpMintAddressStr), zap.String("targetMint", targetTokenMintStr))

	lpMintPubKey, err := solana.PublicKeyFromBase58(lpMintAddressStr)
	if err != nil {
		hs.appLogger.Error("Invalid LP mint address", zap.String("lpMint", lpMintAddressStr), zap.Error(err))
		return 0, fmt.Errorf("invalid LP mint address '%s': %w", lpMintAddressStr, err)
	}
	targetMintPubKey, err := solana.PublicKeyFromBase58(targetTokenMintStr)
	if err != nil {
		hs.appLogger.Error("Invalid target token mint", zap.String("targetMint", targetTokenMintStr), zap.Error(err))
		return 0, fmt.Errorf("invalid target token mint '%s': %w", targetTokenMintStr, err)
	}

	// Step 1: Get largest LP holder
	lpHolders, err := hs.rpcClient.GetTokenLargestAccounts(context.Background(), lpMintPubKey, rpc.CommitmentFinalized)
	if err != nil || lpHolders == nil || len(lpHolders.Value) == 0 {
		hs.appLogger.Error("Failed to fetch largest LP holders", zap.String("lpMint", lpMintAddressStr), zap.Error(err))
		return 0, fmt.Errorf("failed to fetch largest LP holders: %w", err)
	}

	lpHolderAccount := lpHolders.Value[0].Address
	hs.appLogger.Debug("GetLPTokensInPair: Largest LP token holder account", zap.String("account", lpHolderAccount.String()))

	accountInfo, err := hs.rpcClient.GetAccountInfo(context.Background(), lpHolderAccount)
	if err != nil || accountInfo == nil || accountInfo.Value == nil {
		hs.appLogger.Error("Failed to get account info for LP holder", zap.String("account", lpHolderAccount.String()), zap.Error(err))
		return 0, fmt.Errorf("failed to get account info for LP holder: %w", err)
	}

	lpOwner := accountInfo.Value.Owner
	hs.appLogger.Debug("GetLPTokensInPair: Owner of largest LP holder", zap.String("owner", lpOwner.String()))

	// Step 2: Get token accounts of LP owner for the target token mint
	tokenAccountsResult, err := hs.rpcClient.GetTokenAccountsByOwner(
		context.Background(),
		lpOwner,
		&rpc.GetTokenAccountsConfig{
			Mint: &targetMintPubKey,
		},
		&rpc.GetTokenAccountsOpts{
			Commitment: rpc.CommitmentFinalized,
			Encoding:   solana.EncodingJSONParsed,
		},
	)

	if err != nil {
		hs.appLogger.Error("RPC error fetching LP token accounts", zap.Error(err))
		return 0, fmt.Errorf("failed to fetch token accounts: %w", err)
	}
	if len(tokenAccountsResult.Value) == 0 {
		hs.appLogger.Warn("No token accounts found for LP owner and target mint", zap.String("lpOwner", lpOwner.String()), zap.String("targetMint", targetTokenMintStr))
		return 0, fmt.Errorf("no token accounts found")
	}

	parsedRaw := tokenAccountsResult.Value[0].Account.Data
	hs.appLogger.Debug("Raw LP token account data", zap.Any("parsedRaw", parsedRaw))

	parsedBytes, err := json.Marshal(parsedRaw)
	if err != nil {
		hs.appLogger.Error("Failed to marshal LP token account parsed data", zap.Error(err))
		return 0, fmt.Errorf("failed to marshal parsed account data: %w", err)
	}

	type TokenAmount struct {
		Amount string `json:"amount"`
	}
	type Info struct {
		TokenAmount TokenAmount `json:"tokenAmount"`
	}
	type Parsed struct {
		Info Info `json:"info"`
	}
	type Wrapper struct {
		Parsed Parsed `json:"parsed"`
	}

	var parsed Wrapper
	if err := json.Unmarshal(parsedBytes, &parsed); err != nil {
		hs.appLogger.Error("Failed to unmarshal LP token parsed data", zap.ByteString("parsedBytes", parsedBytes), zap.Error(err))
		return 0, fmt.Errorf("failed to unmarshal parsed data: %w", err)
	}

	amountStr := parsed.Parsed.Info.TokenAmount.Amount
	if amountStr == "" {
		hs.appLogger.Error("Parsed LP token amount is empty", zap.Any("parsed", parsed))
		return 0, fmt.Errorf("token amount is empty")
	}

	amount, err := strconv.ParseUint(amountStr, 10, 64)
	if err != nil {
		hs.appLogger.Error("Failed to parse LP token amount string", zap.String("amountStr", amountStr), zap.Error(err))
		return 0, err
	}
	hs.appLogger.Info("LP token amount parsed successfully", zap.Uint64("amount", amount))
	return amount, nil
}

func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (hs *HeliusService) GetTopEOAHolders(mintAddressStr string, numToReturn int, lpPairAddressStr, knownBurnAddressStr string) ([]HolderInfo, error) {
	hs.appLogger.Debug("GetTopEOAHolders: Starting", zap.String("mint", mintAddressStr))

	mintPubKey, err := solana.PublicKeyFromBase58(mintAddressStr)
	if err != nil {
		hs.appLogger.Error("GetTopEOAHolders: Invalid mint address", zap.String("mint", mintAddressStr), zap.Error(err))
		return nil, fmt.Errorf("invalid mint address: %w", err)
	}

	var lpPubKey, burnPubKey solana.PublicKey
	lpValid, burnValid := false, false
	if lpPairAddressStr != "" {
		if pk, errLP := solana.PublicKeyFromBase58(lpPairAddressStr); errLP == nil {
			lpPubKey, lpValid = pk, true
		} else {
			hs.appLogger.Warn("GetTopEOAHolders: Invalid LP Pair address string for exclusion", zap.String("lpPair", lpPairAddressStr), zap.Error(errLP))
		}
	}
	if knownBurnAddressStr != "" {
		if pk, errBurn := solana.PublicKeyFromBase58(knownBurnAddressStr); errBurn == nil {
			burnPubKey, burnValid = pk, true
		} else {
			hs.appLogger.Warn("GetTopEOAHolders: Invalid Burn address string for exclusion", zap.String("burnAddr", knownBurnAddressStr), zap.Error(errBurn))
		}
	}

	largestAccounts, err := hs.rpcClient.GetTokenLargestAccounts(context.Background(), mintPubKey, rpc.CommitmentFinalized)
	if err != nil {
		hs.appLogger.Error("GetTopEOAHolders: RPC GetTokenLargestAccounts failed", zap.String("mint", mintAddressStr), zap.Error(err))
		return nil, fmt.Errorf("GetTokenLargestAccounts failed: %w", err)
	}
	if largestAccounts == nil || largestAccounts.Value == nil {
		hs.appLogger.Warn("GetTopEOAHolders: No largest accounts returned by RPC", zap.String("mint", mintAddressStr))
		return []HolderInfo{}, nil // Return empty, not an error
	}

	hs.appLogger.Debug("GetTopEOAHolders: Fetched largest token accounts", zap.Int("count", len(largestAccounts.Value)))

	holders := make(map[string]uint64)    // EOA Owner Address -> Aggregated Amount
	initialQueryLimit := numToReturn + 25 // Fetch more initially to account for filtering
	processedAccountCount := 0

	for _, accRpcInfo := range largestAccounts.Value {
		if len(holders) >= numToReturn && processedAccountCount >= numToReturn { // Stop if we have enough distinct EOAs
			hs.appLogger.Debug("GetTopEOAHolders: Reached desired number of EOA holders", zap.Int("found", len(holders)))
			break
		}
		if processedAccountCount >= initialQueryLimit { // Hard stop after processing a certain number
			hs.appLogger.Debug("GetTopEOAHolders: Reached initial query limit for processing", zap.Int("processed", processedAccountCount))
			break
		}
		processedAccountCount++

		tokenAccountAddress := accRpcInfo.Address // This is the Token Account Address
		hs.appLogger.Debug("GetTopEOAHolders: Processing token account", zap.String("tokenAccount", tokenAccountAddress.String()), zap.String("rawAmount", accRpcInfo.Amount))

		accInfo, err := hs.rpcClient.GetAccountInfo(context.Background(), tokenAccountAddress)
		if err != nil {
			hs.appLogger.Warn("GetTopEOAHolders: Skipping account due to failed GetAccountInfo", zap.String("tokenAccount", tokenAccountAddress.String()), zap.Error(err))
			continue
		}
		if accInfo == nil || accInfo.Value == nil {
			hs.appLogger.Warn("GetTopEOAHolders: Skipping account due to nil AccountInfo.Value", zap.String("tokenAccount", tokenAccountAddress.String()))
			continue
		}

		owner := accInfo.Value.Owner // This is the actual owner (EOA or Program)

		// Filter out LP, Burn, and common Program addresses
		if (lpValid && owner.Equals(lpPubKey)) || (burnValid && owner.Equals(burnPubKey)) ||
			owner.Equals(solana.TokenProgramID) || owner.Equals(solana.SystemProgramID) ||
			owner.Equals(solana.SPLAssociatedTokenAccountProgramID) ||
			owner.Equals(solana.BPFLoaderProgramID) || owner.Equals(solana.BPFLoaderUpgradeableProgramID) ||
			owner.Equals(solana.StakeProgramID) || owner.Equals(solana.VoteProgramID) || owner.Equals(solana.MemoProgramID) {
			hs.appLogger.Debug("GetTopEOAHolders: Skipping non-EOA or filtered owner", zap.String("tokenAccount", tokenAccountAddress.String()), zap.String("owner", owner.String()))
			continue
		}

		// Now, try to parse the account data to confirm it's a token account and get owner (if needed, though owner is from accInfo.Value.Owner)
		// The main goal here would be if we needed other info from the parsed data.
		// For just getting owner, accInfo.Value.Owner is enough.
		// The "unexpected end of JSON input" error happened when unmarshalling `accInfo.Value.Data.GetRawJSON()`

		// Let's log the raw data that caused issues
		if accInfo.Value.Data == nil {
			hs.appLogger.Warn("GetTopEOAHolders: accInfo.Value.Data is nil, cannot parse owner for filtering", zap.String("tokenAccount", tokenAccountAddress.String()))
			continue
		}

		// Check encoding of this specific account if it helps debugging
		hs.appLogger.Debug("GetTopEOAHolders: Account data type",
			zap.String("tokenAccount", tokenAccountAddress.String()),
			zap.String("dataType", fmt.Sprintf("%T", accInfo.Value.Data)),
		)

		// For GetTopEOAHolders, we primarily need the owner, which we got from accInfo.Value.Owner.
		// The parsing of accInfo.Value.Data here was more for robustly confirming it IS a token account,
		// but if we trust that `GetTokenLargestAccounts` gives us token accounts, and `accInfo.Value.Owner` is an EOA (after filtering),
		// we might not need to re-parse `accInfo.Value.Data` just to confirm owner again.
		// The critical part is that `accInfo.Value.Owner` is correct.

		amount, err := strconv.ParseUint(accRpcInfo.Amount, 10, 64) // Amount is from GetTokenLargestAccounts
		if err != nil {
			hs.appLogger.Warn("GetTopEOAHolders: Failed to parse token amount for potential EOA", zap.String("amountStr", accRpcInfo.Amount), zap.Error(err))
			continue
		}
		holders[owner.String()] += amount
		hs.appLogger.Debug("GetTopEOAHolders: Aggregated for EOA", zap.String("eoaOwner", owner.String()), zap.Uint64("aggregatedAmount", holders[owner.String()]))
	}

	var result []HolderInfo
	for addr, amt := range holders {
		result = append(result, HolderInfo{Address: addr, Amount: amt})
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Amount > result[j].Amount
	})
	if len(result) > numToReturn {
		result = result[:numToReturn]
	}

	hs.appLogger.Info("GetTopEOAHolders: Top EOA holders fetched", zap.Int("returnedCount", len(result)), zap.String("mint", mintAddressStr))
	return result, nil
}

// GetMintFromTokenAccount takes a token account address and returns the mint address it holds.
func (hs *HeliusService) GetMintFromTokenAccount(tokenAccountStr string) (string, error) {
	pubKey, err := solana.PublicKeyFromBase58(tokenAccountStr)
	if err != nil {
		hs.appLogger.Error("GetMintFromTokenAccount: Invalid token account address", zap.String("input", tokenAccountStr), zap.Error(err))
		return "", fmt.Errorf("invalid token account address: %w", err)
	}

	accountInfo, err := hs.rpcClient.GetAccountInfo(context.Background(), pubKey)
	if err != nil || accountInfo == nil || accountInfo.Value == nil {
		hs.appLogger.Error("GetMintFromTokenAccount: Failed to fetch account info", zap.String("address", tokenAccountStr), zap.Error(err))
		return "", fmt.Errorf("failed to fetch account info: %w", err)
	}

	rawJson := accountInfo.Value.Data.GetRawJSON()
	if len(rawJson) == 0 || string(rawJson) == "null" {
		hs.appLogger.Warn("GetMintFromTokenAccount: Empty or null raw JSON", zap.String("address", tokenAccountStr))
		return "", fmt.Errorf("account data is empty or null")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(rawJson, &parsed); err != nil {
		hs.appLogger.Error("GetMintFromTokenAccount: Failed to parse account JSON", zap.String("address", tokenAccountStr), zap.Error(err))
		return "", fmt.Errorf("failed to parse account data: %w", err)
	}

	parsedInfo, ok := parsed["parsed"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("account does not contain parsed data")
	}
	info, ok := parsedInfo["info"].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("account parsed data missing 'info'")
	}
	mintStr, ok := info["mint"].(string)
	if !ok || mintStr == "" {
		return "", fmt.Errorf("could not extract mint from token account")
	}

	hs.appLogger.Debug("GetMintFromTokenAccount: Extracted mint", zap.String("mint", mintStr))
	return mintStr, nil
}
