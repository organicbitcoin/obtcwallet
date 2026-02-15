package wallet

import "testing"

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
