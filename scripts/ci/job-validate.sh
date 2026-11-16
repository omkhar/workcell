#!/usr/bin/env -S BASH_ENV= ENV= bash
workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=scripts/ci/job-validate.sh && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
set -euo pipefail

ROOT_DIR="${workcell_entry_root}"
workcell_validate_token_file_created=""
if [[ -z "${WORKCELL_GITHUB_API_TOKEN_FILE:-}" ]]; then
  workcell_validate_token="${WORKCELL_GITHUB_API_TOKEN:-${GITHUB_TOKEN:-${GH_TOKEN:-}}}"
  if [[ -n "${workcell_validate_token}" ]]; then
    workcell_validate_token_file="$(umask 077 && mktemp "${TMPDIR:-/tmp}/workcell-github-token.XXXXXX")"
    printf '%s' "${workcell_validate_token}" >"${workcell_validate_token_file}"
    export WORKCELL_GITHUB_API_TOKEN_FILE="${workcell_validate_token_file}"
    workcell_validate_token_file_created="${workcell_validate_token_file}"
  fi
fi
unset WORKCELL_GITHUB_API_TOKEN GITHUB_TOKEN GH_TOKEN workcell_validate_token
# The trusted docker client library runs HOST_DOCKER_BIN; a lane names it, so
# a value the job inherits cannot choose the program.
# shellcheck disable=SC2034
HOST_DOCKER_BIN=docker
source "${ROOT_DIR}/scripts/lib/trusted-docker-client.sh"
source "${ROOT_DIR}/scripts/ci/lib/local-docker-parity.sh"
PROFILE="${WORKCELL_CI_VALIDATE_PROFILE:-pr-parity}"
VALIDATOR_IMAGE="${WORKCELL_VALIDATOR_IMAGE:-}"
VALIDATOR_IMAGE_INPUT="${WORKCELL_VALIDATOR_IMAGE:-}"
VALIDATOR_IMAGE_OWNED=0
VALIDATOR_IMAGE_RESERVATION=""
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git -C "${ROOT_DIR}" log -1 --pretty=%ct 2>/dev/null || printf '0')}"
ARCHIVE_REF="${WORKCELL_CI_ARCHIVE_REF:-$(git -C "${ROOT_DIR}" rev-parse HEAD 2>/dev/null || printf 'HEAD')}"
REPOSITORY_NAME="${GITHUB_REPOSITORY:-workcell/local}"
ARTIFACT_DIR="${WORKCELL_CI_INSTALL_ARTIFACT_DIR:-}"
KEEP_ARTIFACT_DIR=0
SKIP_RELEASE_BUNDLE="${WORKCELL_CI_VALIDATE_SKIP_RELEASE_BUNDLE:-0}"
# The hosted CI workflow runs verify-invariants.sh in its own parallel job and
# passes --skip-host-invariants.  This is a command-line flag on purpose: an
# environment variable is inherited from a developer's shell or a parent script
# and could silently disable the check in local parity.
SKIP_HOST_INVARIANTS=0

usage() {
  cat <<'EOF'
Usage: job-validate.sh [--profile repo-core|pr-parity|release-preflight] [--skip-host-invariants]

Run the shared validator-backed repository validation job used by local parity
and GitHub CI. The default profile mirrors the standard PR validate lane.
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --profile)
      PROFILE="${2:-}"
      [[ -n "${PROFILE}" ]] || {
        echo "--profile requires a value" >&2
        exit 2
      }
      shift 2
      ;;
    --skip-host-invariants)
      SKIP_HOST_INVARIANTS=1
      shift
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

case "${PROFILE}" in
  repo-core | pr-parity | release-preflight) ;;
  *)
    echo "Unsupported validate profile: ${PROFILE}" >&2
    exit 2
    ;;
esac

if [[ -z "${ARTIFACT_DIR}" ]]; then
  ARTIFACT_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-ci-install.XXXXXX")"
  KEEP_ARTIFACT_DIR=0
else
  mkdir -p "${ARTIFACT_DIR}"
  KEEP_ARTIFACT_DIR=1
fi

