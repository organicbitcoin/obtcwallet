# OBTCWallet

OBTC is a Bitcoin-derived lifecycle-money experiment. This repository contains
the wallet path for OBTC expiry inspection, renewal workflows, and testnet
engineering previews.

This project is a fork of `btcwallet`. The current binary name and Go module
path still inherit that upstream shape, so examples use `btcwallet` and
`renewall` unless otherwise noted.

## Status

`obtcwallet-testnet-v0.1.0` is a source-only engineering preview. It is meant
for developers and reviewers who want to reproduce the current OBTC testnet
wallet flow. It is not a production wallet release and does not include
prebuilt binaries.

Current public release:

- [obtcwallet-testnet-v0.1.0](https://github.com/organicbitcoin/obtcwallet/releases/tag/obtcwallet-testnet-v0.1.0)

Current milestone:

- [mainnet-candidate-2026-07](https://github.com/organicbitcoin/obtcwallet/milestone/1)

## What is included

- Native OBTC network flags:
  - `--obtcmainnet`
  - `--obtctestnet`
  - `--obtcregtest`
- OBTC node RPC defaults:
  - mainnet: `9528`
  - testnet: `19528`
  - regtest: `29528`
- OBTC wallet legacy RPC defaults:
  - mainnet: `9554`
  - testnet: `19554`
  - regtest: `29554`
- Legacy RPC methods:
  - `obtc.getexpiry`
  - `obtc.renew`
- Batch renewal planning:
  - `renewall --dry-run`
- Local signer path for controlled renewal workflows.
- Local wallet UI:
  - `walletapp` for balance, receive, send, expiry inspection, and manual
    renewal through the existing legacy wallet RPC.

## Wallet Namespace Policy

New wallets created with OBTC network flags use OBTC HD coin types for the
default wallet scopes:

| Network | HD coin type |
|---|---:|
| OBTC mainnet | `20260` |
| OBTC testnet | `20261` |
| OBTC regtest | `20262` |

The policy applies to the default BIP44, BIP49, BIP84, and BIP86 wallet scopes.
Native OBTC account public key exports use the active OBTC chain's extended
public key version bytes.

Imported Bitcoin SLIP-0132 account public keys are accepted only as compatibility
aliases on OBTC networks: mainnet accepts `ypub` and `zpub`, while testnet and
regtest accept `upub` and `vpub`. The wallet logs a warning for these imports
and stores the account under the OBTC coin-type scope for the active network.
Prefer native OBTC account public key versions for new integrations. Older
developer wallets created before this policy may contain legacy coin-type `0`
scopes and should be recreated or explicitly re-imported for namespace-isolated
OBTC operation.

## Known limits

- This is not production financial infrastructure.
- Mainnet-candidate readiness is tracked in
  [`docs/mainnet-readiness.md`](docs/mainnet-readiness.md).
- Non-dry-run renewal requires a synced and funded OBTC testnet wallet and
  remains a follow-up validation item until public txids are recorded.
- `renewall` does not support the `publish_only` signer backend yet.
- Remote signer code exists, but it is outside the v0.1 release scope.
- Auto-renew is opt-in via `--autorenew` and should stay disabled unless it has
  been separately validated for your deployment.
- The repository still inherits upstream `btcwallet` package names, paths, and
  some documentation structure.

## Requirements

- Go 1.24.6 or newer
- Git
- A compatible `obtcd` checkout for OBTC testnet runs:
  <https://github.com/organicbitcoin/obtcd>

## Build

```bash
git clone https://github.com/organicbitcoin/obtcwallet.git
cd obtcwallet

go build -o ./btcwallet .
go build -o ./renewall ./cmd/renewall
go build -o ./walletapp ./cmd/walletapp
```

To produce a checksumed operator artifact directory for a frozen candidate
commit:

```bash
scripts/release/build_release_artifacts.sh --version mainnet-candidate-2026-07
```

## Test

The full upstream wallet test matrix can require external Bitcoin node
dependencies. For the current OBTC engineering preview, use the scoped local
gate:

```bash
go test $(go list ./... | grep -v github.com/btcsuite/btcwallet/chain) -count=1
```

## Minimal OBTC testnet flow

Start an OBTC testnet node:

```bash
btcd --obtctestnet \
  --listen=0.0.0.0:19527 \
  --rpclisten=127.0.0.1:19528 \
  --rpcuser=testuser \
  --rpcpass=testpass \
  --txindex \
  --expiryindex \
  --notls
```

Create the wallet:

```bash
./btcwallet --create --obtctestnet
```

Start the wallet and connect it to `obtcd`:

```bash
./btcwallet --obtctestnet \
  --rpcconnect=127.0.0.1:19528 \
  --btcdusername=testuser \
  --btcdpassword=testpass \
  --username=walletuser \
  --password=walletpass \
  --experimentalrpclisten=127.0.0.1:19556 \
  --noclienttls \
  --noservertls
```

Query expiry state through the legacy wallet RPC:

```bash
curl --user walletuser:walletpass \
  -H 'content-type: text/plain;' \
  --data-binary '{"jsonrpc":"1.0","id":"obtc","method":"obtc.getexpiry","params":[10]}' \
  http://127.0.0.1:19554/
```

Exercise the batch renewal selector without signing or publishing:

```bash
./renewall \
  --connect=127.0.0.1:19556 \
  --walletpass='<private-passphrase>' \
  --amount=0.5 \
  --notls \
  --dry-run
```

Run the local wallet UI:

```bash
./walletapp \
  --wallet-rpc=http://127.0.0.1:19554/ \
  --wallet-user=walletuser \
  --wallet-pass=walletpass
```

Open <http://127.0.0.1:19580/>.  The app is a local operator panel for an
already-running `btcwallet` process.  It does not create or import wallets; it
uses the legacy wallet RPC for receive addresses, temporary unlock, normal
`sendtoaddress` transfers, `obtc.getexpiry`, and manual `obtc.renew`.

By default `walletapp` refuses non-loopback listen addresses.  Passing
`--allow-non-loopback` is an explicit operator decision and should not be used
for reviewer laptops or unattended machines.

## RPC surfaces

Wallet clients can use two local RPC surfaces:

- Legacy JSON-RPC, used for compatibility-oriented wallet calls and OBTC methods
  such as `obtc.getexpiry`.
- Experimental gRPC, used by the controlled renewal automation surface and
  `renewall`.

The gRPC API is not stable and is feature gated behind
`--experimentalrpclisten`.

## Upstream attribution

OBTCWallet is derived from
[btcsuite/btcwallet](https://github.com/btcsuite/btcwallet). The upstream
project's architecture, wallet database, key management, and RPC foundations
remain visible throughout this repository. OBTC-specific changes are layered on
top for network selection, expiry awareness, renewal workflows, and engineering
preview validation.

## Issues

Use this repository's issue tracker for OBTCWallet bugs, release evidence, and
mainnet-candidate work:

<https://github.com/organicbitcoin/obtcwallet/issues>

For launch-tracking work, prefer the
[`mainnet-candidate-2026-07`](https://github.com/organicbitcoin/obtcwallet/milestone/1)
milestone and the labels `mainnet-blocker`, `evidence`, `comms`, and
`post-launch`.

## License

OBTCWallet is licensed under the liberal ISC License.
