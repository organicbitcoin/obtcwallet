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
	if windowBlocks > uint64(^uint32(0)) {
		return 0, fmt.Errorf("expiry window too large: %d", windowBlocks)
	}
	expiry := int64(createHeight) + int64(windowBlocks)
	if expiry > int64(^uint32(0)) {
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

	expiryHeight, err := CalculateExpiryHeight(createHeight, windowBlocks)
	if err != nil {
		return ExpiryInfo{}, err
	}

	blocksToExpiry := expiryHeight - tipHeight
	status := ClassifyExpiryStatus(tipHeight, expiryHeight, expiringThresholdBlocks)
	if status == ExpiryStatusExpired {
		blocksToExpiry = 0
	}

	if amountSat < 0 || projectedReclaimSat < 0 || dustThresholdSat < 0 {
		return ExpiryInfo{}, fmt.Errorf("amount and thresholds must be non-negative")
	}

	renewalRisk := EvaluateRenewalRisk(tipHeight, expiryHeight, renewWarningBlocks)

	return ExpiryInfo{
		CreateHeight:           createHeight,
		ExpiryHeight:           expiryHeight,
		BlocksToExpiry:         blocksToExpiry,
		DaysToExpiry:           EstimateDaysToExpiry(blocksToExpiry),
		Status:                 status,
		DustRisk:               projectedReclaimSat < dustThresholdSat,
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

	spendHeight := tipHeight + 1
	if tipHeight == math.MaxInt32 {
		spendHeight = tipHeight
	}
	if spendHeight < expiryParams.EnableAtHeight {
		return false
	}

	return spendHeight >= int32(expiryParams.CalculateExpiryKey(createHeight))
}
