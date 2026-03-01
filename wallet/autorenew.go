package wallet

import (
	"fmt"
	"sort"
	"time"

	"github.com/btcsuite/btcd/btcutil"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
	"github.com/btcsuite/btcwallet/waddrmgr"
)

const (
	defaultAutoRenewInterval                    = 30 * time.Minute
	defaultAutoRenewExpiryWindowBlocks          = 3679200
	defaultAutoRenewExpiringThresholdBlks       = 144 * 180
	autoRenewDustThresholdSat             int64 = 546
	defaultAutoRenewLabel                       = "obtc.autorenew"
)

type AutoRenewPolicy struct {
	Enabled            bool
	WindowStartBlocks  int32 // trigger when blocks_to_expiry <= this
	WindowEndBlocks    int32 // and blocks_to_expiry >= this
	MaxUtxosPerRun     int
	MaxFeeRateSatPerKB int64
}

// AutoRenewRuntimeConfig defines how the wallet process schedules and executes
// auto-renew runs.
type AutoRenewRuntimeConfig struct {
	Policy                  AutoRenewPolicy
	Interval                time.Duration
	Amount                  btcutil.Amount
	MinConf                 int32
	Account                 uint32
	ExpiryWindowBlocks      uint64
	ExpiringThresholdBlocks int32
	Label                   string
}

type autoRenewCandidate struct {
	outpoint       wire.OutPoint
	amountSat      int64
	blocksToExpiry int32
}

func DefaultAutoRenewPolicy() AutoRenewPolicy {
	return AutoRenewPolicy{
		Enabled:            false,
		WindowStartBlocks:  144 * 365, // ~1 year
		WindowEndBlocks:    144 * 180, // ~6 months
		MaxUtxosPerRun:     100,
		MaxFeeRateSatPerKB: 5000,
	}
}

// DefaultAutoRenewRuntimeConfig returns sane defaults for process-level
// auto-renew execution. Enabled remains false by default.
func DefaultAutoRenewRuntimeConfig() AutoRenewRuntimeConfig {
	return AutoRenewRuntimeConfig{
		Policy:                  DefaultAutoRenewPolicy(),
		Interval:                defaultAutoRenewInterval,
		Amount:                  0,
		MinConf:                 0,
		Account:                 waddrmgr.DefaultAccountNum,
		ExpiryWindowBlocks:      defaultAutoRenewExpiryWindowBlocks,
		ExpiringThresholdBlocks: defaultAutoRenewExpiringThresholdBlks,
		Label:                   defaultAutoRenewLabel,
	}
}

func ValidateAutoRenewPolicy(p AutoRenewPolicy) error {
	if p.WindowStartBlocks < 0 || p.WindowEndBlocks < 0 {
		return fmt.Errorf("renew window must be non-negative")
	}
	if p.WindowStartBlocks < p.WindowEndBlocks {
		return fmt.Errorf("window start must be >= window end")
	}
	if p.MaxUtxosPerRun <= 0 {
		return fmt.Errorf("max utxos per run must be > 0")
	}
	if p.MaxFeeRateSatPerKB <= 0 {
		return fmt.Errorf("max fee rate must be > 0")
	}
	return nil
}

// ValidateAutoRenewRuntimeConfig validates process-level auto-renew runtime
// parameters. Amount must be positive when auto-renew is enabled.
func ValidateAutoRenewRuntimeConfig(c AutoRenewRuntimeConfig) error {
	if err := ValidateAutoRenewPolicy(c.Policy); err != nil {
		return err
	}
	if c.Interval <= 0 {
		return fmt.Errorf("auto-renew interval must be > 0")
	}
	if c.MinConf < 0 {
		return fmt.Errorf("auto-renew minconf must be >= 0")
	}
	if c.ExpiryWindowBlocks == 0 {
		return fmt.Errorf("auto-renew expiry window must be > 0")
	}
	if c.ExpiringThresholdBlocks < 0 {
		return fmt.Errorf("auto-renew expiring threshold must be >= 0")
	}
	if c.Policy.Enabled && c.Amount <= 0 {
		return fmt.Errorf("auto-renew amount must be > 0 when enabled")
	}
	return nil
}

