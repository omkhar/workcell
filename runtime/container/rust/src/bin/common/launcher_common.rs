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
fn install_signal_forwarding() {
    for signal in [libc::SIGINT, libc::SIGTERM] {
        // SAFETY: libc::sigaction is a repr(C) POD; all-zeroes is a valid initial value.
        let mut action: libc::sigaction = unsafe { std::mem::zeroed() };
        // Linux libc exposes the sa_handler/sa_sigaction union as this field;
        // SA_SIGINFO stays clear so the kernel treats it as a one-arg handler.
        action.sa_sigaction = forward_signal_to_managed_child as *const () as libc::sighandler_t;
        action.sa_flags = 0;
        // SAFETY: &action/&mut sa_mask are valid; handler is a real extern "C" fn with SA_SIGINFO clear (one-arg ABI); null oldact is allowed.
        unsafe {
            libc::sigemptyset(&mut action.sa_mask);
            libc::sigaction(signal, &action, std::ptr::null_mut());
        }
    }
}

#[cfg(not(test))]
pub fn spawn_and_wait_request(exec_args: &[CString], script_path: &str) -> i32 {
    install_signal_forwarding();
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
