// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

#[path = "common/launcher_common.rs"]
mod launcher_common;

use std::env;
use std::ffi::{CString, OsStr, OsString};
use std::os::unix::ffi::OsStrExt;
use std::process;

const LAUNCH_TARGETS: &[(&str, &str)] = &[
    (
        "workcell-entrypoint",
        "/usr/local/libexec/workcell/entrypoint.sh",
    ),
    ("git", "/usr/local/libexec/workcell/git-wrapper.sh"),
    ("node", "/usr/local/libexec/workcell/node-wrapper.sh"),
    ("codex", "/usr/local/libexec/workcell/provider-wrapper.sh"),
    ("claude", "/usr/local/libexec/workcell/provider-wrapper.sh"),
    ("gemini", "/usr/local/libexec/workcell/provider-wrapper.sh"),
];

#[derive(Debug)]
struct LaunchRequest {
    target_name: &'static str,
    script_path: &'static str,
    exec_args: Vec<CString>,
}

#[derive(Debug)]
enum LaunchError {
    UnsupportedTarget(String),
    NulArgument,
}

fn lookup_target(argv0: &OsStr) -> Option<(&'static str, &'static str)> {
    let base = argv0.as_bytes().rsplit(|byte| *byte == b'/').next()?;
    let base = std::str::from_utf8(base).ok()?;

    LAUNCH_TARGETS
        .iter()
        .find(|(invocation, _)| *invocation == base)
        .copied()
}

fn build_exec_args(
    script_path: &'static str,
    args: Vec<OsString>,
) -> Result<Vec<CString>, LaunchError> {
    launcher_common::build_bash_exec_args(script_path, args).map_err(|_| LaunchError::NulArgument)
}

fn prepare_launch(argv0: &OsStr, args: Vec<OsString>) -> Result<LaunchRequest, LaunchError> {
    let Some((target_name, script_path)) = lookup_target(argv0) else {
        return Err(LaunchError::UnsupportedTarget(
            argv0.to_string_lossy().into_owned(),
        ));
    };

    Ok(LaunchRequest {
        target_name,
        script_path,
        exec_args: build_exec_args(script_path, args)?,
    })
}

fn format_launch_error(error: LaunchError) -> String {
    match error {
        LaunchError::UnsupportedTarget(target) => {
            format!("Unsupported Workcell launcher target: {target}")
        }
        LaunchError::NulArgument => launcher_common::NulArgumentError.to_string(),
    }
}

