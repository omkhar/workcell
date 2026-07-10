#!/usr/bin/env -S BASH_ENV= ENV= bash

workcell_die() {
  echo "$*" >&2
  exit 2
}

# Only the PID 1 entrypoint path may honor breakglass-style provider flags.
# provider-wrapper.sh always exports WORKCELL_WRAPPER_CONTEXT=1 so nested
# launches cannot re-enable those overrides after the entrypoint policy check.
provider_policy_allows_breakglass() {
  [[ "${WORKCELL_WRAPPER_CONTEXT:-0}" != "1" ]] &&
    [[ "$$" -eq 1 ]] &&
    [[ "${WORKCELL_MODE:-strict}" == "breakglass" ]]
}

effective_codex_profile() {
  local requested_profile="${WORKCELL_MODE:-strict}"

  case "${requested_profile}" in
    strict | development | build)
      printf '%s\n' "${requested_profile}"
      ;;
    *)
      printf 'strict\n'
      ;;
  esac
}

# Normalize a Codex `-c key=value` KEY so equivalent TOML spellings collapse to
# one canonical form before the blocklist match: valid TOML lets a dotted key be
# quoted per segment (features."remote_plugin"), single-quoted (features.'x'), or
# padded with whitespace around the dots (features . plugins). Without this,
# those spellings would slip past every case pattern below — including the
# pre-existing mcp/sandbox/profile blocks. Only the KEY (before the first `=`) is
# normalized; the value is never touched. Segments are split on `.`, trimmed, and
# stripped of ONE matching pair of surrounding double or single quotes. If any
# quote character survives (unbalanced or a quoted segment that itself contained a
# `.` and was split apart), the key is malformed/adversarial and we FAIL CLOSED —
# returning it as blocked rather than guessing its intent.
#
# We deliberately do NOT decode TOML basic-string escapes (a spec parser turns
# `"\u0072emote_plugin"` into `remote_plugin`, `"\u0061pproval_policy"` into
# `approval_policy`, etc.). Re-implementing TOML unescaping in bash would be
# error-prone, so any backslash surviving in a segment is treated as
# malformed/adversarial and FAILS CLOSED — the same posture as a residual quote.
# Bare/quoted keys in the real blocklist never contain backslashes, so this only
# rejects escape-obfuscated keys. Emits the canonical lowercase key on stdout.
codex_normalize_config_key() {
  local key="${1%%=*}"
  local -a segments=()
  local segment normalized="" first=1
  local backslash=$'\\'
  local IFS='.'
  read -r -a segments <<<"${key}"
  for segment in "${segments[@]}"; do
    # Trim surrounding whitespace (handles `features . plugins`).
    segment="${segment#"${segment%%[![:space:]]*}"}"
    segment="${segment%"${segment##*[![:space:]]}"}"
    # Strip one matching pair of surrounding quotes.
    if [[ ${#segment} -ge 2 && "${segment:0:1}" == '"' && "${segment: -1}" == '"' ]]; then
      segment="${segment:1:${#segment}-2}"
    elif [[ ${#segment} -ge 2 && "${segment:0:1}" == "'" && "${segment: -1}" == "'" ]]; then
      segment="${segment:1:${#segment}-2}"
    fi
    # Any residual quote, or any backslash (TOML escape we refuse to decode),
    # => malformed/adversarial => fail closed.
    if [[ "${segment}" == *'"'* || "${segment}" == *"'"* || "${segment}" == *"${backslash}"* ]]; then
      printf '%s\n' '__workcell_malformed__'
      return 0
    fi
    if [[ "${first}" -eq 1 ]]; then
      normalized="${segment}"
      first=0
    else
      normalized="${normalized}.${segment}"
    fi
  done
  printf '%s\n' "${normalized,,}"
}

codex_config_override_is_blocked() {
  local value="$1"
  local key_lower
  key_lower="$(codex_normalize_config_key "${value}")"

  # Fail-closed sentinel for keys that stayed malformed after normalization.
  [[ "${key_lower}" == "__workcell_malformed__" ]] && return 0

  case "${key_lower}" in
    profile | sandbox | sandbox_mode | sandbox_permissions | web_search | approval_policy | project_doc_fallback_filenames | project_root_markers | mcp* | plugins | plugins.* | marketplaces | marketplaces.* | hooks | hooks.* | features.plugins | features.plugin_sharing | features.plugin_hooks | features.remote_plugin | features.remote_control | shell_environment_policy | shell_environment_policy.* | sandbox_workspace_write | sandbox_workspace_write.* | profiles.*.sandbox_mode | profiles.*.approval_policy | profiles.*.web_search | profiles.*.shell_environment_policy | profiles.*.shell_environment_policy.* | profiles.*.sandbox_workspace_write | profiles.*.sandbox_workspace_write.*)
      return 0
      ;;
  esac

  # Codex parses a `-c` value as TOML, so an inline TABLE smuggles blocked child
  # keys under a parent that is not itself blocked above — e.g.
  # `features={remote_plugin=true}` or `profiles={foo={sandbox_mode="danger-full-access"}}`
  # normalize to the bare parent (`features` / `profiles`), which no case matches.
  # When the value (everything after the first `=`, whitespace-trimmed) is an
  # inline table (begins with `{`), block it for every guarded namespace parent:
  # such a table can set any of the banned children. The managed baseline owns
  # these tables, so there is no legitimate `-c` whole-table override. Scalar
  # values (no leading `{`, e.g. model=..., history.persistence="none") are
  # untouched. Guarded parents cover both the gap namespaces (features, profiles,
  # profiles.<name>) and the ones already blocked bare above (kept here so the
  # guard stays robust if a bare-parent case is ever narrowed).
  if [[ "${value}" == *=* ]]; then
    local raw_value="${value#*=}"
    raw_value="${raw_value#"${raw_value%%[![:space:]]*}"}"
    if [[ "${raw_value}" == '{'* ]]; then
      case "${key_lower}" in
        features | plugins | marketplaces | mcp* | hooks | profiles | profiles.* | shell_environment_policy | sandbox_workspace_write)
          return 0
          ;;
      esac
    fi
  fi

  return 1
}

# Value-taking global Codex flags must be consumed before first-subcommand
# detection, or their VALUE would be mistaken for the first command token and
# the subcommand blocklist below would never run (e.g. `--model gpt-5 plugin`
# would treat gpt-5 as the command). Keep the set of value-taking globals in
# this loop in lockstep with codex_first_subcommand in
# runtime/container/provider-wrapper.sh.
reject_unsafe_codex_args() {
  local expect_value=""
  local arg
  local saw_command=0
  local allowed_profile=""

  provider_policy_allows_breakglass && return 0

  # The effective profile is a session property (depends only on WORKCELL_MODE),
  # so resolve it once instead of forking a subshell per --profile occurrence.
  allowed_profile="$(effective_codex_profile)"

  for arg in "$@"; do
    if [[ -n "${expect_value}" ]]; then
      case "${expect_value}" in
        profile)
          [[ "${arg}" != "${allowed_profile}" ]] && workcell_die "Workcell blocked unsafe Codex override: --profile"
          ;;
        cd)
          workcell_die "Workcell blocked unsafe Codex override: --cd"
          ;;
        sandbox)
          [[ "${arg}" == "danger-full-access" ]] && workcell_die "Workcell blocked unsafe Codex override: remove danger-full-access outside breakglass."
          ;;
        config)
          codex_config_override_is_blocked "${arg}" && workcell_die "Workcell blocked unsafe Codex config override: ${arg%%=*}"
          ;;
      esac
      expect_value=""
      continue
    fi

    # A bare `--` ends option/subcommand parsing: every following token is
    # literal prompt text that Codex forwards to the interactive/exec session,
    # never a flag or subcommand (verified: `codex -- plugin` starts the TUI
    # with prompt "plugin" rather than dispatching the plugin subcommand). Stop
    # here so the blocklists do not over-reject a prompt that begins with words
    # like `plugin`/`mcp`. This mirrors codex_first_subcommand in
    # runtime/container/provider-wrapper.sh, which returns at `--`. Flag and
    # value checks BEFORE `--` have already run, so dangerous flags are still
    # rejected; only post-`--` prompt text is exempt.
    if [[ "${arg}" == "--" ]]; then
      break
    fi

    if [[ "${saw_command}" -eq 0 ]] && [[ "${arg}" != -* ]]; then
      saw_command=1
      # plugin (remote marketplace/install) and remote-control (app-server
      # pairing) are never part of the managed GUI backend — the only GUI launch
      # is `codex app-server` (see entrypoint.sh) — so they are blocked on EVERY
      # UI. Keeping them inside the AGENT_UI!=gui guard let an in-container
      # `AGENT_UI=gui codex plugin list` bypass the fence, since AGENT_UI is not
      # pinned/scrubbed at the wrapper boundary.
      case "${arg}" in
        plugin | remote-control)
          workcell_die "Workcell blocked unsupported Codex CLI subcommand: ${arg}"
          ;;
      esac
      # app/app-server are the managed GUI backend; cloud/mcp/sandbox remain
      # GUI-gated as before. This exemption stays scoped to those subcommands.
      if [[ "${AGENT_UI:-cli}" != "gui" ]]; then
        case "${arg}" in
          app | app-server | cloud | mcp | sandbox)
            workcell_die "Workcell blocked unsupported Codex CLI subcommand outside the managed GUI path: ${arg}"
            ;;
        esac
      fi
      continue
    fi

    case "${arg}" in
      # Every Codex `--dangerously-bypass-*` flag is DANGEROUS by codex's own docs
      # (0.143 adds --dangerously-bypass-hook-trust alongside the existing
      # --dangerously-bypass-approvals-and-sandbox); none is valid in managed
      # mode. Glob-match the whole bypass family so future dangerously-bypass
      # flags are covered without a code change (also catches any
      # `--dangerously-bypass-*=value` form). Scope is `--dangerously-bypass-*`,
      # NOT `--dangerously-*`, so it does not swallow non-codex tokens like
      # Claude's `--dangerously-skip-permissions` when they appear as data passed
      # to `codex execpolicy check <command…>`. Keep the remaining explicit unsafe
      # flags in the same case/message.
      --dangerously-bypass-* | --search | --add-dir | --remote | --full-auto | -a | --ask-for-approval | --enable | --disable)
        workcell_die "Workcell blocked unsafe Codex override: ${arg}"
        ;;
      -p | --profile)
        expect_value="profile"
        ;;
      -C | --cd)
        expect_value="cd"
        ;;
      -m | --model | -i | --image | --local-provider | --remote-auth-token-env)
        # Permitted value-taking globals: consume the value so it is never
        # mistaken for the first subcommand (see the function comment). The other
        # value-taking globals (-c/--config, -C/--cd, -s/--sandbox, -p/--profile)
        # are consumed by their own cases above, and --add-dir/--remote/--enable/
        # --disable/-a/--ask-for-approval die on sight before their value is
        # reached, so no value-taking global can desync subcommand detection.
        expect_value="safe"
        ;;
      --ask-for-approval=*)
        workcell_die "Workcell blocked unsafe Codex override: --ask-for-approval"
        ;;
      --add-dir=* | --remote=* | --enable=* | --disable=*)
        workcell_die "Workcell blocked unsafe Codex override: ${arg%%=*}"
        ;;
      --cd=*)
        workcell_die "Workcell blocked unsafe Codex override: --cd"
        ;;
      --profile=*)
        [[ "${arg#--profile=}" != "${allowed_profile}" ]] && workcell_die "Workcell blocked unsafe Codex override: --profile"
        ;;
      -s | --sandbox)
        expect_value="sandbox"
        ;;
      --sandbox=danger-full-access)
        workcell_die "Workcell blocked unsafe Codex override: ${arg}"
        ;;
      -c | --config)
        expect_value="config"
        ;;
      --config=*)
        codex_config_override_is_blocked "${arg#--config=}" && workcell_die "Workcell blocked unsafe Codex config override: ${arg#--config=}"
        ;;
    esac
  done
}

