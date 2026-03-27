#!/bin/bash -p
readonly TRUSTED_HOST_PATH="/Applications/Codex.app/Contents/Resources:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/opt/homebrew/sbin:/usr/local/sbin:/usr/sbin:/sbin:/Applications/Docker.app/Contents/Resources/bin"
if [[ "${WORKCELL_SANITIZED_ENTRYPOINT:-0}" != "1" ]]; then
  exec /usr/bin/env -i \
    PATH="${TRUSTED_HOST_PATH}" \
    HOME="${HOME:-/tmp}" \
    TMPDIR="${TMPDIR:-/tmp}" \
    WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS="${WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS-}" \
    WORKCELL_SANITIZED_ENTRYPOINT=1 \
    /bin/bash -p "$0" "$@"
fi
set -euo pipefail
export PATH="${TRUSTED_HOST_PATH}"

if [[ "${1:-}" == "--self-entrypoint-probe" ]]; then
  head -n 1 "$0" >/dev/null
  echo "check-pinned-inputs-entrypoint-ok"
  exit 0
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
VALIDATOR_DOCKERFILE_PATH="${ROOT_DIR}/tools/validator/Dockerfile"
REMOTE_VALIDATOR_DOCKERFILE_PATH="${ROOT_DIR}/tools/remote-validator/Dockerfile"
PROVIDERS_PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers/package.json"
PROVIDERS_PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers/package-lock.json"
WORKFLOWS_DIR="${ROOT_DIR}/.github/workflows"
CI_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/ci.yml"
RELEASE_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/release.yml"
CODEOWNERS_PATH="${ROOT_DIR}/.github/CODEOWNERS"
CODEX_REQUIREMENTS_PATH="${ROOT_DIR}/adapters/codex/requirements.toml"
CODEX_MCP_CONFIG_PATH="${ROOT_DIR}/adapters/codex/mcp/config.toml"
HOSTED_CONTROLS_POLICY_PATH="${ROOT_DIR}/policy/github-hosted-controls.toml"
MAX_DEBIAN_SNAPSHOT_AGE_DAYS="${WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS:-45}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool python3

python3 - "${DOCKERFILE_PATH}" "${VALIDATOR_DOCKERFILE_PATH}" "${REMOTE_VALIDATOR_DOCKERFILE_PATH}" "${PROVIDERS_PACKAGE_JSON_PATH}" "${PROVIDERS_PACKAGE_LOCK_PATH}" "${WORKFLOWS_DIR}" "${CI_WORKFLOW_PATH}" "${RELEASE_WORKFLOW_PATH}" "${CODEOWNERS_PATH}" "${CODEX_REQUIREMENTS_PATH}" "${CODEX_MCP_CONFIG_PATH}" "${HOSTED_CONTROLS_POLICY_PATH}" "${MAX_DEBIAN_SNAPSHOT_AGE_DAYS}" <<'PY'
import datetime as dt
import json
import pathlib
import re
import sys
import tomllib

runtime_dockerfile = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
validator_dockerfile = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")
remote_validator_dockerfile = pathlib.Path(sys.argv[3]).read_text(encoding="utf-8")
providers_package_json = json.loads(pathlib.Path(sys.argv[4]).read_text(encoding="utf-8"))
providers_package_lock = json.loads(pathlib.Path(sys.argv[5]).read_text(encoding="utf-8"))
workflows_dir = pathlib.Path(sys.argv[6])
ci_workflow = pathlib.Path(sys.argv[7]).read_text(encoding="utf-8")
release_workflow = pathlib.Path(sys.argv[8]).read_text(encoding="utf-8")
codeowners = pathlib.Path(sys.argv[9]).read_text(encoding="utf-8")
codex_requirements = tomllib.loads(pathlib.Path(sys.argv[10]).read_text(encoding="utf-8"))
codex_mcp_config = tomllib.loads(pathlib.Path(sys.argv[11]).read_text(encoding="utf-8"))
hosted_controls_policy = tomllib.loads(pathlib.Path(sys.argv[12]).read_text(encoding="utf-8"))
max_snapshot_age_days = int(sys.argv[13])

def require_arg(text: str, name: str, path: str) -> str:
    match = re.search(rf"^ARG {re.escape(name)}=(.+)$", text, re.MULTILINE)
    if not match:
        raise SystemExit(f"Unable to extract {name} from {path}")
    return match.group(1).strip()

