#!/bin/bash -p
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# Interpreter is pinned to the absolute /bin/bash (present on macOS and
# mainstream Linux), not `/usr/bin/env bash`, so that this pre-trust installer
# cannot be hijacked by a fake `bash` earlier in the caller's PATH — the
# interpreter must be resolved from a trusted path before any download or
# verification runs. `-p` (privileged mode) additionally makes bash ignore
# $BASH_ENV/$ENV and any inherited exported functions, closing the startup-file
# injection vector.
#
# install-release.sh — the verified install path for a tagged Workcell release.
#
# It downloads a tagged release bundle plus its signed SHA256SUMS material,
# verifies the bundle fail-closed with scripts/verify-release-artifact.sh
# BEFORE any bundle code runs, and only then extracts it and hands off to the
# bundle's own scripts/install.sh. Verifying before extraction is what makes
# this sound: a tampered bundle is rejected before its (also-tampered) installer
# could run. The plain scripts/install.sh installs from an already-trusted tree
# (a source checkout or a bundle you verified yourself) and does not download.
set -euo pipefail
IFS=$' \t\n'

# Pre-trust PATH hardening. This script downloads and verifies a release BEFORE
# any of it is trusted, so resolve the tools it calls (curl, tar, find,
# sha256sum, plus cosign/gh via verify-release-artifact.sh) from a fixed trusted
# PATH instead of the caller's ambient PATH. A user-writable early-PATH entry
# must not be able to shadow curl/tar/sha256sum/cosign with a fake that would let
# a tampered artifact install. The default covers the system directories plus the
# documented cosign/gh install locations (`brew install cosign` ->
# /opt/homebrew/bin on Apple Silicon, /usr/local/bin on Intel/Linux); the
# verified bundle's own scripts/install.sh then runs with this same trusted PATH
# (Homebrew lives in /opt/homebrew/bin, which is included). A controlled
# environment may set WORKCELL_INSTALL_TRUSTED_PATH to override. This defends
# against ambient PATH pollution, not an attacker who controls the whole
# environment (BASH_ENV/ENV are already neutralized by the shebang).
export PATH="${WORKCELL_INSTALL_TRUSTED_PATH:-/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/opt/homebrew/bin}"

SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEFAULT_REPO="omkhar/workcell"

usage() {
  cat <<'EOF'
Usage: install-release.sh --version vX.Y.Z [options] [-- INSTALLER_ARGS...]

Download, verify (fail-closed), and install a tagged Workcell release.

Required:
  --version vX.Y.Z   Release tag to install.

Options:
  --repo OWNER/REPO  Release repository (default: omkhar/workcell).
  --attestation      Also require `gh attestation verify` to pass.
  --skip-verify      Install WITHOUT verifying provenance. Requires
                     --i-understand-unverified-install and prints a loud
                     warning. For documented air-gapped use only.
  --i-understand-unverified-install
                     Acknowledge an unverified install (only with --skip-verify).
  -h, --help         Show this help text.

Arguments after `--` are forwarded to the bundle's scripts/install.sh
(e.g. --no-install-deps, --debug).
EOF
}

VERSION=""
REPO="${DEFAULT_REPO}"
REQUIRE_ATTESTATION=0
SKIP_VERIFY=0
ACK_UNVERIFIED=0
declare -a INSTALLER_ARGS=()

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version)
      VERSION="${2:?--version requires a value}"
      shift 2
      ;;
    --repo)
      REPO="${2:?--repo requires OWNER/REPO}"
      shift 2
      ;;
    --attestation)
      REQUIRE_ATTESTATION=1
      shift
      ;;
    --skip-verify)
      SKIP_VERIFY=1
      shift
      ;;
    --i-understand-unverified-install)
      ACK_UNVERIFIED=1
      shift
      ;;
    --)
      shift
      INSTALLER_ARGS=("$@")
      break
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unsupported option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

# Validate everything before touching the network.
if [[ -z "${VERSION}" ]]; then
  echo "Missing required --version" >&2
  usage >&2
  exit 2
