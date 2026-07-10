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
  local expect_features_action=0
  local codex_config_value=""

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

    # After the first subcommand `features`, its ACTION token (enable/disable/
    # list) is the next bare token. `features enable/disable <name>` persistently
    # writes features.<name>=true/false into the writable CODEX_HOME/config.toml
    # (equivalent to the blocked `-c features.<name>=…`), re-enabling e.g.
    # plugins/remote_plugin for the in-TUI browser, so both mutating actions are
    # blocked. `features list` is a read-only inspect (used by the managed
    # validation path) and stays permitted; the managed baseline sets features
    # declaratively in config, so it never needs the imperative toggle.
    if [[ "${expect_features_action}" -eq 1 ]] && [[ "${arg}" != -* ]]; then
      expect_features_action=0
      case "${arg}" in
        enable | disable)
          workcell_die "Workcell blocked unsupported Codex CLI subcommand: features ${arg}"
          ;;
      esac
      continue
    fi

    if [[ "${saw_command}" -eq 0 ]] && [[ "${arg}" != -* ]]; then
      saw_command=1
      # DENY-BY-DEFAULT over the SUBCOMMAND NAMESPACE. Codex's CLI contract is
      # `codex [OPTIONS] [PROMPT]` OR `codex [OPTIONS] <COMMAND> [ARGS]`: the
      # first bare token is a SUBCOMMAND iff it exactly matches one of Codex's
      # known subcommand names, otherwise it is literal PROMPT text that Codex
      # forwards to the session (empirically on pinned 0.142.4:
      # `codex "fix tests" --version` prints the version and exits 0, treating
      # "fix tests" as the prompt, never as a command). So we partition Codex's
      # COMPLETE subcommand set into an ALLOW set (permit — classified safe) and
      # a DENY set (die — dangerous/unsupported); a token in NEITHER set is not a
      # subcommand at all and is permitted as prompt text, exactly as Codex
      # treats it. This preserves the bare-prompt invocation
      # `workcell --agent codex --agent-arg "fix tests"` (Codex P2 review) while
      # still denying every known-dangerous subcommand by exact token.
      #
      # ALLOW (read-only/session surface, verified against the pinned runtime
      # Codex 0.142.4 `codex --help`): exec + its `e` alias, review, login,
      # logout, completion, doctor, apply + its `a` alias, resume, fork, archive,
      # unarchive, delete, help, debug. These take a fixed image and only read
      # state or drive an in-session/session-management flow. `execpolicy` is a
      # hidden (not in `--help`) but real subcommand present in the pinned
      # runtime Codex: it is a pure read-only command classifier
      # (`codex execpolicy check --rules … <cmd>` returns a JSON allow/prompt/
      # forbid decision, mutating nothing) that the managed prompt-autonomy path
      # and verify-invariants both invoke — it MUST stay permitted or the
      # session-rule enforcement breaks (empirically: omitting it fails
      # container-smoke's execpolicy checks). Exact-token discipline (NOT globs)
      # keeps the fence tight: `exec` != `exec-server`, `mcp` != `mcp-server`,
      # so the daemon variants stay denied. Keep this list in lockstep with the
      # non-runtime set in codex_managed_profile_applies, the value-taking
      # globals in codex_first_subcommand (runtime/container/provider-wrapper.sh;
      # same first-token detection), and the classified subcommand fixture
      # tests/fixtures/codex-subcommands.txt (verify-invariants asserts the
      # pinned Codex's full subcommand list is fully partitioned into this ALLOW
      # set plus the DENY set below, so a future pin that adds an UNCLASSIFIED
      # subcommand — which would otherwise leak through as prompt text — fails
      # CI until a human classifies it).
      case "${arg}" in
        exec | e | review | login | logout | completion | doctor | \
          apply | a | resume | fork | archive | unarchive | delete | \
          help | debug | execpolicy)
          continue
          ;;
        features)
          # `features` bare and `features list` are read-only inspects and stay
          # permitted; arm the action check so the next bare token (enable/
          # disable) is caught and denied by the block above.
          expect_features_action=1
          continue
          ;;
        app | app-server)
          # app-server is the managed GUI backend Workcell launches (see
          # entrypoint.sh), so it is permitted ONLY under AGENT_UI=gui; on the
          # CLI path it is not a supported surface and is denied. (`app` is not a
          # subcommand on pinned 0.142.4 but is kept GUI-gated here for the same
          # class so a later pin that reintroduces it stays covered.) The GUI
          # gate is scoped to these two tokens; every DENY-set subcommand below
          # is denied on every UI, so an in-container AGENT_UI=gui override
          # cannot smuggle in plugin/mcp/remote-control the way a GUI-gated
          # blocklist once let it.
          [[ "${AGENT_UI:-cli}" != "gui" ]] &&
            workcell_die "Workcell blocked unsupported Codex CLI subcommand outside the managed GUI path: ${arg}"
          continue
          ;;
        # DENY set — every known-dangerous/unsupported Codex subcommand, denied
        # by EXACT token on every UI. Enumerated against pinned 0.142.4 so that
        # ALLOW ∪ GUI-gated ∪ DENY equals the complete subcommand list (the
        # fixture completeness check enforces this). These are the control-plane,
        # daemon, marketplace, sandbox-escape, and self-update surfaces that the
        # managed session must never reach.
        plugin | remote-control | exec-server | mcp | mcp-server | cloud | \
          cloud-tasks | responses-api-proxy | stdio-to-uds | sandbox | update)
          # cloud-tasks is the pinned-0.142.4 alias of `cloud` (the hosted
          # control-plane surface); responses-api-proxy and stdio-to-uds are
          # HIDDEN (not in `codex --help`) daemon/bridge subcommands the pinned
          # clap Subcommand enum still dispatches on — all denied by exact token
          # on every UI. `update` is not a 0.142.4 variant (it lands in 0.143);
          # it is kept here as harmless forward-compat and is intentionally
          # absent from tests/fixtures/codex-subcommands.txt (that fixture lists
          # only real 0.142.4 tokens), which the completeness check tolerates
          # because it only asserts fixture ⊆ classified, not equality.
          workcell_die "Workcell blocked unsupported Codex CLI subcommand: ${arg}"
          ;;
      esac
      # Not a known subcommand (neither ALLOW, GUI-gated, nor DENY): it is PROMPT
      # text, so permit it exactly as Codex would. `saw_command=1` above stops
      # any later bare token from being re-checked as a subcommand — Codex only
      # dispatches on the FIRST bare token, and everything after a prompt is
      # prompt/argument data. Deny-by-default safety is preserved because the
      # fixture completeness check guarantees no UNCLASSIFIED subcommand exists to
      # fall through here undetected.
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
      # flags in the same case/message. --yolo is Codex's documented alias for
      # --dangerously-bypass-approvals-and-sandbox (it is a hidden alias, so the
      # --dangerously-bypass-* glob does not reach it — block the alias and its
      # =value form explicitly).
      --dangerously-bypass-* | --yolo | --yolo=* | --search | --add-dir | --remote | --full-auto | -a | --ask-for-approval | --enable | --disable)
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
      -c?*)
        # Short config flag glued to its value: Codex accepts both `-cKEY=VALUE`
        # (`-cfeatures.remote_plugin=true`) and `-c=KEY=VALUE` (the `=` is clap's
        # short-flag value separator, so `-c=model=x` carries value `model=x`).
        # Without this case the attached form skipped the blocklist entirely —
        # only separate `-c <value>` and `--config=<value>` were routed — letting
        # `-cfeatures.remote_plugin=true` / `-cfeatures={remote_plugin=true}`
        # re-enable the very surfaces this gate pins off (Codex P1 review). Strip
        # the `-c` prefix and one optional leading `=`, then run the SAME
        # codex_config_override_is_blocked check as the other config forms.
        codex_config_value="${arg#-c}"
        codex_config_value="${codex_config_value#=}"
        codex_config_override_is_blocked "${codex_config_value}" &&
          workcell_die "Workcell blocked unsafe Codex config override: ${codex_config_value}"
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
