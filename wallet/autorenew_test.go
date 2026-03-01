package wallet

import (
	"testing"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
)

func TestDefaultAutoRenewPolicyDirect(t *testing.T) {
	p := DefaultAutoRenewPolicy()
	if p.Enabled {
		t.Fatalf("default should be disabled")
	}
	if p.WindowStartBlocks < p.WindowEndBlocks {
		t.Fatalf("default window invalid")
	}
}

func TestDefaultAutoRenewRuntimeConfigDirect(t *testing.T) {
	cfg := DefaultAutoRenewRuntimeConfig()
	if cfg.Interval <= 0 {
		t.Fatalf("default interval should be positive")
	}
	if cfg.FailureBackoff <= 0 {
		t.Fatalf("default failure backoff should be positive")
	}
	if cfg.Policy.MaxUtxosPerRun <= 0 {
		t.Fatalf("default max utxos should be positive")
	}
	if cfg.Policy.Enabled {
		t.Fatalf("default runtime config should be disabled")
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

func TestValidateAutoRenewRuntimeConfigDirect(t *testing.T) {
	cfg := DefaultAutoRenewRuntimeConfig()
	cfg.Policy.Enabled = true
	cfg.Amount = btcutil.Amount(50_000)
	if err := ValidateAutoRenewRuntimeConfig(cfg); err != nil {
		t.Fatalf("expected valid runtime config, got %v", err)
	}

	bad := cfg
	bad.Amount = 0
	if err := ValidateAutoRenewRuntimeConfig(bad); err == nil {
		t.Fatalf("expected amount validation error when enabled")
	}

	bad = cfg
	bad.Interval = 0
	if err := ValidateAutoRenewRuntimeConfig(bad); err == nil {
		t.Fatalf("expected interval validation error")
	}

	bad = cfg
	bad.FailureBackoff = -1
	if err := ValidateAutoRenewRuntimeConfig(bad); err == nil {
		t.Fatalf("expected failure backoff validation error")
	}

	bad = cfg
	bad.MaxRenewAmountPerRun = -1
	if err := ValidateAutoRenewRuntimeConfig(bad); err == nil {
		t.Fatalf("expected max renew amount per run validation error")
	}

	bad = cfg
	bad.MinConf = -1
	if err := ValidateAutoRenewRuntimeConfig(bad); err == nil {
		t.Fatalf("expected minconf validation error")
	}

	bad = cfg
	bad.ExpiryWindowBlocks = 0
	if err := ValidateAutoRenewRuntimeConfig(bad); err == nil {
		t.Fatalf("expected expiry window validation error")
	}

	bad = cfg
	bad.MaxRenewAmountPerRun = cfg.Amount - 1
	if err := ValidateAutoRenewRuntimeConfig(bad); err == nil {
		t.Fatalf("expected max renew amount >= amount validation error")
	}
}

func TestNormalizeAutoRenewRuntimeConfigDirect(t *testing.T) {
	cfg := AutoRenewRuntimeConfig{
		Policy: DefaultAutoRenewPolicy(),
	}
	cfg = normalizeAutoRenewRuntimeConfig(cfg)
	if cfg.Interval != 30*time.Minute {
		t.Fatalf("unexpected default interval after normalization: %v", cfg.Interval)
	}
	if cfg.Label == "" {
		t.Fatalf("label should be defaulted")
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

func TestLimitAutoRenewCandidatesByBudgetDirect(t *testing.T) {
	mkCandidate := func(id byte) autoRenewCandidate {
		var h chainhash.Hash
		h[0] = id
		return autoRenewCandidate{outpoint: wire.OutPoint{Hash: h, Index: 0}}
	}

	candidates := []autoRenewCandidate{mkCandidate(1), mkCandidate(2), mkCandidate(3)}
	renewAmount := btcutil.Amount(1000)

	if got := limitAutoRenewCandidatesByBudget(candidates, renewAmount, 0); len(got) != 3 {
		t.Fatalf("expected unlimited budget to keep all candidates, got=%d", len(got))
	}

	if got := limitAutoRenewCandidatesByBudget(candidates, renewAmount, 500); len(got) != 0 {
		t.Fatalf("expected too-small budget to remove all candidates, got=%d", len(got))
	}

	if got := limitAutoRenewCandidatesByBudget(candidates, renewAmount, 2000); len(got) != 2 {
		t.Fatalf("expected budget to cap candidates to 2, got=%d", len(got))
	}
}