func normalizeAutoRenewRuntimeConfig(c AutoRenewRuntimeConfig) AutoRenewRuntimeConfig {
	if c.Interval <= 0 {
		c.Interval = defaultAutoRenewInterval
	}
	if c.ExpiryWindowBlocks == 0 {
		c.ExpiryWindowBlocks = defaultAutoRenewExpiryWindowBlocks
	}
	if c.ExpiringThresholdBlocks < 0 {
		c.ExpiringThresholdBlocks = defaultAutoRenewExpiringThresholdBlks
	}
	if c.Label == "" {
		c.Label = defaultAutoRenewLabel
	}
	return c
}

func IsInAutoRenewWindow(blocksToExpiry int32, p AutoRenewPolicy) bool {
	if !p.Enabled {
		return false
	}
	return blocksToExpiry <= p.WindowStartBlocks &&
		blocksToExpiry >= p.WindowEndBlocks
}

func SelectAutoRenewCandidates(items []ExpiryInfo, p AutoRenewPolicy) []int {
	if !p.Enabled || len(items) == 0 {
		return nil
	}
	selected := make([]int, 0, len(items))
	for i, it := range items {
		if IsInAutoRenewWindow(it.BlocksToExpiry, p) {
			selected = append(selected, i)
			if len(selected) >= p.MaxUtxosPerRun {
				break
			}
		}
	}
	return selected
}

// ConfigureAutoRenew updates wallet process auto-renew parameters.
//
// NOTE: This method does not persist configuration to disk.
func (w *Wallet) ConfigureAutoRenew(cfg AutoRenewRuntimeConfig) error {
	if err := ValidateAutoRenewRuntimeConfig(cfg); err != nil {
		return err
	}
	cfg = normalizeAutoRenewRuntimeConfig(cfg)

	w.autoRenewMu.Lock()
	w.autoRenewCfg = cfg
	w.autoRenewConfigured = true

	w.quitMu.Lock()
	started := w.started
	w.quitMu.Unlock()

	if started && cfg.Policy.Enabled && !w.autoRenewLoopRunning {
		w.startAutoRenewLoopLocked()
	}
	w.autoRenewMu.Unlock()

	log.Infof("Auto-renew config updated: enabled=%v interval=%s window=[%d,%d] max_utxos=%d max_feerate_sat_per_kb=%d amount_sat=%d minconf=%d",
		cfg.Policy.Enabled, cfg.Interval, cfg.Policy.WindowStartBlocks,
		cfg.Policy.WindowEndBlocks, cfg.Policy.MaxUtxosPerRun,
		cfg.Policy.MaxFeeRateSatPerKB, int64(cfg.Amount), cfg.MinConf,
	)

	return nil
}

func (w *Wallet) startAutoRenewLoopLocked() {
	if w.autoRenewLoopRunning {
		return
	}

	w.autoRenewLoopRunning = true
	w.wg.Add(1)
	go w.autoRenewLoop()
}

func (w *Wallet) autoRenewConfigSnapshot() (AutoRenewRuntimeConfig, bool) {
	w.autoRenewMu.Lock()
	defer w.autoRenewMu.Unlock()

	if !w.autoRenewConfigured {
		return AutoRenewRuntimeConfig{}, false
	}

	return w.autoRenewCfg, true
}

func (w *Wallet) autoRenewLoop() {
	defer w.wg.Done()
	defer func() {
		w.autoRenewMu.Lock()
		w.autoRenewLoopRunning = false
		w.autoRenewMu.Unlock()
	}()

	for {
		cfg, ok := w.autoRenewConfigSnapshot()
		if !ok {
			return
		}
		cfg = normalizeAutoRenewRuntimeConfig(cfg)

		timer := time.NewTimer(cfg.Interval)
		select {
		case <-timer.C:
		case <-w.quitChan():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		}

		if !cfg.Policy.Enabled {
			continue
		}
		if !w.ChainSynced() {
			log.Debugf("Auto-renew skipped: wallet not yet chain-synced")
			continue
		}
		if w.Locked() {
			log.Debugf("Auto-renew skipped: wallet is locked")
			continue
		}

		w.runAutoRenewOnce(cfg)
	}
}

