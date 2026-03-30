#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    BUILDX_BUILDER="${BUILDX_BUILDER-}" \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME=/tmp \
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH-}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_REMOTE_BUILDKIT_LOCAL_CA="${WORKCELL_REMOTE_BUILDKIT_LOCAL_CA-}" \
    WORKCELL_REMOTE_BUILDKIT_SSL_CERTS="${WORKCELL_REMOTE_BUILDKIT_SSL_CERTS-}" \
    WORKCELL_DOCKER_HOST_HOME_ROOT="${WORKCELL_DOCKER_HOST_HOME_ROOT-}" \
    WORKCELL_DOCKER_HOST_WORKSPACE_ROOT="${WORKCELL_DOCKER_HOST_WORKSPACE_ROOT-}" \
    WORKCELL_REPRO_BUILD_MODE="${WORKCELL_REPRO_BUILD_MODE-}" \
    WORKCELL_REPRO_DOCKER_CONTEXT="${WORKCELL_REPRO_DOCKER_CONTEXT-}" \
    WORKCELL_REPRO_MANIFEST_PATH="${WORKCELL_REPRO_MANIFEST_PATH-}" \
    WORKCELL_REPRO_PLATFORMS="${WORKCELL_REPRO_PLATFORMS-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
DOCKER_CONTEXT_NAME="${WORKCELL_REPRO_DOCKER_CONTEXT:-}"
REPRO_PLATFORMS="${WORKCELL_REPRO_PLATFORMS:-linux/amd64,linux/arm64}"
REPRO_BUILD_MODE="${WORKCELL_REPRO_BUILD_MODE:-serial}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
REPRO_MANIFEST_PATH="${WORKCELL_REPRO_MANIFEST_PATH:-}"
OCI_EXPORT_ROOT=""
OCI_EXPORT_A=""
OCI_EXPORT_B=""
MANIFEST_DIGESTS=()
CONFIG_DIGESTS=()
WORKCELL_DOCKER_SANDBOX_ROOT=""

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "verify-reproducible-build-entrypoint-ok"
  exit 0
fi

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

select_docker_context() {
  select_workcell_docker_context "Requested Docker context" "No healthy Docker context found" colima default
}

docker_cmd() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}

if [[ "${1:-}" == "--self-docker-probe" ]]; then
  require_tool docker
  setup_workcell_trusted_docker_client
  if [[ -n "${DOCKER_CONTEXT_NAME:-}" ]]; then
    select_docker_context
  fi
  buildx_cmd version >/dev/null
  echo "verify-reproducible-build-docker-probe-ok"
  exit 0
fi

build_oci_layout() {
  local platforms="$1"
  local dest="$2"
  local build_source_date_epoch="${SOURCE_DATE_EPOCH}"

  rm -rf "${dest}"
  mkdir -p "$(dirname "${dest}")"
  SOURCE_DATE_EPOCH="${build_source_date_epoch}" buildx_cmd build \
    --no-cache \
    --platform "${platforms}" \
    --build-arg "BUILDKIT_MULTI_PLATFORM=1" \
    --build-arg "SOURCE_DATE_EPOCH=${build_source_date_epoch}" \
    --provenance=false \
    --sbom=false \
    --output "type=oci,dest=${dest},tar=false,oci-mediatypes=true,rewrite-timestamp=true" \
    -f "${ROOT_DIR}/runtime/container/Dockerfile" \
    "${ROOT_DIR}" >/dev/null
}

build_oci_layout_pair() {
  local platforms="$1"
  local dest_a="$2"
  local dest_b="$3"
  local pid_a=""
  local pid_b=""
  local status=0

  case "${REPRO_BUILD_MODE}" in
    parallel)
      build_oci_layout "${platforms}" "${dest_a}" &
      pid_a=$!
      build_oci_layout "${platforms}" "${dest_b}" &
      pid_b=$!
      wait "${pid_a}" || status=1
      wait "${pid_b}" || status=1
      return "${status}"
      ;;
    serial)
      build_oci_layout "${platforms}" "${dest_a}"
      build_oci_layout "${platforms}" "${dest_b}"
      ;;
    *)
      echo "Unsupported WORKCELL_REPRO_BUILD_MODE: ${REPRO_BUILD_MODE}" >&2
      exit 2
      ;;
  esac
}

