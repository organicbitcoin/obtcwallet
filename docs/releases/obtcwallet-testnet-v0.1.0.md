# OBTCWallet Testnet v0.1.0 Engineering Preview

`obtcwallet-testnet-v0.1.0` is a source-only pre-release for developers who want
to reproduce and review the first OBTC testnet wallet path.

This is not a production wallet release and does not include prebuilt binaries.

## What is included

- Native OBTC network flags:
  - `--obtcmainnet`
  - `--obtctestnet`
  - `--obtcregtest`
- Default node RPC ports aligned with `obtcd`:
  - OBTC mainnet: `9528`
  - OBTC testnet: `19528`
  - OBTC regtest: `29528`
- Default wallet legacy RPC ports:
  - OBTC mainnet: `9554`
  - OBTC testnet: `19554`
  - OBTC regtest: `29554`
- OBTC testnet quick-start docs in `README.md`.
- Config tests for OBTC network selection, default node RPC connection, default
  wallet RPC listener, and network flag mutex behavior.

## Intended v0.1 validation surface

- Start `btcwallet` explicitly with `--obtctestnet`.
- Connect the wallet to an `obtcd --obtctestnet` node on `127.0.0.1:19528`.
- Query expiry state through `obtc.getexpiry`.
- Exercise `renewall --dry-run` through the agent gRPC listener.
- Review local signer execution flow before attempting funded renewals.

## Known limits

- Source archive only; no production binaries are published for v0.1.
- Non-dry-run renewals require a synced and funded OBTC testnet wallet and remain
  follow-up validation until public txids are recorded.
- `renewall` does not support the `publish_only` signer backend yet.
- Remote signer code exists, but it is outside the v0.1 release scope.
- Auto-renew is off by default and should stay disabled unless separately
  validated for a deployment.

## Minimal build and test

```bash
go build -o ./btcwallet .
go build -o ./renewall ./cmd/renewall
go test $(go list ./... | grep -v github.com/btcsuite/btcwallet/chain) -count=1
```

## Minimal testnet flow

```bash
# Start obtcd on OBTC testnet.
btcd --obtctestnet \
  --listen=0.0.0.0:19527 \
  --rpclisten=127.0.0.1:19528 \
  --rpcuser=testuser \
  --rpcpass=testpass \
  --txindex \
  --expiryindex \
  --notls

# Create the wallet.
btcwallet --create --obtctestnet

# Start the wallet and connect it to obtcd.
btcwallet --obtctestnet \
  --rpcconnect=127.0.0.1:19528 \
  --btcdusername=testuser \
  --btcdpassword=testpass \
  --username=walletuser \
  --password=walletpass \
  --experimentalrpclisten=127.0.0.1:19556 \
  --noclienttls \
  --noservertls

# Query expiry state through legacy RPC.
curl --user walletuser:walletpass \
  -H 'content-type: text/plain;' \
  --data-binary '{"jsonrpc":"1.0","id":"obtc","method":"obtc.getexpiry","params":[10]}' \
  http://127.0.0.1:19554/

# Exercise the batch renewal selector without signing or publishing.
renewall \
  --connect=127.0.0.1:19556 \
  --walletpass='<private-passphrase>' \
  --amount=0.5 \
  --notls \
  --dry-run
```
