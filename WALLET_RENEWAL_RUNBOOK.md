# OBTC Wallet Renewal Runbook

This runbook is for external reviewers validating wallet expiry visibility and
renewal behavior on regtest or testnet. Do not use mainnet funds for rehearsal.

## Choose A Rehearsal Network

Use one of these paths for the whole run. Do not mix testnet and regtest node,
wallet, or agent ports.

### Testnet Path

Start an OBTC testnet node with expiry index enabled:

```bash
obtcd --obtctestnet \
  --rpclisten=127.0.0.1:19528 \
  --rpcuser=testuser \
  --rpcpass=testpass \
  --txindex \
  --expiryindex \
  --notls
```

Start `obtcwallet` against that node. Keep auto-renew disabled unless this run
is explicitly testing it:

```bash
obtcwallet --obtctestnet \
  --rpcconnect=127.0.0.1:19528 \
  --btcdusername=testuser \
  --btcdpassword=testpass \
  --username=walletuser \
  --password=walletpass \
  --rpclisten=127.0.0.1:19554 \
  --experimentalrpclisten=127.0.0.1:19556 \
  --noclienttls \
  --noservertls \
  --autorenew=0
```

Use these endpoints for the rest of the testnet run:

| Surface | Endpoint |
|---|---|
| Node RPC | `127.0.0.1:19528` |
| Wallet legacy RPC | `http://127.0.0.1:19554/` |
| Agent gRPC | `127.0.0.1:19556` |

### Regtest Path

Start an OBTC regtest node with expiry index enabled:

```bash
obtcd --obtcregtest \
  --rpclisten=127.0.0.1:29528 \
  --rpcuser=testuser \
  --rpcpass=testpass \
  --txindex \
  --expiryindex \
  --notls
```

Start `obtcwallet` against that node:

```bash
obtcwallet --obtcregtest \
  --rpcconnect=127.0.0.1:29528 \
  --btcdusername=testuser \
  --btcdpassword=testpass \
  --username=walletuser \
  --password=walletpass \
  --rpclisten=127.0.0.1:29554 \
  --experimentalrpclisten=127.0.0.1:29556 \
  --noclienttls \
  --noservertls \
  --autorenew=0
```

Use these endpoints for the rest of the regtest run:

| Surface | Endpoint |
|---|---|
| Node RPC | `127.0.0.1:29528` |
| Wallet legacy RPC | `http://127.0.0.1:29554/` |
| Agent gRPC | `127.0.0.1:29556` |

The `renewall` CLI default `localhost:19556` matches the testnet agent path.
Pass `--connect=127.0.0.1:29556` explicitly for regtest.

## View Expiry

Legacy wallet RPC:

```bash
curl --user "$WALLET_RPC_USER:$WALLET_RPC_PASS" \
  --data-binary '{"jsonrpc":"1.0","id":"obtc","method":"obtc.getexpiry","params":[20]}' \
  -H 'content-type:text/plain;' \
  http://127.0.0.1:19554/
```

Regtest uses the same request against the regtest wallet RPC port:

```bash
curl --user "$WALLET_RPC_USER:$WALLET_RPC_PASS" \
  --data-binary '{"jsonrpc":"1.0","id":"obtc","method":"obtc.getexpiry","params":[20]}' \
  -H 'content-type:text/plain;' \
  http://127.0.0.1:29554/
```

Review these fields for each item:

| Field | Meaning |
|---|---|
| `outpoint` | UTXO selected by txid and vout. |
| `amount_sat` | Current output value. |
| `create_height` | Block height where the UTXO was created. |
| `expiry_height` | Height at which normal spending expires. |
| `blocks_to_expiry` | Remaining blocks at the current wallet tip. |
| `days_to_expiry` | Approximate days at 144 blocks/day. |
| `status` | `ok`, `expiring`, or `expired`. |
| `dust_risk` | Whether projected refund value is below dust threshold. |
| `renewal_risk` | Wallet-side advisory renewal risk. |

## Dry-Run Batch Renewal

Use dry-run before signing or publishing:

```bash
renewall \
  --connect=127.0.0.1:19556 \
  --notls \
  --amount 0.5 \
  --limit 10 \
  --dry-run
```

Regtest dry-run uses the regtest agent listener:

```bash
renewall \
  --connect=127.0.0.1:29556 \
  --notls \
  --amount 0.5 \
  --limit 10 \
  --dry-run
```

Useful filters, using the same `--connect` value selected above:

```bash
renewall --connect=127.0.0.1:19556 --notls --amount 0.5 --dry-run --window-start 52560 --window-end 25920
renewall --connect=127.0.0.1:19556 --notls --amount 0.5 --dry-run --include-expired
renewall --connect=127.0.0.1:19556 --notls --amount 0.5 --dry-run --include-near-expiry
```

