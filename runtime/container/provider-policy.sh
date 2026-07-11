#!/usr/bin/env -S BASH_ENV= ENV= bash

workcell_die() {
  echo "$*" >&2
  exit 2
}

# Only the PID 1 entrypoint may honor breakglass overrides. provider-wrapper.sh
# exports WORKCELL_WRAPPER_CONTEXT=1 so nested launches cannot re-enable them.
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

# Canonicalize a Codex `-c key=value` KEY (before the first `=`) so equivalent TOML
# spellings collapse to one lowercase form before the blocklist match: split on `.`,
# trim whitespace (`features . plugins`), strip ONE surrounding quote pair per segment
# (`features."remote_plugin"`). Any residual quote or backslash (a TOML basic-string
# escape we refuse to decode in bash) FAILS CLOSED — real blocklist keys never contain
# them, so this only rejects obfuscated keys. Emits the canonical key.
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

# Match a NORMALIZED (lowercase) Codex config key against the guarded-namespace
# blocklist. Split out so the profile-scoped path can re-test the post-prefix
# remainder against the SAME set: a profile-v2 layer (`[profiles.<name>.…]`) can
# set any key, so every key dangerous at top level is equally dangerous scoped
# under a profile. Returns 0 (guarded) / 1 (not).
codex_config_key_is_guarded() {
  case "$1" in
    profile | sandbox | sandbox_mode | sandbox_permissions | web_search | approval_policy | project_doc_fallback_filenames | project_root_markers | mcp* | plugins | plugins.* | marketplaces | marketplaces.* | hooks | hooks.* | features.plugins | features.plugin_sharing | features.plugin_hooks | features.remote_plugin | features.remote_control | shell_environment_policy | shell_environment_policy.* | sandbox_workspace_write | sandbox_workspace_write.*)
      return 0
      ;;
  esac
  return 1
}

