package wallet

import (
	"fmt"
	"math"

	"github.com/btcsuite/btcd/chaincfg"
)

type ExpiryStatus string

const (
	ExpiryStatusOK       ExpiryStatus = "ok"
	ExpiryStatusExpiring ExpiryStatus = "expiring"
	ExpiryStatusExpired  ExpiryStatus = "expired"
)

type RenewalRisk string

const (
	RenewalRiskOK               RenewalRisk = "ok"
	RenewalRiskNearExpiry       RenewalRisk = "near_expiry"
	RenewalRiskTooLateNextBlock RenewalRisk = "too_late_next_block"
	RenewalRiskExpired          RenewalRisk = "expired"
)

type RenewalRiskInfo struct {
	RenewWarningBlocks     int32
	LatestRenewHeight      int32
	BlocksUntilLatestRenew int32
	RenewableInNextBlock   bool
	NearExpiryWarning      bool
	Risk                   RenewalRisk
}

type ExpiryInfo struct {
	NominalExpiryHeight    int32
	CreateHeight           int32
	ExpiryHeight           int32
	BlocksToExpiry         int32
	DaysToExpiry           int32
	Status                 ExpiryStatus
	DustRisk               bool
	RenewWarningBlocks     int32
	LatestRenewHeight      int32
	BlocksUntilLatestRenew int32
	RenewableInNextBlock   bool
	NearExpiryWarning      bool
	RenewalRisk            RenewalRisk
}

// CalculateExpiryHeight derives the expiry height from a creation height and
// an expiry window in blocks.
func CalculateExpiryHeight(createHeight int32, windowBlocks uint64) (int32, error) {
	if createHeight < 0 {
		return 0, fmt.Errorf("invalid create height: %d", createHeight)
	}
	if windowBlocks > math.MaxInt32 {
		return 0, fmt.Errorf("expiry window too large: %d", windowBlocks)
	}
	expiry := int64(createHeight) + int64(windowBlocks)
	if expiry > math.MaxInt32 {
		return 0, fmt.Errorf("expiry height overflow: %d", expiry)
	}
	return int32(expiry), nil
}

// ClassifyExpiryStatus classifies the current expiry state from tip and
// expiry heights.
func ClassifyExpiryStatus(tipHeight, expiryHeight, expiringThresholdBlocks int32) ExpiryStatus {
	if tipHeight >= expiryHeight {
		return ExpiryStatusExpired
	}
	if expiringThresholdBlocks < 0 {
		expiringThresholdBlocks = 0
	}
	if (expiryHeight - tipHeight) <= expiringThresholdBlocks {
		return ExpiryStatusExpiring
	}
	return ExpiryStatusOK
}

// EstimateDaysToExpiry converts remaining blocks to an approximate day count
// using 144 blocks/day.
func EstimateDaysToExpiry(blocksToExpiry int32) int32 {
	if blocksToExpiry <= 0 {
		return 0
	}
	return blocksToExpiry / 144
}

func EvaluateRenewalRisk(tipHeight, expiryHeight, warningBlocks int32) RenewalRiskInfo {
	if warningBlocks < 0 {
		warningBlocks = 0
	}

	latestRenewHeight := expiryHeight - 1
	if expiryHeight == math.MinInt32 {
		latestRenewHeight = expiryHeight
	}

	blocksUntilLatest := latestRenewHeight - tipHeight
	if blocksUntilLatest < 0 {
		blocksUntilLatest = 0
	}

	nextHeight := tipHeight + 1
	if tipHeight == math.MaxInt32 {
		nextHeight = tipHeight
	}

	info := RenewalRiskInfo{
		RenewWarningBlocks:     warningBlocks,
		LatestRenewHeight:      latestRenewHeight,
		BlocksUntilLatestRenew: blocksUntilLatest,
		Risk:                   RenewalRiskOK,
	}

	if tipHeight >= expiryHeight {
		info.Risk = RenewalRiskExpired
		return info
	}

	if nextHeight >= expiryHeight {
		info.Risk = RenewalRiskTooLateNextBlock
		info.NearExpiryWarning = true
		return info
	}

	info.RenewableInNextBlock = true
	if expiryHeight-tipHeight <= warningBlocks {
		info.Risk = RenewalRiskNearExpiry
		info.NearExpiryWarning = true
	}

	return info
}

