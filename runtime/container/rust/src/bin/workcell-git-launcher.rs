#[path = "common/launcher_common.rs"]
mod launcher_common;

use std::env;
use std::ffi::{CString, OsString};
use std::process;
const TARGET_NAME: &str = "git";
const SCRIPT_PATH: &str = "/usr/local/libexec/workcell/git-wrapper.sh";

#[derive(Debug)]
struct LaunchRequest {
    exec_args: Vec<CString>,
}

#[derive(Debug)]
enum LaunchError {
    NulArgument,
}

fn build_exec_args(args: Vec<OsString>) -> Result<Vec<CString>, LaunchError> {
    launcher_common::build_bash_exec_args(SCRIPT_PATH, args).map_err(|_| LaunchError::NulArgument)
}

fn prepare_launch(args: Vec<OsString>) -> Result<LaunchRequest, LaunchError> {
    Ok(LaunchRequest {
        exec_args: build_exec_args(args)?,
    })
}

fn format_launch_error(error: LaunchError) -> String {
    match error {
        LaunchError::NulArgument => launcher_common::NulArgumentError.to_string(),
    }
}

fn main() {
    let args: Vec<OsString> = env::args_os().skip(1).collect();
    let request = prepare_launch(args).unwrap_or_else(|error| {
        eprintln!("{}", format_launch_error(error));
        process::exit(126);
    });

    launcher_common::sanitize_env();
    launcher_common::set_env_var("WORKCELL_LAUNCH_TARGET", TARGET_NAME);
    process::exit(launcher_common::exec_request(
        &request.exec_args,
        SCRIPT_PATH,
    ));
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::ffi::OsStringExt;
    use std::sync::{Mutex, OnceLock};

    fn env_lock() -> &'static Mutex<()> {
        static LOCK: OnceLock<Mutex<()>> = OnceLock::new();
        LOCK.get_or_init(|| Mutex::new(()))
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
    fn prepare_launch_builds_exec_args_and_rejects_nul_bytes() {
        let request = prepare_launch(vec![OsString::from("--version")]).expect("prepare launch");
        assert_eq!(request.exec_args[0].as_bytes(), b"/bin/bash");
        assert_eq!(
            request.exec_args[1].as_bytes(),
            b"/usr/local/libexec/workcell/git-wrapper.sh"
        );
        assert_eq!(request.exec_args[2].as_bytes(), b"--version");

        let invalid = OsString::from_vec(b"gi\0t".to_vec());
        let err = prepare_launch(vec![invalid]).expect_err("nul args should fail");
        assert_eq!(
            format_launch_error(err),
            "Workcell launcher argument contained a NUL byte."
        );
    }

    #[test]
    fn exec_failure_helpers_format_messages_and_exit_codes() {
        assert_eq!(launcher_common::exit_code_for_errno(libc::ENOENT), 127);
        assert_eq!(launcher_common::exit_code_for_errno(libc::EACCES), 126);
        let message = launcher_common::format_exec_error(SCRIPT_PATH, libc::ENOENT);
        assert!(message.contains("execve(/bin/bash, /usr/local/libexec/workcell/git-wrapper.sh):"));
    }

    #[test]
    fn exec_request_returns_failure_code_when_shell_is_unavailable() {
        let request = LaunchRequest {
            exec_args: vec![
                c"/definitely/missing/bash".to_owned(),
                c"/usr/local/libexec/workcell/git-wrapper.sh".to_owned(),
                CString::new("--version").expect("version arg"),
            ],
        };

        assert_eq!(
            launcher_common::exec_request(&request.exec_args, SCRIPT_PATH),
            127
        );
    }
}