codex_config_override_is_blocked() {
  local value="$1"
  local key_lower
  key_lower="$(codex_normalize_config_key "${value}")"

  # Fail-closed sentinel for keys that stayed malformed after normalization.
  [[ "${key_lower}" == "__workcell_malformed__" ]] && return 0

  # Top-level guarded key (features.remote_plugin, plugins.*, mcp*, hooks*, …).
  codex_config_key_is_guarded "${key_lower}" && return 0

  # Profile-scoped override: `[profiles.<name>.<rest>]` can set any key, re-enabling
  # surfaces the bare blocks reject. The normalizer guarantees `<name>` is exactly ONE
  # segment (a `.` in a quoted name fails closed above), so strip `profiles.<name>.`
  # and re-test the REMAINDER against the SAME blocklist. `profiles`/`profiles.<name>`
  # with no remainder falls through to the inline-table guard below.
  if [[ "${key_lower}" == profiles.?*.?* ]]; then
    local profile_remainder="${key_lower#profiles.}"
    profile_remainder="${profile_remainder#*.}"
    codex_config_key_is_guarded "${profile_remainder}" && return 0
  fi

  # Codex parses a `-c` value as TOML, so an inline TABLE smuggles blocked children
  # under an unblocked parent: `features={remote_plugin=true}` normalizes to bare
  # `features`, which no case matches. When the value (after the first `=`, trimmed)
  # begins with `{`, block it for every guarded-namespace parent — the managed baseline
  # owns these tables, so no legitimate whole-table `-c` override exists. Scalar values
  # (no leading `{`) are untouched. Parents cover the gap namespaces plus the bare-
  # blocked ones (kept so the guard survives a later narrowing of a bare-parent case).
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

# Guard checks shared by the space-separated and attached/glued short-flag forms so
# `-p val`/`-pval`/`--profile=val` (and the sandbox pair) apply the IDENTICAL check and
# emit BYTE-IDENTICAL deny messages (tests + operators grep them). Each takes the raw
# value plus the resolved allowed profile.
codex_reject_unsafe_profile_value() {
  # Strip a leading `=`: clap accepts the short-with-equals form (`-p=VALUE`),
  # so the glued `-p?*` case passes `=VALUE`; the space-separated and
  # `--profile=` callers pass a bare value (no-op here).
  local value="${1#=}"
  [[ "${value}" != "$2" ]] && workcell_die "Workcell blocked unsafe Codex override: --profile"
}

codex_reject_unsafe_sandbox_value() {
  # Strip a leading `=` for the short-with-equals form (`-s=danger-full-access`);
  # space-separated and `--sandbox=` callers pass a bare value (no-op here).
  local value="${1#=}"
  [[ "${value}" == "danger-full-access" ]] && workcell_die "Workcell blocked unsafe Codex override: remove danger-full-access outside breakglass."
}

# Value-taking global Codex flags must be consumed before first-subcommand detection,
# or their VALUE would be mistaken for the command token (`--model gpt-5 plugin` would
# treat gpt-5 as the command). Keep in lockstep with codex_first_subcommand in
# runtime/container/provider-wrapper.sh.
reject_unsafe_codex_args() {
  local expect_value=""
  local arg
  local saw_command=0
  local allowed_profile=""
  local expect_features_action=0
  local codex_config_value=""
  local saw_app_server=0

  provider_policy_allows_breakglass && return 0

  # Session property (WORKCELL_MODE only): resolve once, not per --profile.
  allowed_profile="$(effective_codex_profile)"

  for arg in "$@"; do
    if [[ -n "${expect_value}" ]]; then
      case "${expect_value}" in
        profile)
          codex_reject_unsafe_profile_value "${arg}" "${allowed_profile}"
          ;;
        cd)
          workcell_die "Workcell blocked unsafe Codex override: --cd"
          ;;
        sandbox)
          codex_reject_unsafe_sandbox_value "${arg}"
          ;;
        config)
          codex_config_override_is_blocked "${arg}" && workcell_die "Workcell blocked unsafe Codex config override: ${arg%%=*}"
          ;;
      esac
      expect_value=""
      continue
    fi

    # DENY-BY-DEFAULT after the permitted `app-server` subcommand (GUI path only): ANY
    # user token following `app-server` dies. The managed GUI launch is EXACTLY bare
    # `codex app-server` with NO trailing user args (entrypoint.sh sets `set -- codex
    # app-server`; the sandbox/approval flags it needs are PREPENDED by provider-
    # wrapper.sh AFTER this check, so they never appear here). A token denylist over
    # pinned 0.142.4 leaked `--listen ws://IP:PORT` (a listening socket), `--stdio`,
    # `-c …`, and AppServer{,Daemon}Subcommand control tokens — all reachable via an
    # AGENT_UI=gui override. Rejecting ANY token is stricter, simpler, and future-proof.
    # Runs BEFORE the `--` break: `app-server` has no `[PROMPT]` positional (usage:
    # `app-server [OPTIONS] [COMMAND]`), so a post-`--` token is still not the managed
    # shape and must die too.
    if [[ "${saw_app_server}" -eq 1 ]]; then
      workcell_die "Workcell blocked unsupported Codex app-server argument: ${arg} (only the managed no-arg app-server launch is permitted)"
    fi

    # After the `features` subcommand, its ACTION token is the next bare token.
    # `features enable/disable <name>` persistently writes features.<name> into the
    # writable config.toml (equivalent to the blocked `-c features.<name>=…`), so both
    # mutating actions are blocked; `features list` is a read-only inspect and stays
    # permitted. Runs BEFORE the `--` break: while armed a bare `--` is skipped WITHOUT
    # disarming, so the following action token is still classified — denying the mutating
    # actions across a `--` even if a future pin dispatches the subcommand after `--`
    # (as app-server's optional `[COMMAND]` already does; pinned 0.142.4 clap-errors on
    # `features -- enable` today).
    if [[ "${expect_features_action}" -eq 1 ]]; then
      # A `--` while armed is NOT the action and MUST NOT trip the general `--`
      # break below: skip it, stay armed, and keep scanning for the action token.
      if [[ "${arg}" == "--" ]]; then
        continue
      fi
      # A flag (e.g. `features --json`) is a `features` option, not the action, so
      # leave the arm intact and fall through to the flag/value cases below.
      if [[ "${arg}" != -* ]]; then
        expect_features_action=0
        case "${arg}" in
          enable | disable)
            workcell_die "Workcell blocked unsupported Codex CLI subcommand: features ${arg}"
            ;;
        esac
        continue
      fi
    fi

    # A bare `--` ends option/subcommand parsing: every following token is literal prompt
    # text Codex forwards (verified: `codex -- plugin` starts the TUI with prompt
    # "plugin"). Stop here so the blocklists do not over-reject a prompt beginning with
    # `plugin`/`mcp`. Mirrors codex_first_subcommand, which also returns at `--`. Flag/
    # value checks before `--` have already run; only post-`--` prompt text is exempt.
    # Not reached while a features action is pending (handled above).
    if [[ "${arg}" == "--" ]]; then
      break
    fi

    if [[ "${saw_command}" -eq 0 ]] && [[ "${arg}" != -* ]]; then
      saw_command=1
      # DENY-BY-DEFAULT over the SUBCOMMAND NAMESPACE. Codex's contract is `codex
      # [OPTIONS] [PROMPT]` OR `codex [OPTIONS] <COMMAND> [ARGS]`: the first bare token is
      # a SUBCOMMAND iff it exactly matches a known name, else it is literal PROMPT text
      # (verified 0.142.4: `codex "fix tests" --version` prints the version). So partition
      # the COMPLETE subcommand set into ALLOW (permit) and DENY (die); a token in NEITHER
      # is prompt text, permitted as Codex treats it — preserving the bare-prompt
      # invocation (Codex P2 review) while denying every dangerous subcommand by exact
      # token.
      #
      # ALLOW (read-only/session surface, verified against 0.142.4 `--help`); `execpolicy`
      # is hidden but real — a read-only command classifier the managed autonomy path and
      # verify-invariants invoke, so it MUST stay permitted. Exact-token discipline (NOT
      # globs) keeps the fence tight: `exec` != `exec-server`, `mcp` != `mcp-server`. Keep
      # in lockstep with codex_managed_profile_applies, codex_first_subcommand, and the
      # fixture tests/fixtures/codex-subcommands.txt (verify-invariants asserts the full
      # subcommand list partitions into this ALLOW set plus the DENY set below, so a
      # future pin adding an UNCLASSIFIED subcommand fails CI until classified).
      case "${arg}" in
        exec | e | review | login | logout | completion | doctor | \
          apply | a | resume | fork | archive | unarchive | delete | \
          help | execpolicy)
          continue
          ;;
        features)
          # `features`/`features list` are read-only inspects; arm the action check so
          # the next bare token (enable/disable) is denied above.
          expect_features_action=1
          continue
          ;;
        app | app-server)
          # app-server is the managed GUI backend, permitted ONLY under AGENT_UI=gui;
          # on the CLI path it is denied. (`app` is not a 0.142.4 subcommand but is GUI-
          # gated for the same class if a later pin reintroduces it.) The gate is scoped
          # to these two tokens; every DENY subcommand below is denied on every UI, so an
          # AGENT_UI=gui override cannot smuggle in plugin/mcp/remote-control.
          [[ "${AGENT_UI:-cli}" != "gui" ]] &&
            workcell_die "Workcell blocked unsupported Codex CLI subcommand outside the managed GUI path: ${arg}"
          # Arm the app-server surface scan (block near the top of the loop).
          saw_app_server=1
          continue
          ;;
        # DENY set — every known-dangerous/unsupported subcommand, denied by EXACT token
        # on every UI. Enumerated against 0.142.4 so ALLOW ∪ GUI-gated ∪ DENY equals the
        # complete subcommand list (the fixture completeness check enforces this).
        # Control-plane, daemon, marketplace, sandbox-escape, and self-update surfaces
        # the managed session must never reach. `debug` is denied too: its
        # second-level subcommands are not read-only (`debug app-server` reaches the
        # app-server test client, `debug clear-memories` mutates local memory), and the
        # managed path never uses it.
        plugin | remote-control | exec-server | mcp | mcp-server | cloud | \
          cloud-tasks | responses-api-proxy | stdio-to-uds | sandbox | update | debug)
          # cloud-tasks is the 0.142.4 alias of `cloud`; responses-api-proxy and stdio-to-
          # uds are HIDDEN daemon/bridge subcommands the clap enum still dispatches.
          # `update` is not a 0.142.4 variant (lands in 0.143); kept forward-compat and
          # absent from the fixture (the completeness check asserts fixture ⊆ classified).
          workcell_die "Workcell blocked unsupported Codex CLI subcommand: ${arg}"
          ;;
      esac
      # Not a known subcommand (neither ALLOW, GUI-gated, nor DENY): it is PROMPT text,
      # permitted as Codex would. `saw_command=1` stops any later bare token from being
      # re-checked — Codex dispatches only on the FIRST bare token. Deny-by-default holds
      # because the fixture completeness check guarantees no UNCLASSIFIED subcommand
      # falls through here undetected.
      continue
    fi

    case "${arg}" in
      # Every `--dangerously-bypass-*` flag is DANGEROUS (0.143 adds --dangerously-bypass-
      # hook-trust); glob the whole family (incl. `=value`) so future bypass flags need no
      # code change. Scope is `--dangerously-bypass-*`, NOT `--dangerously-*`, so it does
      # not swallow Claude's `--dangerously-skip-permissions` passed as data to `codex
      # execpolicy check`. --yolo is Codex's hidden alias for --dangerously-bypass-
      # approvals-and-sandbox (the glob does not reach a hidden alias, so block it and its
      # =value form explicitly).
      --dangerously-bypass-* | --yolo | --yolo=* | --search | --add-dir | --remote | --remote-auth-token-env | --full-auto | -a | --ask-for-approval | --enable | --disable)
        workcell_die "Workcell blocked unsafe Codex override: ${arg}"
        ;;
      # ATTACHED/GLUED short value-flags (Codex P1 review). Codex (clap) also accepts a
      # short flag glued to its value (`-pval`/`-sval`/`-Cval`/`-aval`); without these the
      # glued form falls through as an unrecognized token and is FORWARDED, so
      # `-pbreakglass`/`-sdanger-full-access`/`-anever`/`-C/state` bypass the guards the
      # space-separated forms apply. Placed BEFORE the exact-token cases and matched by
      # exact letter (case-sensitive: `-C?*` never catches `-c?*`), so they shadow nothing.
      -a?*)
        workcell_die "Workcell blocked unsafe Codex override: ${arg}"
        ;;
      -C?*)
        workcell_die "Workcell blocked unsafe Codex override: --cd"
        ;;
      -p?*)
        codex_reject_unsafe_profile_value "${arg#-p}" "${allowed_profile}"
        ;;
      -s?*)
        codex_reject_unsafe_sandbox_value "${arg#-s}"
        ;;
      -p | --profile)
        expect_value="profile"
        ;;
      -C | --cd)
        expect_value="cd"
        ;;
      -m | --model | -i | --image | --local-provider)
        # Permitted value-taking globals: consume the value so it is never mistaken for
        # the first subcommand. The other value-taking globals are consumed by their own
        # cases above, and the unsafe ones die on sight before their value is reached, so
        # none can desync subcommand detection.
        expect_value="safe"
        ;;
      --ask-for-approval=*)
        workcell_die "Workcell blocked unsafe Codex override: --ask-for-approval"
        ;;
      --add-dir=* | --remote=* | --remote-auth-token-env=* | --enable=* | --disable=*)
        workcell_die "Workcell blocked unsafe Codex override: ${arg%%=*}"
        ;;
      --cd=*)
        workcell_die "Workcell blocked unsafe Codex override: --cd"
        ;;
      --profile=*)
        codex_reject_unsafe_profile_value "${arg#--profile=}" "${allowed_profile}"
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
        # Short config flag glued to its value: Codex accepts `-cKEY=VALUE` and `-c=KEY=
        # VALUE` (clap's short-flag `=` separator). Without this case the attached form
        # skipped the blocklist (only `-c <value>` and `--config=<value>` were routed),
        # letting `-cfeatures.remote_plugin=true` re-enable pinned-off surfaces (Codex P1
        # review). Strip the `-c` prefix and one optional `=`, then run the SAME check.
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
