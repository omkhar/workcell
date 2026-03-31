use std::env;
use std::ffi::{CString, OsString};
use std::os::raw::c_char;
use std::process;

unsafe extern "C" {
    static mut environ: *mut *mut c_char;
}

const BASH_PATH: &str = "/bin/bash";
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

fn sanitize_env() {
    for key in [
        "BASH_ENV",
        "ENV",
        "LD_AUDIT",
        "LD_LIBRARY_PATH",
        "LD_PRELOAD",
        "NODE_OPTIONS",
        "NODE_PATH",
        "NODE_EXTRA_CA_CERTS",
        "npm_config_userconfig",
        "NPM_CONFIG_USERCONFIG",
        "SSL_CERT_FILE",
        "SSL_CERT_DIR",
    ] {
        env::remove_var(key);
    }
}

fn build_exec_args(args: Vec<OsString>) -> Result<Vec<CString>, LaunchError> {
    let mut exec_args = Vec::new();
    exec_args.push(CString::new(BASH_PATH).expect("static bash path"));
    exec_args.push(CString::new(SCRIPT_PATH).expect("static script path"));
    for arg in args {
        exec_args.push(CString::new(arg.as_encoded_bytes()).map_err(|_| LaunchError::NulArgument)?);
    }
    Ok(exec_args)
}

fn prepare_launch(args: Vec<OsString>) -> Result<LaunchRequest, LaunchError> {
    Ok(LaunchRequest {
        exec_args: build_exec_args(args)?,
    })
}

fn format_launch_error(error: LaunchError) -> String {
    match error {
        LaunchError::NulArgument => "Workcell launcher argument contained a NUL byte.".to_string(),
    }
}

fn exit_code_for_errno(errno: i32) -> i32 {
    if errno == libc::ENOENT {
        127
    } else {
        126
    }
}

fn format_exec_error(errno: i32) -> String {
    format!(
        "execve({}, {}): {}",
        BASH_PATH,
        SCRIPT_PATH,
        std::io::Error::from_raw_os_error(errno)
    )
}

fn exec_request(request: &LaunchRequest) -> i32 {
    let mut argv: Vec<*const c_char> = request.exec_args.iter().map(|arg| arg.as_ptr()).collect();
    argv.push(std::ptr::null());

    let rc = unsafe { libc::execve(request.exec_args[0].as_ptr(), argv.as_ptr(), environ.cast()) };
    let errno = std::io::Error::last_os_error()
        .raw_os_error()
        .unwrap_or(libc::ENOENT);

    if rc != 0 {
        eprintln!("{}", format_exec_error(errno));
    }

    exit_code_for_errno(errno)
}

fn main() {
    let args: Vec<OsString> = env::args_os().skip(1).collect();
    let request = prepare_launch(args).unwrap_or_else(|error| {
        eprintln!("{}", format_launch_error(error));
        process::exit(126);
    });

    sanitize_env();
    env::set_var("WORKCELL_LAUNCH_TARGET", TARGET_NAME);
    process::exit(exec_request(&request));
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
        env::set_var("BASH_ENV", "/tmp/bashenv");
        env::set_var("LD_PRELOAD", "/tmp/preload.so");
        env::set_var("NODE_OPTIONS", "--inspect");
        env::set_var("NODE_EXTRA_CA_CERTS", "/tmp/extra.pem");
        env::set_var("SSL_CERT_FILE", "/tmp/cert.pem");
        env::set_var("SSL_CERT_DIR", "/tmp/certs");

        sanitize_env();

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
        assert_eq!(request.exec_args[0].as_bytes(), BASH_PATH.as_bytes());
        assert_eq!(request.exec_args[1].as_bytes(), SCRIPT_PATH.as_bytes());
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
        assert_eq!(exit_code_for_errno(libc::ENOENT), 127);
        assert_eq!(exit_code_for_errno(libc::EACCES), 126);
        let message = format_exec_error(libc::ENOENT);
        assert!(message.contains("execve(/bin/bash, /usr/local/libexec/workcell/git-wrapper.sh):"));
    }

    #[test]
    fn exec_request_returns_failure_code_when_shell_is_unavailable() {
        let request = LaunchRequest {
            exec_args: vec![
                CString::new("/definitely/missing/bash").expect("missing bash path"),
                CString::new(SCRIPT_PATH).expect("script path"),
                CString::new("--version").expect("version arg"),
            ],
        };

        assert_eq!(exec_request(&request), 127);
    }
}
