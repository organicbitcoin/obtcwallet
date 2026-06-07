package legacyrpc

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/btcsuite/btcd/btcjson"
	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcwallet/waddrmgr"
	"github.com/btcsuite/btcwallet/wallet"
	"github.com/btcsuite/btcwallet/wallet/txrules"
)

const (
	defaultExpiryResultLimit = 100
)

type GetExpiryCmd struct {
	Limit             *int
	BeforeHeight      *int32
	WindowBlocks      *uint64
	ExpiringThreshold *int32
}

type GetExpiryResultItem struct {
	OutPoint               string `json:"outpoint"`
	AmountSat              int64  `json:"amount_sat"`
	CreateHeight           int32  `json:"create_height"`
	ExpiryHeight           int32  `json:"expiry_height"`
	BlocksToExpiry         int32  `json:"blocks_to_expiry"`
	DaysToExpiry           int32  `json:"days_to_expiry"`
	Status                 string `json:"status"`
	DustRisk               bool   `json:"dust_risk"`
	LatestRenewHeight      int32  `json:"latest_renew_height"`
	BlocksUntilLatestRenew int32  `json:"blocks_until_latest_renew"`
	RenewableInNextBlock   bool   `json:"renewable_in_next_block"`
	NearExpiryWarning      bool   `json:"near_expiry_warning"`
	RenewalRisk            string `json:"renewal_risk"`
}

type GetExpiryResult struct {
	TipHeight          int32                 `json:"tip_height"`
	WindowBlocks       uint64                `json:"window_blocks"`
	RenewWarningBlocks int32                 `json:"renew_warning_blocks"`
	Items              []GetExpiryResultItem `json:"items"`
}

type RenewCmd struct {
	OutPoints     []string
	Amount        float64
	TargetAddress *string
	MaxFeeRate    *float64
	MinConf       *int32
}

type RenewResult struct {
	TxID            string   `json:"txid"`
	InputCount      int      `json:"input_count"`
	OutputCount     int      `json:"output_count"`
	FeeRateSatPerKB int64    `json:"fee_rate_sat_per_kb"`
	TargetAddress   string   `json:"target_address"`
	Warnings        []string `json:"warnings,omitempty"`
}

func init() {
	_ = btcjson.RegisterCmd("obtc.getexpiry", (*GetExpiryCmd)(nil), btcjson.UFWalletOnly)
	_ = btcjson.RegisterCmd("obtc.renew", (*RenewCmd)(nil), btcjson.UFWalletOnly)
}