func (w *Wallet) runAutoRenewOnce(cfg AutoRenewRuntimeConfig) {
	candidates, err := w.buildAutoRenewCandidates(cfg)
	if err != nil {
		log.Warnf("Auto-renew candidate selection failed: %v", err)
		return
	}

	if len(candidates) == 0 {
		log.Debugf("Auto-renew run: no candidates in window [%d,%d]",
			cfg.Policy.WindowStartBlocks, cfg.Policy.WindowEndBlocks,
		)
		return
	}

	succeeded := 0
	failed := 0
	for _, candidate := range candidates {
		tx, err := w.renewAutoCandidate(cfg, candidate)
		if err != nil {
			failed++
			log.Warnf("Auto-renew failed for %s (amount_sat=%d blocks_to_expiry=%d): %v",
				candidate.outpoint, candidate.amountSat,
				candidate.blocksToExpiry, err,
			)
			continue
		}

		succeeded++
		log.Infof("Auto-renewed %s -> txid=%s (amount_sat=%d blocks_to_expiry=%d)",
			candidate.outpoint, tx.TxHash(), candidate.amountSat,
			candidate.blocksToExpiry,
		)
	}

	log.Infof("Auto-renew run finished: candidates=%d succeeded=%d failed=%d",
		len(candidates), succeeded, failed,
	)
}

func (w *Wallet) buildAutoRenewCandidates(cfg AutoRenewRuntimeConfig) ([]autoRenewCandidate, error) {
	outputs, err := w.UnspentOutputs(OutputSelectionPolicy{
		Account:               cfg.Account,
		RequiredConfirmations: cfg.MinConf,
	})
	if err != nil {
		return nil, err
	}

	tipHeight := w.Manager.SyncedTo().Height
	candidates := make([]autoRenewCandidate, 0, len(outputs))
	for _, out := range outputs {
		createHeight := out.ContainingBlock.Height
		if createHeight < 0 {
			continue
		}

		amountSat := out.Output.Value
		projectedReclaimSat := (amountSat * 70) / 100
		info, err := BuildExpiryInfo(
			createHeight, tipHeight, cfg.ExpiryWindowBlocks,
			cfg.ExpiringThresholdBlocks, amountSat,
			projectedReclaimSat, autoRenewDustThresholdSat,
		)
		if err != nil {
			log.Warnf("Auto-renew skipped outpoint %s due to invalid expiry info: %v",
				out.OutPoint, err,
			)
			continue
		}

		if !IsInAutoRenewWindow(info.BlocksToExpiry, cfg.Policy) {
			continue
		}
		if btcutil.Amount(amountSat) <= cfg.Amount {
			continue
		}

		candidates = append(candidates, autoRenewCandidate{
			outpoint:       out.OutPoint,
			amountSat:      amountSat,
			blocksToExpiry: info.BlocksToExpiry,
		})
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].blocksToExpiry != candidates[j].blocksToExpiry {
			return candidates[i].blocksToExpiry < candidates[j].blocksToExpiry
		}
		return candidates[i].outpoint.String() < candidates[j].outpoint.String()
	})

	if len(candidates) > cfg.Policy.MaxUtxosPerRun {
		candidates = candidates[:cfg.Policy.MaxUtxosPerRun]
	}

	return candidates, nil
}

func (w *Wallet) renewAutoCandidate(cfg AutoRenewRuntimeConfig,
	candidate autoRenewCandidate) (*wire.MsgTx, error) {

	targetAddr, err := w.NewAddress(cfg.Account, waddrmgr.KeyScopeBIP0044)
	if err != nil {
		return nil, err
	}
	pkScript, err := txscript.PayToAddrScript(targetAddr)
	if err != nil {
		return nil, err
	}

	feeRate := btcutil.Amount(cfg.Policy.MaxFeeRateSatPerKB)
	return w.SendOutputsWithInput(
		[]*wire.TxOut{{Value: int64(cfg.Amount), PkScript: pkScript}},
		nil, // Allow selected outpoints from any key scope in account.
		cfg.Account, cfg.MinConf, feeRate, CoinSelectionLargest,
		cfg.Label, []wire.OutPoint{candidate.outpoint},
	)
}
