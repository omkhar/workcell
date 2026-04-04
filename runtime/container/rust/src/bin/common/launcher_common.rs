// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

use std::env;
use std::ffi::{CString, OsStr, OsString};
use std::fmt;
use std::os::raw::c_char;
use std::os::unix::ffi::OsStrExt;

unsafe extern "C" {
    static mut environ: *mut *mut c_char;
}

pub const BASH_PATH: &str = "/bin/bash";

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
        unsafe { env::remove_var(key) };
    }
}

pub fn set_env_var(key: &str, value: &str) {
    // Rust 2024 makes process-global env mutation unsafe.
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

    let rc = unsafe { libc::execve(exec_args[0].as_ptr(), argv.as_ptr(), environ.cast()) };
    let errno = std::io::Error::last_os_error()
        .raw_os_error()
        .unwrap_or(libc::ENOENT);

    if rc != 0 {
        eprintln!("{}", format_exec_error(script_path, errno));
    }

    exit_code_for_errno(errno)
}
