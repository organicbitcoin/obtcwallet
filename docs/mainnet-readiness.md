# OBTCWallet Mainnet Readiness

Checked: 2026-07-02

This file records the wallet-side checks that matter before treating
`obtcwallet` as ready for the MC1 source-only external technical review release.
It does not approve real-fund or production wallet use.

## Checks Completed

- Current pre-push equivalent test suite passed:
  `.githooks/pre-push`.
- 2026-05-20 scoped mainnet-candidate wallet gates passed:
  - `go test . -run 'Test.*(OBTC|Obtc|AutoRenew|Network|RPC)' -count=1`
  - `go test ./wallet -run 'Expiry|AutoRenew|Renew' -count=1`
  - `go test ./rpc/legacyrpc -run 'OBTC|Obtc|Renew|Expiry' -count=1`
  - `go test ./rpc/rpcserver -run 'Agent|Renew|Signer|Expiry' -count=1`
- 2026-05-20 expired-UTXO spendability evidence passed:
  - `go test ./wallet -run 'Test.*(Expired|ListUnspent|Spendable|OBTC)' -count=1`
  - `TestSpendableViewsSkipExpiredOBTCUtxos` confirms expired OBTC UTXOs are
    excluded from `ListUnspent` and spendable balance while remaining visible
    in total balance.
  - `TestTxToOutputsSkipsExpiredOBTCUtxos` confirms wallet coin selection
    skips expired OBTC UTXOs.
- `--obtcmainnet` selects `netparams.ObtcMainNetParams`.
- `--obtcmainnet` default node RPC connection is
  `127.0.0.1:9528`.
- OBTC mainnet wallet legacy JSON-RPC default port is `9554`.
- `sample-btcwallet.conf` keeps `obtcmainnet=0`, `autorenew=0`, and all
  mainnet-affecting options commented by default.
- `sample-btcwallet.conf` now warns not to use the sample configuration with
  valuable mainnet funds until this readiness list is closed.
- PR #12 merged the replay-signing height policy fix, OBTC HD coin-type policy,
  imported xpub compatibility warning/tests, and local finalized PSBT replay
  validation.
- Current MC1 gate-cleanup baseline:
  `ea0070517641fa9f0ba5731b903aae1c57f24d5a`.
- MC1 is source-only: no project-built wallet archive and no project
  `SHA256SUMS` are distributed for this scope.

## Mainnet-Candidate Capability Boundary

For the first mainnet-candidate operating scope, `obtcwallet` may be described
as a controlled operator wallet path, not as a production wallet for valuable
mainnet funds.

Supported claims for mainnet-candidate documentation:

- Native OBTC network selection exists for `--obtcmainnet`, `--obtctestnet`,
  and `--obtcregtest`.
- `--obtcmainnet` selects OBTC chain parameters, OBTC network directories, and
  the default `obtcd` RPC endpoint `127.0.0.1:9528`.
- The OBTC mainnet legacy wallet JSON-RPC default port is `9554`.
- New OBTC wallets use OBTC HD coin types for default BIP44/BIP49/BIP84/BIP86
  scopes: `20260` for mainnet, `20261` for testnet, and `20262` for regtest.
- Imported Bitcoin SLIP-0132 `ypub`/`zpub`/`upub`/`vpub` account public keys are
  accepted on OBTC networks only as documented compatibility aliases with a
  wallet warning; they are stored under the active OBTC coin-type scope.
- `obtc.getexpiry` is available for wallet-side expiry inspection.
- `obtc.renew` and the local signer path exist for controlled renewal testing.
- `renewall --dry-run` is available for batch-renewal selection and preview
  without signing or publishing.
- Auto-renew exists only as opt-in operator automation and is disabled by
  default.
- Auto-renew is not recommended as an MC1 operator path.
- Funded-wallet failure-mode guidance is recorded in
  `WALLET_OPERATOR_RISK_REVIEW.md`.

Conditional or evidence-gated claims:

- Funded `obtc.renew` and non-dry-run `renewall` evidence exists from the public
  testnet records, but MC1 still does not recommend valuable-fund wallet use.
- Backup, restore, wallet unlock, restart, and rescan guidance is summarized in
  `WALLET_OPERATOR_RISK_REVIEW.md`.
- Remote signer usage is out of scope unless an end-to-end operator run is
  recorded.
- Funded auto-renew scheduler evidence is deferred to a later RC/production
  readiness gate.

Explicitly out of scope for mainnet-candidate:

- Production wallet readiness.
- Default-on auto-renew.
- Recommended auto-renew operation.
- `publish_only` signer backend support.
- Exchange, custody, or broad end-user wallet support.
- Any recommendation to store valuable mainnet funds before the open gaps below
  are closed.

## Open Gaps

- Validate `--autorenew` on a funded controlled environment before enabling it
  for any recommended operator path:
  [#4](https://github.com/organicbitcoin/obtcwallet/issues/4).
- Decide whether the `publish_only` signer backend is required for the first
  mainnet-candidate wallet operator flow; it is not implemented yet:
  [#5](https://github.com/organicbitcoin/obtcwallet/issues/5).
- Exercise the remote signer path end-to-end if it remains in the operator
  story; current mainnet-candidate scope should otherwise document local signer
  only: [#5](https://github.com/organicbitcoin/obtcwallet/issues/5).
- Funded remote signer or publish-only evidence if either path enters a later
  operator release scope.

Closed or downgraded MC1 gate issues are tracked in the `obtcd`
`FINAL_ISSUE_GATE_REVIEW.md`.
