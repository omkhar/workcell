#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

OUTPUT_PATH="${1:-}"
BUILDKIT_IMAGE="${WORKCELL_BUILDKIT_IMAGE:-}"
BUILDX_VERSION_TARGET="${WORKCELL_BUILDX_VERSION:-}"
COSIGN_VERSION_TARGET="${WORKCELL_COSIGN_VERSION:-}"
QEMU_IMAGE="${WORKCELL_QEMU_IMAGE:-}"
SYFT_VERSION_TARGET="${WORKCELL_SYFT_VERSION:-}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

[[ -n "${OUTPUT_PATH}" ]] || {
  echo "usage: $0 OUTPUT_PATH" >&2
  exit 64
}

require_tool docker
require_tool python3

docker_version_json="$(docker version --format '{{json .}}')"
buildx_version="$(docker buildx version)"
buildx_inspect="$(docker buildx inspect --bootstrap)"
cosign_version=""
curl_version="$(curl --version 2>/dev/null | head -n1 || true)"
gzip_version="$(gzip --version 2>/dev/null | head -n1 || true)"
git_version="$(git --version 2>/dev/null || true)"
python_version="$(python3 --version 2>/dev/null || true)"
qemu_version=""
syft_version=""
tar_version="$(tar --version 2>/dev/null | head -n1 || true)"

if command -v cosign >/dev/null 2>&1; then
  cosign_version="$(cosign version 2>/dev/null || true)"
fi

if [[ -n "${QEMU_IMAGE}" ]]; then
  qemu_version="$(docker run --privileged --rm "${QEMU_IMAGE}" --version)"
fi

if command -v syft >/dev/null 2>&1; then
  syft_version="$(syft version 2>/dev/null || true)"
fi

python3 - "${OUTPUT_PATH}" "${BUILDKIT_IMAGE}" "${BUILDX_VERSION_TARGET}" "${COSIGN_VERSION_TARGET}" "${QEMU_IMAGE}" "${SYFT_VERSION_TARGET}" "${buildx_version}" "${buildx_inspect}" "${docker_version_json}" "${qemu_version}" "${cosign_version}" "${curl_version}" "${git_version}" "${gzip_version}" "${python_version}" "${syft_version}" "${tar_version}" <<'PY'
import json
import pathlib
import sys

output_path = pathlib.Path(sys.argv[1])
buildkit_image = sys.argv[2]
buildx_version_target = sys.argv[3]
cosign_version_target = sys.argv[4]
qemu_image = sys.argv[5]
syft_version_target = sys.argv[6]
buildx_version = sys.argv[7]
buildx_inspect = sys.argv[8]
docker_version = json.loads(sys.argv[9])
qemu_version = sys.argv[10]
cosign_version = sys.argv[11]
curl_version = sys.argv[12]
git_version = sys.argv[13]
gzip_version = sys.argv[14]
python_version = sys.argv[15]
syft_version = sys.argv[16]
tar_version = sys.argv[17]

manifest = {
    "schema_version": 1,
    "builder": {
        "buildkit_image": buildkit_image,
        "buildx_inspect": buildx_inspect,
        "buildx_version_target": buildx_version_target,
        "buildx_version": buildx_version,
        "cosign_version_target": cosign_version_target,
        "cosign_version": cosign_version,
        "curl_version": curl_version,
        "docker_version": docker_version,
        "git_version": git_version,
        "gzip_version": gzip_version,
        "python_version": python_version,
        "syft_version_target": syft_version_target,
        "syft_version": syft_version,
        "tar_version": tar_version,
    },
}

if qemu_image:
    manifest["builder"]["qemu"] = {
        "image": qemu_image,
        "version": qemu_version,
    }

output_path.parent.mkdir(parents=True, exist_ok=True)
output_path.write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n", encoding="utf-8")
PY