def require_yaml_key(text: str, name: str, path: str) -> str:
    match = re.search(rf"^\s*{re.escape(name)}:\s*(.+)$", text, re.MULTILINE)
    if not match:
        raise SystemExit(f"Unable to extract {name} from {path}")
    return match.group(1).strip()

def require_pinned_base_image(image: str, label: str, path: str) -> None:
    if not re.match(r"^[^@]+@sha256:[0-9a-f]{64}$", image):
        raise SystemExit(f"{label} in {path} must be pinned by immutable digest, found {image!r}")

def verify_snapshot_freshness(snapshot: str, path: str) -> None:
    snapshot_time = dt.datetime.strptime(snapshot, "%Y%m%dT%H%M%SZ").replace(tzinfo=dt.timezone.utc)
    now = dt.datetime.now(dt.timezone.utc)
    snapshot_age_days = (now - snapshot_time).days
    if snapshot_age_days > max_snapshot_age_days:
        raise SystemExit(
            f"Debian snapshot {snapshot} in {path} is {snapshot_age_days} days old; "
            f"refresh it or raise WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS"
        )

def extract_install_blocks(text: str, path: str) -> list[list[str]]:
    blocks = []
    for match in re.finditer(
        r"apt-get install -y --no-install-recommends(?P<body>.*?)(?:&&)",
        text,
        re.DOTALL,
    ):
        body = match.group("body").replace("\\", " ")
        packages = [token for token in body.split() if token]
        if not packages:
            raise SystemExit(f"Unable to extract package list from install block in {path}")
        blocks.append(packages)
    if not blocks:
        raise SystemExit(f"Unable to find apt install blocks in {path}")
    return blocks

def require_exact_packages(actual: list[str], expected: list[str], label: str, path: str) -> None:
    if actual != expected:
        raise SystemExit(
            f"{label} package set in {path} changed.\n"
            f"expected: {expected}\n"
            f"actual:   {actual}"
        )

def require_regex(text: str, pattern: str, label: str, path: str) -> re.Match[str]:
    match = re.search(pattern, text, re.MULTILINE)
    if not match:
        raise SystemExit(f"{label} in {path} must match {pattern!r}")
    return match

def require_contains(text: str, needle: str, label: str, path: str) -> None:
    if needle not in text:
        raise SystemExit(f"{path} must contain {label}: {needle!r}")

def require_not_regex(text: str, pattern: str, label: str, path: str) -> None:
    if re.search(pattern, text, re.MULTILINE):
        raise SystemExit(f"{path} must not contain {label} matching {pattern!r}")

def walk_strings(value):
    if isinstance(value, str):
        yield value
        return
    if isinstance(value, dict):
        for nested in value.values():
            yield from walk_strings(nested)
        return
    if isinstance(value, list):
        for nested in value:
            yield from walk_strings(nested)

def extract_repro_matrix_entries(strategy_block: str, path: str) -> list[tuple[str, str, str]]:
    entries = re.findall(
        r"^\s{10}- platform:\s*(\S+)\n"
        r"^\s{12}platform_name:\s*(\S+)\n"
        r"^\s{12}runner:\s*(\S+)$",
        strategy_block,
        re.MULTILINE,
    )
    if not entries:
        raise SystemExit(
            f"Unable to extract reproducible-build matrix entries from {path}"
        )
    return entries

def require_no_registry_bootstrap_mcp(config: dict, path: str) -> None:
    disallowed_fragments = (
        "npx",
        "npm exec",
        "pnpm dlx",
        "yarn dlx",
        "bunx",
        "@upstash/context7-mcp",
        "exa-mcp-server",
    )
    for value in walk_strings(config.get("mcp_servers", {})):
        lowered = value.lower()
        for fragment in disallowed_fragments:
            if fragment in lowered:
                raise SystemExit(
                    f"{path} must not seed mutable registry-backed MCP commands; found {value!r}"
                )

