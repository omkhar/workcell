#!/bin/bash -p
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# Interpreter is pinned to the absolute /bin/bash (present on macOS and
# mainstream Linux), not `/usr/bin/env bash`, so that this pre-trust verifier
# cannot be hijacked by a fake `bash` earlier in the caller's PATH — the
# interpreter must be resolved from a trusted path before any verification runs.
# `-p` (privileged mode) additionally makes bash ignore $BASH_ENV/$ENV and any
# inherited exported functions, closing the startup-file injection vector.
#
# verify-release-artifact.sh — installer-side, fail-closed verification of a
# downloaded Workcell release artifact.
#
# The release workflow (.github/workflows/release.yml) signs SHA256SUMS as a
# keyless Sigstore blob (GitHub OIDC -> Fulcio -> Rekor, no long-lived key) and
# lists every published asset's sha256 inside it. This script consumes that same
# material: it verifies SHA256SUMS against the pinned release-workflow identity
# with cosign, then binds the named artifact to its entry in that verified
# SHA256SUMS. Optionally it also runs `gh attestation verify` against the repo.
#
# It refuses to report success unless verification actually happened
# (fail-closed): a missing tool, missing verification material, an unpinned
# identity, or a digest mismatch all exit non-zero. The only bypass is an
# explicit, acknowledged --skip-verify for documented air-gapped installs, which
# prints a loud warning and performs no verification.
#
# Exit codes:
#   0  provenance VERIFIED (signature + digest bound) — this is the ONLY success.
#   10 verification explicitly SKIPPED by acknowledged --skip-verify (UNVERIFIED
#      install); distinct from 0 so a caller can never confuse "verified" with
#      "skipped".
#   1  verification failure (missing tool/material, bad signature, digest
#      mismatch).
#   2  usage error.
set -euo pipefail
IFS=$' \t\n'

# Pre-trust PATH hardening. This script runs BEFORE the downloaded artifact is
# trusted, so resolve every external tool it calls (cosign, gh, sha256sum/shasum,
# awk, wc) from a fixed trusted PATH instead of the caller's ambient PATH. A
# user-writable early-PATH entry must not be able to shadow these with a fake
# cosign/sha256sum that would make a tampered artifact "verify". The default
# covers the system directories plus the documented cosign/gh install locations
# (`brew install cosign` -> /opt/homebrew/bin on Apple Silicon, /usr/local/bin on
# Intel/Linux). A controlled environment — the test harness, or a host with
# cosign in a nonstandard directory — may set WORKCELL_INSTALL_TRUSTED_PATH to
# override. This defends against ambient PATH pollution; an attacker who already
# controls the whole process environment is out of scope (BASH_ENV/ENV are
# already neutralized by the shebang, but such an attacker has other vectors).
export PATH="${WORKCELL_INSTALL_TRUSTED_PATH:-/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/opt/homebrew/bin}"

DEFAULT_REPO="omkhar/workcell"
OIDC_ISSUER="https://token.actions.githubusercontent.com"
# The closed set of releases published before the release workflow moved to the
# repository_dispatch trigger, and therefore the only releases whose signing
# identity is the pushed tag rather than the default branch. Every later release
# must present the default-branch identity. Extend this list only for a release
# that was genuinely published under the old tag-push trigger.
TAG_SIGNED_RELEASES="v1.0.2"
# Distinct from 0 (verified) and from failure codes: an acknowledged skip is an
# unverified install, not a verified one.
EXIT_SKIPPED_UNVERIFIED=10

usage() {
  cat <<'EOF'
Usage: verify-release-artifact.sh --assets-dir DIR --artifact NAME [options]

Verify a downloaded Workcell release artifact against the signed SHA256SUMS
published by the release workflow. Fail-closed: any missing tool, missing
material, or verification failure exits non-zero.

Required:
  --assets-dir DIR   Directory holding the artifact, SHA256SUMS, and
                     SHA256SUMS.sigstore.json (as downloaded from the release).
  --artifact NAME    Basename of the artifact to verify (e.g.
                     workcell-v1.2.3.tar.gz).

Options:
  --repo OWNER/REPO  Release repository to pin the signer identity against
                     (default: omkhar/workcell, or $GITHUB_REPOSITORY).
  --tag vX.Y.Z       Release tag being verified. Naming a release that was
                     published before the dispatch trigger also accepts that
                     one exact historical tag identity; every later release
                     must present the default-branch identity.
  --attestation      Additionally require `gh attestation verify` to pass.
  --skip-verify      Do NOT verify. Requires --i-understand-unverified-install
                     and prints a loud warning. For documented air-gapped use.
  --i-understand-unverified-install
                     Acknowledge an unverified install (only with --skip-verify).
  -h, --help         Show this help text.
EOF
}

