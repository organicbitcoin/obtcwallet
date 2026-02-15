package wallet

import "fmt"

type AutoRenewPolicy struct {
	Enabled            bool
	WindowStartBlocks  int32 // trigger when blocks_to_expiry <= this
	WindowEndBlocks    int32 // and blocks_to_expiry >= this
	MaxUtxosPerRun     int
	MaxFeeRateSatPerKB int64
}

func DefaultAutoRenewPolicy() AutoRenewPolicy {
	return AutoRenewPolicy{
		Enabled:            false,
		WindowStartBlocks:  144 * 365, // ~1 year
		WindowEndBlocks:    144 * 180, // ~6 months
		MaxUtxosPerRun:     100,
		MaxFeeRateSatPerKB: 5000,
	}
}

func ValidateAutoRenewPolicy(p AutoRenewPolicy) error {
	if p.WindowStartBlocks < 0 || p.WindowEndBlocks < 0 {
		return fmt.Errorf("renew window must be non-negative")
	}
	if p.WindowStartBlocks < p.WindowEndBlocks {
		return fmt.Errorf("window start must be >= window end")
	}
	if p.MaxUtxosPerRun <= 0 {
		return fmt.Errorf("max utxos per run must be > 0")
	}
	if p.MaxFeeRateSatPerKB <= 0 {
		return fmt.Errorf("max fee rate must be > 0")
	}
	return nil
}

func IsInAutoRenewWindow(blocksToExpiry int32, p AutoRenewPolicy) bool {
	if !p.Enabled {
		return false
	}
	return blocksToExpiry <= p.WindowStartBlocks && blocksToExpiry >= p.WindowEndBlocks
}

func SelectAutoRenewCandidates(items []ExpiryInfo, p AutoRenewPolicy) []int {
	if !p.Enabled || len(items) == 0 {
		return nil
	}
	selected := make([]int, 0, len(items))
	for i, it := range items {
		if IsInAutoRenewWindow(it.BlocksToExpiry, p) {
			selected = append(selected, i)
			if len(selected) >= p.MaxUtxosPerRun {
				break
			}
		}
	}
	return selected
}
