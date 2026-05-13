# OBTCWallet Mainnet Readiness

Checked: 2026-05-13

This file records the wallet-side checks that matter before treating an OBTC
mainnet-candidate wallet as ready for real funds.

## Checks Completed

- Current pre-push equivalent test suite passed:
  `.githooks/pre-push`.
- `--obtcmainnet` selects `netparams.ObtcMainNetParams`.
- `--obtcmainnet` default node RPC connection is
  `127.0.0.1:9528`.
- OBTC mainnet wallet legacy JSON-RPC default port is `9554`.
- `sample-btcwallet.conf` keeps `obtcmainnet=0`, `autorenew=0`, and all
  mainnet-affecting options commented by default.
- `sample-btcwallet.conf` now warns not to use the sample configuration with
  valuable mainnet funds until this readiness list is closed.

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
