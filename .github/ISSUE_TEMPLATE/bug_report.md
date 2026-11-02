---
name: Bug report
about: Report a reproducible OBTC wallet, renewal, expiry, signing, or RPC problem.
title: '[bug]: '
labels: ["bug"]
assignees: ''
---

## Safety check

Do not continue with a public report if this issue contains exploit details,
seed phrases, private keys, passphrases, RPC credentials, wallet backups,
private endpoints, or unredacted sensitive logs. Use private vulnerability
reporting instead.

## Summary

Describe the problem and its impact.

## Environment

- `obtcwallet` commit hash:
- connected `obtcd` commit hash:
- network (`obtcmainnet`, `obtctestnet`, or `obtcregtest`):
- operating system and architecture:
- `go version`:
- relevant flags and RPC surfaces, with credentials removed:

## Steps to reproduce

List the smallest reliable sequence of commands or actions. Use only testnet or
regtest funds and newly created test wallets in public reproductions.

## Expected result

What should have happened?

## Actual result

What happened instead?

## Redacted evidence

Include relevant heights, txids, RPC responses, stack traces, or logs. Remove
credentials, wallet material, private addresses, hostnames, usernames, and
local filesystem paths that reveal personal information.
