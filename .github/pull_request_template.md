## Purpose

Describe the problem, the change, and the linked issue.

## Scope and risk

- Network: `none` / `obtcmainnet` / `obtctestnet` / `obtcregtest`
- [ ] Signing or key management
- [ ] Renewal or expiry boundaries
- [ ] Network selection or address namespaces
- [ ] RPC exposure or authentication
- [ ] Funds or transaction publication
- [ ] No item above is affected

For every checked risk area, explain positive, negative, and boundary cases and
any compatibility or migration effect.

## Verification

List exact commands and results. Include the OS, Go version, wallet commit, node
commit, and network when the result depends on the environment.

## Checklist

- [ ] I followed [CONTRIBUTING.md](https://github.com/organicbitcoin/obtcwallet/blob/master/CONTRIBUTING.md).
- [ ] The change is focused and contains no unrelated key, network, or fund-safety changes.
- [ ] Tests cover relevant success, rejection, and boundary behavior, or the PR explains why tests are not applicable.
- [ ] A bug fix includes a regression test when practical.
- [ ] Documentation matches current code and does not claim unverified network or release readiness.
- [ ] Logs, examples, screenshots, and fixtures contain no secrets, wallet material, or private identity data.
- [ ] Go changes are formatted with `gofmt`, and CI passes.
- [ ] Any new logging statements use an appropriate subsystem and logging level.