runtime_base_image = require_arg(runtime_dockerfile, "NODE_BASE_IMAGE", "runtime/container/Dockerfile")
validator_base_image = require_arg(validator_dockerfile, "VALIDATOR_BASE_IMAGE", "tools/validator/Dockerfile")
remote_validator_base_image = require_arg(
    remote_validator_dockerfile,
    "VALIDATOR_BASE_IMAGE",
    "tools/remote-validator/Dockerfile",
)
runtime_snapshot = require_arg(runtime_dockerfile, "DEBIAN_SNAPSHOT", "runtime/container/Dockerfile")
validator_snapshot = require_arg(validator_dockerfile, "DEBIAN_SNAPSHOT", "tools/validator/Dockerfile")
remote_validator_snapshot = require_arg(
    remote_validator_dockerfile,
    "DEBIAN_SNAPSHOT",
    "tools/remote-validator/Dockerfile",
)
codex_version = require_arg(runtime_dockerfile, "CODEX_VERSION", "runtime/container/Dockerfile")
runtime_install_blocks = extract_install_blocks(runtime_dockerfile, "runtime/container/Dockerfile")
validator_install_blocks = extract_install_blocks(validator_dockerfile, "tools/validator/Dockerfile")
remote_validator_install_blocks = extract_install_blocks(
    remote_validator_dockerfile,
    "tools/remote-validator/Dockerfile",
)

require_pinned_base_image(runtime_base_image, "NODE_BASE_IMAGE", "runtime/container/Dockerfile")
require_pinned_base_image(validator_base_image, "VALIDATOR_BASE_IMAGE", "tools/validator/Dockerfile")
require_pinned_base_image(
    remote_validator_base_image,
    "VALIDATOR_BASE_IMAGE",
    "tools/remote-validator/Dockerfile",
)
verify_snapshot_freshness(runtime_snapshot, "runtime/container/Dockerfile")
verify_snapshot_freshness(validator_snapshot, "tools/validator/Dockerfile")
verify_snapshot_freshness(remote_validator_snapshot, "tools/remote-validator/Dockerfile")
require_no_registry_bootstrap_mcp(codex_requirements, "adapters/codex/requirements.toml")
require_no_registry_bootstrap_mcp(codex_mcp_config, "adapters/codex/mcp/config.toml")
require_regex(
    runtime_dockerfile,
    r'curl -fsSL "https://github\.com/openai/codex/releases/download/rust-v\$\{CODEX_VERSION\}/codex-\$\{CODEX_ARCH\}\.tar\.gz"',
    "Codex release download URL",
    "runtime/container/Dockerfile",
)
if require_regex(
    runtime_dockerfile,
    r'arm64\)\s+\\\s*CODEX_ARCH="([^"]+)";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";',
    "arm64 Codex mapping",
    "runtime/container/Dockerfile",
).group(1) != "aarch64-unknown-linux-gnu":
    raise SystemExit("arm64 Codex mapping in runtime/container/Dockerfile must use aarch64-unknown-linux-gnu")
if require_regex(
    runtime_dockerfile,
    r'amd64\)\s+\\\s*CODEX_ARCH="([^"]+)";\s+\\\s*CODEX_SHA256="([0-9a-f]{64})";',
    "amd64 Codex mapping",
    "runtime/container/Dockerfile",
).group(1) != "x86_64-unknown-linux-gnu":
    raise SystemExit("amd64 Codex mapping in runtime/container/Dockerfile must use x86_64-unknown-linux-gnu")
if len(runtime_install_blocks) != 2:
    raise SystemExit(
        "runtime/container/Dockerfile must contain exactly two apt install blocks "
        "(runtime base and runtime builder)"
    )
if len(validator_install_blocks) != 1:
    raise SystemExit("tools/validator/Dockerfile must contain exactly one apt install block")
if len(remote_validator_install_blocks) != 1:
    raise SystemExit("tools/remote-validator/Dockerfile must contain exactly one apt install block")