fi
if [[ ! "${VERSION}" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "Invalid --version (expected vX.Y.Z): ${VERSION}" >&2
  exit 2
fi
if [[ ! "${REPO}" =~ ^[0-9A-Za-z._-]+/[0-9A-Za-z._-]+$ ]]; then
  echo "Invalid --repo (expected OWNER/REPO): ${REPO}" >&2
  exit 2
fi
if [[ "${SKIP_VERIFY}" -eq 1 && "${ACK_UNVERIFIED}" -ne 1 ]]; then
  echo "--skip-verify requires --i-understand-unverified-install" >&2
  exit 2
fi

command -v curl >/dev/null 2>&1 || {
  echo "curl is required to download the release" >&2
  exit 1
}
command -v tar >/dev/null 2>&1 || {
  echo "tar is required to unpack the release" >&2
  exit 1
}

BUNDLE_NAME="workcell-${VERSION}.tar.gz"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"

WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-install-release.XXXXXX")"
cleanup() {
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

download() {
  local name="$1"
  echo "Downloading ${name}..." >&2
  if ! curl -fsSL \
    --retry 3 \
    --retry-delay 2 \
    --retry-connrefused \
    --max-time 300 \
    --connect-timeout 15 \
    --max-filesize 1073741824 \
    -o "${WORK_DIR}/${name}" \
    "${BASE_URL}/${name}"; then
    echo "Failed to download required release asset: ${name}" >&2
    exit 1
  fi
}

download "${BUNDLE_NAME}"
if [[ "${SKIP_VERIFY}" -eq 0 ]]; then
  download "SHA256SUMS"
  download "SHA256SUMS.sigstore.json"
fi

verify_args=(--assets-dir "${WORK_DIR}" --artifact "${BUNDLE_NAME}" --repo "${REPO}")
if [[ "${REQUIRE_ATTESTATION}" -eq 1 ]]; then
  verify_args+=(--attestation)
fi
if [[ "${SKIP_VERIFY}" -eq 1 ]]; then
  verify_args+=(--skip-verify --i-understand-unverified-install)
fi

# verify-release-artifact.sh exits 0 ONLY when provenance is genuinely verified,
# and 10 when the operator explicitly acknowledged an unverified air-gapped skip
# (it prints its own loud banner in that case). Any other non-zero code is a
# verification failure and must abort the install. Capturing the code keeps a
# skip from ever being mistaken for a verified install.
verify_rc=0
"${SELF_DIR}/verify-release-artifact.sh" "${verify_args[@]}" || verify_rc=$?
if [[ "${verify_rc}" -ne 0 && "${verify_rc}" -ne 10 ]]; then
  exit "${verify_rc}"
fi

EXTRACT_DIR="${WORK_DIR}/extract"
mkdir -p "${EXTRACT_DIR}"
tar -xzf "${WORK_DIR}/${BUNDLE_NAME}" -C "${EXTRACT_DIR}"

bundle_root="$(find "${EXTRACT_DIR}" -mindepth 1 -maxdepth 1 -type d -name 'workcell-*' -print -quit)"
if [[ -z "${bundle_root}" ]]; then
  echo "Unable to locate extracted bundle root under ${EXTRACT_DIR}" >&2
  exit 1
fi

# Never log "Verified" for a skipped install: verify_rc is 0 (provenance
# verified) or 10 (explicitly skipped via --skip-verify) here — any other code
# already aborted above.
if [[ "${verify_rc}" -eq 10 ]]; then
  echo "UNVERIFIED install (provenance verification skipped via --skip-verify); installing ${BUNDLE_NAME} from ${bundle_root}..." >&2
else
  echo "Verified ${BUNDLE_NAME}; installing from ${bundle_root}..." >&2
fi
exec "${bundle_root}/scripts/install.sh" ${INSTALLER_ARGS[@]:+"${INSTALLER_ARGS[@]}"}
