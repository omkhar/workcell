// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

#[path = "common/launcher_common.rs"]
mod launcher_common;

use std::env;
use std::ffi::{CString, OsStr, OsString};
use std::os::unix::ffi::OsStrExt;
#[cfg(not(test))]
use std::process;

#[derive(Debug)]
struct LaunchTarget {
    name: &'static str,
    script_path: &'static str,
    approved_invocations: &'static [&'static str],
}

const LAUNCH_TARGETS: &[LaunchTarget] = &[
    LaunchTarget {
        name: "workcell-entrypoint",
        script_path: "/usr/local/libexec/workcell/entrypoint.sh",
        approved_invocations: &["/usr/local/bin/workcell-entrypoint"],
    },
    LaunchTarget {
        name: "git",
        script_path: "/usr/local/libexec/workcell/git-wrapper.sh",
        approved_invocations: &[
            "/usr/local/bin/git",
            "/usr/bin/git",
            "/usr/local/libexec/workcell/core/git",
        ],
    },
    LaunchTarget {
        name: "node",
        script_path: "/usr/local/libexec/workcell/node-wrapper.sh",
        approved_invocations: &[
            "/usr/local/bin/node",
            "/usr/local/libexec/workcell/core/node",
        ],
    },
    LaunchTarget {
        name: "codex",
        script_path: "/usr/local/libexec/workcell/provider-wrapper.sh",
        approved_invocations: &[
            "/usr/local/bin/codex",
            "/usr/local/libexec/workcell/core/codex",
        ],
    },
    LaunchTarget {
        name: "claude",
        script_path: "/usr/local/libexec/workcell/provider-wrapper.sh",
        approved_invocations: &[
            "/usr/local/bin/claude",
            "/usr/local/libexec/workcell/core/claude",
        ],
    },
    LaunchTarget {
        name: "copilot",
        script_path: "/usr/local/libexec/workcell/provider-wrapper.sh",
        approved_invocations: &[
            "/usr/local/bin/copilot",
            "/usr/local/libexec/workcell/core/copilot",
        ],
    },
    LaunchTarget {
        name: "gemini",
        script_path: "/usr/local/libexec/workcell/provider-wrapper.sh",
        approved_invocations: &[
            "/usr/local/bin/gemini",
            "/usr/local/libexec/workcell/core/gemini",
        ],
    },
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
    UntrustedInvocation { target: String, invocation: String },
    NulArgument,
}

fn lookup_target(argv0: &OsStr) -> Option<&'static LaunchTarget> {
    let base = argv0.as_bytes().rsplit(|byte| *byte == b'/').next()?;
    let base = std::str::from_utf8(base).ok()?;

    LAUNCH_TARGETS.iter().find(|target| target.name == base)
}

fn build_exec_args(
    script_path: &'static str,
    args: Vec<OsString>,
) -> Result<Vec<CString>, LaunchError> {
    launcher_common::build_bash_exec_args(script_path, args).map_err(|_| LaunchError::NulArgument)
}

#[cfg(target_os = "linux")]
fn current_execfn() -> Option<OsString> {
    // SAFETY: getauxval(AT_EXECFN) has no preconditions; returns 0 when absent (checked below).
    let value = unsafe { libc::getauxval(libc::AT_EXECFN) };
    if value == 0 {
        return None;
    }
    // SAFETY: value is non-zero (checked), a kernel-provided NUL-terminated C string valid for the process lifetime.
    let c_value = unsafe { std::ffi::CStr::from_ptr(value as *const libc::c_char) };
    Some(OsStr::from_bytes(c_value.to_bytes()).to_owned())
}

#[cfg(not(target_os = "linux"))]
fn current_execfn() -> Option<OsString> {
    None
}

fn launch_invocation_is_trusted(target: &LaunchTarget, trusted_invocation: &OsStr) -> bool {
    if !trusted_invocation.as_bytes().contains(&b'/') {
        return false;
    }

    let Some(invocation) = trusted_invocation.to_str() else {
        return false;
    };
    target.approved_invocations.contains(&invocation)
}

fn prepare_launch_with_invocation(
    argv0: &OsStr,
    trusted_invocation: &OsStr,
    args: Vec<OsString>,
) -> Result<LaunchRequest, LaunchError> {
    let Some(target) = lookup_target(argv0) else {
        return Err(LaunchError::UnsupportedTarget(
            argv0.to_string_lossy().into_owned(),
        ));
    };
    if !launch_invocation_is_trusted(target, trusted_invocation) {
        return Err(LaunchError::UntrustedInvocation {
            target: target.name.to_owned(),
            invocation: trusted_invocation.to_string_lossy().into_owned(),
        });
    }

    Ok(LaunchRequest {
        target_name: target.name,
        script_path: target.script_path,
        exec_args: build_exec_args(target.script_path, args)?,
    })
}