require_exact_packages(
    runtime_install_blocks[0],
    [
        "bash",
        "bubblewrap",
        "ca-certificates",
        "curl",
        "fd-find",
        "git",
        "jq",
        "less",
        "openssh-client",
        "passwd",
        "procps",
        "ripgrep",
        "sudo",
        "unzip",
        "util-linux",
        "xz-utils",
    ],
    "Runtime base",
    "runtime/container/Dockerfile",
)
require_exact_packages(
    runtime_install_blocks[1],
    [
        "cargo",
        "rustc",
    ],
    "Runtime builder",
    "runtime/container/Dockerfile",
)
require_exact_packages(
    validator_install_blocks[0],
    [
        "codespell",
        "cargo",
        "git",
        "groff-base",
        "llvm",
        "mandoc",
        "python3",
        "python3-coverage",
        "rustc",
        "rust-clippy",
        "rustfmt",
        "shellcheck",
        "shfmt",
        "yamllint",
    ],
    "Validator",
    "tools/validator/Dockerfile",
)
require_exact_packages(
    remote_validator_install_blocks[0],
    [
        "codespell",
        "cargo",
        "docker-cli",
        "docker-buildx",
        "git",
        "groff-base",
        "llvm",
        "mandoc",
        "python3",
        "python3-coverage",
        "rustc",
        "rust-clippy",
        "rustfmt",
        "shellcheck",
        "shfmt",
        "yamllint",
    ],
    "Remote validator",
    "tools/remote-validator/Dockerfile",
)

root_package = providers_package_lock.get("packages", {}).get("", {})
expected_dependencies = providers_package_json.get("dependencies", {})
actual_dependencies = root_package.get("dependencies", {})
if actual_dependencies != expected_dependencies:
    raise SystemExit(
        "runtime/container/providers/package-lock.json root dependencies do not match package.json"
    )

for package_name in expected_dependencies:
    package_entry = providers_package_lock.get("packages", {}).get(f"node_modules/{package_name}")
    if not package_entry:
        raise SystemExit(f"Missing pinned provider package entry for {package_name}")
    if package_entry.get("version") != expected_dependencies[package_name]:
        raise SystemExit(
            f"Pinned provider package {package_name} is {package_entry.get('version')}, expected {expected_dependencies[package_name]}"
        )
    if not package_entry.get("integrity"):
        raise SystemExit(f"Pinned provider package {package_name} is missing an integrity hash")
    resolved = package_entry.get("resolved", "")
    if not resolved.startswith("https://registry.npmjs.org/"):
        raise SystemExit(f"Pinned provider package {package_name} uses an unexpected source: {resolved!r}")

for package_path, package_entry in providers_package_lock.get("packages", {}).items():
    if package_path == "":
        continue
    if package_entry.get("link") is True:
        raise SystemExit(f"Linked npm dependencies are not allowed in the provider lockfile: {package_path}")
    if not package_entry.get("integrity"):
        raise SystemExit(f"Provider lockfile entry is missing integrity data: {package_path}")
    resolved = package_entry.get("resolved", "")
    if not resolved.startswith("https://registry.npmjs.org/"):
        raise SystemExit(f"Provider lockfile entry uses an unexpected source ({package_path}): {resolved!r}")

ci_buildx_version = require_yaml_key(ci_workflow, "WORKCELL_BUILDX_VERSION", ".github/workflows/ci.yml")
release_buildx_version = require_yaml_key(release_workflow, "WORKCELL_BUILDX_VERSION", ".github/workflows/release.yml")
if ci_buildx_version != release_buildx_version:
    raise SystemExit(
        "WORKCELL_BUILDX_VERSION must match between .github/workflows/ci.yml and .github/workflows/release.yml"
    )
if not re.match(r"^v\d+\.\d+\.\d+$", ci_buildx_version):
    raise SystemExit(
        f"WORKCELL_BUILDX_VERSION must be an exact pinned release (for example v0.32.1), found {ci_buildx_version!r}"
    )

ci_qemu_image = require_yaml_key(ci_workflow, "WORKCELL_QEMU_IMAGE", ".github/workflows/ci.yml")
release_qemu_image = require_yaml_key(release_workflow, "WORKCELL_QEMU_IMAGE", ".github/workflows/release.yml")
if ci_qemu_image != release_qemu_image:
    raise SystemExit(
        "WORKCELL_QEMU_IMAGE must match between .github/workflows/ci.yml and .github/workflows/release.yml"
    )
