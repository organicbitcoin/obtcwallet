# AGENTS.md

## Structure Overview

```
obtcwallet/ (repo root)
  btcwallet.go          # main entry point
  config.go             # configuration and argument parsing
  rpcserver.go          # wallet RPC entry point

  wallet/               # core wallet logic
    (planned)
    expiry.go           # expiry status calculation and filtering
    renew.go            # renewal transaction construction and submission
    policy.go           # optional auto-renew policy

  waddrmgr/             # address, key, and account management
  wtxmgr/               # transaction history and UTXO management
  walletdb/             # wallet database abstraction

  rpc/                  # RPC-related modules
    (planned)
    obtc/               # obtc.getexpiry / obtc.renew

  cmd/                  # command-line programs
    dropwtxmgr/
    sweepaccount/
    (planned)
    renew-all/          # batch renewal entry point

  chain/                # chain backend integration
  netparams/            # network parameter adapters
  internal/             # internal shared modules

  docs/
    phase5_execution_plan.md
    phase5-validation.md

  scripts/              # helper scripts
  build/                # build and release assets
```

## Notes

- The layout above is a recommended placement guide and mixes implemented and planned work.
- If the live repository layout differs, treat the repository state as the source of truth and update this file when needed.
- Keep new modules grouped by responsibility instead of adding unrelated files at the repository root.

## Git Workflow

- Preferred flow: `branch -> develop -> local test -> commit -> push -> PR -> merge`.
- Avoid pushing directly to `master` unless explicitly required.
- Suggested branch prefixes: `feat/...`, `fix/...`, `test/...`, `docs/...`, `chore/...`.
- Use English commit messages with prefixes such as `feat:`, `fix:`, `test:`, `docs:`, or `chore:`.
- Keep commits focused on a single concern when possible.

## Pre-Push Checks

- The repository uses `.githooks/`.
- `pre-commit` checks formatting.
- `pre-push` runs `go test ./... -count=1`.
- If hooks are not installed, run `./scripts/setup-git-hooks.sh`.
- If local `chain` tests require `bitcoind`, fix the environment or run the workflow in an environment that provides it.
- Do not use `--no-verify` for `commit`, `push`, or `merge`.
- If hooks fail and the issue cannot be resolved in the current environment, stop and report the blocker instead of bypassing validation.

## Interaction Constraints

- Match the user's language for interactive discussion.
- Use English for commit messages.
