package wallet

import (
	"github.com/btcsuite/btcd/chaincfg"
)

const (
	// CompatibilityExpiryWindowBlocks is the fallback used when chaincfg does
	// not provide expiry parameters.
	CompatibilityExpiryWindowBlocks uint64 = 3679200

	// CompatibilityDustThresholdSat is the fallback dust threshold.
	CompatibilityDustThresholdSat int64 = 546

	// DefaultProjectedReclaimRatioBps is the default reclaim ratio used by
	// wallet-side expiry planning.
	DefaultProjectedReclaimRatioBps uint32 = 7000

	// DefaultRenewWarningBlocks is the wallet-side safety margin used to warn
	// users that a renewal transaction must confirm soon.  This is not a
	// consensus parameter; any UTXO that has not reached expiry is still
	// renewable if the renewal transaction confirms before expiry.
	DefaultRenewWarningBlocks int32 = 12

	maxDefaultExpiringThresholdBlocks int32 = 144 * 180
)

// ResolvedExpiryPolicy contains the resolved expiry parameters used by the
// wallet for expiry-related computations, UI, and planning.
type ResolvedExpiryPolicy struct {
	WindowBlocks             uint64
	ExpiringThresholdBlocks  int32
	RenewWarningBlocks       int32
	DustThresholdSat         int64
	ProjectedReclaimRatioBps uint32
	Source                   string
}

// ResolveExpiryPolicy resolves wallet-side expiry parameters from chaincfg.
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
		if ratioBps, ok := projectedReclaimRatioFromTax(expiryParams); ok {
			policy.ProjectedReclaimRatioBps = ratioBps
		} else {
			warnings = append(warnings,
				"invalid REAP tax parameters; using compatibility reclaim ratio")
		}
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
	policy.RenewWarningBlocks = DefaultRenewWarningBlocks

	return policy, warnings
}

func projectedReclaimRatioFromTax(expiryParams *chaincfg.ExpiryParams) (uint32, bool) {
	if expiryParams == nil ||
		expiryParams.ReapTaxDenominator <= 0 ||
		expiryParams.ReapTaxNumerator < 0 ||
		expiryParams.ReapTaxNumerator > expiryParams.ReapTaxDenominator {

		return DefaultProjectedReclaimRatioBps, false
	}

	reclaimNumerator := expiryParams.ReapTaxDenominator - expiryParams.ReapTaxNumerator
	return uint32((reclaimNumerator * 10000) / expiryParams.ReapTaxDenominator), true
}

func (p ResolvedExpiryPolicy) ProjectedReclaimSat(amountSat int64) int64 {
	return ProjectedReclaimSat(amountSat, p.ProjectedReclaimRatioBps)
}

func ProjectedReclaimSat(amountSat int64, ratioBps uint32) int64 {
	return (amountSat * int64(ratioBps)) / 10000
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