fn prepare_launch_with_optional_invocation(
    argv0: &OsStr,
    trusted_invocation: Option<OsString>,
    args: Vec<OsString>,
) -> Result<LaunchRequest, LaunchError> {
    let Some(trusted_invocation) = trusted_invocation else {
        let Some(target) = lookup_target(argv0) else {
            return Err(LaunchError::UnsupportedTarget(
                argv0.to_string_lossy().into_owned(),
            ));
        };
        return Err(LaunchError::UntrustedInvocation {
            target: target.name.to_owned(),
            invocation: "unavailable-current-executable".to_owned(),
        });
    };
    prepare_launch_with_invocation(argv0, &trusted_invocation, args)
}

fn prepare_launch(argv0: &OsStr, args: Vec<OsString>) -> Result<LaunchRequest, LaunchError> {
    prepare_launch_with_optional_invocation(argv0, current_execfn(), args)
}

fn format_launch_error(error: LaunchError) -> String {
    match error {
        LaunchError::UnsupportedTarget(target) => {
            format!("Unsupported Workcell launcher target: {target}")
        }
        LaunchError::UntrustedInvocation { target, invocation } => {
            format!("Unsupported Workcell launcher invocation for {target}: {invocation}")
        }
        LaunchError::NulArgument => launcher_common::NulArgumentError.to_string(),
    }
}

fn launch_requires_supervised_wrapper_parent(request: &LaunchRequest) -> bool {
    request.target_name != "workcell-entrypoint"
}

fn copilot_auth_required_for_pid1(target_name: &str) -> Option<&'static str> {
    if target_name != "workcell-entrypoint" {
        return None;
    }

    let value = env::var_os("WORKCELL_COPILOT_AUTH_REQUIRED")?;
    match value.as_bytes() {
        b"0" => Some("0"),
        b"1" => Some("1"),
        _ => None,
    }
}

