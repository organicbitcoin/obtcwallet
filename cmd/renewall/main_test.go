package main

import (
	"encoding/json"
	"testing"
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

func TestSelectOutpointsDirect(t *testing.T) {
	items := []getExpiryItem{
		{OutPoint: "a:0", Status: "ok"},
		{OutPoint: "b:0", Status: "expiring"},
		{OutPoint: "c:0", Status: "expired"},
	}
	got := selectOutpoints(items, 1, true)
	if len(got) != 1 || got[0] != "b:0" {
		t.Fatalf("unexpected selection with limit=1: %v", got)
	}
	got = selectOutpoints(items, 0, true)
	if len(got) != 2 || got[0] != "b:0" || got[1] != "c:0" {
		t.Fatalf("unexpected unlimited selection: %v", got)
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
