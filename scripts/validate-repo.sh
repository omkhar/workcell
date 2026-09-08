#!/bin/bash -p
set -euo pipefail

ROOT_DIR="$(CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"
# shellcheck source=scripts/lib/canonical-build-env.sh
source "${ROOT_DIR}/scripts/lib/canonical-build-env.sh"
workcell_require_modern_privileged_bash "$@"
workcell_require_canonical_build_environment
SKIP_HEAVY_HOST_SHELLCHECK="${WORKCELL_SKIP_HEAVY_HOST_SHELLCHECK:-0}"
VALIDATION_PROFILE="${WORKCELL_VALIDATE_REPO_PROFILE:-release-preflight}"
MARKDOWNLINT_LOCKFILE="${ROOT_DIR}/tools/markdownlint/package-lock.json"
MARKDOWNLINT_BIN=""

resolve_markdownlint_bin() {
  local install_dir=""

  for install_dir in \
    "${ROOT_DIR}/tools/markdownlint" \
    "/usr/local/lib/workcell-markdownlint"; do
    if [[ -x "${install_dir}/node_modules/.bin/markdownlint" ]] &&
      cmp -s "${MARKDOWNLINT_LOCKFILE}" "${install_dir}/node_modules/.workcell-package-lock.json"; then
      printf '%s\n' "${install_dir}/node_modules/.bin/markdownlint"
      return 0
    fi
  done
  return 1
}

HOME="${HOME:-/tmp/workcell-home}"
XDG_CACHE_HOME="${XDG_CACHE_HOME:-${HOME}/.cache}"
WORKCELL_VALIDATE_CACHE_HOME="${WORKCELL_VALIDATE_CACHE_HOME:-${XDG_CACHE_HOME}/workcell/validate}"
GOCACHE="${GOCACHE:-${WORKCELL_VALIDATE_CACHE_HOME}/go-build}"
GOMODCACHE="${GOMODCACHE:-${WORKCELL_VALIDATE_CACHE_HOME}/go-mod}"
CARGO_TARGET_DIR="${CARGO_TARGET_DIR:-${WORKCELL_VALIDATE_CACHE_HOME}/cargo-target}"
TMPDIR="${TMPDIR:-${HOME}/.tmp}"
export HOME XDG_CACHE_HOME WORKCELL_VALIDATE_CACHE_HOME GOCACHE GOMODCACHE CARGO_TARGET_DIR TMPDIR
mkdir -p "${XDG_CACHE_HOME}" "${WORKCELL_VALIDATE_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"

require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}

require_cargo_subcommand() {
  cargo "$1" --version >/dev/null 2>&1 || {
    echo "Missing required cargo subcommand: cargo $1" >&2
    exit 1
  }
}

require_tool shellcheck
require_tool shfmt
require_tool go
require_tool gofmt
require_tool yamllint
require_tool cargo
require_tool rustfmt
require_tool git
require_tool cmp
require_cargo_subcommand clippy
if ! MARKDOWNLINT_BIN="$(resolve_markdownlint_bin)"; then
  echo "Missing or stale locked markdownlint tool: run ./scripts/install-dev-tools.sh on the host or rebuild the validator image" >&2
  exit 1
fi
readonly MARKDOWNLINT_BIN

case "${VALIDATION_PROFILE}" in
  repo-core | pr-parity | release-preflight) ;;
  *)
    echo "Unsupported validate-repo profile: ${VALIDATION_PROFILE}" >&2
    exit 2
    ;;
esac

CITOOLS_BIN=""
BUILD_CACHE_DIR="${ROOT_DIR}/.workcell-build-cache"
SHEBANG_STDOUT=""
SHEBANG_STDERR=""

cleanup() {
  if [[ -n "${CITOOLS_BIN}" && -e "${CITOOLS_BIN}" ]]; then
    rm -f "${CITOOLS_BIN}"
  fi
  if [[ -n "${SHEBANG_STDOUT}" && -e "${SHEBANG_STDOUT}" ]]; then
    rm -f "${SHEBANG_STDOUT}"
  fi
  if [[ -n "${SHEBANG_STDERR}" && -e "${SHEBANG_STDERR}" ]]; then
    rm -f "${SHEBANG_STDERR}"
  fi
  rm -rf "${BUILD_CACHE_DIR}"
}

build_citools() {
  if [[ -n "${CITOOLS_BIN}" ]]; then
    return 0
  fi
  CITOOLS_BIN="$(mktemp "${TMPDIR:-/tmp}/workcell-citools.XXXXXX")"
  (cd "${ROOT_DIR}" && go build -buildvcs=false -o "${CITOOLS_BIN}" ./cmd/workcell-citools)
}

