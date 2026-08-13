// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

use std::env;
use std::ffi::{CString, OsStr, OsString};
use std::fmt;
use std::os::raw::c_char;
use std::os::unix::ffi::OsStrExt;
#[cfg(not(test))]
use std::sync::atomic::{AtomicI32, Ordering};

// SAFETY: matches libc's process-global char **environ; reads assume no concurrent setenv/putenv mutation.
unsafe extern "C" {
    static mut environ: *mut *mut c_char;
}

pub const BASH_PATH: &str = "/bin/bash";

#[cfg(not(test))]
static MANAGED_CHILD_PID: AtomicI32 = AtomicI32::new(0);

const SANITIZED_ENV_KEYS: &[&str] = &[
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
    "WORKCELL_COPILOT_GITHUB_TOKEN",
    "WORKCELL_COPILOT_AUTH_REQUIRED",
    "WORKCELL_COPILOT_TOKEN_FILE",
    "WORKCELL_PROVIDER_LAUNCHER_AUTHORITY",
];

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct NulArgumentError;

impl fmt::Display for NulArgumentError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("Workcell launcher argument contained a NUL byte.")
    }
}

pub fn sanitize_env() {
    for key in SANITIZED_ENV_KEYS {
        // Rust 2024 makes process-global env mutation unsafe.
        // SAFETY: called during single-threaded launcher startup before any thread or child is spawned; no concurrent env access.
        unsafe { env::remove_var(key) };
    }
}

pub fn set_env_var(key: &str, value: &str) {
    // Rust 2024 makes process-global env mutation unsafe.
    // SAFETY: single-threaded startup env mutation; no other thread can be reading the environment.
    unsafe { env::set_var(key, value) };
}

pub fn osstr_to_cstring(value: &OsStr) -> Result<CString, NulArgumentError> {
    CString::new(value.as_bytes()).map_err(|_| NulArgumentError)
}

pub fn build_bash_exec_args(
    script_path: &'static str,
    args: Vec<OsString>,
) -> Result<Vec<CString>, NulArgumentError> {
    let mut exec_args = Vec::with_capacity(args.len() + 2);
    exec_args.push(c"/bin/bash".to_owned());
    exec_args.push(CString::new(script_path).expect("static script path"));
    for arg in args {
        exec_args.push(osstr_to_cstring(&arg)?);
    }
    Ok(exec_args)
}

pub fn exit_code_for_errno(errno: i32) -> i32 {
    if errno == libc::ENOENT { 127 } else { 126 }
}

pub fn format_exec_error(script_path: &str, errno: i32) -> String {
    format!(
        "execve({}, {}): {}",
        BASH_PATH,
        script_path,
        std::io::Error::from_raw_os_error(errno)
    )
}

pub fn exec_request(exec_args: &[CString], script_path: &str) -> i32 {
    let mut argv: Vec<*const c_char> = exec_args.iter().map(|arg| arg.as_ptr()).collect();
    argv.push(std::ptr::null());

    // SAFETY: exec_args is a live non-empty Vec<CString> backing the NULL-terminated argv; environ is libc-initialized; single-threaded.
    let rc = unsafe { libc::execve(exec_args[0].as_ptr(), argv.as_ptr(), environ.cast()) };
    let errno = std::io::Error::last_os_error()
        .raw_os_error()
        .unwrap_or(libc::ENOENT);

    if rc != 0 {
        eprintln!("{}", format_exec_error(script_path, errno));
    }

    exit_code_for_errno(errno)
}

#[cfg(not(test))]
extern "C" fn forward_signal_to_managed_child(signal: libc::c_int) {
    let pid = MANAGED_CHILD_PID.load(Ordering::SeqCst);
    if pid > 0 {
        // SAFETY: kill() is async-signal-safe; pid>0 read via SeqCst atomic; a stale pid yields harmless ESRCH.
        unsafe {
            libc::kill(pid, signal);
        }
    }
}

#[cfg(not(test))]
fn install_signal_forwarding() -> Result<(), std::io::Error> {
    install_signal_forwarding_with(
        forward_signal_to_managed_child as *const () as libc::sighandler_t,
        |mask| {
            // SAFETY: mask is a valid sigset_t from the local sigaction value.
            if unsafe { libc::sigemptyset(mask) } != 0 {
                Err(std::io::Error::last_os_error())
            } else {
                Ok(())
            }
        },
        |signal, action| {
            // SAFETY: action is valid; null oldact is allowed.
            if unsafe { libc::sigaction(signal, action, std::ptr::null_mut()) } != 0 {
                Err(std::io::Error::last_os_error())
            } else {
                Ok(())
            }
        },
    )
}

fn install_signal_forwarding_with<ClearMask, InstallAction>(
    handler: libc::sighandler_t,
    mut clear_mask: ClearMask,
    mut install_action: InstallAction,
) -> Result<(), std::io::Error>
where
    ClearMask: FnMut(&mut libc::sigset_t) -> std::io::Result<()>,
    InstallAction: FnMut(libc::c_int, &libc::sigaction) -> std::io::Result<()>,
{
    for signal in [libc::SIGINT, libc::SIGTERM] {
        // SAFETY: libc::sigaction is a repr(C) POD; all-zeroes is a valid initial value.
        let mut action: libc::sigaction = unsafe { std::mem::zeroed() };
        // Linux libc exposes the sa_handler/sa_sigaction union as this field;
        // SA_SIGINFO stays clear so the kernel treats it as a one-arg handler.
        action.sa_sigaction = handler;
        action.sa_flags = 0;
        clear_mask(&mut action.sa_mask)?;
        install_action(signal, &action)?;
    }
    Ok(())
}

