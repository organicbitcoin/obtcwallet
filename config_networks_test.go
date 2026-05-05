package main

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/btcsuite/btcwallet/netparams"
)

func loadConfigForTest(t *testing.T, args ...string) (*config, error) {
	t.Helper()

	oldArgs := os.Args
	oldActiveNet := activeNet
	t.Cleanup(func() {
		os.Args = oldArgs
		activeNet = oldActiveNet
	})

	baseArgs := []string{
		"btcwallet",
		"--appdata", t.TempDir(),
		"--noinitialload",
		"--noclienttls",
	}
	os.Args = append(baseArgs, args...)

	cfg, _, err := loadConfig()
	return cfg, err
}

func assertListenerPort(t *testing.T, listeners []string, wantPort string) {
	t.Helper()

	if len(listeners) == 0 {
		t.Fatalf("expected default listeners to be configured")
	}

	for _, listener := range listeners {
		_, port, err := net.SplitHostPort(listener)
		if err != nil {
			t.Fatalf("listener %q is not host:port: %v", listener, err)
		}
		if port != wantPort {
			t.Fatalf("listener %q has port %q, want %q", listener, port, wantPort)
		}
	}
}

func TestLoadConfigOBTCNetworkSelection(t *testing.T) {
	testCases := []struct {
		name           string
		flag           string
		wantParams     *netparams.Params
		wantClientPort string
		wantServerPort string
	}{
		{
			name:           "obtc mainnet",
			flag:           "--obtcmainnet",
			wantParams:     &netparams.ObtcMainNetParams,
			wantClientPort: netparams.ObtcMainNetParams.RPCClientPort,
			wantServerPort: netparams.ObtcMainNetParams.RPCServerPort,
		},
		{
			name:           "obtc testnet",
			flag:           "--obtctestnet",
			wantParams:     &netparams.ObtcTestNetParams,
			wantClientPort: netparams.ObtcTestNetParams.RPCClientPort,
			wantServerPort: netparams.ObtcTestNetParams.RPCServerPort,
		},
		{
			name:           "obtc regtest",
			flag:           "--obtcregtest",
			wantParams:     &netparams.ObtcRegTestParams,
			wantClientPort: netparams.ObtcRegTestParams.RPCClientPort,
			wantServerPort: netparams.ObtcRegTestParams.RPCServerPort,
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := loadConfigForTest(t, tc.flag)
			if err != nil {
				t.Fatalf("loadConfig returned error: %v", err)
			}

			if activeNet.Params.Name != tc.wantParams.Params.Name {
				t.Fatalf("activeNet name = %q, want %q",
					activeNet.Params.Name, tc.wantParams.Params.Name,
				)
			}
			if activeNet.RPCClientPort != tc.wantClientPort {
				t.Fatalf("RPC client port = %q, want %q",
					activeNet.RPCClientPort, tc.wantClientPort,
				)
			}
			if activeNet.RPCServerPort != tc.wantServerPort {
				t.Fatalf("RPC server port = %q, want %q",
					activeNet.RPCServerPort, tc.wantServerPort,
				)
			}
			if cfg.RPCConnect != net.JoinHostPort("localhost", tc.wantClientPort) {
				t.Fatalf("rpcconnect = %q, want localhost:%s",
					cfg.RPCConnect, tc.wantClientPort,
				)
			}

			assertListenerPort(t, cfg.LegacyRPCListeners, tc.wantServerPort)

			if gotBase := filepath.Base(cfg.LogDir); gotBase != tc.wantParams.Params.Name {
				t.Fatalf("log dir base = %q, want %q",
					gotBase, tc.wantParams.Params.Name,
				)
			}
		})
	}
}

func TestLoadConfigRejectsMixedOBTCNetworkFlags(t *testing.T) {
	testCases := []struct {
		name string
		args []string
	}{
		{
			name: "bitcoin and obtc testnet",
			args: []string{"--testnet", "--obtctestnet"},
		},
		{
			name: "obtc testnet and obtc regtest",
			args: []string{"--obtctestnet", "--obtcregtest"},
		},
		{
			name: "obtc mainnet and signet",
			args: []string{"--obtcmainnet", "--signet"},
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := loadConfigForTest(t, tc.args...)
			if err == nil {
				t.Fatalf("expected mutually exclusive network error")
			}
			if !strings.Contains(err.Error(), "can't be used together") {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