require_pinned_base_image(ci_qemu_image, "WORKCELL_QEMU_IMAGE", ".github/workflows/ci.yml")
ci_buildkit_image = require_yaml_key(ci_workflow, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/ci.yml")
release_buildkit_image = require_yaml_key(release_workflow, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/release.yml")
if ci_buildkit_image != release_buildkit_image:
    raise SystemExit(
        "WORKCELL_BUILDKIT_IMAGE must match between .github/workflows/ci.yml and .github/workflows/release.yml"
    )
require_pinned_base_image(ci_buildkit_image, "WORKCELL_BUILDKIT_IMAGE", ".github/workflows/ci.yml")
ci_cosign_version = require_yaml_key(ci_workflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/ci.yml")
release_cosign_version = require_yaml_key(release_workflow, "WORKCELL_COSIGN_VERSION", ".github/workflows/release.yml")
if ci_cosign_version != release_cosign_version:
    raise SystemExit(
        "WORKCELL_COSIGN_VERSION must match between .github/workflows/ci.yml and .github/workflows/release.yml"
    )
if not re.match(r"^v\d+\.\d+\.\d+$", ci_cosign_version):
    raise SystemExit(
        f"WORKCELL_COSIGN_VERSION must be an exact pinned release, found {ci_cosign_version!r}"
    )
if f"cosign-release: ${{{{ env.WORKCELL_COSIGN_VERSION }}}}" not in ci_workflow:
    raise SystemExit(".github/workflows/ci.yml must pin the installed cosign binary release")
if f"cosign-release: ${{{{ env.WORKCELL_COSIGN_VERSION }}}}" not in release_workflow:
    raise SystemExit(".github/workflows/release.yml must pin the installed cosign binary release")
if "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}" not in ci_workflow:
    raise SystemExit(".github/workflows/ci.yml must pin the BuildKit daemon image used by setup-buildx-action")
if "driver-opts: image=${{ env.WORKCELL_BUILDKIT_IMAGE }}" not in release_workflow:
    raise SystemExit(".github/workflows/release.yml must pin the BuildKit daemon image used by setup-buildx-action")
if "cache-binary: true" not in ci_workflow:
    raise SystemExit("Pinned buildx binary caching must stay enabled in .github/workflows/ci.yml")
ci_repro_build_job = require_regex(
    ci_workflow,
    r"^  reproducible-build-platform:\n(?P<body>[\s\S]*?)(?=^  reproducible-build:\n)",
    "reproducible-build-platform job",
    ".github/workflows/ci.yml",
).group("body")
if not re.search(r"^\s{4}runs-on:\s*\$\{\{\s*matrix\.runner\s*\}\}$", ci_repro_build_job, re.MULTILINE):
    raise SystemExit(
        ".github/workflows/ci.yml must route reproducible-build-platform through runs-on: ${{ matrix.runner }}"
    )
ci_repro_strategy_block = require_regex(
    ci_repro_build_job,
    r"^    strategy:\n(?P<body>[\s\S]*?)(?=^    steps:\n)",
    "reproducible-build-platform strategy block",
    ".github/workflows/ci.yml",
).group(0)
expected_ci_repro_strategy_block = (
    "    strategy:\n"
    "      fail-fast: false\n"
    "      matrix:\n"
    "        include:\n"
    "          - platform: linux/amd64\n"
    "            platform_name: amd64\n"
    "            runner: ubuntu-latest\n"
    "          - platform: linux/arm64\n"
    "            platform_name: arm64\n"
    "            runner: ubuntu-24.04-arm\n"
)
if ci_repro_strategy_block != expected_ci_repro_strategy_block:
    raise SystemExit(
        ".github/workflows/ci.yml must keep the reviewed reproducible-build matrix structure, including a single native ubuntu-24.04-arm lane for linux/arm64"
    )
ci_repro_matrix_entries = extract_repro_matrix_entries(
    ci_repro_strategy_block,
    ".github/workflows/ci.yml",
)
arm64_entries = [
    entry for entry in ci_repro_matrix_entries
    if entry[0] == "linux/arm64"
]
if arm64_entries != [("linux/arm64", "arm64", "ubuntu-24.04-arm")]:
    raise SystemExit(
        ".github/workflows/ci.yml must define exactly one linux/arm64 reproducible-build matrix entry and it must use runner ubuntu-24.04-arm"
    )
if "docker/setup-qemu-action@" in ci_workflow:
    raise SystemExit(".github/workflows/ci.yml must not configure QEMU in CI now that arm64 reproducible builds use a native runner")
if "cache-binary: false" not in release_workflow:
    raise SystemExit("The publishing release workflow must not cache the Buildx binary")
if "cache-image: false" not in release_workflow:
    raise SystemExit("The publishing release workflow must not cache the QEMU helper image")
release_syft_version = require_yaml_key(release_workflow, "WORKCELL_SYFT_VERSION", ".github/workflows/release.yml")
if not re.match(r"^v\d+\.\d+\.\d+$", release_syft_version):
    raise SystemExit(
        f"WORKCELL_SYFT_VERSION must be an exact pinned release, found {release_syft_version!r}"
    )
if "syft-version: ${{ env.WORKCELL_SYFT_VERSION }}" not in release_workflow:
    raise SystemExit(".github/workflows/release.yml must pin the Syft version used for release SBOM generation")
if "anchore/sbom-action/download-syft@" not in release_workflow:
    raise SystemExit(".github/workflows/release.yml must install the pinned Syft CLI before generating the builder environment manifest")
if "docker buildx imagetools create" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must assemble the published multi-arch manifest with docker buildx imagetools create"
    )
if re.search(
    r"docker/build-push-action@.*?platforms:\s*linux/amd64,linux/arm64",
    release_workflow,
    re.DOTALL,
):
    raise SystemExit(
        ".github/workflows/release.yml must not publish the final multi-arch image through one opaque multi-platform build-push step"
    )
if "COPY runtime/container/rust /workcell-rust" not in runtime_dockerfile:
    raise SystemExit(
        "runtime/container/Dockerfile must vendor the reviewed Rust runtime sources into the builder stage"
    )
if "COPY runtime/container/control-plane-manifest.json /usr/local/libexec/workcell/control-plane-manifest.json" not in runtime_dockerfile:
    raise SystemExit(
        "runtime/container/Dockerfile must copy the reviewed control-plane manifest into the runtime image"
    )
if "cargo build \\" not in runtime_dockerfile or "--locked \\" not in runtime_dockerfile or "--offline \\" not in runtime_dockerfile:
    raise SystemExit(
        "runtime/container/Dockerfile must build the shipped Rust launcher artifacts with cargo --locked --offline"
    )
if "CARGO_HOME=/workcell-rust/cargo-home" not in runtime_dockerfile:
    raise SystemExit(
        "runtime/container/Dockerfile must isolate Cargo home inside the vendored runtime source tree"
    )
if "name: workcell-release-preflight" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must upload the preflight artifact-binding manifests before publication"
    )
if "actions/download-artifact@" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must download the preflight artifact-binding manifests in the publish job"
    )
