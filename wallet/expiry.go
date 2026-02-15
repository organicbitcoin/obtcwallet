package wallet

import "fmt"

type ExpiryStatus string

const (
	ExpiryStatusOK       ExpiryStatus = "ok"
	ExpiryStatusExpiring ExpiryStatus = "expiring"
	ExpiryStatusExpired  ExpiryStatus = "expired"
)

type ExpiryInfo struct {
	CreateHeight   int32
	ExpiryHeight   int32
	BlocksToExpiry int32
	DaysToExpiry   int32
	Status         ExpiryStatus
	DustRisk       bool
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

// BuildExpiryInfo creates a normalized expiry view for wallet UX.
func BuildExpiryInfo(createHeight, tipHeight int32, windowBlocks uint64,
	expiringThresholdBlocks int32, amountSat, projectedReclaimSat, dustThresholdSat int64) (ExpiryInfo, error) {

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

	return ExpiryInfo{
		CreateHeight:   createHeight,
		ExpiryHeight:   expiryHeight,
		BlocksToExpiry: blocksToExpiry,
		DaysToExpiry:   EstimateDaysToExpiry(blocksToExpiry),
		Status:         status,
		DustRisk:       projectedReclaimSat < dustThresholdSat,
	}, nil
}
