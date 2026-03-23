use std::env;
use std::ffi::{CString, OsStr};
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

fn main() {
    let mut args = env::args_os();
    let argv0 = args.next().unwrap_or_else(|| OsStr::new("").to_owned());
    let Some((target_name, script_path)) = lookup_target(&argv0) else {
        eprintln!(
            "Unsupported Workcell launcher target: {}",
            argv0.to_string_lossy()
        );
        process::exit(126);
    };

    sanitize_env();
    env::set_var("WORKCELL_LAUNCH_TARGET", target_name);

    let bash_path = CString::new(BASH_PATH).expect("static bash path");
    let script_path = CString::new(script_path).expect("static script path");

    let mut exec_args = Vec::new();
    exec_args.push(bash_path.clone());
    exec_args.push(script_path.clone());
    for arg in args {
        let Ok(arg) = osstr_to_cstring(&arg) else {
            eprintln!("Workcell launcher argument contained a NUL byte.");
            process::exit(126);
        };
        exec_args.push(arg);
    }

    let mut argv: Vec<*const c_char> = exec_args.iter().map(|arg| arg.as_ptr()).collect();
    argv.push(std::ptr::null());

    let rc = unsafe { libc::execve(bash_path.as_ptr(), argv.as_ptr(), environ.cast()) };
    let errno = std::io::Error::last_os_error()
        .raw_os_error()
        .unwrap_or(libc::ENOENT);

    if rc != 0 {
        eprintln!(
            "execve({}, {}): {}",
            BASH_PATH,
            script_path.to_string_lossy(),
            std::io::Error::from_raw_os_error(errno)
        );
    }

    process::exit(if errno == libc::ENOENT { 127 } else { 126 });
}