if "context: dist/release-source" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must build published images from the archived release source tree"
    )
if "WORKCELL_BUILD_INPUT_ROOT: ${{ github.workspace }}/dist/release-source" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must generate the signed build input manifest from the archived release source tree"
    )
if "WORKCELL_CONTROL_PLANE_ROOT: ${{ github.workspace }}/dist/release-source" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must generate the signed control-plane manifest from the archived release source tree"
    )
if "Verify published platform digests match preflight" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must compare published per-platform image digests against the preflight manifest"
    )
if "Verify release bundle matches preflight" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must compare the published source bundle against the preflight manifest"
    )
if "Verify control-plane manifest matches preflight" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must compare the published control-plane manifest against the preflight manifest"
    )
if "github/codeql-action/init@" not in release_workflow or "github/codeql-action/analyze@" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must rerun CodeQL before publishing release artifacts"
    )
if "language: rust" not in (workflows_dir / "codeql.yml").read_text(encoding="utf-8"):
    raise SystemExit(".github/workflows/codeql.yml must analyze the shipped Rust boundary code")
for required_release_asset in (
    'dist/${{ env.BUNDLE_NAME }}.sigstore.json',
    'dist/workcell-control-plane.sigstore.json',
    'dist/workcell-source.spdx.sigstore.json',
    'dist/workcell-image.spdx.sigstore.json',
):
    if required_release_asset not in release_workflow:
        raise SystemExit(
            f".github/workflows/release.yml must publish direct signature bundles for release artifacts: missing {required_release_asset!r}"
        )
for required_release_manifest in (
    "dist/workcell-control-plane-preflight.json",
    "dist/workcell-control-plane.json",
):
    if required_release_manifest not in release_workflow:
        raise SystemExit(
            f".github/workflows/release.yml must keep the reviewed control-plane manifest flow: missing {required_release_manifest!r}"
        )