run_citools() {
  build_citools
  "${CITOOLS_BIN}" "$@"
}

trap cleanup EXIT

python_files=()
while IFS= read -r file; do
  python_files+=("${file}")
done < <(
  find "${ROOT_DIR}" \
    -path "${ROOT_DIR}/.git" -prune -o \
    -path "${ROOT_DIR}/.venv" -prune -o \
    -path "${ROOT_DIR}/dist" -prune -o \
    -path "${ROOT_DIR}/tmp" -prune -o \
    -path "${ROOT_DIR}/runtime/container/providers/node_modules" -prune -o \
    -path "${ROOT_DIR}/tools/markdownlint/node_modules" -prune -o \
    -type f -name '*.py' -print | sort
)

branding_scan() {
  local pattern="agent-boundary|Agent Boundary|agent boundary"

  if command -v rg >/dev/null 2>&1; then
    rg -n "${pattern}" "${ROOT_DIR}" \
      -g '!**/.git/**' \
      -g '!scripts/validate-repo.sh' \
      -g '!dist/**' \
      -g '!tmp/**'
    return
  fi

  grep -RInE "${pattern}" "${ROOT_DIR}" \
    --exclude-dir=.git \
    --exclude-dir=dist \
    --exclude-dir=tmp \
    --exclude=validate-repo.sh
}

validate_manpage() {
  if command -v mandoc >/dev/null 2>&1; then
    mandoc -Tlint "${ROOT_DIR}/man/workcell.1" >/dev/null
    return
  fi

  if command -v nroff >/dev/null 2>&1; then
    nroff -man "${ROOT_DIR}/man/workcell.1" >/dev/null
    return
  fi

  echo "Missing required tool: mandoc or nroff" >&2
  exit 1
}