fn signal_setup_exit_code(error: &std::io::Error) -> i32 {
    exit_code_for_errno(error.raw_os_error().unwrap_or(libc::EIO))
}

fn format_signal_setup_error(script_path: &str, error: &std::io::Error) -> String {
    format!("install signal forwarding for {script_path}: {error}")
}

#[cfg(not(test))]
pub fn spawn_and_wait_request(exec_args: &[CString], script_path: &str) -> i32 {
    if let Err(err) = install_signal_forwarding() {
        eprintln!("{}", format_signal_setup_error(script_path, &err));
        return signal_setup_exit_code(&err);
    }
    // SAFETY: fork() takes no arguments; child/parent dispatched on the returned pid.
    let pid = unsafe { libc::fork() };
    if pid < 0 {
        let errno = std::io::Error::last_os_error()
            .raw_os_error()
            .unwrap_or(libc::EIO);
        eprintln!(
            "fork({}, {}): {}",
            BASH_PATH,
            script_path,
            std::io::Error::from_raw_os_error(errno)
        );
        return exit_code_for_errno(errno);
    }

    if pid == 0 {
        let mut argv: Vec<*const c_char> = exec_args.iter().map(|arg| arg.as_ptr()).collect();
        argv.push(std::ptr::null());

        // SAFETY: in the forked child; exec_args backs the NULL-terminated argv, environ is libc's env; execve is async-signal-safe.
        let rc = unsafe { libc::execve(exec_args[0].as_ptr(), argv.as_ptr(), environ.cast()) };
        let errno = std::io::Error::last_os_error()
            .raw_os_error()
            .unwrap_or(libc::ENOENT);

        if rc != 0 {
            eprintln!("{}", format_exec_error(script_path, errno));
        }
        // SAFETY: _exit() is async-signal-safe and terminates the failed child without running at-exit handlers.
        unsafe { libc::_exit(exit_code_for_errno(errno)) };
    }

    MANAGED_CHILD_PID.store(pid, Ordering::SeqCst);
    loop {
        let mut status = 0;
        // SAFETY: waitpid writes only through the valid &mut status int; pid is the live child from fork().
        let waited = unsafe { libc::waitpid(pid, &mut status, 0) };
        if waited < 0 {
            let errno = std::io::Error::last_os_error()
                .raw_os_error()
                .unwrap_or(libc::EIO);
            if errno == libc::EINTR {
                continue;
            }
            eprintln!(
                "waitpid({}, {}): {}",
                pid,
                script_path,
                std::io::Error::from_raw_os_error(errno)
            );
            return exit_code_for_errno(errno);
        }

        if libc::WIFEXITED(status) {
            MANAGED_CHILD_PID.store(0, Ordering::SeqCst);
            return libc::WEXITSTATUS(status);
        }
        if libc::WIFSIGNALED(status) {
            MANAGED_CHILD_PID.store(0, Ordering::SeqCst);
            return 128 + libc::WTERMSIG(status);
        }
    }
}

#[cfg(test)]
mod tests {
    use std::cell::RefCell;

    use super::*;

    extern "C" fn test_signal_handler(_signal: libc::c_int) {}

    fn test_handler() -> libc::sighandler_t {
        test_signal_handler as *const () as libc::sighandler_t
    }

    #[test]
    fn signal_forwarding_stops_when_clearing_the_mask_fails() {
        let installed = RefCell::new(Vec::new());
        let result = install_signal_forwarding_with(
            test_handler(),
            |_| Err(std::io::Error::from_raw_os_error(libc::EACCES)),
            |signal, _| {
                installed.borrow_mut().push(signal);
                Ok(())
            },
        );

        let error = result.expect_err("mask failure must stop setup");
        assert_eq!(error.raw_os_error(), Some(libc::EACCES));
        assert_eq!(signal_setup_exit_code(&error), 126);
        assert!(installed.borrow().is_empty());
    }

    #[test]
    fn signal_forwarding_stops_when_installing_an_action_fails() {
        let installed = RefCell::new(Vec::new());
        let result = install_signal_forwarding_with(
            test_handler(),
            |_| Ok(()),
            |signal, _| {
                installed.borrow_mut().push(signal);
                Err(std::io::Error::from_raw_os_error(libc::ENOENT))
            },
        );

        let error = result.expect_err("action failure must stop setup");
        assert_eq!(error.raw_os_error(), Some(libc::ENOENT));
        assert_eq!(signal_setup_exit_code(&error), 127);
        assert_eq!(&*installed.borrow(), &[libc::SIGINT]);
    }

    #[test]
    fn signal_forwarding_installs_both_supported_signals() {
        let installed = RefCell::new(Vec::new());
        let result = install_signal_forwarding_with(
            test_handler(),
            |_| Ok(()),
            |signal, action| {
                assert_eq!(action.sa_flags, 0);
                installed.borrow_mut().push(signal);
                Ok(())
            },
        );

        assert!(result.is_ok());
        assert_eq!(&*installed.borrow(), &[libc::SIGINT, libc::SIGTERM]);
    }

    #[test]
    fn signal_setup_errors_use_safe_text_and_mapped_statuses() {
        let error = std::io::Error::from_raw_os_error(libc::ENOENT);
        assert_eq!(signal_setup_exit_code(&error), 127);
        assert_eq!(
            signal_setup_exit_code(&std::io::Error::other("failure")),
            126
        );
        assert_eq!(
            format_signal_setup_error("/usr/local/libexec/workcell/provider-wrapper.sh", &error),
            "install signal forwarding for /usr/local/libexec/workcell/provider-wrapper.sh: No such file or directory (os error 2)"
        );
    }
}