if "steps.build.outputs.digest" in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must not keep referencing the old single-step multi-platform digest output"
    )
if "gh release " in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must not depend on an ambient gh CLI; use a pinned release-publish action"
    )
if "./scripts/publish-github-release.sh" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must publish assets through the reviewed repo-local GitHub Release API script"
    )
require_contains(
    release_workflow,
    'run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"',
    "a hosted-controls audit in release preflight",
    ".github/workflows/release.yml",
)
require_contains(
    release_workflow,
    'WORKCELL_HOSTED_CONTROLS_REQUIRED: "1"',
    "a fail-closed hosted-controls requirement in release preflight",
    ".github/workflows/release.yml",
)
hosted_controls_workflow = (workflows_dir / "hosted-controls.yml").read_text(encoding="utf-8")
require_contains(
    hosted_controls_workflow,
    'run: ./scripts/run-hosted-controls-audit.sh "${GITHUB_REPOSITORY}"',
    "the hosted-controls workflow wrapper",
    ".github/workflows/hosted-controls.yml",
)
require_contains(
    hosted_controls_workflow,
    'WORKCELL_HOSTED_CONTROLS_TOKEN: ${{ secrets.WORKCELL_HOSTED_CONTROLS_TOKEN }}',
    "the hosted-controls workflow token injection",
    ".github/workflows/hosted-controls.yml",
)
require_contains(
    hosted_controls_workflow,
    'WORKCELL_HOSTED_CONTROLS_REQUIRED: "0"',
    "a non-blocking hosted-controls audit mode for continuous drift detection",
    ".github/workflows/hosted-controls.yml",
)
require_contains(
    release_workflow,
    "environment:\n      name: release",
    "a protected release environment gate",
    ".github/workflows/release.yml",
)
require_contains(
    release_workflow,
    'sudo install -m 0755 "$(command -v cosign)" /usr/local/bin/cosign',
    "trusted-path cosign exposure in the publish job",
    ".github/workflows/release.yml",
)
require_contains(
    release_workflow,
    'sudo install -m 0755 "$(command -v syft)" /usr/local/bin/syft',
    "trusted-path syft exposure in the publish job",
    ".github/workflows/release.yml",
)
for workflow_path in sorted(workflows_dir.glob("*.yml")):
    workflow_text = workflow_path.read_text(encoding="utf-8")
    require_regex(
        workflow_text,
        r"^permissions:\s+\{\}$",
        "workflow-level empty permissions declaration",
        str(workflow_path.relative_to(workflows_dir.parent.parent)),
    )
    require_not_regex(
        workflow_text,
        r"^\s*pull_request_target\s*:",
        "pull_request_target triggers",
        str(workflow_path.relative_to(workflows_dir.parent.parent)),
    )
    require_not_regex(
        workflow_text,
        r"secrets\.[A-Z0-9_]*(?:PAT|PERSONAL_ACCESS_TOKEN)\b|GH_PAT\b|PERSONAL_ACCESS_TOKEN\b",
        "long-lived personal access tokens",
        str(workflow_path.relative_to(workflows_dir.parent.parent)),
    )
    for match in re.finditer(r"^\s*-\s+uses:\s+([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)@([^\s#]+)", workflow_text, re.MULTILINE):
        ref = match.group(2)
        if not re.fullmatch(r"[0-9a-f]{40}", ref):
            raise SystemExit(
                f"{workflow_path.relative_to(workflows_dir.parent.parent)} must pin GitHub Actions by full commit SHA; "
                f"found {match.group(1)}@{ref}"
            )
for required_codeowner in (
    "/.github/workflows/ @omkhar",
    "/scripts/ @omkhar",
    "/runtime/container/ @omkhar",
    "/docs/provenance.md @omkhar",
):
    if required_codeowner not in codeowners:
        raise SystemExit(
            f".github/CODEOWNERS must declare high-risk ownership for {required_codeowner!r}"
        )
release_mode = hosted_controls_policy.get("release_environment", {}).get("mode")
if release_mode not in {"review-gated", "single-owner-private"}:
    raise SystemExit(
        "policy/github-hosted-controls.toml must set release_environment.mode "
        "to 'review-gated' or 'single-owner-private'"
    )

print("Workcell pinned input policy check passed.")
PY
