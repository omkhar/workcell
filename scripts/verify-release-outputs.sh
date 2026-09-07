#!/bin/bash -p
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam

# Verify the release outputs produced by the signing job. This script only
# reads release files and remote metadata. It has no publication authority.
#
# Language-boundary justification (AGENTS.md, "Change discipline"): this
# verifier stays in shell instead of Go, and the justification is recorded here
# in the same change that adds it. Every trust decision the script makes is made
# by an external CLI -- `cosign verify-blob`, `cosign verify`, and
# `gh attestation verify`. What the script itself contributes is argument
# construction against a fixed asset inventory plus digest comparison around
# those calls. It is the workflow-side mirror of the installer-side
# `scripts/verify-release-artifact.sh`, which must run on a consumer machine
# with no Go toolchain; keeping both verifiers in one language and one shape
# lets them be diffed against each other whenever the release asset set moves.
# A Go port would wrap the same three commands through `os/exec` and add a build
# step to a job whose whole purpose is to depend on as little of this repository
# as possible.
set -euo pipefail
IFS=$' \t\n'

readonly TRUSTED_PATH="/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin:/opt/homebrew/bin"
export PATH="${TRUSTED_PATH}"

readonly OIDC_ISSUER="https://token.actions.githubusercontent.com"
readonly SCRIPT_NAME="verify-release-outputs.sh"

ASSETS_DIR=""
REPOSITORY=""
TAG=""
IMAGE_REPOSITORY=""
SOURCE_DIGEST=""
WORKFLOW_DIGEST=""
REQUIRE_ATTESTATIONS=0

usage() {
  cat <<'EOF'
Usage: verify-release-outputs.sh --assets-dir DIR --repo OWNER/REPO --tag TAG --image-repository IMAGE --source-digest SHA --workflow-digest SHA [--attestations]

Verify every release-data signature, the published image signature, and all
ten GitHub artifact attestations when --attestations is provided.
EOF
}

fail() {
  echo "${SCRIPT_NAME}: $*" >&2
  exit 1
}

contains_asset() {
  local wanted="$1"
  shift
  local value=""

  for value in "$@"; do
    [[ "${value}" == "${wanted}" ]] && return 0
  done
  return 1
}

validate_repository() {
  local repository="$1"
  local owner=""
  local name=""

  [[ "${repository}" =~ ^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$ ]] || return 1
  owner="${repository%%/*}"
  name="${repository#*/}"
  [[ "${owner}" != *- && "${name}" != "." && "${name}" != ".." ]]
}

validate_release_tag() {
  local tag="$1"

  # Same accepted set as release.ClassifyTag, which the tag-policy job runs.
  # Do not add a bound this verifier alone enforces: a tag that clears the
  # policy gate and fails here would stall an otherwise valid release.
  [[ "${tag}" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-rc\.([1-9][0-9]*))?$ ]]
}

require_regular_file() {
  local path="$1"

  [[ -f "${path}" && ! -L "${path}" ]] || fail "required release file is missing or unsafe: ${path}"
}

sha256_of() {
  sha256sum "$1" | awk '{print $1}'
}

run_cosign() {
  (
    unset ATTESTATION_TOKEN GITHUB_TOKEN GH_TOKEN
    cosign "$@"
  )
}

verify_blob_signature() {
  local asset="$1"
  local signature="${asset}.sigstore.json"

  run_cosign verify-blob "${ASSETS_DIR}/${asset}" \
    --bundle "${ASSETS_DIR}/${signature}" \
    --certificate-identity "${IDENTITY}" \
    --certificate-oidc-issuer "${OIDC_ISSUER}" \
    --certificate-github-workflow-sha "${WORKFLOW_DIGEST}" \
    >/dev/null || fail "Cosign verification failed for ${asset}"
}