// BuildExpiryInfo creates a normalized expiry view for wallet UX.
func BuildExpiryInfo(createHeight, tipHeight int32, windowBlocks uint64,
	expiringThresholdBlocks int32, amountSat, projectedReclaimSat, dustThresholdSat int64) (ExpiryInfo, error) {

	return BuildExpiryInfoWithRenewWarning(
		createHeight, tipHeight, windowBlocks, expiringThresholdBlocks,
		DefaultRenewWarningBlocks, amountSat, projectedReclaimSat,
		dustThresholdSat,
	)
}

// BuildExpiryInfoWithRenewWarning creates a normalized expiry view with an
// explicit wallet-side renewal warning threshold.  The threshold is advisory
// only and does not change consensus expiry rules.
func BuildExpiryInfoWithRenewWarning(createHeight, tipHeight int32, windowBlocks uint64,
	expiringThresholdBlocks, renewWarningBlocks int32, amountSat, projectedReclaimSat, dustThresholdSat int64) (ExpiryInfo, error) {

	policy := ResolvedExpiryPolicy{
		WindowBlocks:            windowBlocks,
		ExpiringThresholdBlocks: expiringThresholdBlocks,
		RenewWarningBlocks:      renewWarningBlocks,
		DustThresholdSat:        dustThresholdSat,
	}
	return policy.buildExpiryInfo(createHeight, tipHeight, amountSat, projectedReclaimSat)
}

// ExpiryInfo evaluates a confirmed output under the network's activation
// height and the wallet's configured warning thresholds.
func (p ResolvedExpiryPolicy) ExpiryInfo(createHeight, tipHeight int32, amountSat int64) (ExpiryInfo, error) {
	return p.buildExpiryInfo(createHeight, tipHeight, amountSat, p.ProjectedReclaimSat(amountSat))
}

func (p ResolvedExpiryPolicy) buildExpiryInfo(createHeight, tipHeight int32, amountSat, projectedReclaimSat int64) (ExpiryInfo, error) {
	nominal, err := CalculateExpiryHeight(createHeight, p.WindowBlocks)
	if err != nil {
		return ExpiryInfo{}, err
	}

	expiryHeight := max(nominal, p.EnableAtHeight)
	blocksToExpiry := expiryHeight - tipHeight
	status := ClassifyExpiryStatus(tipHeight, expiryHeight, p.ExpiringThresholdBlocks)
	if status == ExpiryStatusExpired {
		blocksToExpiry = 0
	}

	if amountSat < 0 || projectedReclaimSat < 0 || p.DustThresholdSat < 0 {
		return ExpiryInfo{}, fmt.Errorf("amount and thresholds must be non-negative")
	}

	renewalRisk := EvaluateRenewalRisk(tipHeight, expiryHeight, p.RenewWarningBlocks)

	return ExpiryInfo{
		CreateHeight:           createHeight,
		NominalExpiryHeight:    nominal,
		ExpiryHeight:           expiryHeight,
		BlocksToExpiry:         blocksToExpiry,
		DaysToExpiry:           EstimateDaysToExpiry(blocksToExpiry),
		Status:                 status,
		DustRisk:               projectedReclaimSat < p.DustThresholdSat,
		RenewWarningBlocks:     renewalRisk.RenewWarningBlocks,
		LatestRenewHeight:      renewalRisk.LatestRenewHeight,
		BlocksUntilLatestRenew: renewalRisk.BlocksUntilLatestRenew,
		RenewableInNextBlock:   renewalRisk.RenewableInNextBlock,
		NearExpiryWarning:      renewalRisk.NearExpiryWarning,
		RenewalRisk:            renewalRisk.Risk,
	}, nil
}

// IsExpiredForSpending reports whether a confirmed output created at
// createHeight must be treated as expired for normal wallet spending when the
// current chain tip is tipHeight. Wallet spendability is based on the next
// block height because consensus evaluates expiry rules at the transaction's
// inclusion height, not the current tip.
func IsExpiredForSpending(params *chaincfg.Params, createHeight, tipHeight int32) bool {
	if params == nil || createHeight < 0 {
		return false
	}

	expiryParams := chaincfg.GetExpiryParams(params)
	if expiryParams == nil {
		return false
	}

	nominal, err := CalculateExpiryHeight(createHeight, expiryParams.WindowBlocks)
	if err != nil {
		// The expiry lies beyond the range of representable block heights.
		return false
	}
	expiryHeight := max(nominal, expiryParams.EnableAtHeight)
	return int64(tipHeight)+1 >= int64(expiryHeight)
}
