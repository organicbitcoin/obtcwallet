# OBTC Wallet Phase 5 Execution Plan v2

> Goal: make expiry awareness and renewal in `obtcwallet` a small, shippable,
> testable loop.
>
> This revision aligns the plan with the existing `btcwallet` code structure,
> defines the Phase 5A and 5B boundary, and avoids mixing wallet work with
> chain-consensus work.

## 0. Key Conclusions

1. Phase 5A must deliver:
   - `obtc.getexpiry`
   - `obtc.renew`
   - validation documentation and tests

2. This pass does not include:
   - automatic renewal policy, budgets, or fee caps
   - a broad `renew-all` CLI workflow, unless it is separately scoped later

3. Cross-repository ownership:
   - `obtcd`: chain rules, expiry index, REAP consensus, and mining templates
   - `obtcwallet`: wallet-side expiry queries, renewal transaction construction,
     and submission

## 1. Alignment With The Current Repository

`obtcwallet` already has:

- `rpc/legacyrpc` for JSON-RPC
- `rpc/rpcserver` for gRPC
- `wallet/`, `wtxmgr/`, and `waddrmgr/`

Recommended split:

### 5A: Start In `legacyrpc`

- Add wallet-level methods in `wallet/expiry.go` and `wallet/renew.go`.
- Expose `obtc.getexpiry` and `obtc.renew` through `rpc/legacyrpc`.
- Defer gRPC to avoid doubling the initial surface area.

### 5B: Add gRPC / CLI Paths

- Reuse the 5A wallet methods instead of duplicating business logic.

## 2. Data Contract v1

### 2.1 `obtc.getexpiry` Response Fields

Each UTXO should include at least:

- `outpoint`: `txid:vout`
- `amount_sat`
- `create_height`
- `expiry_height`
- `blocks_to_expiry`
- `days_to_expiry`
- `status`: `ok | expiring | expired`
- `dust_risk`: boolean advisory field

### 2.2 `obtc.renew` Response Fields

- `txid`
- `input_count`
- `output_count`
- `fee_sat`
- `renewed_total_sat`
- `target_address`

## 3. Single Source Of Truth For Expiry Parameters

All wallet-side expiry calculations must read OBTC parameters that match
`obtcd`, including window and activation heights.

Requirements:

- Do not hard-code independent wallet-only expiry window constants.
- Use one helper entry point, for example `CalcExpiry(...)` in
  `wallet/expiry.go`.
- Document the parameter source and compatibility behavior for mainnet,
  testnet, and regtest.

## 4. Executable Task Breakdown

## Task A: Wallet Expiry Core

Suggested files:

- `wallet/expiry.go`
- `wallet/expiry_test.go`

Implementation:

- Define an internal `ExpiryInfo` structure.
- Add helpers:
  - `CalcExpiryHeight(createHeight, params)`
  - `ClassifyExpiryStatus(tipHeight, expiryHeight)`
  - `EstimateDaysToExpiry(blocksToExpiry)`

Acceptance:

- Cover boundary cases: `tip = expiry-1`, `tip = expiry`, and
  `tip = expiry+1`.
- Unit tests pass.

## Task B: `obtc.getexpiry` In `legacyrpc`

Suggested files:

- `rpc/legacyrpc/methods.go`
- `rpc/legacyrpc/obtc_methods.go`
- `rpc/legacyrpc/obtc_methods_test.go`

Implementation:

- Method name: `obtc.getexpiry`
- Support v1 filters:
  - optional `before_height`
  - optional `limit`
- Return entries in stable `expiry_height asc, outpoint asc` order.

Acceptance:

- Normal and empty-result scenarios are covered.
- Sorting stability is tested.

## Task C: `obtc.renew` In `legacyrpc`

Suggested files:

- `wallet/renew.go`
- `wallet/renew_test.go`
- `rpc/legacyrpc/obtc_methods.go`

Implementation:

- Input uses explicit outpoints in the first version.
- Default renewal behavior:
  - use a fresh target address by default
  - validate optional `max_feerate`
- Reject:
  - missing outpoints
  - expired outputs when renewal is not allowed by policy
  - invalid parameters

Acceptance:

- At least one successful renewal integration test returns a txid.
- Failure-path error codes and messages are stable.

## Task D: Validation Documentation

File: `docs/phase5-validation.md`

Required content:

- `getexpiry` request and response examples
- `renew` request and response examples
- at least two failure cases with expected errors
- one real recorded txid

## 5. Phase 5B

### 5B-1 Automatic Renewal

- Trigger window before expiry.
- Per-run limits and budget.
- Audit log.

### 5B-2 `renew-all` CLI

- If the CLI depends on `btcctl`, implement that integration in the
  corresponding repository.
- If this repository keeps a local tool command, avoid conflicting with the
  existing ecosystem.

## 6. Test Strategy

### Unit Tests

- Expiry calculation boundaries.
- Status classification.
- Invalid parameters.
- Stable sorting.

### Integration Tests

- Wallet UTXO to `getexpiry`.
- `renew` creates a new output after success.
- Failure paths for invalid outpoints, invalid parameters, and excessive fee
  rates.

### Regression Tests

- Repeated `getexpiry` calls return the same order.
- Repeated requests have clear idempotency behavior.

## 7. Risks And Guards

1. Cross-repository parameter drift.
   - Guard: central parameter reader plus documented parameter source.

2. Scope creep.
   - Guard: keep this pass to 5A; automatic renewal and broad CLI workflows
     stay in 5B.

3. Parallel RPC stacks doubling effort.
   - Guard: ship legacy RPC first, then add gRPC.

4. Unbounded renewal fees.
   - Guard: `max_feerate` and explicit failure responses.

## 8. Milestones And DoD

### M1: Core Layer

- `wallet/expiry.go` plus unit tests.

### M2: Query Path

- `obtc.getexpiry` works, sorts stably, and has passing tests.

### M3: Renewal Path

- `obtc.renew` can broadcast successfully and has clear failure paths.

### M4: Documentation Loop

- `phase5-validation.md` is complete.

Phase 5A DoD:

- [ ] `obtc.getexpiry` is shipped through `legacyrpc`.
- [ ] `obtc.renew` is shipped through `legacyrpc`.
- [ ] `go test ./...` passes, or exceptions are documented.
- [ ] `docs/phase5-validation.md` is complete.

## 9. Immediate Next Steps

Recommended order:

1. Build `wallet/expiry.go` plus tests.
2. Wire `obtc.getexpiry`.
3. Build `wallet/renew.go` plus `obtc.renew`.
4. Add validation documentation.

This keeps Phase 5A in two or three small changes instead of one large
hard-to-review patch.
