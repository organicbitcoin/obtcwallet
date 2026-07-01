# OBTC Wallet Lifecycle Tests

This report summarizes wallet-side tests for OBTC expiry visibility, manual
renewal, batch renewal, and auto-renew safety. It is a reviewer aid, not a
protocol specification.

## Scope

The tests added in Plan 05 do not change consensus rules, wallet renewal
semantics, default auto-renew behavior, signing models, or network parameters.

## Coverage Matrix

| Area | Test | Coverage |
|---|---|---|
| `obtc.getexpiry` fields | `TestMakeGetExpiryResultLifecycleFieldMatrix` | Verifies outpoint, amount, create height, expiry height, blocks to expiry, days to expiry, status, dust risk, and renewal risk fields. |
| `obtc.getexpiry` ordering and filters | Existing `TestMakeGetExpiryResultDirect`, `TestMakeGetExpiryResultLimitAndFilter`, `TestMakeGetExpiryResultNearExpiryFields` | Verifies expiry/outpoint ordering, limit, before-height filtering, and near-expiry fields. |
| `obtc.renew` parameter failures | `TestGetRenewDirectErrorsIncludeActionableReasons`, existing parse tests | Verifies empty/invalid outpoint, fee limit, amount, and minconf errors are surfaced before wallet interaction. |
| Renewal transaction generation | `TestRenewalSelectedOutpointCreatesSignedTransaction` | Verifies selected outpoint is the only input, target output is created, fee is positive, and the transaction validates. |
| Locked wallet guard state | `TestWalletLockedStateObservableForRenewalGuards` | Verifies wallet locked state is observable for renewal and auto-renew guard checks. |
| Agent expiry risk fields | `TestBuildExpiryRiskItemsLifecycleFields` | Verifies agent gRPC expiry risk field mapping for ok, expiring, and expired UTXOs. |
| Agent audit reasons | `TestBuildPreviewDecisionReasonsAreAuditable` | Verifies preview decision logs include selected outpoints count, policy source, fee rate, minconf, target amount, target address, reservation, and dry-run notice without secret-like fields. |
| Agent outpoint errors | `TestParseOutPointStringsUniqueErrorsAreActionable` | Verifies empty, malformed, and duplicate outpoint errors include actionable reasons. |
| `renewall` status filters | `TestSelectOutpointsLifecycleFiltersDirect` | Verifies ok, expiring, expired, near-expiry, and window-based selection behavior. |
| `renewall --dry-run` | Existing `TestRunRenewAllOnceDryRunUsesAgentRiskQuery`, new `TestRunRenewAllOnceDryRunNoCandidatesIsClearNoop` | Verifies dry-run queries risk, prints selected outpoints, does not preview or submit, and reports no candidates as a normal result. |
| `renewall` execution | Existing `TestRunRenewAllOnceExecutesViaAgentFlow` | Verifies capability, signer session, preview, submit, cleanup, and output reporting. |
| `renewall` fee/minconf/target and partial failures | `TestRunRenewAllOncePassesFeeMinconfTargetAndReportsFailures` | Verifies max fee rate, minconf, target address, amount, outpoint, and failure reasons reach the agent flow and stderr. |
| Auto-renew defaults | Existing `TestDefaultAutoRenewPolicyDirect`, `TestDefaultAutoRenewRuntimeConfigDirect`, `TestAutoRenewRuntimeConfigFromOptionsDisabled` | Verifies auto-renew is disabled by default. |
| Auto-renew config validation | Existing `TestValidateAutoRenewPolicyDirect`, `TestValidateAutoRenewRuntimeConfigDirect`, `config_autorenew_test.go` | Verifies invalid amount, fee rate, minconf, interval, expiry window, and budget settings are rejected. |
| Auto-renew window and max UTXOs | Existing `TestIsInAutoRenewWindowDirect`, `TestSelectAutoRenewCandidatesDirect`, new `TestAutoRenewBuildCandidatesAppliesWindowAmountCapAndOrder` | Verifies candidate window bounds, max UTXOs per run, amount threshold, and deterministic order. |
| Auto-renew budget and backoff | Existing `TestLimitAutoRenewCandidatesByBudgetDirect`, `TestAutoRenewBackoffDirect` | Verifies per-run budget truncation and failure backoff state transitions. |

## Commands

Focused tests:

```bash
go test ./wallet -run 'Test(RenewalSelectedOutpoint|AutoRenewBuildCandidates|AutoRenew|DefaultAutoRenew|ValidateAutoRenew|LimitAutoRenew|IsInAutoRenew|SelectAutoRenew|NormalizeAutoRenew)' -count=1 -v
go test ./rpc/legacyrpc -run 'Test(MakeGetExpiryResult|ParseRenew|GetRenew)' -count=1 -v
go test ./rpc/rpcserver -run 'Test(BuildExpiryRiskItems|BuildPreviewDecisionReasons|ParseOutPointStringsUnique|WarningsFromExpiryRisks|PolicyVerdict)' -count=1 -v
go test ./cmd/renewall -run 'Test(SelectOutpoints|RunRenewAllOnce|NewRenewFilter|ParseLoop|RPCConnect)' -count=1 -v
```

Broader validation:

```bash
go test ./wallet ./rpc/legacyrpc ./rpc/rpcserver ./cmd/renewall -count=1
go test ./... -count=1
```

## Current Findings

- Locked state is observable to wallet guards, and auto-renew checks
  `w.Locked()` before running. This plan does not prove the legacy `obtc.renew`
  RPC locked-signing failure with a funded wallet. Human product review should
  decide whether a stable user-facing locked-wallet wrapper is required.
- Legacy `obtc.renew` success is not isolated behind an interface. The direct
  RPC method uses concrete `*wallet.Wallet`, so the automated success coverage
  is at the wallet transaction generation layer and the agent/renewall flow
  layer. A funded regtest or testnet RPC run should still be captured for
  release evidence.
- Auto-renew uses the configured maximum fee rate as the transaction fee-rate
  input. It does not independently estimate a network fee and then skip when
  that estimate exceeds a maximum. Human product review should confirm whether
  the current behavior is sufficient for Mainnet Candidate.

## Remaining Manual Evidence

The following are intentionally not proven by unit tests in this plan:

- funded `obtc.renew` broadcast on regtest/testnet with txid and resulting new
  UTXO expiry height;
- locked-wallet `obtc.renew` RPC failure with a concrete funded wallet and the
  final user-facing error string;
- non-dry-run `renewall` against a funded controlled wallet;
- long-running auto-renew scheduler observation across wallet restart;
- operator log capture proving no sensitive key material is logged during
  renewal failures.
