# Contributing to OBTCWallet

Start with the project-wide
[OBTC contributor guide](https://github.com/organicbitcoin/obtcd/blob/master/CONTRIBUTING.md).
Documentation fixes, tests, reproducible testnet reports, and focused wallet
improvements are welcome.

Use Go 1.24.6 or newer. Build the public wallet entry points with:

```bash
git clone https://github.com/organicbitcoin/obtcwallet.git
cd obtcwallet
go build -o ./btcwallet .
go build -o ./renewall ./cmd/renewall
go build -o ./walletapp ./cmd/walletapp
```

Run the tests relevant to the change. The repository's current scoped local
gate excludes the upstream chain package, whose tests may be timing-sensitive
or require additional dependencies:

```bash
go test $(go list ./... | grep -v github.com/btcsuite/btcwallet/chain) -count=1
```

Record the operating system, `go version`, wallet and node commit hashes, exact
commands, and results. Do not claim a full test pass when any package was
excluded or failed.

Never import a Bitcoin seed, private key, or wallet file into experimental OBTC
software. Use only testnet or regtest funds in public reproductions. Keep wallet
and node RPC listeners on `127.0.0.1`, and remove passphrases, credentials,
wallet material, private endpoints, and personal paths from reports.

Use
[private vulnerability reporting](https://github.com/organicbitcoin/obtcwallet/security/advisories/new)
for sensitive reports. Public issues should include a commit hash, network,
reproduction steps, expected and actual results, and redacted evidence.

Pull requests that affect signing, key management, renewal behavior, expiry
boundaries, network selection, RPC exposure, or funds require positive,
negative, and boundary tests plus an explanation of compatibility and safety
effects. Format Go code with `gofmt` and keep commits focused.

By participating, you agree to follow the [Code of Conduct](CODE_OF_CONDUCT.md).