cleanup() {
  local status=$?
  [[ -z "${workcell_validate_token_file_created}" || "${workcell_validate_token_file_created}" != "${TMPDIR:-/tmp}"/workcell-github-token.* ]] || rm -f "${workcell_validate_token_file_created}"
  if [[ "${VALIDATOR_IMAGE_OWNED}" -eq 1 ]]; then
    cleanup_workcell_owned_validator_image "${VALIDATOR_IMAGE}" "${VALIDATOR_IMAGE_RESERVATION}"
  fi
  cleanup_workcell_ci_docker
  if [[ "${KEEP_ARTIFACT_DIR}" -eq 0 ]]; then
    rm -rf "${ARTIFACT_DIR}"
  fi
  return "${status}"
}
trap cleanup EXIT

if [[ -z "${VALIDATOR_IMAGE_INPUT}" ]]; then
  claim_workcell_validator_image "${ROOT_DIR}" VALIDATOR_IMAGE VALIDATOR_IMAGE_RESERVATION
  VALIDATOR_IMAGE_OWNED=1
  export WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}"
fi

echo "[ci/validate] pinned input policy"
"${ROOT_DIR}/scripts/check-pinned-inputs.sh"

echo "[ci/validate] Rust vendor tree against crates.io"
"${ROOT_DIR}/scripts/check-rust-vendor.sh"

echo "[ci/validate] validator evasion corpus registration"
"${ROOT_DIR}/scripts/check-validator-anchoring.sh"

echo "[ci/validate] GitHub macOS release test runners"
"${ROOT_DIR}/scripts/verify-github-macos-release-test-runners.sh" macos-26 macos-15

echo "[ci/validate] hardened filesystem ratchet"
"${ROOT_DIR}/scripts/check-hardened-fs.sh"

echo "[ci/validate] shell portability banlist"
"${ROOT_DIR}/scripts/check-shell-portability.sh"

echo "[ci/validate] generated artifact freshness"
"${ROOT_DIR}/scripts/check-generated-artifacts.sh"

echo "[ci/validate] build input manifest determinism"
"${ROOT_DIR}/scripts/verify-build-input-manifest.sh"

echo "[ci/validate] upstream Codex release"
"${ROOT_DIR}/scripts/verify-upstream-codex-release.sh"

echo "[ci/validate] upstream Claude release"
"${ROOT_DIR}/scripts/verify-upstream-claude-release.sh"

echo "[ci/validate] upstream Copilot release"
WORKCELL_COPILOT_RELEASE_HELP_MODE=checksum "${ROOT_DIR}/scripts/verify-upstream-copilot-release.sh"

echo "[ci/validate] upstream Gemini release"
"${ROOT_DIR}/scripts/verify-upstream-gemini-release.sh"

echo "[ci/validate] upstream OpenCode release"
"${ROOT_DIR}/scripts/verify-upstream-opencode-release.sh"

echo "[ci/validate] upstream Amp release"
"${ROOT_DIR}/scripts/verify-upstream-amp-release.sh"

echo "[ci/validate] upstream goose release"
"${ROOT_DIR}/scripts/verify-upstream-goose-release.sh"

if [[ "${PROFILE}" == "release-preflight" ]]; then
  # Pinned-upstream-refresh status: only on the release-preflight
  # profile.  Routine PRs rely on the weekly pin-hygiene.yml cron lane
  # for drift detection so unrelated reviews are not blocked by external
  # upstream movement.
  echo "[ci/validate] pinned upstream refresh status"
  "${ROOT_DIR}/scripts/update-upstream-pins.sh" --check
fi

echo "[ci/validate] validator image build"
BUILT_VALIDATOR_IMAGE="$("${ROOT_DIR}/scripts/ci/build-validator-image.sh")"
if [[ "${BUILT_VALIDATOR_IMAGE}" != "${VALIDATOR_IMAGE}" ]]; then
  echo "Validator image builder returned an unexpected reference" >&2
  exit 1
fi
export WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}"

echo "[ci/validate] repository validation in validator"
WORKCELL_VALIDATE_REPO_PROFILE="${PROFILE}" \
  "${ROOT_DIR}/scripts/ci/run-validate-in-validator.sh"

if [[ "${SKIP_HOST_INVARIANTS}" != "1" ]]; then
  echo "[ci/validate] host launcher invariants"
  "${ROOT_DIR}/scripts/verify-invariants.sh"
fi