func makeGetExpiryResult(outputs []*wallet.TransactionOutput, tipHeight int32, windowBlocks uint64,
	expiringThreshold, renewWarningBlocks int32, dustThresholdSat int64, limit int,
	beforeHeight *int32) ([]GetExpiryResultItem, error) {
	items := make([]GetExpiryResultItem, 0, len(outputs))
	for _, out := range outputs {
		createHeight := out.ContainingBlock.Height
		if createHeight < 0 {
			continue
		}
		amountSat := out.Output.Value
		reclaimSat := (amountSat * 70) / 100
		info, err := wallet.BuildExpiryInfoWithRenewWarning(
			createHeight, tipHeight, windowBlocks, expiringThreshold,
			renewWarningBlocks, amountSat, reclaimSat, dustThresholdSat,
		)
		if err != nil {
			return nil, err
		}
		if beforeHeight != nil && info.ExpiryHeight > *beforeHeight {
			continue
		}
		items = append(items, GetExpiryResultItem{
			OutPoint:               out.OutPoint.String(),
			AmountSat:              amountSat,
			CreateHeight:           info.CreateHeight,
			ExpiryHeight:           info.ExpiryHeight,
			BlocksToExpiry:         info.BlocksToExpiry,
			DaysToExpiry:           info.DaysToExpiry,
			Status:                 string(info.Status),
			DustRisk:               info.DustRisk,
			LatestRenewHeight:      info.LatestRenewHeight,
			BlocksUntilLatestRenew: info.BlocksUntilLatestRenew,
			RenewableInNextBlock:   info.RenewableInNextBlock,
			NearExpiryWarning:      info.NearExpiryWarning,
			RenewalRisk:            string(info.RenewalRisk),
		})
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].ExpiryHeight != items[j].ExpiryHeight {
			return items[i].ExpiryHeight < items[j].ExpiryHeight
		}
		return items[i].OutPoint < items[j].OutPoint
	})

	if limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func getExpiry(icmd interface{}, w *wallet.Wallet) (interface{}, error) {
	if w == nil {
		return nil, fmt.Errorf("wallet is nil")
	}
	cmd := icmd.(*GetExpiryCmd)

	limit := defaultExpiryResultLimit
	if cmd.Limit != nil {
		if *cmd.Limit < 0 {
			return nil, &btcjson.RPCError{Code: btcjson.ErrRPCInvalidParameter, Message: "limit must be >= 0"}
		}
		limit = *cmd.Limit
	}

	resolvedPolicy, _ := wallet.ResolveExpiryPolicy(w.ChainParams())

	windowBlocks := resolvedPolicy.WindowBlocks
	if cmd.WindowBlocks != nil {
		windowBlocks = *cmd.WindowBlocks
	}
	expiringThreshold := resolvedPolicy.ExpiringThresholdBlocks
	if cmd.ExpiringThreshold != nil {
		expiringThreshold = *cmd.ExpiringThreshold
	}

	outputs, err := w.UnspentOutputs(wallet.OutputSelectionPolicy{
		Account:               waddrmgr.DefaultAccountNum,
		RequiredConfirmations: 0,
		IncludeExpired:        true,
	})
	if err != nil {
		return nil, err
	}

	tip := w.Manager.SyncedTo().Height
	items, err := makeGetExpiryResult(
		outputs, tip, windowBlocks, expiringThreshold,
		resolvedPolicy.RenewWarningBlocks, resolvedPolicy.DustThresholdSat,
		limit, cmd.BeforeHeight,
	)
	if err != nil {
		return nil, err
	}
	return GetExpiryResult{
		TipHeight:          tip,
		WindowBlocks:       windowBlocks,
		RenewWarningBlocks: resolvedPolicy.RenewWarningBlocks,
		Items:              items,
	}, nil
}

func parseOutPointString(s string) (wire.OutPoint, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return wire.OutPoint{}, fmt.Errorf("invalid outpoint format: %s", s)
	}
	h, err := chainhash.NewHashFromStr(parts[0])
	if err != nil {
		return wire.OutPoint{}, fmt.Errorf("invalid outpoint txid: %w", err)
	}
	idx, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return wire.OutPoint{}, fmt.Errorf("invalid outpoint index: %w", err)
	}
	return wire.OutPoint{Hash: *h, Index: uint32(idx)}, nil
}

func parseOutPointStrings(in []string) ([]wire.OutPoint, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("outpoints must not be empty")
	}
	out := make([]wire.OutPoint, 0, len(in))
	for _, s := range in {
		op, err := parseOutPointString(s)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, nil
}

func parseRenewMinConf(minConf *int32) (int32, error) {
	if minConf == nil {
		return 0, nil
	}
	if *minConf < 0 {
		return 0, ErrNeedPositiveMinconf
	}
	return *minConf, nil
}

func parseRenewFeeRate(maxFeeRate *float64) (btcutil.Amount, error) {
	if maxFeeRate == nil {
		return txrules.DefaultRelayFeePerKb, nil
	}
	if *maxFeeRate <= 0 {
		return 0, &btcjson.RPCError{Code: btcjson.ErrRPCInvalidParameter, Message: "maxfeerate must be > 0"}
	}
	return btcutil.NewAmount(*maxFeeRate)
}

func parseRenewAmount(amount float64) (btcutil.Amount, error) {
	if amount <= 0 {
		return 0, ErrNeedPositiveAmount
	}
	amt, err := btcutil.NewAmount(amount)
	if err != nil {
		return 0, err
	}
	if amt <= 0 {
		return 0, ErrNeedPositiveAmount
	}
	return amt, nil
}

