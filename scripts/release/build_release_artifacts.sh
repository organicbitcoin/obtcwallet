#!/usr/bin/env bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

VERSION="${VERSION:-mainnet-candidate}"
OUT_DIR="${OUT_DIR:-${REPO_ROOT}/dist}"
GOOS_VALUE="${GOOS_VALUE:-}"
GOARCH_VALUE="${GOARCH_VALUE:-}"
CGO_ENABLED_VALUE="${CGO_ENABLED_VALUE:-0}"
COMMIT="${COMMIT:-}"
OBTCD_SRC="${OBTCD_SRC:-${REPO_ROOT}/../obtcd}"
DRY_RUN=0

usage() {
    cat <<EOF
Build OBTC wallet mainnet-candidate release artifacts and checksums.

Usage:
  $0 [options]

Options:
  --version <name>       release name/tag used in output paths (default: mainnet-candidate)
  --version=<name>
  --out <dir>            artifact output directory (default: ./dist)
  --out=<dir>
  --goos <name>          target GOOS (default: current go env GOOS)
  --goos=<name>
  --goarch <name>        target GOARCH (default: current go env GOARCH)
  --goarch=<name>
  --commit <sha>         wallet commit recorded in MANIFEST.md (default: git HEAD)
  --commit=<sha>
  --obtcd-src <dir>      sibling obtcd checkout used by go.mod replace (default: ../obtcd)
  --obtcd-src=<dir>
  --dry-run              print build plan without creating artifacts
  -h, --help             show this help

Examples:
  $0 --version mainnet-candidate-2026-07
  $0 --version mainnet-candidate-2026-07 --goos linux --goarch amd64
  $0 --dry-run
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --version)
            VERSION="$2"
            shift 2
            ;;
        --version=*)
            VERSION="${1#*=}"
            shift
            ;;
        --out)
            OUT_DIR="$2"
            shift 2
            ;;
        --out=*)
            OUT_DIR="${1#*=}"
            shift
            ;;
        --goos)
            GOOS_VALUE="$2"
            shift 2
            ;;
        --goos=*)
            GOOS_VALUE="${1#*=}"
            shift
            ;;
        --goarch)
            GOARCH_VALUE="$2"
            shift 2
            ;;
        --goarch=*)
            GOARCH_VALUE="${1#*=}"
            shift
            ;;
        --commit)
            COMMIT="$2"
            shift 2
            ;;
        --commit=*)
            COMMIT="${1#*=}"
            shift
            ;;
        --obtcd-src)
            OBTCD_SRC="$2"
            shift 2
            ;;
        --obtcd-src=*)
            OBTCD_SRC="${1#*=}"
            shift
            ;;
        --dry-run)
            DRY_RUN=1
            shift
            ;;
        -h|--help)
            usage
            exit 0
            ;;
        *)
            echo "[ERROR] unknown option: $1" >&2
            usage
            exit 1
            ;;
    esac
done

if [[ -z "${GOOS_VALUE}" ]]; then
    GOOS_VALUE="$(go env GOOS)"
fi

if [[ -z "${GOARCH_VALUE}" ]]; then
    GOARCH_VALUE="$(go env GOARCH)"
fi

if [[ -z "${COMMIT}" ]]; then
    COMMIT="$(git -C "${REPO_ROOT}" rev-parse HEAD)"
fi

OBTCD_COMMIT="not_available"
if [[ -d "${OBTCD_SRC}/.git" ]]; then
    OBTCD_COMMIT="$(git -C "${OBTCD_SRC}" rev-parse HEAD)"
fi

SHORT_COMMIT="${COMMIT:0:12}"
ARTIFACT_DIR="${OUT_DIR}/${VERSION}-${SHORT_COMMIT}-${GOOS_VALUE}-${GOARCH_VALUE}"
EXT=""
if [[ "${GOOS_VALUE}" == "windows" ]]; then
    EXT=".exe"
fi

TARGET_NAMES=("btcwallet" "renewall")
TARGET_PKGS=("." "./cmd/renewall")

sha256_file() {
    shasum -a 256 "$1" | awk '{print $1}'
}

