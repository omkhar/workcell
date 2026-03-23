#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

usage() {
  cat <<'EOF' >&2
Usage: publish-github-release.sh TAG FILE...
EOF
  exit 2
}

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

[[ $# -ge 2 ]] || usage

TAG_NAME="$1"
shift

[[ -n "${GITHUB_TOKEN:-}" ]] || {
  echo "GITHUB_TOKEN is required" >&2
  exit 1
}

[[ -n "${GITHUB_REPOSITORY:-}" ]] || {
  echo "GITHUB_REPOSITORY is required" >&2
  exit 1
}

require_tool curl
require_tool python3

for path in "$@"; do
  [[ -f "${path}" ]] || {
    echo "Missing release asset: ${path}" >&2
    exit 1
  }
done

api() {
  local method="$1"
  local url="$2"
  local body_path="${3:-}"
  local response_path="$4"
  local status

  if [[ -n "${body_path}" ]]; then
    status="$(curl -sS \
      -o "${response_path}" \
      -w '%{http_code}' \
      -X "${method}" \
      -H "Authorization: Bearer ${GITHUB_TOKEN}" \
      -H 'Accept: application/vnd.github+json' \
      -H 'X-GitHub-Api-Version: 2022-11-28' \
      -H 'Content-Type: application/json' \
      --data @"${body_path}" \
      "${url}")"
  else
    status="$(curl -sS \
      -o "${response_path}" \
      -w '%{http_code}' \
      -X "${method}" \
      -H "Authorization: Bearer ${GITHUB_TOKEN}" \
      -H 'Accept: application/vnd.github+json' \
      -H 'X-GitHub-Api-Version: 2022-11-28' \
      "${url}")"
  fi

  printf '%s\n' "${status}"
}

TMP_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-release-api.XXXXXX")"

cleanup() {
  rm -rf "${TMP_ROOT}"
}

trap cleanup EXIT

release_url="https://api.github.com/repos/${GITHUB_REPOSITORY}/releases/tags/${TAG_NAME}"
release_json="${TMP_ROOT}/release.json"
create_payload="${TMP_ROOT}/create.json"

status="$(api GET "${release_url}" "" "${release_json}")"
case "${status}" in
  200) ;;
  404)
    python3 - "${TAG_NAME}" "${create_payload}" <<'PY'
import json
import pathlib
import sys

payload = {
    "tag_name": sys.argv[1],
    "generate_release_notes": True,
}
pathlib.Path(sys.argv[2]).write_text(
    json.dumps(payload, separators=(",", ":")),
    encoding="utf-8",
)
PY
    status="$(api POST "https://api.github.com/repos/${GITHUB_REPOSITORY}/releases" "${create_payload}" "${release_json}")"
    [[ "${status}" == "201" ]] || {
      echo "GitHub release creation failed with status ${status}" >&2
      cat "${release_json}" >&2
      exit 1
    }
    ;;
  *)
    echo "GitHub release lookup failed with status ${status}" >&2
    cat "${release_json}" >&2
    exit 1
    ;;
esac

python3 - "${release_json}" "$@" >"${TMP_ROOT}/release-metadata.txt" <<'PY'
import json
import pathlib
import sys

release = json.loads(pathlib.Path(sys.argv[1]).read_text(encoding="utf-8"))
upload_url = release["upload_url"].split("{", 1)[0]
print(release["id"])
print(upload_url)

assets = {asset["name"]: asset["id"] for asset in release.get("assets", [])}
for file_path in sys.argv[2:]:
    print(json.dumps({"name": pathlib.Path(file_path).name, "id": assets.get(pathlib.Path(file_path).name)}))
PY

mapfile -t release_metadata <"${TMP_ROOT}/release-metadata.txt"
release_id="${release_metadata[0]}"
upload_url="${release_metadata[1]}"

for ((i = 2; i < ${#release_metadata[@]}; i++)); do
  asset_record="${release_metadata[i]}"
  asset_name="$(
    python3 - "${asset_record}" <<'PY'
import json
import sys

record = json.loads(sys.argv[1])
print(record["name"])
PY
  )"
  asset_id="$(
    python3 - "${asset_record}" <<'PY'
import json
import sys

record = json.loads(sys.argv[1])
print("" if record["id"] is None else record["id"])
PY
  )"

  if [[ -n "${asset_id}" ]]; then
    delete_status="$(api DELETE "https://api.github.com/repos/${GITHUB_REPOSITORY}/releases/assets/${asset_id}" "" "${TMP_ROOT}/delete-${asset_id}.json")"
    [[ "${delete_status}" == "204" ]] || {
      echo "Failed to delete existing GitHub release asset ${asset_name} (${asset_id})" >&2
      cat "${TMP_ROOT}/delete-${asset_id}.json" >&2
      exit 1
    }
  fi
done

for path in "$@"; do
  asset_name="$(basename "${path}")"
  encoded_name="$(
    python3 - "${asset_name}" <<'PY'
import sys
import urllib.parse

print(urllib.parse.quote(sys.argv[1], safe=""))
PY
  )"
  upload_response="${TMP_ROOT}/upload-${asset_name}.json"
  upload_status="$(curl -sS \
    -o "${upload_response}" \
    -w '%{http_code}' \
    -X POST \
    -H "Authorization: Bearer ${GITHUB_TOKEN}" \
    -H 'Accept: application/vnd.github+json' \
    -H 'X-GitHub-Api-Version: 2022-11-28' \
    -H 'Content-Type: application/octet-stream' \
    --data-binary @"${path}" \
    "${upload_url}?name=${encoded_name}")"
  [[ "${upload_status}" == "201" ]] || {
    echo "Failed to upload GitHub release asset ${asset_name}" >&2
    cat "${upload_response}" >&2
    exit 1
  }
done

printf 'Published GitHub release assets for %s (release id %s)\n' "${TAG_NAME}" "${release_id}"
