package main

import (
	"testing"
	"time"
)

func testDefaultAutoRenewConfig() *config {
	return &config{
		AutoRenewEnabled:              false,
		AutoRenewInterval:             30 * time.Minute,
		AutoRenewFailureBackoff:       15 * time.Minute,
		AutoRenewAmount:               0,
		AutoRenewMaxRenewAmountPerRun: 0,
		AutoRenewMinConf:              0,
		AutoRenewWindowStart:          52560,
		AutoRenewWindowEnd:            25920,
		AutoRenewMaxUtxos:             100,
		AutoRenewMaxFeeRateSatPerKB:   5000,
		AutoRenewExpiryWindowBlocks:   3679200,
		AutoRenewExpiringThreshold:    25920,
	}
}

func TestAutoRenewRuntimeConfigFromOptionsDisabled(t *testing.T) {
	cfg := testDefaultAutoRenewConfig()
	out, err := autoRenewRuntimeConfigFromOptions(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.Policy.Enabled {
		t.Fatalf("expected disabled policy")
	}
	if out.Amount != 0 {
		t.Fatalf("expected zero amount when not configured, got %d", out.Amount)
	}
}

func TestAutoRenewRuntimeConfigFromOptionsEnabledNeedsAmount(t *testing.T) {
	cfg := testDefaultAutoRenewConfig()
	cfg.AutoRenewEnabled = true
	if _, err := autoRenewRuntimeConfigFromOptions(cfg); err == nil {
		t.Fatalf("expected missing amount error when auto-renew is enabled")
	}
}

func TestAutoRenewRuntimeConfigFromOptionsEnabledValid(t *testing.T) {
	cfg := testDefaultAutoRenewConfig()
	cfg.AutoRenewEnabled = true
	cfg.AutoRenewAmount = 0.5
	cfg.AutoRenewInterval = 45 * time.Minute
	cfg.AutoRenewFailureBackoff = 20 * time.Minute
	cfg.AutoRenewMaxRenewAmountPerRun = 1.5
	out, err := autoRenewRuntimeConfigFromOptions(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !out.Policy.Enabled {
		t.Fatalf("expected enabled policy")
	}
	if out.Amount <= 0 {
		t.Fatalf("expected positive amount, got %d", out.Amount)
	}
	if out.Interval != 45*time.Minute {
		t.Fatalf("unexpected interval: %v", out.Interval)
	}
	if out.FailureBackoff != 20*time.Minute {
		t.Fatalf("unexpected failure backoff: %v", out.FailureBackoff)
	}
	if out.MaxRenewAmountPerRun <= out.Amount {
		t.Fatalf("expected max renew amount per run > amount, got amount=%d max=%d",
			out.Amount, out.MaxRenewAmountPerRun,
		)
	}
}

func TestAutoRenewRuntimeConfigFromOptionsNegativeAmount(t *testing.T) {
	cfg := testDefaultAutoRenewConfig()
	cfg.AutoRenewAmount = -1
	if _, err := autoRenewRuntimeConfigFromOptions(cfg); err == nil {
		t.Fatalf("expected negative amount error")
	}
}

func TestAutoRenewRuntimeConfigFromOptionsNegativeMaxRenewAmountPerRun(t *testing.T) {
	cfg := testDefaultAutoRenewConfig()
	cfg.AutoRenewMaxRenewAmountPerRun = -1
	if _, err := autoRenewRuntimeConfigFromOptions(cfg); err == nil {
		t.Fatalf("expected negative max renew amount per run error")
	}
}

func TestAutoRenewRuntimeConfigFromOptionsTooSmallMaxRenewAmountPerRun(t *testing.T) {
	cfg := testDefaultAutoRenewConfig()
	cfg.AutoRenewEnabled = true
	cfg.AutoRenewAmount = 0.5
	cfg.AutoRenewMaxRenewAmountPerRun = 0.1
	if _, err := autoRenewRuntimeConfigFromOptions(cfg); err == nil {
		t.Fatalf("expected max renew amount per run validation error")
	}
}