fn main() {
    let mut args = env::args_os();
    let argv0 = args.next().unwrap_or_else(|| OsStr::new("").to_owned());
    let remaining_args: Vec<OsString> = args.collect();
    let request = prepare_launch(&argv0, remaining_args).unwrap_or_else(|error| {
        eprintln!("{}", format_launch_error(error));
        process::exit(126);
    });

    launcher_common::sanitize_env();
    launcher_common::set_env_var("WORKCELL_LAUNCH_TARGET", request.target_name);
    process::exit(launcher_common::exec_request(
        &request.exec_args,
        request.script_path,
    ));
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::ffi::OsString;
    use std::os::unix::ffi::OsStringExt;
    use std::sync::{Mutex, OnceLock};

    fn env_lock() -> &'static Mutex<()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(()))
    }

    #[test]
    fn lookup_target_matches_known_invocations() {
        assert_eq!(
            lookup_target(OsStr::new("/usr/local/bin/workcell-entrypoint")),
            Some((
                "workcell-entrypoint",
                "/usr/local/libexec/workcell/entrypoint.sh"
            ))
        );
        assert_eq!(
            lookup_target(OsStr::new("claude")),
            Some(("claude", "/usr/local/libexec/workcell/provider-wrapper.sh"))
        );
        assert_eq!(lookup_target(OsStr::new("unknown")), None);
    }

    #[test]
    fn prepare_launch_builds_exec_args_for_known_targets() {
        let request = prepare_launch(
            OsStr::new("/usr/local/bin/codex"),
            vec![OsString::from("--version")],
        )
        .expect("prepare launch");

        assert_eq!(request.target_name, "codex");
        assert_eq!(
            request.script_path,
            "/usr/local/libexec/workcell/provider-wrapper.sh"
        );
        assert_eq!(request.exec_args[0].as_bytes(), b"/bin/bash");
        assert_eq!(
            request.exec_args[1].as_bytes(),
            b"/usr/local/libexec/workcell/provider-wrapper.sh"
        );
        assert_eq!(request.exec_args[2].as_bytes(), b"--version");
    }

    #[test]
    fn prepare_launch_rejects_unknown_targets_and_nul_args() {
        let unknown =
            prepare_launch(OsStr::new("unknown"), vec![]).expect_err("unknown target should fail");
        assert_eq!(
            format_launch_error(unknown),
            "Unsupported Workcell launcher target: unknown"
        );

        let invalid = OsString::from_vec(b"co\0dex".to_vec());
        let nul =
            prepare_launch(OsStr::new("codex"), vec![invalid]).expect_err("nul arg should fail");
        assert_eq!(
            format_launch_error(nul),
            "Workcell launcher argument contained a NUL byte."
        );
    }

    #[test]
    fn osstr_to_cstring_rejects_nul_bytes() {
        assert!(launcher_common::osstr_to_cstring(OsStr::new("codex")).is_ok());
        assert!(launcher_common::osstr_to_cstring(OsStr::from_bytes(b"co\0dex")).is_err());
    }

    #[test]
    fn sanitize_env_clears_sensitive_loader_and_node_state() {
        let _guard = env_lock().lock().expect("env lock");
        launcher_common::set_env_var("BASH_ENV", "/tmp/bashenv");
        launcher_common::set_env_var("LD_PRELOAD", "/tmp/preload.so");
        launcher_common::set_env_var("NODE_OPTIONS", "--inspect");
        launcher_common::set_env_var("NODE_EXTRA_CA_CERTS", "/tmp/extra.pem");
        launcher_common::set_env_var("SSL_CERT_FILE", "/tmp/cert.pem");
        launcher_common::set_env_var("SSL_CERT_DIR", "/tmp/certs");

        launcher_common::sanitize_env();

        assert!(env::var_os("BASH_ENV").is_none());
        assert!(env::var_os("LD_PRELOAD").is_none());
        assert!(env::var_os("NODE_OPTIONS").is_none());
        assert!(env::var_os("NODE_EXTRA_CA_CERTS").is_none());
        assert!(env::var_os("SSL_CERT_FILE").is_none());
        assert!(env::var_os("SSL_CERT_DIR").is_none());
    }

    #[test]
    fn exec_failure_helpers_format_messages_and_exit_codes() {
        assert_eq!(launcher_common::exit_code_for_errno(libc::ENOENT), 127);
        assert_eq!(launcher_common::exit_code_for_errno(libc::EACCES), 126);
        let message = launcher_common::format_exec_error(
            "/usr/local/libexec/workcell/provider-wrapper.sh",
            libc::ENOENT,
        );
        assert!(
            message.contains("execve(/bin/bash, /usr/local/libexec/workcell/provider-wrapper.sh):")
        );
    }

    #[test]
    fn exec_request_returns_failure_code_when_shell_is_unavailable() {
        let request = LaunchRequest {
            target_name: "codex",
            script_path: "/usr/local/libexec/workcell/provider-wrapper.sh",
            exec_args: vec![
                c"/definitely/missing/bash".to_owned(),
                CString::new("/usr/local/libexec/workcell/provider-wrapper.sh")
                    .expect("provider wrapper path"),
                CString::new("--version").expect("version arg"),
            ],
        };

        assert_eq!(
            launcher_common::exec_request(&request.exec_args, request.script_path),
            127
        );
    }
}
