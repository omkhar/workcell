#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKER_CONTEXT_NAME="${WORKCELL_REPRO_DOCKER_CONTEXT:-}"
REPRO_PLATFORMS="${WORKCELL_REPRO_PLATFORMS:-linux/amd64,linux/arm64}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
REPRO_MANIFEST_PATH="${WORKCELL_REPRO_MANIFEST_PATH:-}"
OCI_EXPORT_ROOT=""
OCI_EXPORT_A=()
OCI_EXPORT_B=()
LAYOUT_DIGESTS=()
MANIFEST_DIGESTS=()
CONFIG_DIGESTS=()

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

select_docker_context() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    return
  fi

  if docker context inspect colima >/dev/null 2>&1; then
    DOCKER_CONTEXT_NAME="colima"
    return
  fi

  if docker context inspect default >/dev/null 2>&1; then
    DOCKER_CONTEXT_NAME="default"
  fi
}

docker_cmd() {
  if [[ -n "${DOCKER_CONTEXT_NAME}" ]]; then
    docker --context "${DOCKER_CONTEXT_NAME}" "$@"
  else
    docker "$@"
  fi
}

platform_slug() {
  printf '%s\n' "$1" | tr '/,' '__'
}

build_oci_layout() {
  local platform="$1"
  local dest="$2"
  local build_source_date_epoch="${SOURCE_DATE_EPOCH}"

  rm -rf "${dest}"
  SOURCE_DATE_EPOCH="${build_source_date_epoch}" docker_cmd buildx build \
    --no-cache \
    --platform "${platform}" \
    --build-arg "BUILDKIT_MULTI_PLATFORM=1" \
    --build-arg "SOURCE_DATE_EPOCH=${build_source_date_epoch}" \
    --provenance=false \
    --sbom=false \
    --output "type=oci,dest=${dest},tar=false,oci-mediatypes=true,rewrite-timestamp=true" \
    -f "${ROOT_DIR}/runtime/container/Dockerfile" \
    "${ROOT_DIR}" >/dev/null
}

layout_digest() {
  local dir="$1"

  (
    cd "${dir}"
    find . -type f | LC_ALL=C sort | while IFS= read -r file; do
      shasum -a 256 "${file}"
    done
  ) | shasum -a 256 | awk '{print $1}'
}

manifest_digest() {
  local dir="$1"

  python3 - "${dir}/index.json" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as handle:
    index = json.load(handle)
print(index["manifests"][0]["digest"])
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
  rm -rf "${OCI_EXPORT_ROOT}"
}

trap cleanup EXIT

require_tool docker
require_tool python3
require_tool shasum
select_docker_context
docker_cmd buildx inspect --bootstrap >/dev/null

OCI_EXPORT_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/workcell-repro.XXXXXX")"
IFS=',' read -r -a platform_list <<<"${REPRO_PLATFORMS}"
for index in "${!platform_list[@]}"; do
  platform="${platform_list[${index}]}"
  slug="$(platform_slug "${platform}")"
  OCI_EXPORT_A[index]="${OCI_EXPORT_ROOT}/a-${slug}"
  OCI_EXPORT_B[index]="${OCI_EXPORT_ROOT}/b-${slug}"
  build_oci_layout "${platform}" "${OCI_EXPORT_A[${index}]}"
  build_oci_layout "${platform}" "${OCI_EXPORT_B[${index}]}"
done

for index in "${!platform_list[@]}"; do
  platform="${platform_list[${index}]}"
  digest_a="$(layout_digest "${OCI_EXPORT_A[${index}]}")"
  digest_b="$(layout_digest "${OCI_EXPORT_B[${index}]}")"
  manifest_a="$(manifest_digest "${OCI_EXPORT_A[${index}]}")"
  manifest_b="$(manifest_digest "${OCI_EXPORT_B[${index}]}")"
  config_a="$(config_digest "${OCI_EXPORT_A[${index}]}" "${manifest_a}")"
  config_b="$(config_digest "${OCI_EXPORT_B[${index}]}" "${manifest_b}")"

  if [[ "${digest_a}" != "${digest_b}" ]]; then
    echo "Non-reproducible OCI export for ${platform}: ${digest_a} != ${digest_b}" >&2
    echo "Manifest digests (${platform}): ${manifest_a} != ${manifest_b}" >&2
    echo "Config digests (${platform}): ${config_a} != ${config_b}" >&2
    exit 1
  fi

  LAYOUT_DIGESTS[index]="${digest_a}"
  MANIFEST_DIGESTS[index]="${manifest_a}"
  CONFIG_DIGESTS[index]="${config_a}"
done

if [[ -n "${REPRO_MANIFEST_PATH}" ]]; then
  python3 - "${REPRO_MANIFEST_PATH}" "${SOURCE_DATE_EPOCH}" "${#platform_list[@]}" "${platform_list[@]}" "${LAYOUT_DIGESTS[@]}" -- "${MANIFEST_DIGESTS[@]}" -- "${CONFIG_DIGESTS[@]}" <<'PY'
import json
import pathlib
import sys

manifest_path = pathlib.Path(sys.argv[1])
source_date_epoch = int(sys.argv[2])
count = int(sys.argv[3])
argv = list(sys.argv[4:])

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

layouts = take_until_separator(argv)
manifests = take_until_separator(argv)
configs = argv

if not (len(platforms) == len(layouts) == len(manifests) == len(configs) == count):
    raise SystemExit("Reproducibility manifest argument lengths do not match")

manifest = {
    "source_date_epoch": source_date_epoch,
    "platforms": {
        platform: {
            "layout_sha256": layout,
            "image_manifest_digest": manifest_digest,
            "config_digest": config_digest,
        }
        for platform, layout, manifest_digest, config_digest in zip(
            platforms, layouts, manifests, configs, strict=True
        )
    },
}

manifest_path.parent.mkdir(parents=True, exist_ok=True)
manifest_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
fi

echo "Workcell reproducible build verification passed."
