# OBTC Wallet Security Policy

## Scope

This policy applies to OBTC wallet code, wallet RPC surfaces, expiry inspection,
manual renewal, batch renewal, Auto-Renew scheduling, signing boundaries, release
artifacts, and wallet operator documentation.

The wallet follows the broader OBTC project security boundary described in the
node policy:

https://github.com/organicbitcoin/obtcd/blob/master/SECURITY.md

## Experimental Status

OBTC wallet material is experimental. Source-only previews and testnet flows are
not production wallet releases and must not be treated as safe for real funds.

Funded renewal and non-dry-run batch renewal claims remain evidence-gated until
public txids, commands, commit hashes, and heights are linked.

## Reporting Sensitive Issues

Do not open a public issue with seed phrases, private keys, wallet backups,
signing material, real-fund claim attempts, unpublished peer details, private
logs, or exploit details.

For sensitive wallet reports, use GitHub private vulnerability reporting for the
OBTC project if it is available. If private reporting is not available, open a
minimal public issue asking for a secure reporting channel and include no
secrets, exploit details, or real wallet material.

Use public wallet issues only for non-sensitive bugs, documentation gaps,
reproducible testnet-only failures, and questions that do not reveal private
material:

https://github.com/organicbitcoin/obtcwallet/issues

## Private-Key And Claim Safety

Never import a Bitcoin seed phrase or Bitcoin private key into experimental OBTC
software, a website, a support form, a public issue, or a debugging transcript.

Do not send real-fund claim attempts, real wallet material, or recovery phrases
to maintainers. Final claim tooling, if published, must provide a safer reviewed
flow.

## What To Include

For non-sensitive wallet reports, include:

- affected repository and commit hash;
- network (`obtc mainnet-candidate`, `obtctestnet`, or `obtcregtest`);
- exact wallet and node commands used;
- expected behavior and observed behavior;
- relevant heights, txids, and logs with secrets removed.