shell_files=(
  "${ROOT_DIR}/.githooks/commit-msg"
  "${ROOT_DIR}/.githooks/pre-commit"
  "${ROOT_DIR}/.githooks/pre-push"
  "${ROOT_DIR}/scripts/bootstrap-dev.sh"
  "${ROOT_DIR}/scripts/check-dead-code.sh"
  "${ROOT_DIR}/scripts/check-doc-language.sh"
  "${ROOT_DIR}/scripts/check-generated-artifacts.sh"
  "${ROOT_DIR}/scripts/check-doc-links.sh"
  "${ROOT_DIR}/scripts/check-doc-support-matrix-fields.sh"
  "${ROOT_DIR}/scripts/check-public-repo-hygiene.sh"
  "${ROOT_DIR}/scripts/check-pr-shape.sh"
  "${ROOT_DIR}/scripts/check-publish-commit-signatures.sh"
  "${ROOT_DIR}/scripts/check-repo-readiness.sh"
  "${ROOT_DIR}/scripts/check-pinned-inputs.sh"
  "${ROOT_DIR}/scripts/check-validator-anchoring.sh"
  "${ROOT_DIR}/scripts/check-public-contract.sh"
  "${ROOT_DIR}/scripts/certify-c3-parallel-sessions.sh"
  "${ROOT_DIR}/scripts/build-and-test.sh"
  "${ROOT_DIR}/scripts/ci-plan.sh"
  "${ROOT_DIR}/scripts/bench/run-exec-guard-bench.sh"
  "${ROOT_DIR}/scripts/bench/run-startup-bench.sh"
  "${ROOT_DIR}/scripts/workcell"
  "${ROOT_DIR}/scripts/check-workflows.sh"
  "${ROOT_DIR}/scripts/ci/build-validator-image.sh"
  "${ROOT_DIR}/scripts/ci/cost-report.sh"
  "${ROOT_DIR}/scripts/ci/flaky-report.sh"
  "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
  "${ROOT_DIR}/scripts/ci/job-docs.sh"
  "${ROOT_DIR}/scripts/ci/job-fuzz.sh"
  "${ROOT_DIR}/scripts/ci/job-mutation.sh"
  "${ROOT_DIR}/scripts/ci/job-pin-hygiene.sh"
  "${ROOT_DIR}/scripts/ci/job-pr-shape.sh"
  "${ROOT_DIR}/scripts/ci/job-release-asset-acl.sh"
  "${ROOT_DIR}/scripts/ci/job-validate.sh"
  "${ROOT_DIR}/scripts/ci/lib/validator-passwd.sh"
  "${ROOT_DIR}/scripts/ci/run-docs-in-validator.sh"
  "${ROOT_DIR}/scripts/ci/run-fuzz-in-validator.sh"
  "${ROOT_DIR}/scripts/ci/run-mutation-in-validator.sh"
  "${ROOT_DIR}/scripts/ci/run-validate-in-validator.sh"
  "${ROOT_DIR}/scripts/colima-egress-allowlist.sh"
  "${ROOT_DIR}/scripts/container-smoke.sh"
  "${ROOT_DIR}/scripts/dev-quick-check.sh"
  "${ROOT_DIR}/scripts/go-port-validate.sh"
  "${ROOT_DIR}/scripts/install-dev-tools.sh"
  "${ROOT_DIR}/scripts/lint-dockerfiles.sh"
  "${ROOT_DIR}/scripts/lib/extract_direct_mounts"
  "${ROOT_DIR}/scripts/lib/canonical-build-env.sh"
  "${ROOT_DIR}/scripts/lib/go-run-env.sh"
  "${ROOT_DIR}/scripts/lib/launcher/host-detect.sh"
  "${ROOT_DIR}/scripts/lib/launcher/host-exec.sh"
  "${ROOT_DIR}/scripts/lib/launcher/go-hostutil.sh"
  "${ROOT_DIR}/scripts/lib/launcher/egress-endpoints.sh"
  "${ROOT_DIR}/scripts/lib/trusted-entrypoint.sh"
  "${ROOT_DIR}/scripts/lib/manage_injection_policy"
  "${ROOT_DIR}/scripts/lib/pty_transcript"
  "${ROOT_DIR}/scripts/lib/render_injection_bundle"
  "${ROOT_DIR}/scripts/lib/resolve_credential_sources"
  "${ROOT_DIR}/scripts/lib/scenario_manifest"
  "${ROOT_DIR}/scripts/lib/sessionctl-shim.sh"
  "${ROOT_DIR}/scripts/lib/shellproto.sh"
  "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
  "${ROOT_DIR}/scripts/generate-control-plane-manifest.sh"
  "${ROOT_DIR}/scripts/generate-builder-environment-manifest.sh"
  "${ROOT_DIR}/scripts/generate-release-checksums.sh"
  "${ROOT_DIR}/scripts/generate-homebrew-formula.sh"
  "${ROOT_DIR}/scripts/generate-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/generate-workflow-lane-manifest.sh"
  "${ROOT_DIR}/scripts/install.sh"
  "${ROOT_DIR}/scripts/install-release.sh"
  "${ROOT_DIR}/scripts/install-workcell.sh"
  "${ROOT_DIR}/scripts/uninstall.sh"
  "${ROOT_DIR}/scripts/pre-merge.sh"
  "${ROOT_DIR}/scripts/provider-e2e.sh"
  "${ROOT_DIR}/scripts/repo-publish-pr.sh"
  "${ROOT_DIR}/scripts/retry.sh"
  "${ROOT_DIR}/scripts/publish-github-release.sh"
  "${ROOT_DIR}/scripts/check-release-tag-signature.sh"
  "${ROOT_DIR}/scripts/publish-provider-bump-pr.sh"
  "${ROOT_DIR}/scripts/publish-upstream-refresh-pr.sh"
  "${ROOT_DIR}/scripts/run-hosted-controls-audit.sh"
  "${ROOT_DIR}/scripts/run-mutation-tests.sh"
  "${ROOT_DIR}/scripts/update-upstream-pins.sh"
  "${ROOT_DIR}/scripts/update-provider-pins.sh"
  "${ROOT_DIR}/scripts/verify-coverage.sh"
  "${ROOT_DIR}/scripts/verify-github-hosted-controls.sh"
  "${ROOT_DIR}/scripts/verify-mutation-score.sh"
  "${ROOT_DIR}/scripts/validate-repo.sh"
  "${ROOT_DIR}/scripts/verify-build-input-manifest.sh"
  "${ROOT_DIR}/scripts/verify-upstream-claude-release.sh"
  "${ROOT_DIR}/scripts/verify-control-plane-manifest.sh"
  "${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh"
  "${ROOT_DIR}/scripts/verify-release-artifact.sh"
  "${ROOT_DIR}/scripts/verify-release-bundle.sh"
  "${ROOT_DIR}/scripts/verify-release-outputs.sh"
  "${ROOT_DIR}/scripts/verify-invariants.sh"
  "${ROOT_DIR}/scripts/verify-invariants-live.sh"
  "${ROOT_DIR}/scripts/verify-operator-contract.sh"
  "${ROOT_DIR}/scripts/verify-workflow-lanes.sh"
  "${ROOT_DIR}/scripts/verify-requirements-coverage.sh"
  "${ROOT_DIR}/scripts/verify-reproducible-build.sh"
  "${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"
  "${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh"
  "${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh"
  "${ROOT_DIR}/scripts/with-validation-snapshot.sh"
  "${ROOT_DIR}/adapters/claude/hooks/guard-bash.sh"
  "${ROOT_DIR}/runtime/container/entrypoint.sh"
  "${ROOT_DIR}/runtime/container/bin/apt-helper.sh"
  "${ROOT_DIR}/runtime/container/bin/apt-wrapper.sh"
  "${ROOT_DIR}/runtime/container/bin/sudo-wrapper.sh"
  "${ROOT_DIR}/runtime/container/detached-stdin-wrapper.sh"
  "${ROOT_DIR}/runtime/container/assurance.sh"
  "${ROOT_DIR}/runtime/container/development-wrapper.sh"
  "${ROOT_DIR}/runtime/container/bin/git"
  "${ROOT_DIR}/runtime/container/bin/node"
  "${ROOT_DIR}/runtime/container/home-control-plane.sh"
  "${ROOT_DIR}/runtime/container/provider-policy.sh"
  "${ROOT_DIR}/runtime/container/provider-wrapper.sh"
  "${ROOT_DIR}/runtime/container/runtime-user.sh"
  "${ROOT_DIR}/scripts/run-scenario-tests.sh"
  "${ROOT_DIR}/scripts/verify-scenario-coverage.sh"
  "${ROOT_DIR}/scripts/verify-control-plane-parity.sh"
  "${ROOT_DIR}/verify/invariants/control-plane-lockstep.sh"
  "${ROOT_DIR}/verify/invariants/harnesses/git-probes/snapshot-kind-probe.sh"
  "${ROOT_DIR}/verify/invariants/harnesses/git-probes/snapshot-probe.sh"
  "${ROOT_DIR}/verify/invariants/harnesses/install-deps/brew.sh"
  "${ROOT_DIR}/verify/invariants/harnesses/install-deps/sysctl.sh"
  "${ROOT_DIR}/verify/invariants/harnesses/install-deps/uname.sh"
)

