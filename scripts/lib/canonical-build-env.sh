# shellcheck shell=bash
# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 Omkhar Arasaratnam
#
# Establish the Go and Git process environment for each reviewed entrypoint
# that explicitly sources this helper. This is intentionally not a claim about
# ungated workflow roots, arbitrary direct compiler invocations, tool identity,
# or the contents of caller-selected storage paths.

_workcell_canonical_env_reject_nonempty() {
  local name="$1"

  case "${name}" in
    "" | [0123456789]* | *[!abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_]*)
      echo "Canonical build environment rejects an ambient variable with an unsafe identifier." >&2
      return 2
      ;;
  esac
  if [[ -n "${!name-}" ]]; then
    printf 'Canonical build environment rejects ambient %s; rerun with the variable unset.\n' "${name}" >&2
    return 2
  fi
  unset "${name}"
}

_workcell_canonical_env_require_value() {
  local name="$1"
  local expected="$2"
  local actual="${!name-}"

  if [[ -n "${actual}" && "${actual}" != "${expected}" ]]; then
    printf 'Canonical build environment rejects ambient %s; rerun with the variable unset.\n' "${name}" >&2
    return 2
  fi
}

_workcell_canonical_env_is_passive_go_toolcache_alias() {
  local name="$1"

  [[ "${name}" =~ ^GOROOT_[0123456789]+_[0123456789]+_(X64|ARM64)$ ]]
}

workcell_require_modern_privileged_bash() {
  local candidate=""

  if [[ "$-" != *p* ]]; then
    echo "Canonical entrypoint requires privileged Bash mode; execute the script directly." >&2
    return 2
  fi
  ((BASH_VERSINFO[0] >= 4)) && return 0
  for candidate in /opt/homebrew/bin/bash /usr/local/bin/bash; do
    [[ -x "${candidate}" ]] || continue
    if "${candidate}" -p -c '((BASH_VERSINFO[0] >= 4))'; then
      exec "${candidate}" -p "$0" "$@"
    fi
  done
  echo "Canonical entrypoint requires Bash 4 or newer at a trusted absolute path." >&2
  return 2
}

