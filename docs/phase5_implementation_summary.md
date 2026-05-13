# OBTC Wallet Phase 5 Implementation Summary

> This document summarizes the current `obtcwallet` implementation for readers
> who need to understand the repository quickly:
>
> 1. what role this repository plays in OBTC;
> 2. what Phase 5 has implemented;
> 3. where future development and validation should continue.

## 1. One-Line Positioning

`obtcwallet` owns wallet-side expiry awareness and renewal operations.

Chain-side rules, including the expiry index, REAP consensus, and mining
template injection, are owned by `obtcd`.

## 2. Current Phase 5 Goal

This scope focuses on Phase 5A:

- `obtc.getexpiry`: query wallet UTXO expiry state;
- `obtc.renew`: manually renew selected UTXOs;
- related unit tests and validation documentation.

Out of scope for this pass:

- automatic renewal policy, timers, budgets, and randomized windows;
- large-scale batch CLI workflows, unless separately scoped later.

## 3. Implemented Work

### A. Wallet Expiry Model

File: `wallet/expiry.go`

Implemented functions:

- `CalculateExpiryHeight`
- `ClassifyExpiryStatus`
- `EstimateDaysToExpiry`
- `BuildExpiryInfo`

Purpose:

- map UTXO creation height to expiry height;
- classify state as `ok`, `expiring`, or `expired`;
- expose day estimates and a `dust_risk` advisory field.

Tests: `wallet/expiry_test.go`

### B. Legacy RPC Extensions

Files:

- `rpc/legacyrpc/obtc_methods.go`
- `rpc/legacyrpc/methods.go`

Registered commands:

- `obtc.getexpiry`
- `obtc.renew`

`obtc.getexpiry`:

- supports parameters such as `limit` and `before_height`;
- returns stable ordering by expiry height and outpoint.

`obtc.renew`:

- supports outpoints, amount, target address, maximum fee rate, and minimum
  confirmations;
- can generate a fresh target address by default;
- returns txid, input/output counts, fee rate, and other summary fields.

Tests: `rpc/legacyrpc/obtc_methods_test.go`

## 4. Code Index

- `wallet/expiry.go`: expiry calculation and status classification.
- `wallet/expiry_test.go`: direct tests for expiry helpers.
- `rpc/legacyrpc/obtc_methods.go`: `obtc.getexpiry` and `obtc.renew` logic.
- `rpc/legacyrpc/obtc_methods_test.go`: RPC helper and parameter tests.
- `rpc/legacyrpc/methods.go`: command routing registration.
- `cmd/renewall/main.go`: batch renewal CLI with dry-run, window filtering,
  and scheduled multi-run execution.
- `cmd/renewall/main_test.go`: `renewall` parameter and filtering tests.

## 5. Completion Snapshot

| Area | Status | Notes |
|---|---|---|
| Wallet expiry core | Complete | Calculation, classification, and aggregation helpers are available. |
| `obtc.getexpiry` | Complete | Wired through legacy RPC. |
| `obtc.renew` | Complete v1 | Parameter validation and explicit-input renewal path are wired. |
| Automatic renewal | Partial | Policy primitives exist; CLI scheduling exists; in-process wallet scheduling is not fully wired. |
| Batch CLI workflow | Available | `cmd/renewall` supports `dry-run`, `window-start`, `window-end`, `interval`, and `runs`. |
| Validation docs | Complete | `phase5-validation` records requests, responses, failure cases, txid, and test commands. |

## 6. Test And Quality Constraints

- New helper functions should have direct unit tests.
- Executed coverage includes:
  - direct `wallet/expiry_*` tests;
  - new `obtc_methods` helper and parameter parsing tests;
  - focused critical-path tests.

Notes:

- Full repository tests may fail in environments without a `bitcoind`
  executable for chain integration tests.
- During wallet feature work, prioritize target module tests and static checks
  that are stable in the local environment.

## 7. Recommended Phase 5B Work

1. Wire the in-process automatic renewal scheduler:
   - bind `wallet/autorenew.go` policy to the execution path;
   - define lifecycle and concurrency behavior.

2. Add automatic renewal audit and risk controls:
   - record candidates, successes, failures, and fee summaries per run;
   - add maximum budget and failure backoff behavior.

3. Strengthen end-to-end validation:
   - cover scheduled execution, restart recovery, and reconnect races;
   - turn validation scripts into durable documentation.

## 8. Glossary

- UTXO: Unspent Transaction Output.
- REAP: Reclaim Expired Assets Protocol.
- RPC: Remote Procedure Call.
- Legacy RPC: the `btcwallet` compatible JSON-RPC service.
- Dust: an output below an economically spendable threshold.
