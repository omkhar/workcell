// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//! Git control-plane policy: pure argument, inline-config, and environment
//! classification for the exec guard. These functions decide whether a `git`
//! invocation carries an unsafe control-plane override (config keys, path
//! overrides, `--no-verify`, or ambient `GIT_*`/editor/pager environment). They
//! hold no `unsafe` code and no ABI surface; the interposed exec functions in
//! `lib.rs` call into them.

use crate::is_git_path;

pub(crate) fn git_config_key_has_prefix_and_suffix(key: &str, prefix: &str, suffix: &str) -> bool {
    // `prefix`/`suffix` are ASCII, so slice with char-boundary-safe `get`: a git
    // config key with a multibyte character straddling the byte offset returns
    // `None` (correctly no match) instead of panicking on a non-boundary index.
    key.len() > prefix.len() + suffix.len()
        && key
            .get(..prefix.len())
            .is_some_and(|head| head.eq_ignore_ascii_case(prefix))
        && key
            .get(key.len() - suffix.len()..)
            .is_some_and(|tail| tail.eq_ignore_ascii_case(suffix))
}

pub(crate) fn git_config_key_is_blocked(key: &str) -> bool {
    matches!(
        key.to_ascii_lowercase().as_str(),
        "core.askpass"
            | "core.editor"
            | "core.fsmonitor"
            | "core.hookspath"
            | "core.pager"
            | "core.sshcommand"
            | "core.worktree"
            | "credential.helper"
            | "diff.external"
            | "include.path"
            | "sequence.editor"
    ) || git_config_key_has_prefix_and_suffix(key, "credential.", ".helper")
        || git_config_key_has_prefix_and_suffix(key, "includeif.", ".path")
        || (key.len() > 6
            && key
                .get(..6)
                .is_some_and(|head| head.eq_ignore_ascii_case("pager.")))
}

pub(crate) fn git_config_spec_is_blocked(spec: &str) -> bool {
    let key = spec.split_once('=').map(|(key, _)| key).unwrap_or(spec);
    let value = spec.split_once('=').map(|(_, value)| value);
    if let Some(value) = value
        && git_config_spec_value_is_explicit_safe(key, value)
    {
        return false;
    }
    !key.is_empty() && git_config_key_is_blocked(key)
}

pub(crate) fn git_config_spec_value_is_explicit_safe(key: &str, value: &str) -> bool {
    key.eq_ignore_ascii_case("core.fsmonitor")
        && matches!(
            value.to_ascii_lowercase().as_str(),
            "" | "false" | "0" | "no" | "off"
        )
}

#[cfg(test)]
pub(crate) fn should_block(path: &str, args: &[String]) -> bool {
    should_block_reason(path, args).is_some()
}

pub(crate) fn should_block_reason(path: &str, args: &[String]) -> Option<&'static str> {
    if !is_git_path(path) || args.is_empty() {
        return None;
    }

    let mut saw_commit = false;
    let mut expect_config_arg = false;
    let mut expect_path_override_arg: Option<GitPathOverrideKind> = None;
    let mut expect_chdir_arg = false;

    for arg in args.iter().skip(1) {
        if expect_chdir_arg {
            expect_chdir_arg = false;
            continue;
        }

        if let Some(kind) = expect_path_override_arg.take() {
            if !git_path_override_is_allowed(kind, arg) {
                return Some(git_path_override_reason(kind));
            }
            continue;
        }

        if expect_config_arg {
            expect_config_arg = false;
            if git_config_spec_is_blocked(arg) {
                return Some("unsafe-inline-config");
            }
            continue;
        }

        if arg == "--" {
            break;
        }
        if saw_commit && git_commit_short_arg_invokes_no_verify(arg) {
            return Some("unsafe-commit-short-no-verify");
        }

        match arg.as_str() {
            "-c" | "--config-env" => {
                expect_config_arg = true;
                continue;
            }
            "-C" => {
                expect_chdir_arg = true;
                continue;
            }
            "--exec-path" => return Some("unsafe-exec-path"),
            "--no-verify" => return Some("unsafe-no-verify"),
            "--git-dir" => {
                expect_path_override_arg = Some(GitPathOverrideKind::GitDir);
                continue;
            }
            "--work-tree" => {
                expect_path_override_arg = Some(GitPathOverrideKind::WorkTree);
                continue;
            }
            "commit" => saw_commit = true,
            _ => {}
        }

        if let Some(spec) = arg.strip_prefix("--config-env=")
            && git_config_spec_is_blocked(spec)
        {
            return Some("unsafe-config-env");
        }
        if arg.starts_with("--exec-path=") {
            return Some("unsafe-exec-path");
        }
        if let Some(value) = arg.strip_prefix("--git-dir=")
            && !git_path_override_is_allowed(GitPathOverrideKind::GitDir, value)
        {
            return Some("unsafe-git-dir");
        }
        if let Some(value) = arg.strip_prefix("--work-tree=")
            && !git_path_override_is_allowed(GitPathOverrideKind::WorkTree, value)
        {
            return Some("unsafe-work-tree");
        }
    }

    None
}

