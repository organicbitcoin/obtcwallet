package main

import (
	"encoding/json"
	"testing"
	"time"
)

func TestShouldRenewDirect(t *testing.T) {
	if !shouldRenew("expiring", false) {
		t.Fatalf("expiring should be selected")
	}
	if shouldRenew("expired", false) {
		t.Fatalf("expired should not be selected when includeExpired=false")
	}
	if !shouldRenew("expired", true) {
		t.Fatalf("expired should be selected when includeExpired=true")
	}
}

func TestNewRenewFilterDirect(t *testing.T) {
	if _, err := newRenewFilter(false, 100, -1); err == nil {
		t.Fatalf("expected error when only one window bound is set")
	}
	if _, err := newRenewFilter(false, 50, 100); err == nil {
		t.Fatalf("expected error when window-start < window-end")
	}

	f, err := newRenewFilter(true, -1, -1)
	if err != nil {
		t.Fatalf("unexpected error for disabled window filter: %v", err)
	}
	if f.useWindow {
		t.Fatalf("expected window filter disabled")
	}

	f, err = newRenewFilter(true, 100, 50)
	if err != nil {
		t.Fatalf("unexpected error for valid window filter: %v", err)
	}
	if !f.useWindow || f.windowStart != 100 || f.windowEnd != 50 {
		t.Fatalf("unexpected window filter value: %+v", f)
	}
}

func TestSelectOutpointsDirect(t *testing.T) {
	items := []getExpiryItem{
		{OutPoint: "a:0", Status: "ok", BlocksToExpiry: 300},
		{OutPoint: "b:0", Status: "expiring", BlocksToExpiry: 120},
		{OutPoint: "c:0", Status: "expired", BlocksToExpiry: -5},
		{OutPoint: "d:0", Status: "ok", BlocksToExpiry: 80},
	}

	statusFilter, err := newRenewFilter(true, -1, -1)
	if err != nil {
		t.Fatalf("new status filter: %v", err)
	}
	got := selectOutpoints(items, 0, statusFilter)
	if len(got) != 2 || got[0] != "b:0" || got[1] != "c:0" {
		t.Fatalf("unexpected status selection: %v", got)
	}

	windowFilter, err := newRenewFilter(false, 150, 50)
	if err != nil {
		t.Fatalf("new window filter: %v", err)
	}
	got = selectOutpoints(items, 0, windowFilter)
	if len(got) != 2 || got[0] != "b:0" || got[1] != "d:0" {
		t.Fatalf("unexpected window selection: %v", got)
	}

	got = selectOutpoints(items, 1, windowFilter)
	if len(got) != 1 || got[0] != "b:0" {
		t.Fatalf("unexpected window selection with limit=1: %v", got)
	}
}

func TestParseLoopConfigDirect(t *testing.T) {
	cfg, err := parseLoopConfig("", 1)
	if err != nil {
		t.Fatalf("unexpected run-once config error: %v", err)
	}
	if cfg.interval != 0 || cfg.runs != 0 {
		t.Fatalf("unexpected run-once config value: %+v", cfg)
	}

	if _, err := parseLoopConfig("", 2); err == nil {
		t.Fatalf("expected runs-without-interval error")
	}
	if _, err := parseLoopConfig("10m", -1); err == nil {
		t.Fatalf("expected negative-runs error")
	}
	if _, err := parseLoopConfig("not-a-duration", 1); err == nil {
		t.Fatalf("expected invalid-interval error")
	}

	cfg, err = parseLoopConfig("10s", 3)
	if err != nil {
		t.Fatalf("unexpected scheduled config error: %v", err)
	}
	if cfg.interval != 10*time.Second || cfg.runs != 3 {
		t.Fatalf("unexpected scheduled config value: %+v", cfg)
	}
}

func TestBuildRenewParamMessagesDirect(t *testing.T) {
	params := buildRenewParamMessages("abc:1", 0.01, "", 0, 2)
	if len(params) != 5 {
		t.Fatalf("unexpected params len: %d", len(params))
	}
	var outpoints []string
	if err := json.Unmarshal(params[0], &outpoints); err != nil {
		t.Fatalf("decode outpoints: %v", err)
	}
	if len(outpoints) != 1 || outpoints[0] != "abc:1" {
		t.Fatalf("bad outpoints param: %v", outpoints)
	}
	var target *string
	if err := json.Unmarshal(params[2], &target); err != nil {
		t.Fatalf("decode target: %v", err)
	}
	if target != nil {
		t.Fatalf("expected nil target when empty")
	}
}

func TestMustRawDirect(t *testing.T) {
	r := mustRaw(map[string]int{"a": 1})
	if len(r) == 0 {
		t.Fatalf("mustRaw should return non-empty json")
	}
}