oci_subject_digest() {
  local dir="$1"

  python3 - "${dir}" <<'PY'
import hashlib
import json
import pathlib
import sys

layout_dir = pathlib.Path(sys.argv[1])
index_path = layout_dir / "index.json"
index_bytes = index_path.read_bytes()
index = json.loads(index_bytes)
manifests = index.get("manifests", [])

if not manifests:
    raise SystemExit("OCI export index does not contain any manifests")

if manifests and all("platform" not in entry for entry in manifests):
    if len(manifests) != 1:
        raise SystemExit(
            "Expected a single top-level OCI index wrapper entry for multi-platform export"
        )
    digest = manifests[0].get("digest")
    if not isinstance(digest, str) or not digest.startswith("sha256:"):
        raise SystemExit(f"Malformed wrapped OCI index digest: {digest!r}")
    print(digest)
elif len(manifests) == 1:
    digest = manifests[0].get("digest")
    if not isinstance(digest, str) or not digest.startswith("sha256:"):
        raise SystemExit(f"Malformed OCI subject digest: {digest!r}")
    print(digest)
else:
    def strip_annotations(value):
        if isinstance(value, dict):
            return {
                key: strip_annotations(item)
                for key, item in value.items()
                if key != "annotations"
            }
        if isinstance(value, list):
            return [strip_annotations(item) for item in value]
        return value

    canonical = json.dumps(
        strip_annotations(index),
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")
    print(f"sha256:{hashlib.sha256(canonical).hexdigest()}")
PY
}

manifest_digest() {
  local dir="$1"
  local platform="$2"

  python3 - "${dir}" "${platform}" <<'PY'
import json
import pathlib
import sys

platform_parts = sys.argv[2].split("/")
if len(platform_parts) not in (2, 3):
    raise SystemExit(f"Unsupported platform selector: {sys.argv[2]!r}")

layout_dir = pathlib.Path(sys.argv[1])
with (layout_dir / "index.json").open("r", encoding="utf-8") as handle:
    index = json.load(handle)

manifests = index.get("manifests", [])
if manifests and all("platform" not in entry for entry in manifests):
    if len(manifests) != 1:
        raise SystemExit(
            "Expected a single top-level OCI index wrapper entry for multi-platform export"
        )
    digest = manifests[0].get("digest")
    if not isinstance(digest, str) or not digest.startswith("sha256:"):
        raise SystemExit(f"Malformed wrapped OCI index digest: {digest!r}")
    nested_index_path = layout_dir / "blobs" / "sha256" / digest.split(":", 1)[1]
    with nested_index_path.open("r", encoding="utf-8") as handle:
        index = json.load(handle)
    manifests = index.get("manifests", [])

requested_os = platform_parts[0]
requested_arch = platform_parts[1]
requested_variant = platform_parts[2] if len(platform_parts) == 3 else None
matches = []

for manifest in manifests:
    candidate = manifest.get("platform", {})
    if candidate.get("os") != requested_os:
        continue
    if candidate.get("architecture") != requested_arch:
        continue
    if candidate.get("variant") != requested_variant:
        continue
    matches.append(manifest["digest"])

if len(matches) != 1:
    raise SystemExit(
        f"Expected exactly one manifest for {sys.argv[2]!r}, found {len(matches)}"
    )

print(matches[0])
PY
}

config_digest() {
  local dir="$1"
  local manifest="$2"

  python3 - "${dir}" "${manifest}" <<'PY'
import json
import pathlib
import sys

root = pathlib.Path(sys.argv[1]) / "blobs" / "sha256"
digest = sys.argv[2].split(":", 1)[1]

while True:
    with (root / digest).open("r", encoding="utf-8") as handle:
        manifest = json.load(handle)
    if "config" in manifest:
        print(manifest["config"]["digest"])
        break
    digest = manifest["manifests"][0]["digest"].split(":", 1)[1]
PY
}

cleanup() {
  cleanup_workcell_trusted_docker_client
  rm -rf "${OCI_EXPORT_ROOT}"
}

trap cleanup EXIT

require_tool docker
require_tool python3
require_tool shasum
setup_workcell_trusted_docker_client
select_docker_context
ensure_workcell_selected_builder
buildx_cmd inspect --bootstrap >/dev/null

if [[ -n "${WORKCELL_DOCKER_HOST_WORKSPACE_ROOT:-}" ]]; then
  mkdir -p "${ROOT_DIR}/tmp"
  OCI_EXPORT_ROOT="$(mktemp -d "${ROOT_DIR}/tmp/workcell-repro.XXXXXX")"
else
  OCI_EXPORT_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-repro.XXXXXX")"
fi
IFS=',' read -r -a platform_list <<<"${REPRO_PLATFORMS}"

OCI_EXPORT_A="${OCI_EXPORT_ROOT}/a"
OCI_EXPORT_B="${OCI_EXPORT_ROOT}/b"
build_oci_layout_pair "${REPRO_PLATFORMS}" "${OCI_EXPORT_A}" "${OCI_EXPORT_B}"

subject_digest_a="$(oci_subject_digest "${OCI_EXPORT_A}")"
subject_digest_b="$(oci_subject_digest "${OCI_EXPORT_B}")"
repro_failed=0

for index in "${!platform_list[@]}"; do
  platform="${platform_list[${index}]}"
  manifest_a="$(manifest_digest "${OCI_EXPORT_A}" "${platform}")"
  manifest_b="$(manifest_digest "${OCI_EXPORT_B}" "${platform}")"
  config_a="$(config_digest "${OCI_EXPORT_A}" "${manifest_a}")"
  config_b="$(config_digest "${OCI_EXPORT_B}" "${manifest_b}")"

  if [[ "${manifest_a}" != "${manifest_b}" ]]; then
    echo "Manifest digests (${platform}): ${manifest_a} != ${manifest_b}" >&2
    repro_failed=1
  fi

  if [[ "${config_a}" != "${config_b}" ]]; then
    echo "Config digests (${platform}): ${config_a} != ${config_b}" >&2
    repro_failed=1
  fi

  MANIFEST_DIGESTS[index]="${manifest_a}"
  CONFIG_DIGESTS[index]="${config_a}"
done

if [[ "${subject_digest_a}" != "${subject_digest_b}" ]]; then
  echo "Non-reproducible OCI export subject digest: ${subject_digest_a} != ${subject_digest_b}" >&2
  repro_failed=1
fi

if [[ "${repro_failed}" -ne 0 ]]; then
  exit 1
fi

if [[ -n "${REPRO_MANIFEST_PATH}" ]]; then
  python3 - "${REPRO_MANIFEST_PATH}" "${SOURCE_DATE_EPOCH}" "${subject_digest_a}" "${#platform_list[@]}" "${platform_list[@]}" "${MANIFEST_DIGESTS[@]}" -- "${CONFIG_DIGESTS[@]}" <<'PY'
import json
import pathlib
import sys

manifest_path = pathlib.Path(sys.argv[1])
source_date_epoch = int(sys.argv[2])
oci_subject_digest = sys.argv[3]
count = int(sys.argv[4])
argv = list(sys.argv[5:])

platforms = argv[:count]
argv = argv[count:]

def take_until_separator(items):
    values = []
    while items and items[0] != "--":
        values.append(items.pop(0))
    if not items:
        raise SystemExit("Malformed reproducibility manifest arguments")
    items.pop(0)
    return values

manifests = take_until_separator(argv)
configs = argv

if not (len(platforms) == len(manifests) == len(configs) == count):
    raise SystemExit("Reproducibility manifest argument lengths do not match")

manifest = {
    "oci_subject_digest": oci_subject_digest,
    "source_date_epoch": source_date_epoch,
    "platforms": {
        platform: {
            "image_manifest_digest": manifest_digest,
            "config_digest": config_digest,
        }
        for platform, manifest_digest, config_digest in zip(
            platforms, manifests, configs, strict=True
        )
    },
}

manifest_path.parent.mkdir(parents=True, exist_ok=True)
manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
fi

echo "Workcell reproducible build verification passed."
