#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/local/go/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "update-provider-pins-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
POLICY_PATH="${ROOT_DIR}/policy/provider-bumps.toml"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
PROVIDERS_PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers/package.json"
PROVIDERS_DIR="${ROOT_DIR}/runtime/container/providers"

mode="summary"
now_override=""

usage() {
  cat <<'EOF'
Usage: scripts/update-provider-pins.sh [--apply | --check | --json] [--now RFC3339]

Modes:
  --apply   Update pinned provider versions to the newest stable releases older than the configured cool-off and refresh package-lock.json.
  --check   Exit non-zero when an eligible stable provider update is available.
  --json    Print the resolved provider bump plan as JSON.

Without a mode flag, the script prints a human-readable summary.
EOF
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

NPM_BIN="${WORKCELL_NPM_BIN:-}"

resolve_npm_bin() {
  if [[ -n "${NPM_BIN}" && -x "${NPM_BIN}" ]]; then
    return 0
  fi
  if NPM_BIN="$(command -v npm 2>/dev/null)"; then
    return 0
  fi
  for candidate in \
    /opt/homebrew/bin/npm \
    /usr/local/bin/npm \
    /usr/bin/npm \
    "${HOME}"/.nvm/versions/node/*/bin/npm; do
    if [[ -x "${candidate}" ]]; then
      NPM_BIN="${candidate}"
      return 0
    fi
  done
  echo "Missing required tool: npm" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --apply)
      mode="apply"
      shift
      ;;
    --check)
      mode="check"
      shift
      ;;
    --json)
      mode="json"
      shift
      ;;
    --now)
      now_override="${2:-}"
      if [[ -z "${now_override}" ]]; then
        echo "--now requires an RFC3339 value." >&2
        exit 2
      fi
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

require_tool go
require_tool jq
if [[ "${mode}" == "apply" ]]; then
  resolve_npm_bin
  npm_bin_dir="$(dirname "${NPM_BIN}")"
  export PATH="${npm_bin_dir}:${PATH}"
fi

plan_cmd=(go run ./cmd/workcell-metadatautil provider-bump-plan "${POLICY_PATH}" "${DOCKERFILE_PATH}" "${PROVIDERS_PACKAGE_JSON_PATH}")
if [[ -n "${now_override}" ]]; then
  plan_cmd+=("${now_override}")
fi

plan_json="$(
  cd "${ROOT_DIR}"
  "${plan_cmd[@]}"
)"

if [[ "${mode}" == "json" ]]; then
  printf '%s\n' "${plan_json}"
  exit 0
fi

print_summary() {
  jq -r '
    "Provider bump policy: " + (.cooloff_hours|tostring) + "h cool-off (cutoff " + .cutoff + ")",
    (
      .providers
      | to_entries
      | sort_by(.key)
      | .[]
      | "  " + .key + ": " + .value.current_version + " -> " + .value.target_version +
        (if .value.changed then " (update available, published " + .value.published_at + ")" else " (up to date)" end)
    )
  ' <<<"${plan_json}"
}

if [[ "${mode}" == "summary" ]]; then
  print_summary
  exit 0
fi

if jq -e '.has_changes' >/dev/null <<<"${plan_json}"; then
  if [[ "${mode}" == "check" ]]; then
    print_summary
    exit 1
  fi
else
  if [[ "${mode}" == "check" ]]; then
    print_summary
    exit 0
  fi
  echo "No eligible stable provider pin updates found."
  exit 0
fi

plan_path="$(mktemp "${TMPDIR:-/tmp}/workcell-provider-bump-plan.XXXXXX.json")"
cleanup() {
  rm -f "${plan_path}"
}
trap cleanup EXIT
printf '%s\n' "${plan_json}" >"${plan_path}"

(
  cd "${ROOT_DIR}"
  go run ./cmd/workcell-metadatautil apply-provider-bump-plan "${plan_path}" "${POLICY_PATH}" "${DOCKERFILE_PATH}" "${PROVIDERS_PACKAGE_JSON_PATH}"
)

(
  cd "${PROVIDERS_DIR}"
  "${NPM_BIN}" install --package-lock-only --ignore-scripts --no-audit --no-fund
)

"${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"
"${ROOT_DIR}/scripts/verify-upstream-claude-release.sh"
"${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh"

print_summary