reject_unsafe_claude_args() {
  local arg
  local saw_command=0

  provider_policy_allows_breakglass && return 0

  for arg in "$@"; do
    if [[ "${saw_command}" -eq 0 ]] && [[ "${arg}" != -* ]]; then
      saw_command=1
      case "${arg}" in
        install | update)
          workcell_die "Workcell blocked Claude lifecycle command: ${arg}"
          ;;
      esac
      continue
    fi

    case "${arg}" in
      --dangerously-skip-permissions | --allow-dangerously-skip-permissions | --add-dir | --allowedTools | --mcp-config | --plugin-dir | --settings | --setting-sources | --system-prompt | --append-system-prompt)
        workcell_die "Workcell blocked unsafe Claude override: ${arg}"
        ;;
      --permission-mode | --permission-mode=*)
        workcell_die "Workcell blocked Claude autonomy override: use the host workcell --agent-autonomy option instead."
        ;;
      --add-dir=* | --allowedTools=* | --mcp-config=* | --plugin-dir=* | --settings=* | --setting-sources=* | --system-prompt=* | --append-system-prompt=*)
        workcell_die "Workcell blocked unsafe Claude override: ${arg%%=*}"
        ;;
    esac
  done
}

reject_unsafe_gemini_args() {
  local expect_value=""
  local arg
  local arg_lower=""

  provider_policy_allows_breakglass && return 0

  for arg in "$@"; do
    if [[ -n "${expect_value}" ]]; then
      case "${expect_value}" in
        approval-mode)
          workcell_die "Workcell blocked Gemini autonomy override: use the host workcell --agent-autonomy option instead."
          ;;
      esac
      expect_value=""
      continue
    fi

    arg_lower="${arg,,}"
    case "${arg_lower}" in
      --*dangerously* | --*bypass*permission* | --sandbox | --sandbox=* | --add-dir | --add-dir=* | -y | --yolo)
        workcell_die "Workcell blocked unsafe Gemini override: ${arg}"
        ;;
      --approval-mode)
        expect_value="approval-mode"
        ;;
      --approval-mode=*)
        workcell_die "Workcell blocked Gemini autonomy override: use the host workcell --agent-autonomy option instead."
        ;;
    esac
  done
}

