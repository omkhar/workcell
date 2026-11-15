#!/usr/bin/env -S BASH_ENV= ENV= bash
# Re-derive the toolchain upstream-refresh candidate from its base.
#
# Usage: upstream-refresh-rederive.sh CANDIDATE_DIR OUTPUT_FILE
#
# Run from a clean checkout of the candidate base_sha, in a job apart from the
# refresh job, with a read-only token. CANDIDATE_DIR is the toolchain
# directory of the candidate artifact. The script runs the toolchain updater
# again, the way the refresh job does. The updater resolves the Debian
# snapshot itself, so the candidate does not choose it. The script requires
# that snapshot to equal the candidate's, and the tree it gets to equal the
# candidate tree_oid. Then it appends verdict=rederived:<base sha>:<tree oid>
# to OUTPUT_FILE, the step output file.
#
# The scope guard checks that each changed line is a pin line. This check
# binds each pin value, digests and checksums included, to what upstream
# publishes now. When upstream moved since the refresh, for example a new
# Debian snapshot after midnight UTC, the trees differ and the candidate waits
# for a person.
set -euo pipefail

usage() {
  echo "Usage: $0 CANDIDATE_DIR OUTPUT_FILE" >&2
  exit 2
}

[[ $# -eq 2 ]] || usage
candidate_dir="$1"
output="$2"
[[ ${candidate_dir} == /* ]] || candidate_dir="${PWD}/${candidate_dir}"
[[ ${output} == /* ]] || output="${PWD}/${output}"
# Only this script writes the verdict, at the end. Children do not inherit the
# runner command files; that is hygiene, since one could still find them
# through /proc. The verdict holds because the job reads it only on a step that
# succeeded, this script exits 0 only on the candidate tree, and the publisher
# checks its base and tree against the candidate.
unset GITHUB_OUTPUT GITHUB_ENV GITHUB_PATH GITHUB_STATE GITHUB_STEP_SUMMARY

die() {
  echo "upstream-refresh-rederive: $*" >&2
  exit 1
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "${ROOT_DIR}"

# A run with only a provider candidate has nothing to re-derive. It prints no
# verdict, so a toolchain candidate could not publish on it.
if [[ ! -e "${candidate_dir}" && ! -L "${candidate_dir}" ]]; then
  echo "upstream-refresh-rederive: no toolchain candidate to re-derive"
  exit 0
fi

metadata="${candidate_dir}/metadata.json"
patch="${candidate_dir}/patch"
[[ -f "${metadata}" && ! -L "${metadata}" && -f "${patch}" && ! -L "${patch}" ]] || die "no toolchain candidate in ${candidate_dir}"
meta() { jq -r "$1 // \"\"" "${metadata}"; }
[[ "$(meta .candidate)" == toolchain ]] || die "the candidate is not a toolchain candidate"
base_sha="$(meta .base_sha)"
tree_oid="$(meta .tree_oid)"
[[ "${base_sha}" =~ ^[0-9a-f]{40}$ && "${tree_oid}" =~ ^[0-9a-f]{40}$ ]] || die "the candidate metadata has no base or tree"
patch_sha256="$(shasum -a 256 "${patch}" | awk '{print $1}')"
[[ "${patch_sha256}" == "$(meta .patch_sha256)" ]] || die "candidate patch digest mismatch"
head_sha="$(git rev-parse --verify "HEAD^{commit}")" || die "cannot read the checkout HEAD"
[[ "${head_sha}" == "${base_sha}" ]] || die "checkout ${head_sha} is not at candidate base ${base_sha}"
status="$(git status --porcelain --untracked-files=all)" || die "cannot read the checkout status"
[[ -z "${status}" ]] || die "the checkout is not clean"

# The candidate's Debian snapshot: the one its bootstrap manifest change sets,
# or else the base pin, which the refresh held.
manifest=runtime/container/debian-bootstrap.env
snapshot="$(awk -v header="diff --git a/${manifest} b/${manifest}" '
  /^diff --git / { in_manifest = ($0 == header); next }
  in_manifest && /^\+DEBIAN_SNAPSHOT=/ { print substr($0, 18) }
' "${patch}")"
if [[ -z "${snapshot}" ]]; then
  snapshot="$(awk '/^DEBIAN_SNAPSHOT=/ { print substr($0, 17) }' "${manifest}")"
fi
[[ "${snapshot}" =~ ^[0-9]{8}T000000Z$ ]] || die "unable to read exactly one Debian snapshot from the candidate"

# No --debian-snapshot: the updater looks the snapshot up, as in the refresh
# job. --check reuses the plan of --apply through WORKCELL_DEBIAN_PLAN_RECORD.
./scripts/update-upstream-pins.sh --apply --toolchain-only
./scripts/update-upstream-pins.sh --check --toolchain-only
./scripts/check-pinned-inputs.sh

resolved="$(awk '/^DEBIAN_SNAPSHOT=/ { print substr($0, 17) }' "${manifest}")"
[[ "${resolved}" =~ ^[0-9]{8}T000000Z$ ]] || die "the updater left no single Debian snapshot in ${manifest}"
if [[ "${resolved}" != "${snapshot}" ]]; then
  echo "::notice::The re-derived Debian snapshot ${resolved} differs from the candidate snapshot ${snapshot}."
  die "re-derived Debian snapshot ${resolved} is not candidate snapshot ${snapshot}"
fi

# The tree as upstream-refresh-candidate.sh computes it, in a private index.
index="$(mktemp "${RUNNER_TEMP:-${TMPDIR:-/tmp}}/upstream-refresh-rederive-index.XXXXXX")"
rm -f "${index}"
trap 'rm -f "${index}"' EXIT
GIT_INDEX_FILE="${index}" git read-tree HEAD
GIT_INDEX_FILE="${index}" git add -A
tree="$(GIT_INDEX_FILE="${index}" git write-tree)" || die "cannot write the re-derived tree"
if [[ "${tree}" != "${tree_oid}" ]]; then
  echo "::notice::The re-derived toolchain tree ${tree} differs from the candidate tree ${tree_oid}. Upstream may have moved since the refresh."
  die "re-derived tree ${tree} is not candidate tree ${tree_oid}"
fi
printf 'verdict=rederived:%s:%s\n' "${base_sha}" "${tree}" >>"${output}"
