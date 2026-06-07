package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
)

func TestCalculateExpiryHeight(t *testing.T) {
	h, err := CalculateExpiryHeight(100, 144)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h != 244 {
		t.Fatalf("got %d want 244", h)
	}

	if _, err := CalculateExpiryHeight(-1, 100); err == nil {
		t.Fatalf("expected error for negative create height")
	}
}

func TestClassifyExpiryStatusBoundaries(t *testing.T) {
	threshold := int32(100)
	if got := ClassifyExpiryStatus(1000, 1000, threshold); got != ExpiryStatusExpired {
		t.Fatalf("expected expired, got %s", got)
	}
	if got := ClassifyExpiryStatus(950, 1000, threshold); got != ExpiryStatusExpiring {
		t.Fatalf("expected expiring, got %s", got)
	}
	if got := ClassifyExpiryStatus(800, 1000, threshold); got != ExpiryStatusOK {
		t.Fatalf("expected ok, got %s", got)
	}
}

func TestEstimateDaysToExpiryDirect(t *testing.T) {
	if got := EstimateDaysToExpiry(-1); got != 0 {
		t.Fatalf("negative blocks should map to 0 days, got %d", got)
	}
	if got := EstimateDaysToExpiry(0); got != 0 {
		t.Fatalf("zero blocks should map to 0 days, got %d", got)
	}
	if got := EstimateDaysToExpiry(288); got != 2 {
		t.Fatalf("expected 2 days, got %d", got)
	}
}

func TestBuildExpiryInfo(t *testing.T) {
	info, err := BuildExpiryInfo(
		100, 150, 200, // create/tip/window
		100,             // expiring threshold
		10000, 400, 546, // amount/reclaim/dust
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ExpiryHeight != 300 {
		t.Fatalf("expiry mismatch: %d", info.ExpiryHeight)
	}
	if info.Status != ExpiryStatusOK {
		t.Fatalf("status mismatch: %s", info.Status)
	}
	if !info.DustRisk {
		t.Fatalf("expected dust risk true")
	}
	if info.RenewalRisk != RenewalRiskOK {
		t.Fatalf("expected renewal risk ok, got %s", info.RenewalRisk)
	}
}

func TestBuildExpiryInfoExpired(t *testing.T) {
	info, err := BuildExpiryInfo(
		100, 400, 200,
		100,
		10000, 1000, 546,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.Status != ExpiryStatusExpired {
		t.Fatalf("expected expired, got %s", info.Status)
	}
	if info.BlocksToExpiry != 0 {
		t.Fatalf("expired blocks must be 0, got %d", info.BlocksToExpiry)
	}
}

func TestEvaluateRenewalRiskBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		tip                    int32
		expiry                 int32
		warning                int32
		wantRisk               RenewalRisk
		wantRenewableNext     bool
		wantNearWarning        bool
		wantBlocksUntilLatest int32
	}{
		{
			name:                   "outside warning window",
			tip:                    87,
			expiry:                 100,
			warning:                12,
			wantRisk:               RenewalRiskOK,
			wantRenewableNext:      true,
			wantBlocksUntilLatest: 12,
		},
		{
			name:                   "warning boundary included",
			tip:                    88,
			expiry:                 100,
			warning:                12,
			wantRisk:               RenewalRiskNearExpiry,
			wantRenewableNext:      true,
			wantNearWarning:        true,
			wantBlocksUntilLatest: 11,
		},
		{
			name:                   "one block before expiry is too late for next block",
			tip:                    99,
			expiry:                 100,
			warning:                12,
			wantRisk:               RenewalRiskTooLateNextBlock,
			wantNearWarning:        true,
			wantBlocksUntilLatest: 0,
		},
		{
			name:                   "expired at expiry height",
			tip:                    100,
			expiry:                 100,
			warning:                12,
			wantRisk:               RenewalRiskExpired,
			wantBlocksUntilLatest: 0,
		},
		{
			name:                   "negative warning clamps to zero",
			tip:                    98,
			expiry:                 100,
			warning:                -1,
			wantRisk:               RenewalRiskOK,
			wantRenewableNext:      true,
			wantBlocksUntilLatest: 1,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := EvaluateRenewalRisk(test.tip, test.expiry, test.warning)
			if got.Risk != test.wantRisk {
				t.Fatalf("risk mismatch: got %s want %s", got.Risk, test.wantRisk)
			}
			if got.RenewableInNextBlock != test.wantRenewableNext {
				t.Fatalf("renewable next mismatch: got %v want %v",
					got.RenewableInNextBlock, test.wantRenewableNext)
			}
			if got.NearExpiryWarning != test.wantNearWarning {
				t.Fatalf("near warning mismatch: got %v want %v",
					got.NearExpiryWarning, test.wantNearWarning)
			}
			if got.BlocksUntilLatestRenew != test.wantBlocksUntilLatest {
				t.Fatalf("blocks until latest mismatch: got %d want %d",
					got.BlocksUntilLatestRenew, test.wantBlocksUntilLatest)
			}
			if got.LatestRenewHeight != test.expiry-1 {
				t.Fatalf("latest renew height mismatch: got %d want %d",
					got.LatestRenewHeight, test.expiry-1)
			}
		})
	}
}

func TestBuildExpiryInfoWithRenewWarning(t *testing.T) {
	info, err := BuildExpiryInfoWithRenewWarning(
		100, 188, 100,
		25, 12,
		10000, 7000, 546,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if info.ExpiryHeight != 200 {
		t.Fatalf("expiry mismatch: %d", info.ExpiryHeight)
	}
	if info.Status != ExpiryStatusExpiring {
		t.Fatalf("expected expiring, got %s", info.Status)
	}
	if info.RenewalRisk != RenewalRiskNearExpiry {
		t.Fatalf("expected near-expiry risk, got %s", info.RenewalRisk)
	}
	if !info.RenewableInNextBlock || !info.NearExpiryWarning {
		t.Fatalf("unexpected renew flags: %+v", info)
	}
	if info.LatestRenewHeight != 199 || info.BlocksUntilLatestRenew != 11 {
		t.Fatalf("unexpected latest renew fields: %+v", info)
	}
}

func TestIsExpiredForSpendingUsesNextBlockHeight(t *testing.T) {
	t.Parallel()

	if !IsExpiredForSpending(&chaincfg.ObtcRegTestParams, 98, 241) {
		t.Fatalf("expected block 98 output to be expired at tip 241")
	}
	if IsExpiredForSpending(&chaincfg.ObtcRegTestParams, 99, 241) {
		t.Fatalf("expected block 99 output to remain spendable at tip 241")
	}
	if IsExpiredForSpending(&chaincfg.TestNet3Params, 98, 241) {
		t.Fatalf("non-OBTC networks must ignore expiry spendability")
	}
}