# These scripts are linted but are not executable in the tree. The container
# image sets the mode on copy, and the parity library is sourced, not run.
non_executable_shell_files=(
  "${ROOT_DIR}/runtime/container/bin/sudo-wrapper.sh"
  "${ROOT_DIR}/runtime/container/detached-stdin-wrapper.sh"
  "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
  "${ROOT_DIR}/scripts/verify-invariants-live.sh"
  "${ROOT_DIR}/verify/invariants/harnesses/git-probes/snapshot-kind-probe.sh"
  "${ROOT_DIR}/verify/invariants/harnesses/git-probes/snapshot-probe.sh"
)

# Capture the scenario-test walk before the loop reads it. This inventory
# decides which scenario tests get linted at all, so a masked `find` failure
# would quietly shrink the lint set that the completeness check below then
# reports as complete. `set -e` with `pipefail` aborts on a failed walk, and an
# empty result is rejected rather than treated as "no scenario tests".
scenario_test_listing="$(find "${ROOT_DIR}/tests/scenarios" -type f -name 'test-*.sh' -print | sort)"

if [[ -z "${scenario_test_listing}" ]]; then
  echo "No scenario tests found under tests/scenarios; refusing a vacuous lint set" >&2
  exit 1
fi

while IFS= read -r file; do
  [[ -n "${file}" ]] || continue
  shell_files+=("${file}")
done <<<"${scenario_test_listing}"

# The list above is hand-maintained, so a new script can enter the tree
# unlinted. Assert that the list covers every tracked bash script. Match on the
# shebang, not on the file suffix: several linted scripts have no `.sh` name.
declare -A linted_shell_files=()
for file in "${shell_files[@]}"; do
  linted_shell_files["${file#"${ROOT_DIR}/"}"]=1
done