fail() {
  echo "verify-release-artifact: $*" >&2
  exit 1
}

sha256_of() {
  local path="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${path}" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${path}" | awk '{print $1}'
  else
    fail "no sha256 tool available (need sha256sum or shasum)"
  fi
}

# Escape Go-regexp metacharacters in a literal string so it can be embedded in a
# --certificate-identity-regexp without a repo/owner name (which may contain '.',
# etc.) widening the match. cosign has no QuoteMeta on the CLI, so we escape in
# shell. Pure bash (no external tool) and bash-3.2 safe; only the intended tag
# wildcard is left free by the caller.
regex_escape() {
  local s="$1" out="" c i
  for ((i = 0; i < ${#s}; i++)); do
    c="${s:i:1}"
    case "${c}" in
      \\ | '.' | '+' | '*' | '?' | '(' | ')' | '[' | ']' | '{' | '}' | '^' | '$' | '|')
        out+="\\${c}"
        ;;
      *)
        out+="${c}"
        ;;
    esac
  done
  printf '%s' "${out}"
}

ASSETS_DIR=""
ARTIFACT=""
TAG=""
REPO="${GITHUB_REPOSITORY:-${DEFAULT_REPO}}"
REQUIRE_ATTESTATION=0
SKIP_VERIFY=0
ACK_UNVERIFIED=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --assets-dir)
      ASSETS_DIR="${2:?--assets-dir requires a path}"
      shift 2
      ;;
    --artifact)
      ARTIFACT="${2:?--artifact requires a name}"
      shift 2
      ;;
    --repo)
      REPO="${2:?--repo requires OWNER/REPO}"
      shift 2
      ;;
    --tag)
      TAG="${2:?--tag requires a release tag}"
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

