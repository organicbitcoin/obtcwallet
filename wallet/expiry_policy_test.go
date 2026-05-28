package wallet

import (
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
)

func TestDefaultExpiringThresholdBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		window uint64
		want   int32
	}{
		{window: 362880, want: 25920},
		{window: 1008, want: 252},
		{window: 144, want: 36},
		{window: 1, want: 1},
	}

	for _, test := range tests {
		if got := defaultExpiringThresholdBlocks(test.window); got != test.want {
			t.Fatalf("window=%d threshold=%d want=%d",
				test.window, got, test.want)
		}
	}
}

func TestResolveExpiryPolicyOBTCMainNet(t *testing.T) {
	t.Parallel()

	policy, warnings := ResolveExpiryPolicy(&chaincfg.ObtcMainNetParams)

	if policy.WindowBlocks != 362880 {
		t.Fatalf("unexpected window blocks: %d", policy.WindowBlocks)
	}
	if policy.DustThresholdSat != 720 {
		t.Fatalf("unexpected dust threshold: %d", policy.DustThresholdSat)
	}
	if policy.ExpiringThresholdBlocks != 25920 {
		t.Fatalf("unexpected threshold: %d", policy.ExpiringThresholdBlocks)
	}
	if policy.Source != "obtcd_chaincfg" {
		t.Fatalf("unexpected source: %s", policy.Source)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

func TestResolveExpiryPolicyOBTCTestNet(t *testing.T) {
	t.Parallel()

	policy, warnings := ResolveExpiryPolicy(&chaincfg.ObtcTestNetParams)

	if policy.WindowBlocks != 144 {
		t.Fatalf("unexpected window blocks: %d", policy.WindowBlocks)
	}
	if policy.DustThresholdSat != 720 {
		t.Fatalf("unexpected dust threshold: %d", policy.DustThresholdSat)
	}
	if policy.ExpiringThresholdBlocks != 36 {
		t.Fatalf("unexpected threshold: %d", policy.ExpiringThresholdBlocks)
	}
	if policy.Source != "obtcd_chaincfg" {
		t.Fatalf("unexpected source: %s", policy.Source)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

func TestResolveExpiryPolicyOBTCRegTest(t *testing.T) {
	t.Parallel()

	policy, warnings := ResolveExpiryPolicy(&chaincfg.ObtcRegTestParams)

	if policy.WindowBlocks != 144 {
		t.Fatalf("unexpected window blocks: %d", policy.WindowBlocks)
	}
	if policy.DustThresholdSat != 720 {
		t.Fatalf("unexpected dust threshold: %d", policy.DustThresholdSat)
	}
	if policy.ExpiringThresholdBlocks != 36 {
		t.Fatalf("unexpected threshold: %d", policy.ExpiringThresholdBlocks)
	}
	if policy.Source != "obtcd_chaincfg" {
		t.Fatalf("unexpected source: %s", policy.Source)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

func TestResolveExpiryPolicyFallsBackForBitcoinMainNet(t *testing.T) {
	t.Parallel()

	policy, warnings := ResolveExpiryPolicy(&chaincfg.MainNetParams)

	if policy.WindowBlocks != CompatibilityExpiryWindowBlocks {
		t.Fatalf("unexpected fallback window: %d", policy.WindowBlocks)
	}
	if policy.DustThresholdSat != CompatibilityDustThresholdSat {
		t.Fatalf("unexpected fallback dust: %d", policy.DustThresholdSat)
	}
	if policy.ExpiringThresholdBlocks != maxDefaultExpiringThresholdBlocks {
		t.Fatalf("unexpected fallback threshold: %d",
			policy.ExpiringThresholdBlocks)
	}
	if policy.Source != "compatibility_default" {
		t.Fatalf("unexpected fallback source: %s", policy.Source)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected fallback warnings")
	}
}

func TestResolveExpiryPolicyNilParams(t *testing.T) {
	t.Parallel()

	policy, warnings := ResolveExpiryPolicy(nil)

	if policy.WindowBlocks != CompatibilityExpiryWindowBlocks {
		t.Fatalf("unexpected fallback window: %d", policy.WindowBlocks)
	}
	if policy.Source != "compatibility_default" {
		t.Fatalf("unexpected fallback source: %s", policy.Source)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected fallback warnings")
	}
}
