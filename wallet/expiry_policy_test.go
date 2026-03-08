package wallet

import (
	"os"
	"path/filepath"
	"strings"
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

func TestParseOBTCExpiryProfilesFile(t *testing.T) {
	t.Parallel()

	path := writeTempOBTCParamsFile(t)

	profiles, err := parseOBTCExpiryProfilesFile(path)
	if err != nil {
		t.Fatalf("parseOBTCExpiryProfilesFile error: %v", err)
	}

	mainnet, ok := profiles["obtcmainnet"]
	if !ok {
		t.Fatalf("missing obtcmainnet profile")
	}
	if mainnet.WindowBlocks != 362880 || mainnet.DustThresholdSat != 720 {
		t.Fatalf("unexpected mainnet profile: %+v", mainnet)
	}

	regtest, ok := profiles["obtcregtest"]
	if !ok {
		t.Fatalf("missing obtcregtest profile")
	}
	if regtest.WindowBlocks != 144 || regtest.DustThresholdSat != 720 {
		t.Fatalf("unexpected regtest profile: %+v", regtest)
	}
}

func TestResolveExpiryPolicyFromSources(t *testing.T) {
	t.Parallel()

	path := writeTempOBTCParamsFile(t)
	params := &chaincfg.Params{Name: "obtctestnet"}

	policy, warnings := resolveExpiryPolicyFromSources(
		params, []string{path}, "",
	)

	if policy.WindowBlocks != 1008 {
		t.Fatalf("unexpected window blocks: %d", policy.WindowBlocks)
	}
	if policy.DustThresholdSat != 720 {
		t.Fatalf("unexpected dust threshold: %d", policy.DustThresholdSat)
	}
	if policy.ExpiringThresholdBlocks != 252 {
		t.Fatalf("unexpected threshold: %d", policy.ExpiringThresholdBlocks)
	}
	if !strings.Contains(policy.Source, "obtcd_chaincfg:") {
		t.Fatalf("unexpected source: %s", policy.Source)
	}
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
}

func TestResolveExpiryPolicyFallsBackToCompatibilityDefaults(t *testing.T) {
	t.Parallel()

	params := &chaincfg.Params{Name: "mainnet"}
	policy, warnings := resolveExpiryPolicyFromSources(params, nil, "")

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

func TestResolveExpiryPolicyUsesNetworkOverride(t *testing.T) {
	t.Parallel()

	path := writeTempOBTCParamsFile(t)
	params := &chaincfg.Params{Name: "mainnet"}

	policy, warnings := resolveExpiryPolicyFromSources(
		params, []string{path}, "obtcregtest",
	)

	if policy.WindowBlocks != 144 {
		t.Fatalf("unexpected override window: %d", policy.WindowBlocks)
	}
	if policy.Source == "compatibility_default" {
		t.Fatalf("expected non-default source")
	}
	if len(warnings) == 0 {
		t.Fatalf("expected override warning")
	}
}

func writeTempOBTCParamsFile(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "params_obtc.go")
	content := `package chaincfg

import "github.com/btcsuite/btcd/wire"

type Params struct {
	Name string
	Net  wire.BitcoinNet
}

type ExpiryParams struct {
	WindowBlocks       uint64
	ReapDustThresholdSat int64
}

var ObtcMainNetParams = Params{
	Name: "obtcmainnet",
}

var ObtcTestNetParams = Params{
	Name: "obtctestnet",
}

var ObtcRegTestParams = Params{
	Name: "obtcregtest",
}

func GetExpiryParams(params *Params) *ExpiryParams {
	switch params.Net {
	case wire.ObtcMainNet:
		return &ExpiryParams{
			WindowBlocks:         362880,
			ReapDustThresholdSat: 720,
		}
	case wire.ObtcTestNet:
		return &ExpiryParams{
			WindowBlocks:         1008,
			ReapDustThresholdSat: 720,
		}
	case wire.ObtcRegNet:
		return &ExpiryParams{
			WindowBlocks:         144,
			ReapDustThresholdSat: 720,
		}
	default:
		return nil
	}
}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp params file: %v", err)
	}

	return path
}
