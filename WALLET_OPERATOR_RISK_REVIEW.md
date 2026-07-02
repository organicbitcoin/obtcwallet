# OBTC Wallet Operator Risk Review

Assessment date: 2026-07-02

Scope: `v0.1.0-mainnet-candidate.1` source-only external technical review. This
is not a production wallet release and not a recommendation to hold valuable
mainnet funds.

Assessed source commit:
`0bde8d27b8853fd9cf58e0084dba12788a32fab2`.

## Summary

The MC1 wallet path is acceptable for external technical review and controlled
testnet/regtest rehearsal with non-blocking limitations. The recommended MC1
operator path is manual inspection, `obtc.getexpiry`, manual renewal, and
`renewall --dry-run` or controlled local-signer execution in test environments.

Auto-renew is disabled by default and is not recommended as an MC1 operator
path. Funded auto-renew scheduler validation remains a later RC/production
readiness item.

## Evidence References

Existing evidence from the control-plane release records:

- wrong-passphrase drill:
  `obtcwallet_wrong_passphrase_drill_20260523T1920Z.md`;
- public testnet checkpoint:
  `obtc_public_testnet_checkpoint_20260531.md`;
- public testnet success report:
  `obtc_public_testnet_success_report_20260602.md`.

Repository-local evidence:

- `WALLET_LIFECYCLE_TESTS.md`;
- `WALLET_RENEWAL_RUNBOOK.md`;
- `README.md`;
- `docs/mainnet-readiness.md`;
- `wallet/replay_test.go`;
- `wallet/createtx_test.go`;
- `wallet/psbt_test.go`;
- `wallet/import_test.go`;
- `rpc/rpcserver/agentwallet_server_test.go`.

## Risk Matrix

| Risk | MC1 status | Mitigation / operator guidance |
|---|---|---|
| Wrong private passphrase | Covered by existing drill | Wrong passphrase returns a decrypt/passphrase error and does not perform a mutating wallet action. Operators must never paste seed words or private keys into logs or issue comments. |
| Stale or unsynced chain | Covered by tests and prior drill | Renewal and signing paths fail closed when chain freshness cannot be proven. PR #12 also prevents stale wallet height from producing post-activation OBTC signatures without replay protection. |
| Temporary node/RPC failure | Covered by prior public testnet evidence | Treat RPC failure as a stop condition. Reconnect only to the intended OBTC node/network and rerun expiry/renewal inspection after recovery. |
| Expired-boundary fail-closed behavior | Covered by tests and public testnet evidence | Normal spending of expired UTXOs is rejected after activation. Renewal flows must be run before expiry or must report failure rather than silently spending. |
| Backup, restore, rescan | Covered by public testnet evidence | Use OBTC-specific appdata/network directories. After restore or rescan, recheck chain sync, balance, expiry output, and transaction history before renewal. |
| Wrong network flag | Operator risk | Always pass exactly one explicit OBTC flag: `--obtcmainnet`, `--obtctestnet`, or `--obtcregtest`. Do not rely on inherited Bitcoin defaults. |
| Wrong node RPC endpoint | Operator risk | Match wallet network to node RPC port: mainnet `9528`, testnet `19528`, regtest `29528`. A testnet wallet pointed at a mainnet or Bitcoin endpoint is out of scope. |
| Disabled TLS | Acceptable only in local controlled rehearsal | `--notls`, `--noclienttls`, and `--noservertls` examples are for loopback testnet/regtest drills. Do not expose unauthenticated or cleartext wallet RPC beyond localhost. |
| Exposed wallet RPC listener | Operator risk | Bind wallet RPC to localhost unless there is a reviewed network isolation plan. Treat wallet RPC credentials as secrets. |
| Insufficient confirmations | Controlled by minconf settings | Use a positive `--minconf` for funded renewal. Do not renew against unconfirmed or unstable inputs unless a specific test requires it. |
| Renewal amount mistakes | Operator risk | Dry-run first. Confirm selected outpoints, target amount, max fee rate, min confirmations, and target address before signing. |
| Wallet DB / network directory mixups | Operator risk | Keep separate appdata directories per network. Never reuse a Bitcoin wallet directory for OBTC rehearsal. Recreate older developer OBTC wallets that used legacy coin-type `0` scopes unless explicitly testing compatibility. |
| Auto-renew scheduler | Deferred | Auto-renew remains disabled by default and is not recommended for MC1. Funded scheduler evidence, restart behavior, RPC-failure behavior, and operator-visible run history remain later readiness work. |

## MC1 Operator Boundary

Allowed to describe for MC1:

- source build and local tests;
- explicit OBTC network selection;
- expiry inspection with `obtc.getexpiry`;
- manual renewal review on regtest/testnet;
- `renewall --dry-run`;
- local-signer controlled test execution when the operator understands the
  funded testnet risk.

Not allowed to imply for MC1:

- production wallet readiness;
- real-fund safety;
- default-on or recommended auto-renew;
- exchange, custody, or broad end-user wallet support;
- remote signer production readiness without a separate end-to-end operator
  record.

## Follow-Up Items

These are non-blocking for MC1 external technical review but remain blockers
before broader wallet readiness claims:

- funded auto-renew scheduler drill;
- runtime restart and temporary RPC-failure evidence for auto-renew;
- operator-visible auto-renew run history decision;
- funded remote signer / publish-only operator evidence if that path enters
  release scope;
- stable product wording for locked-wallet renewal errors if funded operators
  rely on the legacy RPC surface.

