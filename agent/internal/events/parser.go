package events

// solMintAddress represents the mint address for native SOL.
const solMintAddress = "So11111111111111111111111111111111111111112"

// ExtractNonSolMintFromEvent attempts to extract the first non-SOL token mint address
// from known locations within a Helius webhook event structure.
// It returns the mint address and true if found, otherwise an empty string and false.
func ExtractNonSolMintFromEvent(event map[string]interface{}) (string, bool) {
	// 1. Check "tokenTransfers"
	if transfers, hasTransfers := event["tokenTransfers"].([]interface{}); hasTransfers {
		for _, transfer := range transfers {
			if transferMap, ok := transfer.(map[string]interface{}); ok {
				mint, mintOk := transferMap["mint"].(string)
				if mintOk && mint != "" && mint != solMintAddress {
					return mint, true
				}
			}
		}
	}

	// 2. Check "events.swap" (Outputs first, then Inputs)
	if events, hasEvents := event["events"].(map[string]interface{}); hasEvents {
		if swapEvent, hasSwap := events["swap"].(map[string]interface{}); hasSwap {
			// Check tokenOutputs
			if tokenOutputs, has := swapEvent["tokenOutputs"].([]interface{}); has {
				for _, output := range tokenOutputs {
					if outputMap, ok := output.(map[string]interface{}); ok {
						mint, mintOk := outputMap["mint"].(string)
						if mintOk && mint != "" && mint != solMintAddress {
							return mint, true
						}
					}
				}
			}
			// Check tokenInputs (if not found in outputs)
			if tokenInputs, has := swapEvent["tokenInputs"].([]interface{}); has {
				for _, input := range tokenInputs {
					if inputMap, ok := input.(map[string]interface{}); ok {
						mint, mintOk := inputMap["mint"].(string)
						if mintOk && mint != "" && mint != solMintAddress {
							return mint, true
						}
					}
				}
			}
		}
	}

	// If we reach here, no suitable mint was found
	return "", false
}