workcell_require_canonical_build_environment() {
  local name=""
  local -a raw_environment_status=()

  # Privileged Bash ignores these startup inputs for this shell but retains
  # them in the exported environment. Reject executable values and remove
  # empty startup variables so an ordinary Bash descendant cannot consume
  # them later. Bash versions disagree about whether retained BASH_FUNC_*%%
  # entries appear in shell variable expansion, so inspect the raw environment
  # through fixed absolute tools and a scrubbed grep environment.
  _workcell_canonical_env_reject_nonempty BASH_ENV || return 2
  _workcell_canonical_env_reject_nonempty ENV || return 2
  if /usr/bin/env | /usr/bin/env -i LC_ALL=C /usr/bin/grep '^BASH_FUNC_' >/dev/null; then
    echo "Canonical build environment rejects ambient exported Bash functions." >&2
    return 2
  else
    raw_environment_status=("${PIPESTATUS[@]}")
    if ((raw_environment_status[0] != 0 || raw_environment_status[1] != 1)); then
      echo "Canonical build environment could not inspect ambient exported Bash functions." >&2
      return 2
    fi
  fi

  # GOFLAGS is rejected wholesale so tags, overlays, and other flag spellings
  # cannot select a different package graph. GOENV=off and GOWORK=off prevent
  # persisted Go configuration and an ambient workspace from restoring inputs.
  _workcell_canonical_env_reject_nonempty GOFLAGS || return 2
  _workcell_canonical_env_require_value GOENV off || return 2
  _workcell_canonical_env_require_value GOWORK off || return 2

  # Fail closed on current, internal, and future Go environment inputs. Only
  # the three storage locations below remain caller-selectable; their contents
  # are a separately documented, lower-assurance dependency.
  for name in "${!GO@}"; do
    [[ -n "${name}" ]] || continue
    case "${name}" in
      GOENV | GOWORK | GOPATH | GOCACHE | GOMODCACHE)
        continue
        ;;
    esac
    # GitHub-hosted runners publish versioned tool-cache aliases such as
    # GOROOT_1_24_X64. They are not Go tool inputs, but leaving them available
    # to descendants would make the canonical environment depend on ambient
    # runner metadata. Accept only the exact passive grammar and remove it.
    if _workcell_canonical_env_is_passive_go_toolcache_alias "${name}"; then
      if ! unset "${name}" 2>/dev/null; then
        printf 'Canonical build environment could not scrub ambient %s.\n' "${name}" >&2
        return 2
      fi
      continue
    fi
    _workcell_canonical_env_reject_nonempty "${name}" || return 2
  done
  for name in "${!CGO@}"; do
    [[ -n "${name}" ]] || continue
    _workcell_canonical_env_reject_nonempty "${name}" || return 2
  done
  for name in CC CXX FC AR GCCGO GCCGOTOOLDIR PKG_CONFIG NETRC GCM_INTERACTIVE; do
    _workcell_canonical_env_reject_nonempty "${name}" || return 2
  done

  # Fail closed on every ambient Git override except the exact canonical
  # values established below. This avoids a version-sensitive denylist:
  # attributes, external helpers, repository/object selection, config,
  # templates, test hooks, and future GIT_* inputs all stay outside the
  # canonical view. Callers may still use a narrowly scoped assignment after
  # this gate (for example, pre-merge's temporary GIT_INDEX_FILE).
  for name in "${!GIT_@}"; do
    [[ -n "${name}" ]] || continue
    case "${name}" in
      GIT_NO_REPLACE_OBJECTS | \
        GIT_CONFIG_NOSYSTEM | \
        GIT_CONFIG_SYSTEM | \
        GIT_CONFIG_GLOBAL | \
        GIT_CONFIG_COUNT | \
        GIT_CONFIG_KEY_0 | \
        GIT_CONFIG_VALUE_0 | \
        GIT_ATTR_NOSYSTEM | \
        GIT_ATTR_SYSTEM | \
        GIT_ATTR_GLOBAL)
        continue
        ;;
    esac
    _workcell_canonical_env_reject_nonempty "${name}" || return 2
  done

  # These exact values are safe and idempotent when one canonical entrypoint
  # invokes another. They suppress ambient system/global config, global
  # attributes, and replacement objects without preventing later trusted,
  # child-scoped fixture assignments.
  _workcell_canonical_env_require_value GIT_NO_REPLACE_OBJECTS 1 || return 2
  _workcell_canonical_env_require_value GIT_CONFIG_NOSYSTEM 1 || return 2
  _workcell_canonical_env_require_value GIT_CONFIG_SYSTEM /dev/null || return 2
  _workcell_canonical_env_require_value GIT_CONFIG_GLOBAL /dev/null || return 2
  _workcell_canonical_env_require_value GIT_CONFIG_COUNT 1 || return 2
  _workcell_canonical_env_require_value GIT_CONFIG_KEY_0 core.attributesFile || return 2
  _workcell_canonical_env_require_value GIT_CONFIG_VALUE_0 /dev/null || return 2
  _workcell_canonical_env_require_value GIT_ATTR_NOSYSTEM 1 || return 2
  _workcell_canonical_env_require_value GIT_ATTR_SYSTEM /dev/null || return 2
  _workcell_canonical_env_require_value GIT_ATTR_GLOBAL /dev/null || return 2

  GOFLAGS=""
  GOENV=off
  GOWORK=off
  GIT_NO_REPLACE_OBJECTS=1
  GIT_CONFIG_NOSYSTEM=1
  GIT_CONFIG_SYSTEM=/dev/null
  GIT_CONFIG_GLOBAL=/dev/null
  GIT_CONFIG_COUNT=1
  GIT_CONFIG_KEY_0=core.attributesFile
  GIT_CONFIG_VALUE_0=/dev/null
  GIT_ATTR_NOSYSTEM=1
  GIT_ATTR_SYSTEM=/dev/null
  GIT_ATTR_GLOBAL=/dev/null
  WORKCELL_CANONICAL_BUILD_ENV=1
  export GOFLAGS GOENV GOWORK
  export GIT_NO_REPLACE_OBJECTS GIT_CONFIG_NOSYSTEM
  export GIT_CONFIG_SYSTEM GIT_CONFIG_GLOBAL GIT_ATTR_NOSYSTEM
  export GIT_CONFIG_COUNT GIT_CONFIG_KEY_0 GIT_CONFIG_VALUE_0
  export GIT_ATTR_SYSTEM GIT_ATTR_GLOBAL
  export WORKCELL_CANONICAL_BUILD_ENV
}