reject_unsafe_copilot_args() {
  local expect_value=""
  local arg
  local arg_lower=""
  local attached_prompt_value=""
  local saw_command=0

  provider_policy_allows_breakglass && return 0

  for arg in "$@"; do
    if [[ -n "${expect_value}" ]]; then
      case "${arg}" in
        -*)
          workcell_die "Workcell blocked unsafe Copilot override: ${arg}"
          ;;
      esac
      expect_value=""
      continue
    fi

    if [[ "${saw_command}" -eq 0 ]] && [[ "${arg}" != -* ]]; then
      saw_command=1
      case "${arg}" in
        init | login | mcp | plugin | skill | update)
          workcell_die "Workcell blocked Copilot lifecycle/control-plane command: ${arg}"
          ;;
      esac
      continue
    fi

    arg_lower="${arg,,}"
    case "${arg_lower}" in
      --acp | --add-dir | --add-github-mcp-tool | --add-github-mcp-toolset | --additional-mcp-config | --agent | --allow-all | --allow-all-mcp-server-instructions | --allow-all-paths | --allow-all-tools | --allow-all-urls | --allow-tool | --allow-url | --attachment | --autopilot | --available-tools | --bash-env | -c | --config-dir | --connect | --continue | --deny-tool | --deny-url | --disable-builtin-mcps | --disable-mcp-server | --disallow-temp-dir | --dynamic-retrieval | --enable-all-github-mcp-tools | --enable-memory | --excluded-tools | --experimental | --extension-sdk-path | --interactive | --log-dir | --max-autopilot-continues | --mode | --name | --no-ask-user | --no-auto-update | --no-bash-env | --no-custom-instructions | --no-remote | --no-remote-export | --output-format | --plan | --plugin-dir | --remote | --remote-export | --resume | --secret-env-vars | --session-id | --share | --share-gist | --worktree | --yolo)
        workcell_die "Workcell blocked unsafe Copilot override: ${arg}"
        ;;
      --acp=* | --add-dir=* | --add-github-mcp-tool=* | --add-github-mcp-toolset=* | --additional-mcp-config=* | --agent=* | --allow-all=* | --allow-all-mcp-server-instructions=* | --allow-all-paths=* | --allow-all-tools=* | --allow-all-urls=* | --allow-tool=* | --allow-url=* | --attachment=* | --autopilot=* | --available-tools=* | --bash-env=* | -c=* | --config-dir=* | --connect=* | --continue=* | --deny-tool=* | --deny-url=* | --disable-builtin-mcps=* | --disable-mcp-server=* | --disallow-temp-dir=* | --dynamic-retrieval=* | --enable-all-github-mcp-tools=* | --enable-memory=* | --excluded-tools=* | --experimental=* | --extension-sdk-path=* | --interactive=* | --log-dir=* | --max-autopilot-continues=* | --mode=* | --name=* | --no-ask-user=* | --no-auto-update=* | --no-bash-env=* | --no-custom-instructions=* | --no-remote=* | --no-remote-export=* | --output-format=* | --plan=* | --plugin-dir=* | --remote=* | --remote-export=* | --resume=* | --secret-env-vars=* | --session-id=* | --share=* | --share-gist=* | --worktree=* | --yolo=*)
        workcell_die "Workcell blocked unsafe Copilot override: ${arg%%=*}"
        ;;
    esac

    case "${arg}" in
      -p | --prompt)
        expect_value="prompt"
        ;;
      -p?*)
        attached_prompt_value="${arg:2}"
        if [[ "${attached_prompt_value}" == -* ]]; then
          workcell_die "Workcell blocked unsafe Copilot override: ${attached_prompt_value}"
        fi
        ;;
      --prompt=*)
        attached_prompt_value="${arg#--prompt=}"
        if [[ "${attached_prompt_value}" == -* ]]; then
          workcell_die "Workcell blocked unsafe Copilot override: ${attached_prompt_value}"
        fi
        ;;
      --model)
        expect_value="safe"
        ;;
      --model=*) ;;
      -C | -i | -n | -r | -w)
        workcell_die "Workcell blocked unsafe Copilot override: ${arg}"
        ;;
      -C?* | -c?* | -i?* | -n?* | -r?* | -w?*)
        workcell_die "Workcell blocked unsafe Copilot override: ${arg:0:2}"
        ;;
      -A | -a)
        workcell_die "Workcell blocked unsafe Copilot override: ${arg}"
        ;;
      -A?* | -a?*)
        workcell_die "Workcell blocked unsafe Copilot override: ${arg:0:2}"
        ;;
      -[!-]?*)
        workcell_die "Workcell blocked bundled Copilot short options: ${arg}"
        ;;
    esac
  done
}

# entrypoint.sh validates the full provider command before launch. After that,
# provider-wrapper.sh calls the per-provider reject helpers directly because it
# has already fixed the launch target and only needs to re-check user argv.
validate_command_args() {
  local expected_command="$1"

  [[ $# -gt 0 ]] || return 0
  shift

  if [[ $# -eq 0 ]]; then
    return 0
  fi

  if [[ "$1" != "${expected_command}" ]]; then
    workcell_die "Workcell blocked non-provider command: $1 (use the host launcher --allow-arbitrary-command path only for lower-assurance boundary debugging)."
  fi

  case "${expected_command}" in
    codex)
      reject_unsafe_codex_args "${@:2}"
      ;;
    claude)
      reject_unsafe_claude_args "${@:2}"
      ;;
    copilot)
      reject_unsafe_copilot_args "${@:2}"
      ;;
    gemini)
      reject_unsafe_gemini_args "${@:2}"
      ;;
  esac
}
