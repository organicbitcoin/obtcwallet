package wallet

import "testing"

func TestDefaultAutoRenewPolicyDirect(t *testing.T) {
	p := DefaultAutoRenewPolicy()
	if p.Enabled {
		t.Fatalf("default should be disabled")
	}
	if p.WindowStartBlocks < p.WindowEndBlocks {
		t.Fatalf("default window invalid")
	}
}

func TestValidateAutoRenewPolicyDirect(t *testing.T) {
	ok := AutoRenewPolicy{Enabled: true, WindowStartBlocks: 100, WindowEndBlocks: 50, MaxUtxosPerRun: 10, MaxFeeRateSatPerKB: 1000}
	if err := ValidateAutoRenewPolicy(ok); err != nil {
		t.Fatalf("expected valid policy, got %v", err)
	}

	bad := ok
	bad.WindowStartBlocks = 1
	bad.WindowEndBlocks = 2
	if err := ValidateAutoRenewPolicy(bad); err == nil {
		t.Fatalf("expected invalid window error")
	}

	bad = ok
	bad.MaxUtxosPerRun = 0
	if err := ValidateAutoRenewPolicy(bad); err == nil {
		t.Fatalf("expected invalid max utxos error")
	}
}

func TestIsInAutoRenewWindowDirect(t *testing.T) {
	p := AutoRenewPolicy{Enabled: true, WindowStartBlocks: 100, WindowEndBlocks: 50, MaxUtxosPerRun: 10, MaxFeeRateSatPerKB: 1000}
	if !IsInAutoRenewWindow(75, p) {
		t.Fatalf("75 should be in window")
	}
	if IsInAutoRenewWindow(120, p) {
		t.Fatalf("120 should be out of window")
	}
	if IsInAutoRenewWindow(40, p) {
		t.Fatalf("40 should be out of window")
	}
	p.Enabled = false
	if IsInAutoRenewWindow(75, p) {
		t.Fatalf("disabled policy should always be false")
	}
}

func TestSelectAutoRenewCandidatesDirect(t *testing.T) {
	p := AutoRenewPolicy{Enabled: true, WindowStartBlocks: 100, WindowEndBlocks: 50, MaxUtxosPerRun: 2, MaxFeeRateSatPerKB: 1000}
	items := []ExpiryInfo{
		{BlocksToExpiry: 120},
		{BlocksToExpiry: 90},
		{BlocksToExpiry: 70},
		{BlocksToExpiry: 40},
	}
	idx := SelectAutoRenewCandidates(items, p)
	if len(idx) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(idx))
	}
	if idx[0] != 1 || idx[1] != 2 {
		t.Fatalf("unexpected selected indexes: %v", idx)
	}
}
