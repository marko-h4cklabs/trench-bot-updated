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

func (hs *HeliusService) GetLPTokensInPair(lpPairAccountAddressStr string, targetTokenMintStr string) (uint64, error) {
	hs.appLogger.Debug("GetLPTokensInPair: Starting", zap.String("lpPairAccount", lpPairAccountAddressStr), zap.String("targetMint", targetTokenMintStr))

	lpPairOwnerPubKey, err := solana.PublicKeyFromBase58(lpPairAccountAddressStr)
	if err != nil {
		hs.appLogger.Error("Invalid LP pair address", zap.String("lpPairAccount", lpPairAccountAddressStr), zap.Error(err))
		return 0, fmt.Errorf("invalid LP pair address '%s': %w", lpPairAccountAddressStr, err)
	}
	targetMintPubKey, err := solana.PublicKeyFromBase58(targetTokenMintStr)
	if err != nil {
		hs.appLogger.Error("Invalid target token mint", zap.String("targetMint", targetTokenMintStr), zap.Error(err))
		return 0, fmt.Errorf("invalid target token mint address '%s': %w", targetTokenMintStr, err)
	}

	tokenAccountsResult, err := hs.rpcClient.GetTokenAccountsByOwner(
		context.Background(),
		lpPairOwnerPubKey,
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
		hs.appLogger.Warn("No token accounts found for LP pair owner and target mint", zap.String("lpOwner", lpPairAccountAddressStr), zap.String("targetMint", targetTokenMintStr))
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
		hs.appLogger.Error("Invalid mint address", zap.String("mint", mintAddressStr), zap.Error(err))
		return nil, fmt.Errorf("invalid mint address: %w", err)
	}

	var lpPubKey, burnPubKey solana.PublicKey
	lpValid, burnValid := false, false
	if lpPairAddressStr != "" {
		if pk, err := solana.PublicKeyFromBase58(lpPairAddressStr); err == nil {
			lpPubKey, lpValid = pk, true
		}
	}
	if knownBurnAddressStr != "" {
		if pk, err := solana.PublicKeyFromBase58(knownBurnAddressStr); err == nil {
			burnPubKey, burnValid = pk, true
		}
	}

	largestAccounts, err := hs.rpcClient.GetTokenLargestAccounts(context.Background(), mintPubKey, rpc.CommitmentFinalized)
	if err != nil {
		hs.appLogger.Error("RPC GetTokenLargestAccounts failed", zap.String("mint", mintAddressStr), zap.Error(err))
		return nil, fmt.Errorf("GetTokenLargestAccounts failed: %w", err)
	}
	if largestAccounts == nil || largestAccounts.Value == nil {
		hs.appLogger.Warn("No largest accounts returned", zap.String("mint", mintAddressStr))
		return nil, fmt.Errorf("no largest accounts found")
	}

	hs.appLogger.Debug("Fetched largest accounts", zap.Int("count", len(largestAccounts.Value)))

	holders := make(map[string]uint64)
	initialQueryLimit := numToReturn + 25
	for i, acc := range largestAccounts.Value {
		if len(holders) >= numToReturn && i >= numToReturn {
			break
		}
		if i >= initialQueryLimit {
			break
		}

		accInfo, err := hs.rpcClient.GetAccountInfo(context.Background(), acc.Address)
		if err != nil || accInfo == nil || accInfo.Value == nil {
			hs.appLogger.Warn("Skipping account due to failed GetAccountInfo", zap.String("address", acc.Address.String()), zap.Error(err))
			continue
		}

		var parsedData map[string]interface{}
		if err := json.Unmarshal(accInfo.Value.Data.GetRawJSON(), &parsedData); err != nil {
			hs.appLogger.Warn("Failed to parse JSON from account data", zap.String("address", acc.Address.String()), zap.Error(err))
			continue
		}
		parsed, ok := parsedData["parsed"].(map[string]interface{})
		if !ok {
			hs.appLogger.Warn("AccountInfo parsed field is not a map", zap.String("address", acc.Address.String()))
			continue
		}

		info, ok := parsed["info"].(map[string]interface{})
		if !ok {
			hs.appLogger.Warn("AccountInfo info field is not a map", zap.String("address", acc.Address.String()))
			continue
		}

		ownerStr, ok := info["owner"].(string)
		if !ok || ownerStr == "" {
			hs.appLogger.Warn("Owner string missing or invalid", zap.String("address", acc.Address.String()))
			continue
		}

		// Skip known LP or burn owners
		if lpValid && ownerStr == lpPubKey.String() {
			continue
		}
		if burnValid && ownerStr == burnPubKey.String() {
			continue
		}

		// Parse amount
		amount, err := strconv.ParseUint(acc.Amount, 10, 64)
		if err != nil {
			hs.appLogger.Warn("Failed to parse token amount", zap.String("amount", acc.Amount), zap.Error(err))
			continue
		}

		holders[ownerStr] += amount
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

	hs.appLogger.Info("Top EOA holders fetched", zap.Int("returned", len(result)), zap.String("mint", mintAddressStr))
	return result, nil
}
