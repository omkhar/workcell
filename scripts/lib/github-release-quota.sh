# shellcheck shell=bash
# Helpers for an upstream release check that reads the GitHub API without a
# token. Such a run has only the unauthenticated quota of 60 requests an hour,
# which a local gate shares with other local tools. The scripts that source
# this file require curl and jq. They read no token here: CI passes a token
# and takes its own path.

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
# metadata of pins that passed a full check.
github_release_cache_dir() {
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
  dir="$(github_release_cache_dir)"
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
  dir="$(github_release_cache_dir)"
  (umask 077 && mkdir -p "${dir}") || return 1
  github_release_cache_dir_ok "${dir}" || return 1
  staged="$(mktemp "${dir}/.${name}.XXXXXX")" || return 1
  if ! cp "${input}" "${staged}" || ! mv -f "${staged}" "${dir}/${name}"; then
    rm -f "${staged}"
    return 1
  fi
}
