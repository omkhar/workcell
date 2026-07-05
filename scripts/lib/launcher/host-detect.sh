#!/usr/bin/env -S BASH_ENV= ENV= bash
# shellcheck shell=bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# scripts/lib/launcher/host-detect.sh — host-detection module extracted
# from scripts/workcell as the first increment of the launcher
# decomposition (roadmap item D4).  These helpers normalise the host OS,
# architecture, and (on Linux) distribution/version into the lowercased
# values the launcher's support-matrix logic consumes.  They depend only
# on uname/ps/PPID/env vars and each other — no other launcher function —
# so they are a self-contained, behaviour-preserving unit.  See
# docs/launcher-contract.md for the module contract.

support_matrix_host_override_allowed() {
  local parent_cmdline=""

  [[ "${WORKCELL_VERIFY_INVARIANTS_SANITIZED_ENTRYPOINT:-0}" == "1" ]] || return 1
  if [[ -r "/proc/${PPID}/cmdline" ]]; then
    parent_cmdline="$(tr '\0' ' ' <"/proc/${PPID}/cmdline" 2>/dev/null || true)"
  elif command -v ps >/dev/null 2>&1; then
    parent_cmdline="$(ps -p "${PPID}" -o command= 2>/dev/null || true)"
  fi
  case "${parent_cmdline}" in
    *"scripts/verify-invariants.sh"* | \
      *"tests/scenarios/shared/test-copilot-session-dry-run.sh"* | \
      *"tests/scenarios/shared/test-compat-target-dry-run.sh"* | \
      *"tests/scenarios/shared/test-gcp-remote-vm-dry-run.sh"* | \
      *"tests/scenarios/shared/test-aws-remote-vm-dry-run.sh"*)
      return 0
      ;;
  esac
  return 1
}

detected_host_os() {
  local host_os=""

  # verify-invariants uses a reserved harness-only host override so Linux
  # validation runners can still exercise launch assembly without weakening the
  # real operator-facing support boundary.
  if support_matrix_host_override_allowed && [[ -n "${WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS:-}" ]]; then
    printf '%s\n' "${WORKCELL_TEST_SUPPORT_MATRIX_HOST_OS}"
    return 0
  fi

  host_os="$(uname -s 2>/dev/null || true)"
  case "${host_os}" in
    Darwin)
      printf 'macos\n'
      ;;
    Linux)
      printf 'linux\n'
      ;;
    MINGW* | MSYS* | CYGWIN* | Windows_NT)
      printf 'windows\n'
      ;;
    *)
      printf '%s\n' "$(printf '%s' "${host_os}" | tr '[:upper:]' '[:lower:]')"
      ;;
  esac
}

detected_host_arch() {
  local host_arch=""

  if support_matrix_host_override_allowed && [[ -n "${WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH:-}" ]]; then
    printf '%s\n' "${WORKCELL_TEST_SUPPORT_MATRIX_HOST_ARCH}"
    return 0
  fi

  host_arch="$(uname -m 2>/dev/null || true)"
  case "${host_arch}" in
    arm64 | aarch64)
      printf 'arm64\n'
      ;;
    x86_64 | amd64)
      printf 'amd64\n'
      ;;
    *)
      printf '%s\n' "$(printf '%s' "${host_arch}" | tr '[:upper:]' '[:lower:]')"
      ;;
  esac
}

detected_host_distro() {
  local host_distro=""

  if support_matrix_host_override_allowed && [[ -n "${WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO:-}" ]]; then
    printf '%s\n' "${WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO}"
    return 0
  fi

  if [[ "$(detected_host_os)" != "linux" ]]; then
    printf 'none\n'
    return 0
  fi
  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    host_distro="${ID:-}"
  fi
  if [[ -z "${host_distro}" ]]; then
    host_distro="unknown"
  fi
  printf '%s\n' "$(printf '%s' "${host_distro}" | tr '[:upper:]' '[:lower:]')"
}

detected_host_distro_version() {
  local host_distro_version=""

  if support_matrix_host_override_allowed && [[ -n "${WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO_VERSION:-}" ]]; then
    printf '%s\n' "${WORKCELL_TEST_SUPPORT_MATRIX_HOST_DISTRO_VERSION}"
    return 0
  fi

  if [[ "$(detected_host_os)" != "linux" ]]; then
    printf 'none\n'
    return 0
  fi
  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    . /etc/os-release
    host_distro_version="${VERSION_ID:-${VERSION_CODENAME:-}}"
  fi
  if [[ -z "${host_distro_version}" ]]; then
    host_distro_version="unknown"
  fi
  printf '%s\n' "$(printf '%s' "${host_distro_version}" | tr '[:upper:]' '[:lower:]')"
}
