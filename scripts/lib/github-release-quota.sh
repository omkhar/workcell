# shellcheck shell=bash
# Helpers for an upstream release check that reads the GitHub API without a
# token. Such a run has only the unauthenticated quota of 60 requests an hour,
# which a local gate shares with other local tools. The scripts that source
# this file require curl and jq. They read no token here: CI passes a token
# and takes its own path.

# github_release_local_run TOKEN_AVAILABLE succeeds only for a local run that
# may read the release cache and may skip the check on a spent quota: a run
# with no token, not in GitHub Actions, and not asked for a full check.
# scripts/lib/trusted-entrypoint.sh sets WORKCELL_GITHUB_ACTIONS on the first
# entry of a GitHub Actions run and keeps it across each sanitized re-exec.
# A caller that verifies pins it just wrote sets
# WORKCELL_UPSTREAM_RELEASE_REQUIRE_FULL_CHECK=1.
github_release_local_run() {
  [[ "$1" == "0" ]] || return 1
  [[ "${WORKCELL_GITHUB_ACTIONS:-0}" != "1" ]] || return 1
  [[ "${WORKCELL_UPSTREAM_RELEASE_REQUIRE_FULL_CHECK:-0}" != "1" ]]
}

# github_unauthenticated_get URL OUTPUT ACCEPT writes the body of URL to OUTPUT.
# It returns 0 for status 200, 3 when GitHub refuses the request because the
# quota is spent, and 1 for any other failure. It does not retry a 403, and
# it retries a 429 for at most 60 seconds, because a retry cannot succeed
# before the quota resets.
github_unauthenticated_get() {
  local url="$1"
  local output="$2"
  local accept="$3"
  local headers_file=""
  local status=""
  local remaining=""
  local reset=""
  local reset_text="an unknown time"

  headers_file="$(mktemp "${TMPDIR:-/tmp}/workcell-github-headers.XXXXXX")"
  if ! status="$(curl -sSL --retry 5 --retry-delay 5 --retry-max-time 60 --connect-timeout 20 \
    --speed-limit 1024 --speed-time 60 \
    -D "${headers_file}" \
    -o "${output}" \
    -w '%{http_code}' \
    -H "Accept: ${accept}" \
    "${url}")"; then
    rm -f "${headers_file}"
    echo "GitHub request failed: ${url}" >&2
    return 1
  fi
  if [[ "${status}" == "200" ]]; then
    rm -f "${headers_file}"
    return 0
  fi
  remaining="$(github_response_header "${headers_file}" x-ratelimit-remaining)"
  reset="$(github_response_header "${headers_file}" x-ratelimit-reset)"
  rm -f "${headers_file}"
  if [[ ("${status}" == "403" || "${status}" == "429") && "${remaining}" == "0" ]]; then
    if [[ "${reset}" =~ ^[0-9]{1,12}$ ]]; then
      reset_text="$(jq -nr --arg reset "${reset}" '$reset | tonumber | todate')"
    fi
    echo "GitHub API rate limit reached (status ${status}); the quota resets at ${reset_text}: ${url}" >&2
    return 3
  fi
  echo "Unexpected GitHub API response ${status}: ${url}" >&2
  return 1
}

# github_response_header FILE NAME prints the last value of the header NAME
# (in lower case) in the headers that curl wrote to FILE.
github_response_header() {
  awk -F': *' -v name="$2" '{ sub(/\r$/, "") } tolower($1) == name { value = $2 } END { print value }' "$1"
}

