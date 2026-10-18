# shellcheck shell=bash
# Checks a provider package-lock.json against the npm registry. The scripts
# that source this file require curl and jq.

# extract_lock_field LOCK_PATH PACKAGE_PATH FIELD prints one field of a
# package-lock.json entry, or nothing when the entry or field is missing.
extract_lock_field() {
  local lock_path="$1"
  local package_path="$2"
  local field_name="$3"
  jq -r --arg package_path "${package_path}" --arg field_name "${field_name}" '
    .packages[$package_path][$field_name] // empty
  ' "${lock_path}"
}

# verify_registry_package NAME PACKAGE_PATH REGISTRY_PATH VERSION RESOLVED
# INTEGRITY fails unless the registry metadata of NAME@VERSION names the same
# tarball and integrity hash as the lockfile entry.
verify_registry_package() {
  local package_name="$1"
  local package_path="$2"
  local registry_path="$3"
  local version="$4"
  local expected_resolved="$5"
  local expected_integrity="$6"
  local metadata_path="${TMPDIR:-/tmp}/workcell-${registry_path##*/}-${version}.json"

  curl -fsSL --retry 5 --retry-all-errors --retry-delay 5 --connect-timeout 20 \
    "https://registry.npmjs.org/${registry_path}/${version}" -o "${metadata_path}"

  local actual_version actual_resolved actual_integrity
  actual_version="$(jq -r '.version' "${metadata_path}")"
  actual_resolved="$(jq -r '.dist.tarball' "${metadata_path}")"
  actual_integrity="$(jq -r '.dist.integrity' "${metadata_path}")"

  if [[ "${actual_version}" != "${version}" ]]; then
    echo "${package_name} registry metadata returned ${actual_version}, expected ${version}" >&2
    exit 1
  fi
  if [[ "${actual_resolved}" != "${expected_resolved}" ]]; then
    echo "${package_name} tarball URL mismatch: expected ${expected_resolved}, got ${actual_resolved}" >&2
    exit 1
  fi
  if [[ "${actual_integrity}" != "${expected_integrity}" ]]; then
    echo "${package_name} integrity mismatch for ${package_path}" >&2
    exit 1
  fi

  rm -f "${metadata_path}"
}