verify_checksum_binding() {
  local asset="$1"
  local expected=""
  local actual=""

  expected="$(awk -v name="${asset}" '$2 == name {print $1}' "${ASSETS_DIR}/SHA256SUMS")"
  [[ "$(wc -l <<<"${expected}")" -eq 1 && "${expected}" =~ ^[0-9a-f]{64}$ ]] ||
    fail "SHA256SUMS has no unique digest for ${asset}"
  actual="$(sha256_of "${ASSETS_DIR}/${asset}")"
  [[ "${actual}" == "${expected}" ]] ||
    fail "SHA256SUMS digest mismatch for ${asset}: ${actual} != ${expected}"
}

verify_checksum_inventory() {
  local digest=""
  local asset=""
  local extra=""
  local count=0
  local seen_assets=$'\n'

  while read -r digest asset extra; do
    [[ -n "${digest}" && -n "${asset}" && -z "${extra}" ]] ||
      fail "SHA256SUMS contains a malformed entry"
    [[ "${digest}" =~ ^[0-9a-f]{64}$ ]] || fail "SHA256SUMS contains an invalid digest for ${asset}"
    contains_asset "${asset}" "${CHECKSUM_ASSETS[@]}" || fail "SHA256SUMS contains an unexpected asset: ${asset}"
    [[ "${seen_assets}" != *$'\n'"${asset}"$'\n'* ]] || fail "SHA256SUMS contains a duplicate asset: ${asset}"
    seen_assets+="${asset}"$'\n'
    count=$((count + 1))
  done <"${ASSETS_DIR}/SHA256SUMS"

  [[ "${count}" -eq "${#CHECKSUM_ASSETS[@]}" ]] || fail "SHA256SUMS does not contain the exact release asset set"
}

verify_named_image_tag() {
  local tag_name="$1"
  local tagged_image_ref="${IMAGE_REPOSITORY}:${tag_name}"
  local tagged_image_verification=""

  tagged_image_verification="$(run_cosign verify "${tagged_image_ref}" \
    --certificate-identity "${IDENTITY}" \
    --certificate-oidc-issuer "${OIDC_ISSUER}" \
    --certificate-github-workflow-sha "${WORKFLOW_DIGEST}")" ||
    fail "Cosign image verification failed for ${tagged_image_ref}"
  jq -e --arg expected "sha256:${image_digest}" \
    'type == "array" and length > 0 and all(.[]; .critical.image["docker-manifest-digest"] == $expected)' \
    <<<"${tagged_image_verification}" >/dev/null ||
    fail "named image tag ${tagged_image_ref} does not bind to sha256:${image_digest}"
}

verify_attestation() {
  local subject="$1"
  local predicate_type="${2:-https://slsa.dev/provenance/v1}"
  local token="${ATTESTATION_TOKEN}"

  (
    unset ATTESTATION_TOKEN GITHUB_TOKEN GH_TOKEN
    GH_HOST="github.com" GH_TOKEN="${token}" \
      GH_ENTERPRISE_TOKEN="" GITHUB_ENTERPRISE_TOKEN="" \
      gh attestation verify "${subject}" \
      --repo "${REPOSITORY}" \
      --cert-identity "${IDENTITY}" \
      --source-ref "refs/heads/main" \
      --source-digest "${WORKFLOW_DIGEST}" \
      --signer-digest "${WORKFLOW_DIGEST}" \
      --cert-oidc-issuer "${OIDC_ISSUER}" \
      --predicate-type "${predicate_type}" \
      --deny-self-hosted-runners \
      >/dev/null
  ) || fail "GitHub attestation verification failed for ${subject}"
}

