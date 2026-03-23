use std::env;
use std::ffi::CString;
use std::os::raw::c_char;
use std::process;

unsafe extern "C" {
    static mut environ: *mut *mut c_char;
}

const BASH_PATH: &str = "/bin/bash";
const TARGET_NAME: &str = "git";
const SCRIPT_PATH: &str = "/usr/local/libexec/workcell/git-wrapper.sh";

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

fn main() {
    let args: Vec<CString> = env::args_os()
        .skip(1)
        .map(|arg| CString::new(arg.as_encoded_bytes()).map_err(|_| ()))
        .collect::<Result<_, _>>()
        .unwrap_or_else(|_| {
            eprintln!("Workcell launcher argument contained a NUL byte.");
            process::exit(126);
        });

    sanitize_env();
    env::set_var("WORKCELL_LAUNCH_TARGET", TARGET_NAME);

    let bash_path = CString::new(BASH_PATH).expect("static bash path");
    let script_path = CString::new(SCRIPT_PATH).expect("static script path");

    let mut exec_args = Vec::with_capacity(args.len() + 2);
    exec_args.push(bash_path.clone());
    exec_args.push(script_path.clone());
    exec_args.extend(args);

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
            SCRIPT_PATH,
            std::io::Error::from_raw_os_error(errno)
        );
    }

    process::exit(if errno == libc::ENOENT { 127 } else { 126 });
}