print_plan() {
    cat <<EOF
[INFO] wallet release artifact build plan
  version: ${VERSION}
  wallet commit: ${COMMIT}
  obtcd src: ${OBTCD_SRC}
  obtcd commit: ${OBTCD_COMMIT}
  target: ${GOOS_VALUE}/${GOARCH_VALUE}
  cgo: ${CGO_ENABLED_VALUE}
  output: ${ARTIFACT_DIR}
  binaries: ${TARGET_NAMES[*]}
EOF
}

write_manifest() {
    local manifest_file="$1"
    local generated_at="$2"

    {
        echo "# OBTC Wallet Mainnet-Candidate Artifact Manifest"
        echo
        echo "| Field | Value |"
        echo "|---|---|"
        echo "| Version | \`${VERSION}\` |"
        echo "| Wallet commit | \`${COMMIT}\` |"
        echo "| obtcd replace source | \`${OBTCD_SRC}\` |"
        echo "| obtcd commit | \`${OBTCD_COMMIT}\` |"
        echo "| Generated UTC | \`${generated_at}\` |"
        echo "| Target | \`${GOOS_VALUE}/${GOARCH_VALUE}\` |"
        echo "| Go version | \`$(go version)\` |"
        echo "| CGO_ENABLED | \`${CGO_ENABLED_VALUE}\` |"
        echo
        echo "## Scope"
        echo
        echo "These artifacts are for the controlled OBTC mainnet-candidate operator wallet path."
        echo "They do not by themselves assert production wallet readiness or funded renewal validation."
        echo
        echo "## Artifacts"
        echo
        echo "| File | SHA256 |"
        echo "|---|---|"
        local artifact
        for artifact in "${ARTIFACT_DIR}"/*; do
            if [[ -f "${artifact}" && "$(basename "${artifact}")" != "MANIFEST.md" && "$(basename "${artifact}")" != "SHA256SUMS" ]]; then
                echo "| \`$(basename "${artifact}")\` | \`$(sha256_file "${artifact}")\` |"
            fi
        done
        echo
        echo "## Verification"
        echo
        echo '```bash'
        echo "cd ${ARTIFACT_DIR}"
        echo "shasum -a 256 -c SHA256SUMS"
        echo '```'
        echo
        echo "Sign this manifest or attach the signature artifact before public release."
    } >"${manifest_file}"
}

main() {
    print_plan

    if [[ ${DRY_RUN} -eq 1 ]]; then
        echo "[OK] dry run complete"
        return 0
    fi

    mkdir -p "${ARTIFACT_DIR}"

    local i name pkg output
    for i in "${!TARGET_NAMES[@]}"; do
        name="${TARGET_NAMES[$i]}"
        pkg="${TARGET_PKGS[$i]}"
        output="${ARTIFACT_DIR}/${name}-${GOOS_VALUE}-${GOARCH_VALUE}${EXT}"
        echo "[INFO] building ${name} from ${pkg}"
        (
            cd "${REPO_ROOT}"
            env GOOS="${GOOS_VALUE}" GOARCH="${GOARCH_VALUE}" CGO_ENABLED="${CGO_ENABLED_VALUE}" \
                go build -trimpath -ldflags="-buildid=" -o "${output}" "${pkg}"
        )
    done

    write_manifest "${ARTIFACT_DIR}/MANIFEST.md" "$(date -u +"%Y-%m-%dT%H:%M:%SZ")"

    local sums_file="${ARTIFACT_DIR}/SHA256SUMS"
    : >"${sums_file}"
    local artifact
    for artifact in "${ARTIFACT_DIR}"/*; do
        if [[ -f "${artifact}" && "$(basename "${artifact}")" != "SHA256SUMS" ]]; then
            printf "%s  %s\n" "$(sha256_file "${artifact}")" "$(basename "${artifact}")" >>"${sums_file}"
        fi
    done

    echo "[OK] artifacts written to ${ARTIFACT_DIR}"
    echo "[OK] verify with: (cd ${ARTIFACT_DIR} && shasum -a 256 -c SHA256SUMS)"
}

main