main() {
  local token_value=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --assets-dir)
        ASSETS_DIR="${2:?--assets-dir requires a directory}"
        shift 2
        ;;
      --repo)
        REPOSITORY="${2:?--repo requires OWNER/REPO}"
        shift 2
        ;;
      --tag)
        TAG="${2:?--tag requires a release tag}"
        shift 2
        ;;
      --image-repository)
        IMAGE_REPOSITORY="${2:?--image-repository requires an image repository}"
        shift 2
        ;;
      --source-digest)
        SOURCE_DIGEST="${2:?--source-digest requires a commit digest}"
        shift 2
        ;;
      --workflow-digest)
        WORKFLOW_DIGEST="${2:?--workflow-digest requires a commit digest}"
        shift 2
        ;;
      --attestations)
        REQUIRE_ATTESTATIONS=1
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

  [[ -n "${ASSETS_DIR}" && -d "${ASSETS_DIR}" ]] || fail "assets directory is required"
  # A symlinked assets directory makes the inventory walk below emit nothing --
  # `find` does not descend a command-line symlink -- while the per-asset checks
  # still resolve through it, so an unexpected file would pass unseen.
  [[ ! -L "${ASSETS_DIR}" ]] || fail "assets directory must not be a symlink: ${ASSETS_DIR}"
  validate_repository "${REPOSITORY}" || fail "invalid repository: ${REPOSITORY}"
  validate_release_tag "${TAG}" || fail "invalid release tag: ${TAG}"
  [[ "${IMAGE_REPOSITORY}" == "ghcr.io/${REPOSITORY}" ]] ||
    fail "image repository must match release repository ghcr.io/${REPOSITORY}: ${IMAGE_REPOSITORY}"
  [[ "${SOURCE_DIGEST}" =~ ^[0-9a-f]{40}$ ]] || fail "source digest must be a 40-character lowercase Git commit ID"
  [[ "${WORKFLOW_DIGEST}" =~ ^[0-9a-f]{40}$ ]] || fail "workflow digest must be a 40-character lowercase Git commit ID"
  [[ "${SOURCE_DIGEST}" == "${WORKFLOW_DIGEST}" ]] || fail "release source and trusted workflow digests must match"
  command -v cosign >/dev/null 2>&1 || fail "cosign is required"
  if [[ "${REQUIRE_ATTESTATIONS}" -eq 1 ]]; then
    token_value="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
    [[ -n "${token_value}" ]] || fail "GITHUB_TOKEN or GH_TOKEN is required for attestation verification"
    [[ "${#token_value}" -le 4096 && ! "${token_value}" =~ [[:space:][:cntrl:]] ]] ||
      fail "attestation token must be bounded and must not contain whitespace or control characters"
    ATTESTATION_TOKEN="${token_value}"
    export -n ATTESTATION_TOKEN 2>/dev/null || true
    unset GITHUB_TOKEN GH_TOKEN
    command -v gh >/dev/null 2>&1 || fail "gh is required for attestation verification"
  fi

  BUNDLE_NAME="workcell-${TAG}.tar.gz"
  DATA_ASSETS=(
    "${BUNDLE_NAME}"
    "workcell.rb"
    "workcell-image.digest"
    "workcell-build-inputs.json"
    "workcell-control-plane.json"
    "workcell-builder-environment.json"
    "SHA256SUMS"
    "workcell-source.spdx.json"
    "workcell-image.spdx.json"
  )
  SIGNATURE_ASSETS=()
  CHECKSUM_ASSETS=()
  for asset in "${DATA_ASSETS[@]}"; do
    SIGNATURE_ASSETS+=("${asset}.sigstore.json")
    [[ "${asset}" == "SHA256SUMS" ]] || CHECKSUM_ASSETS+=("${asset}")
  done

  EXPECTED_ASSETS=("${DATA_ASSETS[@]}" "${SIGNATURE_ASSETS[@]}")
  # Bash does not propagate a process-substitution failure, so `find` errors are
  # invisible to the loop below. Emit a lone NUL after a successful `find` and
  # treat it as a completion sentinel: the loop reads it as an empty path, which
  # `find` itself can never produce. A truncated walk therefore fails closed
  # instead of leaving the per-asset checks, which only open names they already
  # expect, to report success over an inventory that was never read.
  listed_count=0
  walk_completed=0
  while IFS= read -r -d '' path; do
    if [[ -z "${path}" ]]; then
      walk_completed=1
      continue
    fi
    require_regular_file "${path}"
    asset="${path##*/}"
    contains_asset "${asset}" "${EXPECTED_ASSETS[@]}" || fail "unexpected release file: ${asset}"
    listed_count=$((listed_count + 1))
  done < <(find "${ASSETS_DIR}" -mindepth 1 -maxdepth 1 -print0 && printf '\0')

  [[ "${walk_completed}" -eq 1 ]] || fail "release directory listing failed"
  # A completed walk can still observe nothing: `find` exits 0 without descending
  # a command-line symlink. Assert it saw the whole inventory, not just that it
  # ran to completion.
  [[ "${listed_count}" -eq "${#EXPECTED_ASSETS[@]}" ]] ||
    fail "release directory listing is incomplete: read ${listed_count} of ${#EXPECTED_ASSETS[@]} expected entries"

  for asset in "${DATA_ASSETS[@]}" "${SIGNATURE_ASSETS[@]}"; do
    require_regular_file "${ASSETS_DIR}/${asset}"
  done
  jq -e --arg expected "${SOURCE_DIGEST}" '.build.ref == $expected' \
    "${ASSETS_DIR}/workcell-build-inputs.json" >/dev/null ||
    fail "build input manifest does not bind to release commit ${SOURCE_DIGEST}"

  IDENTITY="https://github.com/${REPOSITORY}/.github/workflows/release.yml@refs/heads/main"
  for asset in "${DATA_ASSETS[@]}"; do
    verify_blob_signature "${asset}"
  done

  verify_checksum_inventory
  for asset in "${CHECKSUM_ASSETS[@]}"; do
    verify_checksum_binding "${asset}"
  done

  image_digest_line_count="$(awk 'END {print NR}' "${ASSETS_DIR}/workcell-image.digest")"
  [[ "${image_digest_line_count}" -eq 1 ]] ||
    fail "image digest file must contain exactly one line"
  image_ref="$(awk 'NR == 1 {print}' "${ASSETS_DIR}/workcell-image.digest")"
  image_prefix="${IMAGE_REPOSITORY}@sha256:"
  [[ "${image_ref}" == "${image_prefix}"* ]] ||
    fail "image digest file does not bind to ${IMAGE_REPOSITORY}: ${image_ref}"
  image_digest="${image_ref#"${image_prefix}"}"
  [[ "${image_digest}" =~ ^[0-9a-f]{64}$ ]] ||
    fail "image digest file does not contain a lowercase SHA-256 digest"
  run_cosign verify "${image_ref}" \
    --certificate-identity "${IDENTITY}" \
    --certificate-oidc-issuer "${OIDC_ISSUER}" \
    --certificate-github-workflow-sha "${WORKFLOW_DIGEST}" \
    >/dev/null || fail "Cosign image verification failed for ${image_ref}"

  verify_named_image_tag "${TAG}"
  verify_named_image_tag "sha-${SOURCE_DIGEST}"

  if [[ "${REQUIRE_ATTESTATIONS}" -eq 1 ]]; then
    verify_attestation "oci://${image_ref}"
    verify_attestation "oci://${image_ref}" "https://spdx.dev/Document/v2.3"
    for asset in \
      "${BUNDLE_NAME}" \
      workcell.rb \
      workcell-image.digest \
      workcell-build-inputs.json \
      workcell-control-plane.json \
      workcell-builder-environment.json \
      SHA256SUMS; do
      verify_attestation "${ASSETS_DIR}/${asset}"
    done
    verify_attestation "${ASSETS_DIR}/${BUNDLE_NAME}" "https://spdx.dev/Document/v2.3"
  fi

  echo "Verified release outputs for ${TAG}." >&2
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
  main "$@"
fi
