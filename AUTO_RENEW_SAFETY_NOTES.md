# OBTC Auto-Renew Safety Notes

Auto-renew is an opt-in wallet process feature. It must remain disabled by
default unless a release plan explicitly changes that policy.

## Defaults

| Setting | Default |
|---|---|
| Enabled | false |
| Interval | 30 minutes |
| Failure backoff | 15 minutes |
| Window start | about 1 year before expiry |
| Window end | about 6 months before expiry |
| Max UTXOs per run | 100 |
| Max fee rate | 5000 sat/KB |
| Amount | 0, invalid when enabled |
| Max renewal amount per run | 0, unlimited |

## Safety Boundaries

- `autorenewamount` must be greater than zero when enabled.
- `autorenewwindowstart` must be greater than or equal to
  `autorenewwindowend`.
- `autorenewmaxutxos` must be greater than zero.
- `autorenewmaxfeerate` must be greater than zero.
- `autorenewmaxrenewamountperrun`, when set, must be at least one renewal
  amount.
- locked wallets should not auto-sign.
- unsynced wallets should not auto-renew.
- failed runs should set failure backoff.
- no-candidate runs should exit normally.

## What The Tests Prove

| Test | Proof |
|---|---|
| `TestDefaultAutoRenewPolicyDirect` | The policy is disabled by default. |
| `TestDefaultAutoRenewRuntimeConfigDirect` | Runtime defaults are positive and disabled. |
| `TestAutoRenewRuntimeConfigFromOptionsDisabled` | CLI config keeps auto-renew disabled by default. |
| `TestValidateAutoRenewRuntimeConfigDirect` | Invalid amount, interval, backoff, minconf, expiry window, and budget are rejected. |
| `TestAutoRenewBuildCandidatesAppliesWindowAmountCapAndOrder` | Candidate scan applies window bounds, amount threshold, max UTXO cap, and deterministic order. |
| `TestLimitAutoRenewCandidatesByBudgetDirect` | Per-run renewal amount budget truncates candidates. |
| `TestAutoRenewBackoffDirect` | Failure backoff is set after failures and cleared after success. |

## Known Limits

- The tests do not run a wall-clock scheduler loop for 72 hours.
- The tests do not use real network fee estimates; current code uses the
  configured max fee rate as the fee rate passed to transaction construction.
- The tests do not prove persistence of auto-renew state across process
  restart. Current runtime configuration is not persisted by
  `ConfigureAutoRenew`.
- The tests do not prove funded mainnet behavior.

## Human Review Items

- Decide whether locked-wallet errors should be wrapped into one stable
  user-facing message.
- Decide whether auto-renew should add a separate fee-estimate skip reason in
  addition to the configured fee-rate input.
- Decide whether an operator-visible run history is required before Mainnet
  Candidate.
- Confirm that release notes continue to state auto-renew is opt-in and
  evidence-gated.