# Accept only the interpreter that the shebang actually selects. Testing every
# token matches `#!/usr/bin/env -S echo bash`, which runs `echo`. Resolve the
# command position instead: the first token is the interpreter, and when that
# interpreter is `env`, skip its options and its `VAR=value` assignments to
# reach the command. A split string may be attached or detached and its long
# option may be abbreviated to any unambiguous prefix, so `-S bash`, `-Sbash`,
# `-iSbash`, `--split-string=bash` and `--spl=bash` all select Bash.
is_bash_shebang() {
  local line="$1"
  local -a tokens=()
  local token="" long_option="" command="" cluster=""
  local index=0 position=0

  [[ "${line}" == '#!'* ]] || return 1
  IFS=$' \t' read -r -a tokens <<<"${line#'#!'}"
  [[ "${#tokens[@]}" -gt 0 ]] || return 1

  if [[ "${tokens[0]##*/}" != "env" ]]; then
    command="${tokens[0]}"
  else
    for ((index = 1; index < ${#tokens[@]}; index++)); do
      token="${tokens[index]}"
      case "${token}" in
        --)
          command="${tokens[index + 1]:-}"
          break
          ;;
        --*=*)
          long_option="${token%%=*}"
          long_option="${long_option#--}"
          if [[ -n "${long_option}" && "split-string" == "${long_option}"* ]]; then
            token="${token#*=}"
            if [[ -n "${token}" ]]; then
              command="${token}"
              break
            fi
          fi
          ;;
        -*)
          # Walk the short option cluster one letter at a time. `S` introduces
          # the split string, and `u` and `C` take a value that may be attached,
          # so their argument must not be read as further option letters: the
          # `S` in `-uPOSIXLY_CORRECT` names a variable, not a split string.
          cluster="${token#-}"
          for ((position = 0; position < ${#cluster}; position++)); do
            case "${cluster:position:1}" in
              S)
                # Attached, the command is here. Detached, it follows, after
                # any further assignments.
                command="${cluster:position+1}"
                break
                ;;
              u | C)
                [[ -n "${cluster:position+1}" ]] || ((index++))
                break
                ;;
              *) ;;
            esac
          done
          [[ -z "${command}" ]] || break
          ;;
        *=*) ;;
        *)
          command="${token}"
          break
          ;;
      esac
    done
  fi

  [[ -n "${command}" ]] || return 1
  command="${command//\"/}"
  command="${command//\'/}"
  # `env -S` expands a variable reference and substitutes an empty string for an
  # unset one, so `#!/usr/bin/env -S /bin/ba${UNSET}sh` runs Bash. That value
  # cannot be resolved here. Report an unresolvable command as a candidate so
  # the completeness gate demands the script instead of skipping it silently.
  [[ "${command}" != *'$'* ]] || return 0
  [[ "${command##*/}" == "bash" ]]
}

# Read every first-line shebang from the index rather than from the worktree.
# A filesystem read trusts the whole path: a symlink at the leaf or at any
# parent directory redirects it outside the checkout, and no Bash test closes
# that gap because Bash cannot express fd-relative `openat`. Git resolves a
# tracked path against the index instead, so no directory component is
# followed and there is no window between the check and the read. It also
# removes the end-of-file case, because Git yields the line rather than a
# `read` status.
#
# `git grep` exits 1 only when nothing matched, and the inventory assertion
# below rejects that. It exits 0 with partial output when an indexed blob is
# unreadable, and reports the failure on stderr alone. Read it through a file
# so both channels are testable: a diagnostic means the index was not read
# completely, and an omitted path would otherwise be classified as "not Bash"
# and silently left unlinted.
SHEBANG_STDOUT="$(mktemp "${TMPDIR:-/tmp}/workcell-shebangs.XXXXXX")"
SHEBANG_STDERR="$(mktemp "${TMPDIR:-/tmp}/workcell-shebang-errors.XXXXXX")"
shebang_read_status=0
# `-a` rather than `-I`: Bash runs a script that carries a NUL byte, but `-I`
# treats that blob as binary and omits it from the listing without an error.
# The omitted path would then be classified as "not Bash" and left unlinted.
git -C "${ROOT_DIR}" grep --cached -z -a -n -E '^#!' \
  >"${SHEBANG_STDOUT}" 2>"${SHEBANG_STDERR}" || shebang_read_status=$?

if [[ "${shebang_read_status}" -gt 1 || -s "${SHEBANG_STDERR}" ]]; then
  echo "Reading tracked shebangs from the index failed; shell lint coverage is unverified" >&2
  sed 's/^/  /' "${SHEBANG_STDERR}" >&2
  exit 1
fi

declare -A tracked_shebangs=()
while IFS= read -r -d '' shebang_path &&
  IFS= read -r -d '' shebang_lineno &&
  IFS= read -r shebang_line; do
  [[ "${shebang_lineno}" == "1" ]] || continue
  tracked_shebangs["${shebang_path}"]="${shebang_line}"