#[cfg(not(test))]
fn main() {
    let mut args = env::args_os();
    let argv0 = args.next().unwrap_or_else(|| OsStr::new("").to_owned());
    let remaining_args: Vec<OsString> = args.collect();
    let request = prepare_launch(&argv0, remaining_args).unwrap_or_else(|error| {
        eprintln!("{}", format_launch_error(error));
        process::exit(126);
    });

    let copilot_auth_required = copilot_auth_required_for_pid1(request.target_name);
    launcher_common::sanitize_env();
    if let Some(value) = copilot_auth_required {
        launcher_common::set_env_var("WORKCELL_COPILOT_AUTH_REQUIRED", value);
    }
    launcher_common::set_env_var("WORKCELL_LAUNCH_TARGET", request.target_name);
    if request.script_path == "/usr/local/libexec/workcell/provider-wrapper.sh" {
        launcher_common::set_env_var("WORKCELL_PROVIDER_LAUNCHER_AUTHORITY", "1");
    }
    if launch_requires_supervised_wrapper_parent(&request) {
        process::exit(launcher_common::spawn_and_wait_request(
            &request.exec_args,
            request.script_path,
        ));
    }
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
            lookup_target(OsStr::new("/usr/local/bin/workcell-entrypoint"))
                .map(|target| (target.name, target.script_path)),
            Some((
                "workcell-entrypoint",
                "/usr/local/libexec/workcell/entrypoint.sh"
            ))
        );
        assert_eq!(
            lookup_target(OsStr::new("claude")).map(|target| (target.name, target.script_path)),
            Some(("claude", "/usr/local/libexec/workcell/provider-wrapper.sh"))
        );
        assert_eq!(
            lookup_target(OsStr::new("copilot")).map(|target| (target.name, target.script_path)),
            Some(("copilot", "/usr/local/libexec/workcell/provider-wrapper.sh"))
        );
        assert!(lookup_target(OsStr::new("unknown")).is_none());
    }

    #[test]
    fn prepare_launch_builds_exec_args_for_known_targets() {
        let request = prepare_launch_with_invocation(
            OsStr::new("/usr/local/bin/codex"),
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
    fn approved_invocations_match_only_installed_paths() {
        for target in LAUNCH_TARGETS {
            for approved_invocation in target.approved_invocations {
                let request = prepare_launch_with_invocation(
                    OsStr::new(approved_invocation),
                    OsStr::new(approved_invocation),
                    vec![OsString::from("--version")],
                )
                .expect("approved invocation");
                assert_eq!(request.target_name, target.name);
                assert_eq!(request.script_path, target.script_path);
            }
        }

        let copilot = lookup_target(OsStr::new("copilot")).expect("copilot target");
        assert!(!launch_invocation_is_trusted(
            copilot,
            OsStr::new("copilot")
        ));
        assert!(!launch_invocation_is_trusted(
            copilot,
            OsStr::from_bytes(b"/usr/local/bin/co\xffpilot")
        ));
        assert!(!launch_invocation_is_trusted(
            copilot,
            OsStr::new("/workspace/bin/copilot")
        ));
    }

    #[test]
    fn prepare_launch_rejects_spoofed_launcher_invocations() {
        let exec_a = prepare_launch_with_invocation(
            OsStr::new("copilot"),
            OsStr::new("/usr/local/libexec/workcell/core/launcher"),
            vec![],
        )
        .expect_err("exec -a spoof should fail");
        assert_eq!(
            format_launch_error(exec_a),
            "Unsupported Workcell launcher invocation for copilot: /usr/local/libexec/workcell/core/launcher"
        );

        let workspace_symlink = prepare_launch_with_invocation(
            OsStr::new("/workspace/tmp/copilot"),
            OsStr::new("/workspace/tmp/copilot"),
            vec![],
        )
        .expect_err("workspace symlink invocation should fail");
        assert_eq!(
            format_launch_error(workspace_symlink),
            "Unsupported Workcell launcher invocation for copilot: /workspace/tmp/copilot"
        );

        let request = prepare_launch_with_invocation(
            OsStr::new("copilot"),
            OsStr::new("/usr/local/bin/copilot"),
            vec![],
        )
        .expect("approved path with shell-provided argv0");
        assert_eq!(request.target_name, "copilot");

        let git_request = prepare_launch_with_invocation(
            OsStr::new("/usr/bin/git"),
            OsStr::new("/usr/bin/git"),
            vec![OsString::from("--version")],
        )
        .expect("approved /usr/bin/git trampoline");
        assert_eq!(git_request.target_name, "git");

        let missing_execfn = prepare_launch_with_optional_invocation(
            OsStr::new("copilot"),
            None,
            vec![OsString::from("--version")],
        )
        .expect_err("missing trusted executable identity should fail closed");
        assert_eq!(
            format_launch_error(missing_execfn),
            "Unsupported Workcell launcher invocation for copilot: unavailable-current-executable"
        );
    }

    #[test]
    fn launch_supervises_shell_wrappers_but_not_pid1_entrypoint() {
        let provider = prepare_launch_with_invocation(
            OsStr::new("/usr/local/bin/copilot"),
            OsStr::new("/usr/local/bin/copilot"),
            vec![],
        )
        .expect("provider launch");
        let node = prepare_launch_with_invocation(
            OsStr::new("/usr/local/bin/node"),
            OsStr::new("/usr/local/bin/node"),
            vec![],
        )
        .expect("node launch");
        let entrypoint = prepare_launch_with_invocation(
            OsStr::new("/usr/local/bin/workcell-entrypoint"),
            OsStr::new("/usr/local/bin/workcell-entrypoint"),
            vec![],
        )
        .expect("entrypoint launch");

        assert!(launch_requires_supervised_wrapper_parent(&provider));
        assert!(launch_requires_supervised_wrapper_parent(&node));
        assert!(!launch_requires_supervised_wrapper_parent(&entrypoint));
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
        let nul = prepare_launch_with_invocation(
            OsStr::new("codex"),
            OsStr::new("/usr/local/bin/codex"),
            vec![invalid],
        )
        .expect_err("nul arg should fail");
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
        launcher_common::set_env_var("WORKCELL_COPILOT_AUTH_REQUIRED", "0");

        launcher_common::sanitize_env();

        assert!(env::var_os("BASH_ENV").is_none());
        assert!(env::var_os("LD_PRELOAD").is_none());
        assert!(env::var_os("NODE_OPTIONS").is_none());
        assert!(env::var_os("NODE_EXTRA_CA_CERTS").is_none());
        assert!(env::var_os("SSL_CERT_FILE").is_none());
        assert!(env::var_os("SSL_CERT_DIR").is_none());
        assert!(env::var_os("WORKCELL_COPILOT_AUTH_REQUIRED").is_none());
    }

    #[test]
    fn copilot_auth_required_is_preserved_only_for_pid1_entrypoint() {
        let _guard = env_lock().lock().expect("env lock");

        launcher_common::set_env_var("WORKCELL_COPILOT_AUTH_REQUIRED", "1");
        assert_eq!(
            copilot_auth_required_for_pid1("workcell-entrypoint"),
            Some("1")
        );
        assert_eq!(copilot_auth_required_for_pid1("copilot"), None);

        launcher_common::set_env_var("WORKCELL_COPILOT_AUTH_REQUIRED", "0");
        assert_eq!(
            copilot_auth_required_for_pid1("workcell-entrypoint"),
            Some("0")
        );

        launcher_common::set_env_var("WORKCELL_COPILOT_AUTH_REQUIRED", "maybe");
        assert_eq!(copilot_auth_required_for_pid1("workcell-entrypoint"), None);

        // SAFETY: test-only env cleanup; the test does not run concurrently with other environment access.
        unsafe { env::remove_var("WORKCELL_COPILOT_AUTH_REQUIRED") };
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
    fn all_launch_targets_have_no_nul_bytes() {
        for target in LAUNCH_TARGETS {
            assert!(
                CString::new(target.name).is_ok(),
                "LAUNCH_TARGETS entry name contains a NUL byte: {:?}",
                target.name
            );
            assert!(
                CString::new(target.script_path).is_ok(),
                "LAUNCH_TARGETS entry path contains a NUL byte: {:?}",
                target.script_path
            );
        }
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