# github_release_cache_dir prints the per-user directory that keeps the release
# metadata of pins that passed a full check. It fails when HOME is not an
# absolute directory that the user owns, or is a shared temporary directory
# such as the /tmp fallback of a sanitized entry.
github_release_cache_dir() {
  case "${HOME:-}" in
    /*) ;;
    *) return 1 ;;
  esac
  case "${HOME%/}" in
    "" | /tmp | /var/tmp | /private/tmp | /private/var/tmp | "${TMPDIR%/}") return 1 ;;
  esac
  [[ -d "${HOME}" && ! -L "${HOME}" && -O "${HOME}" ]] || return 1
  printf '%s\n' "${HOME}/.cache/workcell/upstream-releases"
}

# github_release_cache_name_ok NAME succeeds when NAME is a plain file name.
github_release_cache_name_ok() {
  [[ "$1" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*\.json$ ]]
}

# github_release_cache_dir_ok DIR succeeds when DIR is a real directory that
# the user owns. It then makes DIR private to the user.
github_release_cache_dir_ok() {
  local dir="$1"

  [[ -d "${dir}" && ! -L "${dir}" && -O "${dir}" ]] || return 1
  chmod 700 "${dir}"
}

# github_release_cache_load NAME OUTPUT copies the cached entry NAME to OUTPUT.
# It fails when there is no such entry, or when the entry or the directory is
# not a regular file or directory that the user owns.
github_release_cache_load() {
  local name="$1"
  local output="$2"
  local dir=""

  github_release_cache_name_ok "${name}" || return 1
  dir="$(github_release_cache_dir)" || return 1
  github_release_cache_dir_ok "${dir}" || return 1
  [[ -f "${dir}/${name}" && ! -L "${dir}/${name}" && -O "${dir}/${name}" ]] || return 1
  cp "${dir}/${name}" "${output}"
}

# github_release_cache_store NAME INPUT keeps a copy of INPUT as the entry NAME.
# Call it only after a full check of the pin that INPUT describes.
github_release_cache_store() {
  local name="$1"
  local input="$2"
  local dir=""
  local staged=""

  github_release_cache_name_ok "${name}" || return 1
  dir="$(github_release_cache_dir)" || return 1
  (umask 077 && mkdir -p "${dir}") || return 1
  github_release_cache_dir_ok "${dir}" || return 1
  staged="$(mktemp "${dir}/.${name}.XXXXXX")" || return 1
  if ! cp "${input}" "${staged}" || ! mv -f "${staged}" "${dir}/${name}"; then
    rm -f "${staged}"
    return 1
  fi
}

# github_release_cache_entry_write OUTPUT VERSION METADATA NAME=SHA256...
# writes a cache entry that binds the release metadata of VERSION to the
# asset digests that the full check computed from the downloaded assets.
github_release_cache_entry_write() {
  local output="$1"
  local version="$2"
  local metadata="$3"
  local pins=""
  shift 3

  pins="$(github_release_digest_args_json "$@")" || return 1
  jq -n --arg version "${version}" --argjson pins "${pins}" --slurpfile release "${metadata}" \
    '{version: $version, verified_assets: $pins, release: $release[0]}' >"${output}"
}

# github_release_cache_entry_matches ENTRY VERSION NAME=SHA256... succeeds only
# when ENTRY is for VERSION, its verified digests are exactly the given pins,
# and its release metadata names each asset once with the same digest.
github_release_cache_entry_matches() {
  local entry="$1"
  local version="$2"
  local pins=""
  shift 2

  pins="$(github_release_digest_args_json "$@")" || return 1
  jq -e --arg version "${version}" --argjson pins "${pins}" '
    . as $entry |
    $entry.version == $version and
    $entry.verified_assets == $pins and
    $entry.release.tag_name == ("v" + $version) and
    ([$pins | to_entries[] | . as $pin |
      [$entry.release.assets[]? | select(.name == $pin.key)] |
      length == 1 and .[0].digest == ("sha256:" + $pin.value)] | all)
  ' "${entry}" >/dev/null 2>&1
}

# github_release_digest_args_json NAME=SHA256... prints the pins as a JSON
# object. It fails on an argument that is not a name and a sha256 digest.
github_release_digest_args_json() {
  local arg=""
  local json="{}"

  [[ "$#" -gt 0 ]] || return 1
  for arg in "$@"; do
    [[ "${arg}" =~ ^[A-Za-z0-9][A-Za-z0-9._+-]*=[0-9a-f]{64}$ ]] || return 1
    json="$(jq -c --arg name "${arg%%=*}" --arg sha "${arg#*=}" '. + {($name): $sha}' <<<"${json}")" || return 1
  done
  printf '%s\n' "${json}"
}