done <"${SHEBANG_STDOUT}"

if [[ "${#tracked_shebangs[@]}" -eq 0 ]]; then
  echo "Tracked shebang inventory is empty; shell lint coverage is unverified" >&2
  exit 1
fi

# Bash does not propagate a process-substitution failure, so a `git ls-files`
# error would leave this check reading an empty inventory and passing. Emit a
# lone NUL after a successful listing and require it: a truncated listing then
# fails closed instead of reporting complete coverage over a partial tree.
#
# `ls-files -s` reports the index mode, so the file type comes from Git rather
# than from a filesystem probe. Only a regular blob can be a script, so a
# symlink (120000) and a submodule (160000) are skipped.
unlinted_shell_files=()
lint_inventory_completed=0
# shellcheck disable=SC2312 # the NUL sentinel and lint_inventory_completed assertion below are the compensating control
while IFS= read -r -d '' index_entry; do
  if [[ -z "${index_entry}" ]]; then
    lint_inventory_completed=1
    continue
  fi
  tracked_mode="${index_entry%% *}"
  tracked_path="${index_entry#*$'\t'}"
  [[ "${tracked_mode}" == "100644" || "${tracked_mode}" == "100755" ]] || continue
  [[ -n "${linted_shell_files[${tracked_path}]:-}" ]] && continue
  is_bash_shebang "${tracked_shebangs[${tracked_path}]:-}" || continue
  unlinted_shell_files+=("${tracked_path}")
done < <(git -C "${ROOT_DIR}" ls-files -sz && printf '\0')

if [[ "${lint_inventory_completed}" -ne 1 ]]; then
  echo "Tracked file listing failed; shell lint coverage is unverified" >&2
  exit 1
fi

if [[ "${#unlinted_shell_files[@]}" -gt 0 ]]; then
  echo "Tracked bash scripts are missing from the validate-repo.sh lint list:" >&2
  printf '  %s\n' "${unlinted_shell_files[@]}" >&2
  echo "Add each script to shell_files so it is linted." >&2
  exit 1
fi

should_skip_shellcheck_file() {
  local file="$1"

  [[ "${SKIP_HEAVY_HOST_SHELLCHECK}" == "1" ]] || return 1
  case "${file}" in
    "${ROOT_DIR}/scripts/workcell" | "${ROOT_DIR}/scripts/verify-invariants.sh" | "${ROOT_DIR}/scripts/verify-invariants-live.sh")
      return 0
      ;;
  esac
  return 1
}

for file in "${shell_files[@]}"; do
  if should_skip_shellcheck_file "${file}"; then
    continue
  fi
  shellcheck -x "${file}"
done

