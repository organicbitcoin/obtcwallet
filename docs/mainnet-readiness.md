# OBTCWallet Mainnet Readiness

Checked: 2026-05-20

This file records the wallet-side checks that matter before treating an OBTC
mainnet-candidate wallet as ready for real funds.

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
- `obtc.getexpiry` is available for wallet-side expiry inspection.
- `obtc.renew` and the local signer path exist for controlled renewal testing.
- `renewall --dry-run` is available for batch-renewal selection and preview
  without signing or publishing.
- Auto-renew exists only as opt-in operator automation and is disabled by
  default.

Conditional or evidence-gated claims:

- Funded `obtc.renew` remains evidence-gated until a public txid, command, and
  node-height record are attached to the release evidence.
- Non-dry-run `renewall` remains evidence-gated. If it is not validated before
  mainnet-candidate, release notes must explicitly exclude it from the first
  operating scope.
- Backup, restore, wallet unlock, restart, and rescan flows require operator
  evidence before they are described as mainnet-candidate-ready.
- Remote signer usage is out of scope unless an end-to-end operator run is
  recorded.

Explicitly out of scope for mainnet-candidate:

- Production wallet readiness.
- Default-on auto-renew.
- `publish_only` signer backend support.
- Exchange, custody, or broad end-user wallet support.
- Any recommendation to store valuable mainnet funds before the open gaps below
  are closed.

## Open Gaps

- Record a funded OBTC testnet or mainnet-candidate `obtc.renew` transaction
  with txid, wallet command, and node height evidence:
  [#2](https://github.com/organicbitcoin/obtcwallet/issues/2).
- Record a non-dry-run `renewall` transaction with txid, or explicitly exclude
  non-dry-run `renewall` from the first mainnet-candidate operating scope:
  [#3](https://github.com/organicbitcoin/obtcwallet/issues/3).
- Validate `--autorenew` on a funded controlled environment before enabling it
  for any mainnet-candidate wallet:
  [#4](https://github.com/organicbitcoin/obtcwallet/issues/4).
- Decide whether the `publish_only` signer backend is required for the first
  mainnet-candidate wallet operator flow; it is not implemented yet:
  [#5](https://github.com/organicbitcoin/obtcwallet/issues/5).
- Exercise the remote signer path end-to-end if it remains in the operator
  story; current mainnet-candidate scope should otherwise document local signer
  only: [#5](https://github.com/organicbitcoin/obtcwallet/issues/5).
- Produce operator evidence for backup, restore, wallet unlock, renewal,
  restart, and rescan paths using OBTC network directories:
  [#6](https://github.com/organicbitcoin/obtcwallet/issues/6).
- Publish release artifacts, checksums, and operator build instructions for the
  exact commit used by mainnet-candidate operators:
  [#7](https://github.com/organicbitcoin/obtcwallet/issues/7).
- Perform a focused review of funded-wallet failure modes: wrong network flag,
  wrong RPC endpoint, disabled TLS scope, exposed wallet RPC, stale chain tip,
  insufficient confirmations, and renewal amount mistakes:
  [#8](https://github.com/organicbitcoin/obtcwallet/issues/8).

All issues are tracked under the `mainnet-candidate-2026-07` milestone with
the `operator-readiness` label.