Dry-run must not open a signer session, preview a renewal, submit a renewal, or
broadcast a transaction. It should print selected outpoints or `no renew
candidates selected`.

## Manual Renew

Renew a specific outpoint through legacy wallet RPC:

```bash
curl --user "$WALLET_RPC_USER:$WALLET_RPC_PASS" \
  --data-binary '{"jsonrpc":"1.0","id":"obtc","method":"obtc.renew","params":[["TXID:VOUT"],0.5]}' \
  -H 'content-type:text/plain;' \
  http://127.0.0.1:19554/
```

Regtest uses the regtest wallet RPC port:

```bash
curl --user "$WALLET_RPC_USER:$WALLET_RPC_PASS" \
  --data-binary '{"jsonrpc":"1.0","id":"obtc","method":"obtc.renew","params":[["TXID:VOUT"],0.5]}' \
  -H 'content-type:text/plain;' \
  http://127.0.0.1:29554/
```

Optional parameters:

- target address;
- max fee rate;
- min confirmations.

After confirmation, rerun `obtc.getexpiry` and verify that the renewal output is
a new UTXO with a new create height and expiry height.

## Execute Renewall

Non-dry-run `renewall` requires a synced funded wallet and signer access:

```bash
renewall \
  --connect=127.0.0.1:19556 \
  --notls \
  --walletpass "$WALLET_PRIVATE_PASSPHRASE" \
  --amount 0.5 \
  --limit 5 \
  --maxfeerate 0.00005 \
  --minconf 1
```

Regtest execution uses the regtest agent listener:

```bash
renewall \
  --connect=127.0.0.1:29556 \
  --notls \
  --walletpass "$WALLET_PRIVATE_PASSPHRASE" \
  --amount 0.5 \
  --limit 5 \
  --maxfeerate 0.00005 \
  --minconf 1
```

The command should:

1. read wallet state;
2. query expiry risk;
3. issue a renewal capability;
4. open a signer session;
5. preview one renewal per selected outpoint;
6. submit successful previews;
7. close the signer session and revoke the capability.

## Configure Auto-Renew

Auto-renew is disabled by default and is not recommended as an MC1 operator
path. The example below is only for controlled regtest/testnet rehearsal when
the test objective is specifically auto-renew behavior:

```bash
obtcwallet --obtctestnet \
  --rpcconnect=127.0.0.1:19528 \
  --btcdusername=testuser \
  --btcdpassword=testpass \
  --username=walletuser \
  --password=walletpass \
  --rpclisten=127.0.0.1:19554 \
  --experimentalrpclisten=127.0.0.1:19556 \
  --noclienttls \
  --noservertls \
  --autorenew=1 \
  --autorenewamount=0.5 \
  --autorenewinterval=30m \
  --autorenewfailurebackoff=15m \
  --autorenewwindowstart=52560 \
  --autorenewwindowend=25920 \
  --autorenewmaxutxos=10 \
  --autorenewmaxfeerate=5000 \
  --autorenewmaxrenewamountperrun=2.5
```

Expected safety properties:

- no run happens when `--autorenew=0`;
- amount must be greater than zero when enabled;
- window bounds must be valid;
- max UTXOs and per-run amount budget cap candidate count;
- failures set backoff before the next run;
- locked wallet skips execution.

## Logs

Review wallet logs for:

- candidate selection failures;
- budget cap applied;
- per-outpoint renewal failures with outpoint and reason;
- run summary with candidate, success, and failure counts;
- backoff active messages.

Logs must not contain private keys, seed words, or wallet passphrases.

## Common Errors

| Error | Action |
|---|---|
| `wallet chain state is not synced` | Wait for the wallet and node to sync. |
| `outpoints must not be empty` | Provide at least one `TXID:VOUT`. |
| `invalid outpoint format` | Use `txid:vout` format. |
| `maxfeerate must be > 0` | Use a positive max fee rate or omit the option. |
| `walletpass is required` | Provide passphrase for local signer execution. |
| `publish_only signer backend` | Use external signing flow; `renewall` execution does not support publish-only. |
| locked or decrypt-key failure | Unlock the wallet before signing; user-facing wording needs product review. |

## Testnet/Regtest Only

- funded renewal rehearsal;
- non-dry-run `renewall`;
- auto-renew scheduler observation;
- failure injection and restart/backoff testing.

## Not Recommended For Ordinary Users

- manually selecting near-expiry UTXOs unless they understand confirmation risk;
- renewing expired UTXOs without checking protocol behavior;
- enabling auto-renew before funded testnet evidence exists;
- running non-dry-run `renewall` without dry-run and fee review first.
