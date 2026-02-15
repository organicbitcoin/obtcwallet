package legacyrpc

import (
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcwallet/wallet"
)

func testOutPoint(i uint32) wire.OutPoint {
	return wire.OutPoint{Hash: chainhash.Hash{byte(i)}, Index: i}
}

func TestMakeGetExpiryResultDirect(t *testing.T) {
	outputs := []*wallet.TransactionOutput{
		{
			OutPoint: testOutPoint(2),
			Output:   wire.TxOut{Value: 1000},
			ContainingBlock: wallet.BlockIdentity{
				Height: 10,
			},
		},
		{
			OutPoint: testOutPoint(1),
			Output:   wire.TxOut{Value: 2000},
			ContainingBlock: wallet.BlockIdentity{
				Height: 20,
			},
		},
	}
	before := int32(200)
	items, err := makeGetExpiryResult(outputs, 30, 100, 50, 10, &before)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].ExpiryHeight > items[1].ExpiryHeight {
		t.Fatalf("expected sorted by expiry height")
	}
}

func TestMakeGetExpiryResultLimitAndFilter(t *testing.T) {
	outputs := []*wallet.TransactionOutput{
		{OutPoint: testOutPoint(1), Output: wire.TxOut{Value: 1000}, ContainingBlock: wallet.BlockIdentity{Height: 10}},
		{OutPoint: testOutPoint(2), Output: wire.TxOut{Value: 1000}, ContainingBlock: wallet.BlockIdentity{Height: 20}},
		{OutPoint: testOutPoint(3), Output: wire.TxOut{Value: 1000}, ContainingBlock: wallet.BlockIdentity{Height: 30}},
	}
	before := int32(125) // with window 100, keep heights 10 and 20 only
	items, err := makeGetExpiryResult(outputs, 0, 100, 10, 1, &before)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected limit+filter to return 1 item, got %d", len(items))
	}
}

func TestGetExpiryDirectNilWallet(t *testing.T) {
	_, err := getExpiry(&GetExpiryCmd{}, nil)
	if err == nil {
		t.Fatalf("expected nil wallet error")
	}
}

func TestParseOutPointStringDirect(t *testing.T) {
	h := chainhash.Hash{1}
	op, err := parseOutPointString(h.String() + ":7")
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if op.Hash != h || op.Index != 7 {
		t.Fatalf("parsed outpoint mismatch: %+v", op)
	}

	if _, err := parseOutPointString("bad"); err == nil {
		t.Fatalf("expected bad format error")
	}
	if _, err := parseOutPointString("nothex:1"); err == nil {
		t.Fatalf("expected bad hash error")
	}
	if _, err := parseOutPointString(h.String() + ":x"); err == nil {
		t.Fatalf("expected bad index error")
	}
}

func TestParseOutPointStringsDirect(t *testing.T) {
	h1 := chainhash.Hash{1}
	h2 := chainhash.Hash{2}
	ops, err := parseOutPointStrings([]string{h1.String() + ":0", h2.String() + ":1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 outpoints, got %d", len(ops))
	}
	if _, err := parseOutPointStrings(nil); err == nil {
		t.Fatalf("expected empty list error")
	}
}

func TestGetRenewDirectNilWallet(t *testing.T) {
	_, err := getRenew(&RenewCmd{OutPoints: []string{"x:0"}, Amount: 1}, nil)
	if err == nil {
		t.Fatalf("expected nil wallet error")
	}
}

func TestParseRenewMinConfDirect(t *testing.T) {
	if got, err := parseRenewMinConf(nil); err != nil || got != 0 {
		t.Fatalf("nil minconf unexpected: got=%d err=%v", got, err)
	}
	v := int32(3)
	if got, err := parseRenewMinConf(&v); err != nil || got != 3 {
		t.Fatalf("minconf parse mismatch: got=%d err=%v", got, err)
	}
	neg := int32(-1)
	if _, err := parseRenewMinConf(&neg); err == nil {
		t.Fatalf("expected negative minconf error")
	}
}

func TestParseRenewFeeRateDirect(t *testing.T) {
	if got, err := parseRenewFeeRate(nil); err != nil || got <= 0 {
		t.Fatalf("default fee rate parse failed: got=%d err=%v", got, err)
	}
	v := 2.5
	if got, err := parseRenewFeeRate(&v); err != nil || got <= 0 {
		t.Fatalf("custom fee rate parse failed: got=%d err=%v", got, err)
	}
	bad := 0.0
	if _, err := parseRenewFeeRate(&bad); err == nil {
		t.Fatalf("expected non-positive fee rate error")
	}
}

func TestParseRenewAmountDirect(t *testing.T) {
	if got, err := parseRenewAmount(0.0001); err != nil || got <= 0 {
		t.Fatalf("amount parse failed: got=%d err=%v", got, err)
	}
	if _, err := parseRenewAmount(0); err == nil {
		t.Fatalf("expected zero amount error")
	}
	if _, err := parseRenewAmount(-1); err == nil {
		t.Fatalf("expected negative amount error")
	}
}

func TestGetRenewDirectParamValidation(t *testing.T) {
	// Parse/validation paths run before wallet interaction.
	_, err := getRenew(&RenewCmd{OutPoints: nil, Amount: 1}, &wallet.Wallet{})
	if err == nil {
		t.Fatalf("expected outpoints validation error")
	}
	_, err = getRenew(&RenewCmd{OutPoints: []string{"bad"}, Amount: 1}, &wallet.Wallet{})
	if err == nil {
		t.Fatalf("expected outpoint parse error")
	}
	_, err = getRenew(&RenewCmd{OutPoints: []string{"00:0"}, Amount: 0}, &wallet.Wallet{})
	if err == nil {
		t.Fatalf("expected positive amount error")
	}
}
