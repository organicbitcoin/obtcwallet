package legacyrpc

import (
	"testing"

	"github.com/btcsuite/btcd/chaincfg"
)

func TestLegacyInfoTestNetFlag(t *testing.T) {
	tests := []struct {
		name   string
		params *chaincfg.Params
		want   bool
	}{
		{
			name:   "bitcoin mainnet",
			params: &chaincfg.MainNetParams,
			want:   false,
		},
		{
			name:   "bitcoin testnet3",
			params: &chaincfg.TestNet3Params,
			want:   true,
		},
		{
			name:   "bitcoin testnet4",
			params: &chaincfg.TestNet4Params,
			want:   true,
		},
		{
			name:   "bitcoin regtest",
			params: &chaincfg.RegressionNetParams,
			want:   true,
		},
		{
			name:   "bitcoin simnet",
			params: &chaincfg.SimNetParams,
			want:   true,
		},
		{
			name:   "obtc mainnet",
			params: &chaincfg.ObtcMainNetParams,
			want:   false,
		},
		{
			name:   "obtc testnet",
			params: &chaincfg.ObtcTestNetParams,
			want:   true,
		},
		{
			name:   "obtc regtest",
			params: &chaincfg.ObtcRegTestParams,
			want:   true,
		},
		{
			name:   "nil",
			params: nil,
			want:   false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := legacyInfoTestNetFlag(test.params)
			if got != test.want {
				t.Fatalf("unexpected testnet flag: got %v want %v",
					got, test.want)
			}
		})
	}
}