func selectedRenewalWarnings(w *wallet.Wallet, selected []wire.OutPoint,
	minConf int32) []string {

	if w == nil || len(selected) == 0 {
		return nil
	}

	policy, warnings := wallet.ResolveExpiryPolicy(w.ChainParams())
	outputs, err := w.UnspentOutputs(wallet.OutputSelectionPolicy{
		Account:               waddrmgr.DefaultAccountNum,
		RequiredConfirmations: minConf,
		IncludeExpired:        true,
	})
	if err != nil {
		return append(warnings,
			"unable to inspect selected outpoint expiry risk: "+err.Error())
	}

	byOutpoint := make(map[wire.OutPoint]*wallet.TransactionOutput, len(outputs))
	for _, output := range outputs {
		byOutpoint[output.OutPoint] = output
	}

	tip := w.Manager.SyncedTo().Height
	hasNearExpiry := false
	hasTooLateNextBlock := false
	hasExpired := false
	for _, op := range selected {
		output := byOutpoint[op]
		if output == nil || output.ContainingBlock.Height < 0 {
			continue
		}

		amountSat := output.Output.Value
		reclaimSat := (amountSat * int64(policy.ProjectedReclaimRatioBps)) / 10000
		info, err := wallet.BuildExpiryInfoWithRenewWarning(
			output.ContainingBlock.Height, tip, policy.WindowBlocks,
			policy.ExpiringThresholdBlocks, policy.RenewWarningBlocks,
			amountSat, reclaimSat, policy.DustThresholdSat,
		)
		if err != nil {
			warnings = append(warnings,
				"unable to inspect selected outpoint expiry risk: "+err.Error())
			continue
		}

		switch info.RenewalRisk {
		case wallet.RenewalRiskExpired:
			hasExpired = true
		case wallet.RenewalRiskTooLateNextBlock:
			hasTooLateNextBlock = true
		case wallet.RenewalRiskNearExpiry:
			hasNearExpiry = true
		}
	}

	if hasExpired {
		warnings = append(warnings,
			"selected outpoints include expired UTXOs; renewal will fail unless the wallet view is stale")
	}
	if hasTooLateNextBlock {
		warnings = append(warnings,
			"selected outpoints are too close to expiry for the next block to renew them; renewal confirmation risk is extreme")
	}
	if hasNearExpiry {
		warnings = append(warnings,
			"selected outpoints are near expiry; renewal only succeeds if confirmed before expiry height")
	}

	sort.Strings(warnings)
	return warnings
}

func getRenew(icmd interface{}, w *wallet.Wallet) (interface{}, error) {
	if w == nil {
		return nil, fmt.Errorf("wallet is nil")
	}
	cmd := icmd.(*RenewCmd)

	selected, err := parseOutPointStrings(cmd.OutPoints)
	if err != nil {
		return nil, &btcjson.RPCError{Code: btcjson.ErrRPCInvalidParameter, Message: err.Error()}
	}
	minConf, err := parseRenewMinConf(cmd.MinConf)
	if err != nil {
		return nil, err
	}
	feeRate, err := parseRenewFeeRate(cmd.MaxFeeRate)
	if err != nil {
		return nil, err
	}
	amt, err := parseRenewAmount(cmd.Amount)
	if err != nil {
		return nil, err
	}
	warnings := selectedRenewalWarnings(w, selected, minConf)

	var targetAddr btcutil.Address
	var targetAddrStr string
	if cmd.TargetAddress != nil && *cmd.TargetAddress != "" {
		targetAddr, err = decodeAddress(*cmd.TargetAddress, w.ChainParams())
		if err != nil {
			return nil, err
		}
		targetAddrStr = targetAddr.EncodeAddress()
	} else {
		targetAddr, err = w.NewAddress(waddrmgr.DefaultAccountNum, waddrmgr.KeyScopeBIP0044)
		if err != nil {
			return nil, err
		}
		targetAddrStr = targetAddr.EncodeAddress()
	}
	pkScript, err := txscript.PayToAddrScript(targetAddr)
	if err != nil {
		return nil, err
	}

	tx, err := w.SendOutputsWithInput(
		[]*wire.TxOut{{Value: int64(amt), PkScript: pkScript}},
		nil, // Allow selected outpoints from any key scope in default account.
		waddrmgr.DefaultAccountNum,
		minConf,
		feeRate,
		wallet.CoinSelectionLargest,
		"obtc.renew",
		selected,
	)
	if err != nil {
		if waddrmgr.IsError(err, waddrmgr.ErrLocked) {
			return nil, &ErrWalletUnlockNeeded
		}
		return nil, err
	}

	return RenewResult{
		TxID:            tx.TxHash().String(),
		InputCount:      len(tx.TxIn),
		OutputCount:     len(tx.TxOut),
		FeeRateSatPerKB: int64(feeRate),
		TargetAddress:   targetAddrStr,
		Warnings:        warnings,
	}, nil
}