# A lost exit status turns a trust decision into a vacuous pass: an unread
# inventory looks the same as a clean one. SC2311 and SC2312 detect it, but they
# are optional checks and stay off by default. Enable them on the scripts where
# a masked return would admit an unverified release, commit, or public surface.
# Ratchet: add files as they are cleaned, never remove one.
# Select the subset from the verified inventory above, not from a filesystem
# glob. A glob matches whatever the checkout holds, so an untracked or
# symlinked `scripts/check-evil.sh` would enter the subset and ShellCheck would
# open its target outside the repository. Selecting from `shell_files` keeps
# this subset inside the list the completeness check has already verified.
fail_open_critical_shell_files=()
for file in "${shell_files[@]}"; do
  case "${file#"${ROOT_DIR}/"}" in
    scripts/verify-release-outputs.sh | scripts/verify-release-artifact.sh | scripts/check-*.sh | .githooks/*)
      fail_open_critical_shell_files+=("${file}")
      ;;
  esac
done

if [[ "${#fail_open_critical_shell_files[@]}" -eq 0 ]]; then
  echo "Fail-open-critical subset is empty; masked-return coverage is unverified" >&2
  exit 1
fi

for file in "${fail_open_critical_shell_files[@]}"; do
  shellcheck -x -o check-extra-masked-returns "${file}"
done

shfmt -ln=bash -i 2 -ci -d "${shell_files[@]}"
"${ROOT_DIR}/scripts/lint-dockerfiles.sh"

declare -A shell_files_without_exec_bit=()
for file in "${non_executable_shell_files[@]}"; do
  shell_files_without_exec_bit["${file}"]=1
done

for file in "${shell_files[@]}"; do
  if [[ -n "${shell_files_without_exec_bit[${file}]:-}" ]]; then
    continue
  fi
  if [[ ! -x "${file}" ]]; then
    echo "Expected executable script: ${file}" >&2
    exit 1
  fi
done

for scratch_dir in \
  "${ROOT_DIR}/adapters/codex/.codex/memories" \
  "${ROOT_DIR}/adapters/codex/.codex/tmp"; do
  if find "${scratch_dir}" -mindepth 1 -print -quit 2>/dev/null | grep -q .; then
    echo "Unexpected adapter scratch state present: ${scratch_dir}" >&2
    exit 1
  fi
done

if [[ "${#python_files[@]}" -gt 0 ]]; then
  echo "Unexpected Python source files remain in scripts/lib:" >&2
  printf '  %s\n' "${python_files[@]}" >&2
  exit 1
fi
go_files=()
while IFS= read -r -d '' path; do
  go_files+=("${path}")
done < <(find "${ROOT_DIR}/cmd" "${ROOT_DIR}/internal" -type f -name '*.go' -print0 | sort -z)
if [[ "${#go_files[@]}" -gt 0 ]]; then
  if gofmt -l "${go_files[@]}" | grep -q .; then
    echo "Go files are not formatted with gofmt." >&2
    exit 1
  fi
fi
go vet ./...
go test ./...

# The deterministic fuzz seed corpora already run above via `go test ./...`
# as regression tests on every PR, which is the release-relevant assurance.
# The nondeterministic active-fuzzing *time budget* deliberately does not run
# on this PR-blocking path: active fuzzing is a time-bounded discovery hunt,
# not a deterministic gate, and running it here made PRs flaky (spurious
# per-input timeouts with no reproducer) without adding release assurance. The
# extended active budget runs on the scheduled fuzz lane
# (.github/workflows/fuzz.yml -> scripts/ci/job-fuzz.sh), which exercises the
# same targets (FuzzParse, FuzzParseSSHDirective, and the injection/metadata
# parsers). See docs/fuzzing.md and docs/ci-efficiency-and-reliability.md.

"${ROOT_DIR}/scripts/check-dead-code.sh"
"${ROOT_DIR}/scripts/check-public-repo-hygiene.sh"

json_files=()
while IFS= read -r path; do
  [[ -n "${path}" ]] || continue
  json_files+=("${path}")
done < <(
  find "${ROOT_DIR}/adapters" "${ROOT_DIR}/.github" "${ROOT_DIR}/runtime/container/providers" "${ROOT_DIR}/tests/scenarios" \
    -path '*/node_modules' -prune -o \
    -type f -name '*.json' -print | sort
)
if [[ "${#json_files[@]}" -gt 0 ]]; then
  run_citools validate-json "${json_files[@]}"
fi

toml_files=()
while IFS= read -r path; do
  [[ -n "${path}" ]] || continue
  toml_files+=("${path}")
done < <(
  find "${ROOT_DIR}" \
    -path "${ROOT_DIR}/.git" -prune -o \
    -path "${ROOT_DIR}/dist" -prune -o \
    -path "${ROOT_DIR}/tmp" -prune -o \
    -path "${ROOT_DIR}/runtime/container/providers/node_modules" -prune -o \
    -path "${ROOT_DIR}/tools/markdownlint/node_modules" -prune -o \
    -type f -name '*.toml' -print | sort
)
if [[ "${#toml_files[@]}" -gt 0 ]]; then
  run_citools validate-toml "${toml_files[@]}"
fi

yamllint -d "{extends: default, rules: {comments: disable, document-start: disable, line-length: disable, truthy: disable}}" \
  "${ROOT_DIR}/.github/dependency-review-config.yml" \
  "${ROOT_DIR}/.github/dependabot.yml" \
  "${ROOT_DIR}/.github/workflows"

"${ROOT_DIR}/scripts/verify-build-input-manifest.sh"
"${ROOT_DIR}/scripts/verify-control-plane-manifest.sh"
"${ROOT_DIR}/scripts/check-public-contract.sh"
"${ROOT_DIR}/scripts/verify-operator-contract.sh"
"${ROOT_DIR}/scripts/verify-requirements-coverage.sh"

doc_files=()
while IFS= read -r -d '' file; do
  doc_files+=("${file}")
