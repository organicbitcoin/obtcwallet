# Phase 5 Validation (OBTC Wallet)

Date: 2026-02-27

## Scope

Validated legacy RPC methods:

- `obtc.getexpiry`
- `obtc.renew`

Validation covers:

1. Success path for `obtc.renew` with a real txid.
2. Response schema sanity for `obtc.getexpiry`.
3. Stable failure behavior for invalid renew requests.

## Test Environment

- `obtcwallet` branch: `docs/phase5-validation-evidence`
- Chain: `simnet`
- Node: local `btcd` (`--simnet --notls`)
- Wallet RPC: local legacy RPC (`127.0.0.1:19554`)

A disposable end-to-end harness was executed under `/tmp/obtcwallet-e2e9`.

## `obtc.getexpiry` Validation

### Request

```bash
curl --user walletuser:walletpass \
  -H 'content-type: text/plain;' \
  --data-binary '{"jsonrpc":"1.0","id":"obtc","method":"obtc.getexpiry","params":[20]}' \
  http://127.0.0.1:19554/
```

### Sample Response

```json
{
  "jsonrpc": "1.0",
  "result": {
    "tip_height": 131,
    "window_blocks": 3679200,
    "items": [
      {
        "outpoint": "a59014b733a7522f19708bc62ce0f10e7a4e0ba6527d98b16b2ce4bb9b69a603:0",
        "amount_sat": 5000000000,
        "create_height": 1,
        "expiry_height": 3679201,
        "blocks_to_expiry": 3679070,
        "days_to_expiry": 25549,
        "status": "ok",
        "dust_risk": false
      }
    ]
  },
  "error": null,
  "id": "obtc"
}
```

Result: schema and data fields matched Phase 5 expectations.

## `obtc.renew` Success Path

### Request

```bash
curl --user walletuser:walletpass \
  -H 'content-type: text/plain;' \
  --data-binary '{"jsonrpc":"1.0","id":"obtc","method":"obtc.renew","params":[["a59014b733a7522f19708bc62ce0f10e7a4e0ba6527d98b16b2ce4bb9b69a603:0"],0.5]}' \
  http://127.0.0.1:19554/
```

### Response

```json
{
  "jsonrpc": "1.0",
  "result": {
    "txid": "e7fc6fcc87b3f25224e43aa1ee927932850fe636f7d254aa72d6f701f1576a5e",
    "input_count": 1,
    "output_count": 2,
    "fee_rate_sat_per_kb": 1000,
    "target_address": "SMTYrD6g1YKDrxCr9PqvuEPdpsJz48Xem1"
  },
  "error": null,
  "id": "obtc"
}
```

Result: renewal transaction broadcast succeeded.

Recorded txid: `e7fc6fcc87b3f25224e43aa1ee927932850fe636f7d254aa72d6f701f1576a5e`

## Failure Cases

### Case 1: Empty Outpoints

Request params: `[], 0.5`

Response:

```json
{
  "error": {
    "code": -8,
    "message": "outpoints must not be empty"
  }
}
```

Result: expected validation error.

### Case 2: Non-Positive Amount

Request params: `["<valid-outpoint>"], 0`

Response:

```json
{
  "error": {
    "code": -8,
    "message": "amount must be positive"
  }
}
```

Result: expected validation error.

## `cmd/renewall` Additional Validation

Enhancements:

- supports `blocks_to_expiry` window filtering through `--window-start` and
  `--window-end`;
- supports scheduled multi-run execution through `--interval` and `--runs`.

Example: windowed dry-run:

```bash
renewall \
  --connect=127.0.0.1:19556 --walletpass '<private-passphrase>' --notls \
  --amount 0.5 --fetchlimit 1000 --limit 20 \
  --window-start 52560 --window-end 25920 \
  --dry-run
```

Example: run every 30 minutes for 4 total runs:

```bash
renewall \
  --connect=127.0.0.1:19556 --walletpass '<private-passphrase>' --notls \
  --amount 0.5 --interval 30m --runs 4
```

## Additional Notes

This validation run includes a fix for renewal eligibility handling during
chain reconnect and sync races:

- `obtc.renew` now selects inputs across key scopes (`nil` coin selection
  scope) while preserving account restriction.
- Transaction creation now uses the higher of chain-client and wallet-manager
  synced heights to avoid stale-height false negatives for coinbase maturity
  checks.

Related tests run:

- `go test ./wallet -run 'AutoRenew|Renew|Expiry|CreateTx|CreateSimple' -count=1`
- `go test ./rpc/legacyrpc -run 'obtc|renew|getexpiry|Renew|Expiry' -count=1`
- `go test ./cmd/renewall -count=1`, including window filtering and scheduling
  parameter parsing tests.