[[ -n "${ARTIFACT}" ]] || {
  echo "Missing required --artifact" >&2
  usage >&2
  exit 2
}
if [[ "${ARTIFACT}" == */* ]]; then
  echo "--artifact must be a basename, not a path: ${ARTIFACT}" >&2
  exit 2
fi
# Same tag shape the release workflow accepts, so a malformed value cannot reach
# the identity expression.
if [[ -n "${TAG}" ]] && [[ ! "${TAG}" =~ ^v[0-9A-Za-z.-]{1,63}$ ]]; then
  echo "--tag must be a release tag of the form vX.Y.Z: ${TAG}" >&2
  exit 2
fi

if [[ "${SKIP_VERIFY}" -eq 1 ]]; then
  if [[ "${ACK_UNVERIFIED}" -ne 1 ]]; then
    echo "verify-release-artifact: --skip-verify requires --i-understand-unverified-install" >&2
    echo "Refusing to skip verification without an explicit acknowledgement." >&2
    exit 2
  fi
  {
    echo "############################################################"
    echo "# WARNING: installing WITHOUT verifying release provenance. #"
    echo "############################################################"
    echo "Artifact '${ARTIFACT}' was NOT checked against the signed SHA256SUMS."
    echo "This is an UNVERIFIED install; provenance was NOT verified."
    echo "You are trusting this file's integrity and origin yourself."
    echo "Only do this for a documented air-gapped install where you have"
    echo "already verified the artifact out of band (see docs/install.md)."
  } >&2
  # Exit 10 (not 0): an acknowledged skip is an unverified install. Reserving 0
  # for genuine verification keeps a caller from mistaking a skip for success.
  exit "${EXIT_SKIPPED_UNVERIFIED}"
fi

[[ -n "${ASSETS_DIR}" ]] || {
  echo "Missing required --assets-dir" >&2
  usage >&2
  exit 2
}

command -v cosign >/dev/null 2>&1 || fail \
  "cosign is required to verify the release signature but was not found. Install it (e.g. 'brew install cosign') or, for a documented air-gapped install, re-run with --skip-verify --i-understand-unverified-install."

[[ -d "${ASSETS_DIR}" ]] || fail "assets directory not found: ${ASSETS_DIR}"

sums_path="${ASSETS_DIR}/SHA256SUMS"
bundle_path="${ASSETS_DIR}/SHA256SUMS.sigstore.json"
artifact_path="${ASSETS_DIR}/${ARTIFACT}"

[[ -f "${artifact_path}" ]] || fail "artifact not found: ${artifact_path}"
[[ -f "${sums_path}" ]] || fail "verification material missing: ${sums_path}"
[[ -f "${bundle_path}" ]] || fail "verification material missing: ${bundle_path} (the release publishes it alongside SHA256SUMS)"

# Anchor (^…$) and escape every fixed segment, leaving no wildcard at all.
# release.yml is triggered solely by repository_dispatch, which always loads the
# workflow from the default branch, so the keyless Fulcio identity of a release
# published under that trigger is exactly
# https://github.com/OWNER/REPO/.github/workflows/release.yml@refs/heads/main.
#
# Releases published before that change were signed from the pushed tag. That
# identity is accepted only for the closed set of releases below, and only when
# the caller names one of them. Accepting it for any requested tag would let a
# credential that can create a tag, but not change the default branch, sign a
# release from an arbitrary commit and defeat the branch binding entirely.
# Both alternatives stay exact, so neither can match a different release.
repo_escaped="$(regex_escape "${REPO}")"
identity_prefix="https://github\.com/${repo_escaped}/\.github/workflows/release\.yml@"
identity_refs="refs/heads/main"
if [[ -n "${TAG}" ]] && [[ " ${TAG_SIGNED_RELEASES} " == *" ${TAG} "* ]]; then
  identity_refs="${identity_refs}|refs/tags/$(regex_escape "${TAG}")"
fi
identity_regexp="^${identity_prefix}(${identity_refs})\$"

echo "Verifying ${ARTIFACT} against ${REPO} release signing identity..." >&2

cosign verify-blob "${sums_path}" \
  --bundle "${bundle_path}" \
  --certificate-identity-regexp "${identity_regexp}" \
  --certificate-oidc-issuer "${OIDC_ISSUER}" \
  >/dev/null || fail "cosign could not verify SHA256SUMS against ${identity_regexp}"

expected_digest="$(awk -v name="${ARTIFACT}" '$2 == name {print $1}' "${sums_path}")"
if [[ -z "${expected_digest}" ]]; then
  fail "SHA256SUMS (verified) has no entry for ${ARTIFACT}; it is not part of this signed release"
fi
# shellcheck disable=SC2312 # wc counts an already-captured string; the empty case is rejected above
if [[ "$(printf '%s\n' "${expected_digest}" | wc -l)" -ne 1 ]]; then
  fail "SHA256SUMS has multiple entries for ${ARTIFACT}; refusing ambiguous match"
fi

actual_digest="$(sha256_of "${artifact_path}")"
if [[ "${actual_digest}" != "${expected_digest}" ]]; then
  fail "digest mismatch for ${ARTIFACT}: computed ${actual_digest}, signed SHA256SUMS says ${expected_digest}"
fi

if [[ "${REQUIRE_ATTESTATION}" -eq 1 ]]; then
  command -v gh >/dev/null 2>&1 || fail \
    "--attestation requires the GitHub CLI (gh) but it was not found"
  echo "Verifying GitHub attestation for ${ARTIFACT}..." >&2
  # gh's --signer-workflow is compiled into a certificate-SAN *regex*, not an
  # exact match (cli/cli#9507), so an unescaped '.' in it would over-match. Pin
  # the signer with the SAME anchored, escaped regex used for the cosign
  # signature above — the attestation SAN is the same keyless
  # release.yml@refs/heads/main identity — via --cert-identity-regex, and pin the OIDC
  # issuer explicitly. --repo is an exact owner/repo match for attestation
  # lookup. This keeps the attestation identity pin exactly as tight as the
  # cosign one, with no over-match.
  gh attestation verify "${artifact_path}" \
    --repo "${REPO}" \
    --cert-identity-regex "${identity_regexp}" \
    --cert-oidc-issuer "${OIDC_ISSUER}" \
    >/dev/null || fail "gh attestation verify failed for ${ARTIFACT}"
fi

echo "OK: ${ARTIFACT} verified against ${REPO} signed SHA256SUMS (sha256:${expected_digest})" >&2
if [[ "${REQUIRE_ATTESTATION}" -eq 1 ]]; then
  echo "OK: ${ARTIFACT} GitHub attestation verified for ${REPO}" >&2
fi
