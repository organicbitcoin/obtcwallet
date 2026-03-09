package wallet

import (
	"github.com/btcsuite/btcd/chaincfg"
)

const (
	// CompatibilityExpiryWindowBlocks is the conservative fallback used when
	// no OBTC-aware source is available (e.g. plain Bitcoin networks).
	CompatibilityExpiryWindowBlocks uint64 = 3679200

	// CompatibilityDustThresholdSat is the fallback dust threshold used when
	// no OBTC-aware source is available.
	CompatibilityDustThresholdSat int64 = 546

	// DefaultProjectedReclaimRatioBps is the default reclaim ratio used by
	// wallet-side expiry UX and planning layers.
	DefaultProjectedReclaimRatioBps uint32 = 7000

	maxDefaultExpiringThresholdBlocks int32 = 144 * 180
)

// ResolvedExpiryPolicy contains the resolved expiry parameters used by the
// wallet for expiry-related computations, UI, and planning.
type ResolvedExpiryPolicy struct {
	WindowBlocks             uint64
	ExpiringThresholdBlocks  int32
	DustThresholdSat         int64
	ProjectedReclaimRatioBps uint32
	Source                   string
}

// ResolveExpiryPolicy resolves wallet-side expiry parameters from the current
// chain parameters. For OBTC networks it directly reads the canonical values
// from chaincfg.GetExpiryParams(). For non-OBTC networks it returns
// conservative compatibility defaults.
func ResolveExpiryPolicy(params *chaincfg.Params) (ResolvedExpiryPolicy, []string) {
	policy := ResolvedExpiryPolicy{
		ProjectedReclaimRatioBps: DefaultProjectedReclaimRatioBps,
	}
	var warnings []string

	var expiryParams *chaincfg.ExpiryParams
	if params != nil {
		expiryParams = chaincfg.GetExpiryParams(params)
	}
	if expiryParams != nil {
		policy.WindowBlocks = expiryParams.WindowBlocks
		policy.DustThresholdSat = expiryParams.ReapDustThresholdSat
		policy.Source = "obtcd_chaincfg"
	} else {
		policy.WindowBlocks = CompatibilityExpiryWindowBlocks
		policy.DustThresholdSat = CompatibilityDustThresholdSat
		policy.Source = "compatibility_default"
		warnings = append(warnings,
			"chain parameters do not identify an OBTC network; "+
				"using compatibility expiry defaults")
	}

	policy.ExpiringThresholdBlocks = defaultExpiringThresholdBlocks(
		policy.WindowBlocks,
	)

	return policy, warnings
}

func defaultExpiringThresholdBlocks(windowBlocks uint64) int32 {
	if windowBlocks == 0 {
		return maxDefaultExpiringThresholdBlocks
	}

	threshold := windowBlocks / 4
	if threshold == 0 {
		threshold = 1
	}
	if threshold > uint64(maxDefaultExpiringThresholdBlocks) {
		threshold = uint64(maxDefaultExpiringThresholdBlocks)
	}

	return int32(threshold)
}