if [[ "${PROFILE}" == "release-preflight" ]] && [[ "${SKIP_RELEASE_BUNDLE}" != "1" ]]; then
  echo "[ci/validate] release bundle reproducibility"
  WORKCELL_VALIDATOR_IMAGE="${VALIDATOR_IMAGE}" \
    SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
    WORKCELL_RELEASE_BUNDLE_NAME="workcell-${ARCHIVE_REF}.tar.gz" \
    WORKCELL_RELEASE_BUNDLE_PREFIX="workcell-${ARCHIVE_REF}" \
    WORKCELL_RELEASE_BUNDLE_REF="${ARCHIVE_REF}" \
    "${ROOT_DIR}/scripts/verify-release-bundle.sh"
fi

if [[ "${PROFILE}" != "repo-core" ]]; then
  echo "[ci/validate] install artifact build"
  setup_workcell_ci_docker
  validator_uid="$(id -u)"
  validator_gid="$(id -g)"
  # GitHub-hosted runners are exclusive per-job, so the planted-symlink
  # TOCTOU surface that motivates mktemp in scripts/build-and-test.sh is
  # not reachable here.  Keep the predictable path for CI.
  require_workcell_ci_workspace_mount "${VALIDATOR_IMAGE}" "${ROOT_DIR}"
  validator_workspace_mount="$(workcell_ci_workspace_mount_spec "${ROOT_DIR}" false)"
  validator_home="/tmp/workcell-home-${validator_uid}"
  validator_cache="${validator_home}/.cache"
  validator_tmp="${validator_home}/.tmp"
  bundle_name="workcell-ci-${ARCHIVE_REF}.tar.gz"
  bundle_path="${ARTIFACT_DIR}/${bundle_name}"
  mkdir -p "${ARTIFACT_DIR}"
  # shellcheck disable=SC2016
  workcell_ci_docker run --rm \
    --user "${validator_uid}:${validator_gid}" \
    --entrypoint /bin/bash \
    --mount "${validator_workspace_mount}" \
    -w /workspace \
    -e HOME="${validator_home}" \
    -e XDG_CACHE_HOME="${validator_cache}" \
    -e GOCACHE="${validator_cache}/go-build" \
    -e GOMODCACHE="${validator_cache}/go-mod" \
    -e CARGO_TARGET_DIR="${validator_cache}/cargo-target" \
    -e TMPDIR="${validator_tmp}" \
    -e SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH}" \
    -e ARCHIVE_REF="${ARCHIVE_REF}" \
    -e BUNDLE_NAME="${bundle_name}" \
    -e BUNDLE_PREFIX="workcell-ci-${ARCHIVE_REF}/" \
    "${VALIDATOR_IMAGE}" \
    -lc '
      set -euo pipefail
      mkdir -p "${HOME}" "${XDG_CACHE_HOME}" "${GOCACHE}" "${GOMODCACHE}" "${CARGO_TARGET_DIR}" "${TMPDIR}"
      git -c safe.directory=/workspace -c tar.umask=0022 archive \
        --format=tar \
        --mtime="@${SOURCE_DATE_EPOCH}" \
        --prefix="${BUNDLE_PREFIX}" \
        "${ARCHIVE_REF}" | gzip -n -9
    ' >"${bundle_path}"
  bundle_sha="$(sha256sum "${bundle_path}" | awk '{print $1}')" # portability-exempt: runs only in the Linux CI lane
  "${ROOT_DIR}/scripts/generate-homebrew-formula.sh" \
    "ci-${ARCHIVE_REF}" \
    "${bundle_sha}" \
    "${ARTIFACT_DIR}/workcell.rb" \
    --repository "${REPOSITORY_NAME}"
  # Emit a SHA256SUMS file alongside the bundle so the install-verification
  # job (which downloads the artifact across a job boundary) can
  # `sha256sum -c` the bundle before extraction.  upload-artifact between
  # jobs is internal infra, but pinning the content here means a flaky
  # upload or any future third-job mutation surfaces as a verifiable
  # check rather than as a silent install over a different bundle.
  (
    cd "${ARTIFACT_DIR}"
    sha256sum "${bundle_name}" >SHA256SUMS # portability-exempt: runs only in the Linux CI lane
  )
fi

echo "Workcell shared validate job passed."
