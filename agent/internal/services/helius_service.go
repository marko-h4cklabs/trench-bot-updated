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

// NewHeliusService creates a new instance of HeliusService.
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
	mintPubKey, err := solana.PublicKeyFromBase58(mintAddressStr)
	if err != nil {
		return 0, fmt.Errorf("invalid mint address '%s': %w", mintAddressStr, err)
	}
	out, err := hs.rpcClient.GetTokenSupply(context.Background(), mintPubKey, rpc.CommitmentFinalized)
	if err != nil || out == nil || out.Value == nil {
		return 0, fmt.Errorf("GetTokenSupply failed: %w", err)
	}
	supply, err := strconv.ParseUint(out.Value.Amount, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse token supply amount: %w", err)
	}
	return supply, nil
}

func (hs *HeliusService) GetLPTokensInPair(lpPairAccountAddressStr string, targetTokenMintStr string) (uint64, error) {
	lpPairOwnerPubKey, err := solana.PublicKeyFromBase58(lpPairAccountAddressStr)
	if err != nil {
		return 0, fmt.Errorf("invalid LP pair address '%s': %w", lpPairAccountAddressStr, err)
	}
	targetMintPubKey, err := solana.PublicKeyFromBase58(targetTokenMintStr)
	if err != nil {
		return 0, fmt.Errorf("invalid target token mint address '%s': %w", targetTokenMintStr, err)
	}

	tokenAccountsResult, err := hs.rpcClient.GetTokenAccountsByOwner(
		context.Background(),
		lpPairOwnerPubKey,
		&rpc.GetTokenAccountsConfig{
			Mint:      &targetMintPubKey,
			ProgramId: solana.TokenProgramID.ToPointer(),
		},
		&rpc.GetTokenAccountsOpts{
			Commitment: rpc.CommitmentFinalized,
			Encoding:   solana.EncodingJSONParsed,
		},
	)
	if err != nil || len(tokenAccountsResult.Value) == 0 {
		return 0, fmt.Errorf("failed to fetch token accounts: %w", err)
	}

	parsedRaw := tokenAccountsResult.Value[0].Account.Data
	parsedBytes, err := json.Marshal(parsedRaw)
	if err != nil {
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
		return 0, fmt.Errorf("failed to unmarshal parsed data: %w", err)
	}

	amountStr := parsed.Parsed.Info.TokenAmount.Amount
	if amountStr == "" {
		return 0, fmt.Errorf("token amount is empty")
	}
	return strconv.ParseUint(amountStr, 10, 64)
}

func getMapKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func (hs *HeliusService) GetTopEOAHolders(mintAddressStr string, numToReturn int, lpPairAddressStr, knownBurnAddressStr string) ([]HolderInfo, error) {
	mintPubKey, err := solana.PublicKeyFromBase58(mintAddressStr)
	if err != nil {
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
	if err != nil || largestAccounts == nil || largestAccounts.Value == nil {
		return nil, fmt.Errorf("GetTokenLargestAccounts failed: %w", err)
	}

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
			continue
		}
		owner := accInfo.Value.Owner
		if (lpValid && owner.Equals(lpPubKey)) || (burnValid && owner.Equals(burnPubKey)) {
			continue
		}
		if owner.Equals(solana.TokenProgramID) || owner.Equals(solana.SystemProgramID) || owner.Equals(solana.SPLAssociatedTokenAccountProgramID) {
			continue
		}
		amount, err := strconv.ParseUint(acc.Amount, 10, 64)
		if err != nil {
			continue
		}
		holders[owner.String()] += amount
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
	return result, nil
}
