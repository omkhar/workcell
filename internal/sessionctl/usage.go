// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

// usageText is the canonical help text for `workcell session <cmd>`,
// migrated verbatim from scripts/workcell's session_usage() function.
const usageText = `Usage: workcell session start [launch-options] [-- provider-args...]
       workcell session attach --id SESSION_ID
       workcell session send --id SESSION_ID --message TEXT [--no-newline]
       workcell session stop --id SESSION_ID [--force]
       workcell session list [options]
       workcell session show --id SESSION_ID
       workcell session delete --id SESSION_ID
       workcell session logs --id SESSION_ID --kind audit|debug|file-trace|transcript
       workcell session timeline --id SESSION_ID
       workcell session diff --id SESSION_ID [--output PATH]
       workcell session export --id SESSION_ID [--format json|ocsf] [--output PATH]

Commands:
  start
    Launch a detached background session using the standard Workcell launch options.
    Detached sessions default to ` + "`" + `--session-workspace isolated` + "`" + `.
    --session-workspace direct|isolated
                              Select whether detached sessions clone a clean git workspace
                              or reuse the current workspace path directly

  attach
    --id SESSION_ID           Attach the host terminal to a running detached session
    --no-stdin               Stream output only without forwarding host stdin

  send
    --id SESSION_ID           Send one steering message to a running detached session
    --message TEXT            Message text to inject into the session stdin
    --no-newline              Do not append a trailing newline to the injected message

  stop
    --id SESSION_ID           Stop a running detached session started with ` + "`" + `workcell session start` + "`" + `
    --force                   Force-remove the container instead of a graceful stop

  list
    --workspace PATH          Filter recorded attached and detached sessions to PATH
    --colima-profile NAME     Filter recorded attached and detached sessions to the selected profile
    --json                    Emit machine-readable JSON instead of the default table with live status and control
    --verbose                 Emit a wider text table with target, workspace transport,
                              branch, and worktree metadata

  show
    --id SESSION_ID           Show one recorded session
    --text                    Emit stable key=value lines instead of JSON

  delete
    --id SESSION_ID           Delete one terminal recorded session and clean stopped local artifacts
    --record-only             Delete only the durable session record and keep container/log artifacts
    --dry-run                 Print the planned cleanup without deleting anything

  logs
    --id SESSION_ID           Print the recorded log for one session
    --kind KIND               One of audit, debug, file-trace, or transcript

  timeline
    --id SESSION_ID           Print audit-log timeline entries for one session

  diff
    --id SESSION_ID           Diff the current workspace against the recorded launch git base
    --output PATH             Write the rendered diff bundle to PATH instead of stdout

  export
    --id SESSION_ID           Export one recorded session plus matching audit records
    --format json|ocsf        Output shape: json (default) or ocsf OCSF-mapped JSONL
                              (one Application Lifecycle event per line, redacted)
    --output PATH             Write the exported bundle to PATH instead of stdout

Notes:
  - session commands run on the host and do not start the Workcell runtime.
  - records are durable host-side metadata for detached, completed, or aborted launches.
  - ` + "`" + `session diff` + "`" + ` is read-only and compares the current workspace state to the
    clean launch git base recorded when the session started.
  - ` + "`" + `session diff` + "`" + ` is unavailable when the session started from a dirty git
    worktree, from a workspace without a recorded git base, or from a workspace
    that is not a self-contained git worktree.
  - ` + "`" + `session export` + "`" + ` includes audit records that match the recorded session id.
  - ` + "`" + `session export --format ocsf` + "`" + ` maps the recorded session to an OCSF
    Application Lifecycle event (class 6002) as JSONL, versioned by
    metadata.version and metadata.mapping_version, with credentials and paths
    redacted by the shared support-bundle rule-set.
  - ` + "`" + `session delete` + "`" + ` never rewrites the shared profile audit log.
  - ` + "`" + `session delete` + "`" + ` cleans only explicitly recorded session-owned artifacts and
    refuses running sessions or running session containers.
  - ` + "`" + `session start` + "`" + `, ` + "`" + `session send` + "`" + `, and ` + "`" + `session stop` + "`" + ` emit stable key=value
    summaries so detached-session control stays scriptable on the host.
`

// UsageText returns the canonical `workcell session` help string.
func UsageText() string {
	return usageText
}
