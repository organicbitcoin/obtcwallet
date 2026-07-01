# OBTC Wallet Renewal Runbook

This runbook is for external reviewers validating wallet expiry visibility and
renewal behavior on regtest or testnet. Do not use mainnet funds for rehearsal.

## Start Wallet

Start an OBTC-aware node with expiry index enabled:

```bash
obtcd --obtcmainnet --expiryindex
```

Start `obtcwallet` against the intended non-mainnet environment. Keep
auto-renew disabled unless this run is explicitly testing it:

```bash
obtcwallet \
  --obtcmainnet=0 \
  --autorenew=0
```

For agent gRPC workflows, use the agent listener configured by the wallet. The
`renewall` CLI default is `localhost:19556`.

## View Expiry

Legacy wallet RPC:

```bash
curl --user "$WALLET_RPC_USER:$WALLET_RPC_PASS" \
  --data-binary '{"jsonrpc":"1.0","id":"obtc","method":"obtc.getexpiry","params":[20]}' \
  -H 'content-type:text/plain;' \
  http://127.0.0.1:8332/
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
  --connect localhost:19556 \
  --notls \
  --amount 0.5 \
  --limit 10 \
  --dry-run
```

Useful filters:

```bash
renewall --amount 0.5 --dry-run --window-start 52560 --window-end 25920
renewall --amount 0.5 --dry-run --include-expired
renewall --amount 0.5 --dry-run --include-near-expiry
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
  http://127.0.0.1:8332/
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
  --connect localhost:19556 \
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

Auto-renew is disabled by default. Enable only in controlled regtest/testnet
rehearsal:

```bash
obtcwallet \
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
