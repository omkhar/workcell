use std::env;
use std::ffi::{CString, OsStr, OsString};
use std::os::raw::c_char;
use std::os::unix::ffi::OsStrExt;
use std::process;

unsafe extern "C" {
    static mut environ: *mut *mut c_char;
}

const BASH_PATH: &str = "/bin/bash";

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

fn sanitize_env() {
    for key in [
        "BASH_ENV",
        "ENV",
        "LD_AUDIT",
        "LD_LIBRARY_PATH",
        "LD_PRELOAD",
        "NODE_OPTIONS",
        "NODE_PATH",
        "npm_config_userconfig",
        "NPM_CONFIG_USERCONFIG",
        "SSL_CERT_FILE",
        "SSL_CERT_DIR",
    ] {
        env::remove_var(key);
    }
}

fn osstr_to_cstring(value: &OsStr) -> Result<CString, ()> {
    CString::new(value.as_bytes()).map_err(|_| ())
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
    let mut exec_args = Vec::new();
    exec_args.push(CString::new(BASH_PATH).expect("static bash path"));
    exec_args.push(CString::new(script_path).expect("static script path"));
    for arg in args {
        exec_args.push(osstr_to_cstring(&arg).map_err(|_| LaunchError::NulArgument)?);
    }
    Ok(exec_args)
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

fn format_exec_error(script_path: &str, errno: i32) -> String {
    format!(
        "execve({}, {}): {}",
        BASH_PATH,
        script_path,
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
        eprintln!("{}", format_exec_error(request.script_path, errno));
    }

    exit_code_for_errno(errno)
}

fn main() {
    let mut args = env::args_os();
    let argv0 = args.next().unwrap_or_else(|| OsStr::new("").to_owned());
    let remaining_args: Vec<OsString> = args.collect();
    let request = prepare_launch(&argv0, remaining_args).unwrap_or_else(|error| {
        eprintln!("{}", format_launch_error(error));
        process::exit(126);
    });

    sanitize_env();
    env::set_var("WORKCELL_LAUNCH_TARGET", request.target_name);
    process::exit(exec_request(&request));
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
        assert_eq!(request.exec_args[0].as_bytes(), BASH_PATH.as_bytes());
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
        assert!(osstr_to_cstring(OsStr::new("codex")).is_ok());
        assert!(osstr_to_cstring(OsStr::from_bytes(b"co\0dex")).is_err());
    }

    #[test]
    fn sanitize_env_clears_sensitive_loader_and_node_state() {
        let _guard = env_lock().lock().expect("env lock");
        env::set_var("BASH_ENV", "/tmp/bashenv");
        env::set_var("LD_PRELOAD", "/tmp/preload.so");
        env::set_var("NODE_OPTIONS", "--inspect");
        env::set_var("SSL_CERT_FILE", "/tmp/cert.pem");
        env::set_var("SSL_CERT_DIR", "/tmp/certs");

        sanitize_env();

        assert!(env::var_os("BASH_ENV").is_none());
        assert!(env::var_os("LD_PRELOAD").is_none());
        assert!(env::var_os("NODE_OPTIONS").is_none());
        assert!(env::var_os("SSL_CERT_FILE").is_none());
        assert!(env::var_os("SSL_CERT_DIR").is_none());
    }

    #[test]
    fn exec_failure_helpers_format_messages_and_exit_codes() {
        assert_eq!(exit_code_for_errno(libc::ENOENT), 127);
        assert_eq!(exit_code_for_errno(libc::EACCES), 126);
        let message = format_exec_error(
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
                CString::new("/definitely/missing/bash").expect("missing bash path"),
                CString::new("/usr/local/libexec/workcell/provider-wrapper.sh")
                    .expect("provider wrapper path"),
                CString::new("--version").expect("version arg"),
            ],
        };

        assert_eq!(exec_request(&request), 127);
    }
}