done < <(find "${ROOT_DIR}" \
  -path "${ROOT_DIR}/.git" -prune -o \
  -path "${ROOT_DIR}/dist" -prune -o \
  -path "${ROOT_DIR}/tmp" -prune -o \
  -path "${ROOT_DIR}/.venv" -prune -o \
  -path "${ROOT_DIR}/runtime/container/providers/node_modules" -prune -o \
  -path "${ROOT_DIR}/tools/markdownlint/node_modules" -prune -o \
  -path "${ROOT_DIR}/runtime/container/rust/vendor" -prune -o \
  -path "${ROOT_DIR}/runtime/container/rust/target" -prune -o \
  -type f \( -name '*.md' -o -name '*.txt' -o -name '*.1' \) -print0 | sort -z)

markdown_files=()
while IFS= read -r -d '' file; do
  markdown_files+=("${file}")
done < <(find "${ROOT_DIR}" \
  -path "${ROOT_DIR}/.git" -prune -o \
  -path "${ROOT_DIR}/dist" -prune -o \
  -path "${ROOT_DIR}/tmp" -prune -o \
  -path "${ROOT_DIR}/.venv" -prune -o \
  -path "${ROOT_DIR}/runtime/container/providers/node_modules" -prune -o \
  -path "${ROOT_DIR}/tools/markdownlint/node_modules" -prune -o \
  -path "${ROOT_DIR}/runtime/container/rust/vendor" -prune -o \
  -path "${ROOT_DIR}/runtime/container/rust/target" -prune -o \
  -type f -name '*.md' -print0 | sort -z)

if command -v codespell >/dev/null 2>&1; then
  codespell --config "${ROOT_DIR}/.codespellrc" "${doc_files[@]}"
else
  echo "Skipping spelling checks because codespell is not installed locally." >&2
fi

if [[ "${#markdown_files[@]}" -gt 0 ]]; then
  "${MARKDOWNLINT_BIN}" "${markdown_files[@]}"
fi

validate_manpage

# Check A: docs/examples/ must exist and be non-empty
if [[ ! -d "${ROOT_DIR}/docs/examples" ]] || ! find "${ROOT_DIR}/docs/examples" -type f -print -quit | grep -q .; then
  echo "docs/examples/ must exist and be non-empty" >&2
  exit 1
fi

# Check B: TOML validation for docs/examples/ is already covered by the
# validate-toml pass above, which scans the repository tree.

# Check C: Credential pattern scan in tests/ and docs/examples/
run_citools scan-credential-patterns "${ROOT_DIR}"

if branding_scan; then
  echo "Found stale pre-rename branding." >&2
  exit 1
fi

(
  cd "${ROOT_DIR}/runtime/container/rust"
  cargo fmt --all --check
  cargo clippy --all-targets --locked --offline -- -D warnings
  cargo test --locked --offline
)

# A4: clippy::undocumented_unsafe_blocks enforces `// SAFETY:` on `unsafe {}`
# blocks but NOT on `unsafe extern` blocks. Guard those explicitly so a new
# undocumented FFI declaration also fails CI. The pattern matches any ABI form of
# the block opener (`unsafe extern {`, `unsafe extern "C" {`,
# `unsafe extern "system" {`, ...) while excluding `unsafe extern "…" fn`
# declarations and type aliases, which end in `fn`/`(` rather than `{`.
# rustfmt (enforced above) keeps each block opener on a single line.
while IFS=: read -r extern_file extern_line _; do
  if ! sed -n "$((extern_line - 1))p" "${extern_file}" | grep -q '// SAFETY:'; then
    echo "unsafe extern block missing a preceding // SAFETY: comment at ${extern_file}:${extern_line}" >&2
    exit 1
  fi
done < <(grep -rnE 'unsafe extern( +"[^"]*")? *\{' "${ROOT_DIR}/runtime/container/rust/src")

if [[ "${VALIDATION_PROFILE}" == "release-preflight" ]]; then
  "${ROOT_DIR}/scripts/verify-mutation-score.sh"
  "${ROOT_DIR}/scripts/verify-coverage.sh"
fi

# Pre-build hostutil so scenario tests skip `go run` overhead on every invocation
mkdir -p "${BUILD_CACHE_DIR}"
(cd "${ROOT_DIR}" && go build -buildvcs=false -o "${BUILD_CACHE_DIR}/hostutil" ./cmd/workcell-hostutil)

# Check E: deterministic repo-required scenarios plus control-plane parity
"${ROOT_DIR}/scripts/run-scenario-tests.sh" --repo-required
"${ROOT_DIR}/scripts/verify-scenario-coverage.sh"
"${ROOT_DIR}/scripts/verify-control-plane-parity.sh"

echo "Workcell repository validation passed."
