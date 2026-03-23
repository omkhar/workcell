#!/usr/bin/env -S BASH_ENV= ENV= bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DOCKERFILE_PATH="${ROOT_DIR}/runtime/container/Dockerfile"
VALIDATOR_DOCKERFILE_PATH="${ROOT_DIR}/tools/validator/Dockerfile"
PROVIDERS_PACKAGE_JSON_PATH="${ROOT_DIR}/runtime/container/providers/package.json"
PROVIDERS_PACKAGE_LOCK_PATH="${ROOT_DIR}/runtime/container/providers/package-lock.json"
CI_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/ci.yml"
RELEASE_WORKFLOW_PATH="${ROOT_DIR}/.github/workflows/release.yml"
CODEX_REQUIREMENTS_PATH="${ROOT_DIR}/adapters/codex/requirements.toml"
CODEX_MCP_CONFIG_PATH="${ROOT_DIR}/adapters/codex/mcp/config.toml"
MAX_DEBIAN_SNAPSHOT_AGE_DAYS="${WORKCELL_MAX_DEBIAN_SNAPSHOT_AGE_DAYS:-45}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_tool python3

python3 - "${DOCKERFILE_PATH}" "${VALIDATOR_DOCKERFILE_PATH}" "${PROVIDERS_PACKAGE_JSON_PATH}" "${PROVIDERS_PACKAGE_LOCK_PATH}" "${CI_WORKFLOW_PATH}" "${RELEASE_WORKFLOW_PATH}" "${CODEX_REQUIREMENTS_PATH}" "${CODEX_MCP_CONFIG_PATH}" "${MAX_DEBIAN_SNAPSHOT_AGE_DAYS}" <<'PY'
import datetime as dt
import json
import pathlib
import re
import sys
import tomllib

runtime_dockerfile = pathlib.Path(sys.argv[1]).read_text(encoding="utf-8")
validator_dockerfile = pathlib.Path(sys.argv[2]).read_text(encoding="utf-8")
providers_package_json = json.loads(pathlib.Path(sys.argv[3]).read_text(encoding="utf-8"))
providers_package_lock = json.loads(pathlib.Path(sys.argv[4]).read_text(encoding="utf-8"))
ci_workflow = pathlib.Path(sys.argv[5]).read_text(encoding="utf-8")
release_workflow = pathlib.Path(sys.argv[6]).read_text(encoding="utf-8")
codex_requirements = tomllib.loads(pathlib.Path(sys.argv[7]).read_text(encoding="utf-8"))
codex_mcp_config = tomllib.loads(pathlib.Path(sys.argv[8]).read_text(encoding="utf-8"))
max_snapshot_age_days = int(sys.argv[9])

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
runtime_snapshot = require_arg(runtime_dockerfile, "DEBIAN_SNAPSHOT", "runtime/container/Dockerfile")
validator_snapshot = require_arg(validator_dockerfile, "DEBIAN_SNAPSHOT", "tools/validator/Dockerfile")
codex_version = require_arg(runtime_dockerfile, "CODEX_VERSION", "runtime/container/Dockerfile")
runtime_install_blocks = extract_install_blocks(runtime_dockerfile, "runtime/container/Dockerfile")
validator_install_blocks = extract_install_blocks(validator_dockerfile, "tools/validator/Dockerfile")

require_pinned_base_image(runtime_base_image, "NODE_BASE_IMAGE", "runtime/container/Dockerfile")
require_pinned_base_image(validator_base_image, "VALIDATOR_BASE_IMAGE", "tools/validator/Dockerfile")
verify_snapshot_freshness(runtime_snapshot, "runtime/container/Dockerfile")
verify_snapshot_freshness(validator_snapshot, "tools/validator/Dockerfile")
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
require_exact_packages(
    runtime_install_blocks[0],
    [
        "bash",
        "ca-certificates",
        "curl",
        "fd-find",
        "git",
        "jq",
        "less",
        "openssh-client",
        "procps",
        "ripgrep",
        "unzip",
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
        "mandoc",
        "python3",
        "rustc",
        "rustfmt",
        "shellcheck",
        "shfmt",
        "yamllint",
    ],
    "Validator",
    "tools/validator/Dockerfile",
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
if "cache-image: true" not in ci_workflow:
    raise SystemExit("Pinned QEMU image caching must stay enabled in .github/workflows/ci.yml")
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
if "Verify published platform digests match preflight" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must compare published per-platform image digests against the preflight manifest"
    )
if "Verify release bundle matches preflight" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must compare the published source bundle against the preflight manifest"
    )
if "github/codeql-action/init@" not in release_workflow or "github/codeql-action/analyze@" not in release_workflow:
    raise SystemExit(
        ".github/workflows/release.yml must rerun CodeQL before publishing release artifacts"
    )
if "language: rust" not in pathlib.Path(sys.argv[6]).with_name("codeql.yml").read_text(encoding="utf-8"):
    raise SystemExit(".github/workflows/codeql.yml must analyze the shipped Rust boundary code")
for required_release_asset in (
    'dist/${{ env.BUNDLE_NAME }}.sigstore.json',
    'dist/workcell-source.spdx.sigstore.json',
    'dist/workcell-image.spdx.sigstore.json',
):
    if required_release_asset not in release_workflow:
        raise SystemExit(
            f".github/workflows/release.yml must publish direct signature bundles for release artifacts: missing {required_release_asset!r}"
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

print("Workcell pinned input policy check passed.")
PY