#[derive(Clone, Copy)]
enum GitPathOverrideKind {
    GitDir,
    WorkTree,
}

fn git_path_override_reason(kind: GitPathOverrideKind) -> &'static str {
    match kind {
        GitPathOverrideKind::GitDir => "unsafe-git-dir",
        GitPathOverrideKind::WorkTree => "unsafe-work-tree",
    }
}

fn git_path_override_is_allowed(kind: GitPathOverrideKind, value: &str) -> bool {
    let value = value.trim_end_matches('/');
    match kind {
        GitPathOverrideKind::GitDir => value == "/workspace/.git",
        GitPathOverrideKind::WorkTree => value == "/workspace",
    }
}

pub(crate) fn git_commit_short_arg_invokes_no_verify(arg: &str) -> bool {
    if !arg.starts_with('-') || arg.starts_with("--") || arg.len() <= 1 {
        return false;
    }

    for ch in arg[1..].chars() {
        match ch {
            'n' => return true,
            'm' | 'F' | 'C' | 'c' | 't' | 'u' | 'S' => return false,
            _ => {}
        }
    }

    false
}

pub(crate) fn env_has_unsafe_git_override(env_entries: &[String]) -> bool {
    let mut count = 0usize;

    for entry in env_entries {
        // Inline argv config may disable fsmonitor with explicit false-ish
        // values, but env-sourced Git config stays fail-closed because it is
        // inherited ambiently.
        if let Some(value) = entry.strip_prefix("GIT_CONFIG_PARAMETERS=") {
            let lower = value.to_ascii_lowercase();
            if lower.contains("core.askpass")
                || lower.contains("core.editor")
                || lower.contains("core.fsmonitor")
                || lower.contains("core.hookspath")
                || lower.contains("core.pager")
                || lower.contains("core.sshcommand")
                || lower.contains("core.worktree")
                || lower.contains("credential.helper")
                || lower.contains("diff.external")
                || lower.contains("include.path")
                || lower.contains("includeif.")
                || lower.contains("pager.")
                || lower.contains("sequence.editor")
            {
                return true;
            }
        }

        if entry.starts_with("GIT_DIR=")
            || entry.starts_with("GIT_WORK_TREE=")
            || entry.starts_with("GIT_COMMON_DIR=")
            || entry.starts_with("GIT_EXEC_PATH=")
            || entry.starts_with("GIT_OBJECT_DIRECTORY=")
            || entry.starts_with("GIT_ALTERNATE_OBJECT_DIRECTORIES=")
            || entry.starts_with("GIT_INDEX_FILE=")
            || entry.starts_with("GIT_ASKPASS=")
            || entry.starts_with("GIT_EDITOR=")
            || entry.starts_with("GIT_EXTERNAL_DIFF=")
            || entry.starts_with("GIT_CONFIG_SYSTEM=")
            || entry.starts_with("GIT_PAGER=")
            || entry.starts_with("GIT_SEQUENCE_EDITOR=")
            || entry.starts_with("GIT_SSH=")
            || entry.starts_with("GIT_SSH_COMMAND=")
            || entry.starts_with("SSH_ASKPASS=")
            || entry.starts_with("EDITOR=")
            || entry.starts_with("PAGER=")
            || entry.starts_with("VISUAL=")
        {
            return true;
        }

        if let Some(value) = entry.strip_prefix("GIT_CONFIG_GLOBAL=")
            && value != "/dev/null"
        {
            return true;
        }

        if let Some(value) = entry.strip_prefix("GIT_CONFIG_NOSYSTEM=")
            && value != "1"
        {
            return true;
        }

        if let Some(value) = entry.strip_prefix("GIT_CONFIG_COUNT=") {
            count = value.parse::<usize>().unwrap_or(0);
        }
    }

    if count == 0 {
        return false;
    }

    env_entries.iter().any(|entry| {
        entry
            .strip_prefix("GIT_CONFIG_KEY_")
            .and_then(|value| value.split_once('=').map(|(_, key)| key))
            .is_some_and(git_config_key_is_blocked)
    })
}
