// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

#![allow(clippy::missing_safety_doc)]
#![deny(unsafe_op_in_unsafe_fn)]

use libc::{c_char, c_int, c_long, c_void, pid_t};
#[cfg(all(
    target_os = "linux",
    any(target_arch = "x86_64", target_arch = "aarch64")
))]
use std::arch::global_asm;
use std::env;
use std::ffi::{CStr, CString};
use std::fs::{self, File};
use std::io::Read;
use std::mem::{self, MaybeUninit};
use std::os::unix::fs::MetadataExt;
use std::path::Path;
use std::sync::OnceLock;

// SAFETY: matches libc's process-global char **environ; reads assume no concurrent setenv/putenv mutation.
unsafe extern "C" {
    static mut environ: *mut *mut c_char;
}

#[cfg(all(target_os = "linux", target_arch = "x86_64"))]
global_asm!(
    r#"
    .text
    .globl syscall
    .type syscall,@function
syscall:
    jmp workcell_syscall_shim
"#
);

#[cfg(all(target_os = "linux", target_arch = "aarch64"))]
global_asm!(
    r#"
    .text
    .globl syscall
    .type syscall,%function
syscall:
    b workcell_syscall_shim
"#
);

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ProtectedRuntime {
    None,
    Git,
    Node,
    Codex,
    Claude,
    Copilot,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ApprovedWrapper {
    None,
    Development,
    Git,
    Node,
    Provider,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct StatSignature {
    dev: u64,
    ino: u64,
    size: i64,
    mode: u32,
}

const PROTECTED_GIT_PATHS: &[&str] = &[
    "/usr/local/libexec/workcell/core/git",
    "/usr/local/libexec/workcell/git",
    "/usr/local/libexec/workcell/real/git",
];

const PROTECTED_RUNTIME_PATHS: &[(ProtectedRuntime, &str)] = &[
    (
        ProtectedRuntime::Git,
        "/usr/local/libexec/workcell/real/git",
    ),
    (
        ProtectedRuntime::Node,
        "/usr/local/libexec/workcell/real/node",
    ),
    (
        ProtectedRuntime::Codex,
        "/usr/local/libexec/workcell/real/codex",
    ),
    (
        ProtectedRuntime::Claude,
        "/usr/local/libexec/workcell/real/claude",
    ),
    (
        ProtectedRuntime::Copilot,
        "/usr/local/libexec/workcell/real/copilot",
    ),
];

const APPROVED_WRAPPER_SCRIPTS: &[(ApprovedWrapper, &str)] = &[
    (
        ApprovedWrapper::Development,
        "/usr/local/libexec/workcell/development-wrapper.sh",
    ),
    (
        ApprovedWrapper::Git,
        "/usr/local/libexec/workcell/git-wrapper.sh",
    ),
    (
        ApprovedWrapper::Node,
        "/usr/local/libexec/workcell/node-wrapper.sh",
    ),
    (
        ApprovedWrapper::Provider,
        "/usr/local/libexec/workcell/provider-wrapper.sh",
    ),
];
const APPROVED_WRAPPER_LAUNCHERS: &[&str] = &["/bin/bash"];
const APPROVED_NATIVE_LAUNCHERS: &[&str] = &[
    "/usr/local/libexec/workcell/core/launcher",
    "/usr/local/libexec/workcell/core/git",
];

const MUTABLE_EXEC_ROOTS: &[&str] = &["/workspace", "/state"];
const ALLOWED_LD_PRELOAD: &str = "/usr/local/lib/libworkcell_exec_guard.so";
const AT_EMPTY_PATH_FLAG: c_int = 0x1000;

#[cfg(target_os = "linux")]
const SYS_EXECVE: c_long = libc::SYS_execve as c_long;
#[cfg(target_os = "linux")]
const SYS_EXECVEAT: c_long = libc::SYS_execveat as c_long;
#[cfg(not(target_os = "linux"))]
const SYS_EXECVE: c_long = -1;
#[cfg(not(target_os = "linux"))]
const SYS_EXECVEAT: c_long = -1;

const ARG_BLOCK_MESSAGE_PREFIX: &str =
    "Workcell blocked git control-plane override: remove unsafe git override (reason: ";
const ARG_BLOCK_MESSAGE_SUFFIX: &str = ").\n";
const ENV_BLOCK_MESSAGE: &str = "Workcell blocked git control-plane override: remove GIT_CONFIG_*, GIT_CONFIG_GLOBAL, GIT_CONFIG_SYSTEM, GIT_DIR, GIT_WORK_TREE, GIT_COMMON_DIR, GIT_EXEC_PATH, GIT_OBJECT_DIRECTORY, GIT_ALTERNATE_OBJECT_DIRECTORIES, GIT_INDEX_FILE, GIT_ASKPASS, GIT_EDITOR, GIT_SEQUENCE_EDITOR, GIT_SSH, GIT_SSH_COMMAND, SSH_ASKPASS, EDITOR, PAGER, or VISUAL overrides.\n";
const PROTECTED_RUNTIME_BLOCK_MESSAGE: &str =
    "Workcell blocked direct protected runtime execution outside approved wrappers.\n";
const MUTABLE_NATIVE_EXEC_BLOCK_MESSAGE: &str = "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile.\n";
const WORKCELL_LAUNCHER_LOADER_ENV_BLOCK_MESSAGE: &str =
    "Workcell blocked unsafe dynamic-loader environment for Workcell launcher execution.\n";

type ExecveFn =
    unsafe extern "C" fn(*const c_char, *const *const c_char, *const *const c_char) -> c_int;
type ExecvFn = unsafe extern "C" fn(*const c_char, *const *const c_char) -> c_int;
type ExecvpFn = unsafe extern "C" fn(*const c_char, *const *const c_char) -> c_int;
type ExecvpeFn =
    unsafe extern "C" fn(*const c_char, *const *const c_char, *const *const c_char) -> c_int;
type ExecveatFn = unsafe extern "C" fn(
    c_int,
    *const c_char,
    *const *const c_char,
    *const *const c_char,
    c_int,
) -> c_int;
type FexecveFn = unsafe extern "C" fn(c_int, *const *const c_char, *const *const c_char) -> c_int;
type PosixSpawnFn = unsafe extern "C" fn(
    *mut pid_t,
    *const c_char,
    *const libc::posix_spawn_file_actions_t,
    *const libc::posix_spawnattr_t,
    *const *const c_char,
    *const *const c_char,
) -> c_int;
type PosixSpawnpFn = unsafe extern "C" fn(
    *mut pid_t,
    *const c_char,
    *const libc::posix_spawn_file_actions_t,
    *const libc::posix_spawnattr_t,
    *const *const c_char,
    *const *const c_char,
) -> c_int;
type SyscallFn = unsafe extern "C" fn(c_long, ...) -> c_long;

static EXECVE_FN: OnceLock<ExecveFn> = OnceLock::new();
static EXECV_FN: OnceLock<ExecvFn> = OnceLock::new();
static EXECVP_FN: OnceLock<ExecvpFn> = OnceLock::new();
static EXECVPE_FN: OnceLock<ExecvpeFn> = OnceLock::new();
static EXECVEAT_FN: OnceLock<ExecveatFn> = OnceLock::new();
static FEXECVE_FN: OnceLock<FexecveFn> = OnceLock::new();
static POSIX_SPAWN_FN: OnceLock<PosixSpawnFn> = OnceLock::new();
static POSIX_SPAWNP_FN: OnceLock<PosixSpawnpFn> = OnceLock::new();
static REAL_SYSCALL_FN: OnceLock<SyscallFn> = OnceLock::new();
static STRICT_MUTABLE_EXEC_BLOCK: OnceLock<bool> = OnceLock::new();
static PROTECTED_RUNTIME_SIGS: OnceLock<Vec<(ProtectedRuntime, StatSignature)>> = OnceLock::new();
static PROTECTED_GIT_SIGS: OnceLock<Vec<StatSignature>> = OnceLock::new();
static DYNAMIC_LOADER_SIGS: OnceLock<Vec<(String, StatSignature)>> = OnceLock::new();

fn current_mode_blocks_mutable_native_exec() -> bool {
    *STRICT_MUTABLE_EXEC_BLOCK.get_or_init(|| {
        let Ok(environment) = fs::read("/proc/1/environ") else {
            return true;
        };

        let mut mode: Option<String> = None;
        let mut profile: Option<String> = None;
        for entry in environment
            .split(|byte| *byte == 0)
            .filter(|entry| !entry.is_empty())
        {
            if let Some(value) = entry.strip_prefix(b"WORKCELL_MODE=") {
                mode = Some(String::from_utf8_lossy(value).into_owned());
            } else if let Some(value) = entry.strip_prefix(b"CODEX_PROFILE=") {
                profile = Some(String::from_utf8_lossy(value).into_owned());
            }
        }

        !matches_non_strict(mode.as_deref()) && !matches_non_strict(profile.as_deref())
    })
}

fn matches_non_strict(value: Option<&str>) -> bool {
    matches!(value, Some(candidate) if !candidate.is_empty() && !candidate.eq_ignore_ascii_case("strict"))
}

fn path_has_root_prefix(path: &str, root: &str) -> bool {
    path == root
        || path
            .strip_prefix(root)
            .is_some_and(|suffix| suffix.starts_with('/'))
}

fn resolved_path_is_mutable_root(path: &str) -> bool {
    MUTABLE_EXEC_ROOTS
        .iter()
        .any(|root| path_has_root_prefix(path, root))
}

fn parse_nonnegative_long(value: &str) -> Option<i64> {
    value.parse::<i64>().ok().filter(|value| *value >= 0)
}

fn path_is_current_process_fd_path(path: &str) -> Option<c_int> {
    let components: Vec<&str> = path
        .split('/')
        .filter(|component| !component.is_empty() && *component != ".")
        .collect();

    match components.as_slice() {
        ["dev", "stdin"] => Some(libc::STDIN_FILENO),
        ["dev", "stdout"] => Some(libc::STDOUT_FILENO),
        ["dev", "stderr"] => Some(libc::STDERR_FILENO),
        ["dev", "fd", fd] => fd.parse::<c_int>().ok(),
        ["proc", "self", "fd", fd] => fd.parse::<c_int>().ok(),
        ["proc", "thread-self", "fd", fd] => fd.parse::<c_int>().ok(),
        ["proc", pid, "fd", fd]
            // SAFETY: getpid() is a niladic syscall with no preconditions and no invalid return.
            if parse_nonnegative_long(pid) == Some(unsafe { libc::getpid() as i64 }) =>
        {
            fd.parse::<c_int>().ok()
        }
        ["proc", "self", "task", _tid, "fd", fd] => fd.parse::<c_int>().ok(),
        ["proc", pid, "task", _tid, "fd", fd]
            // SAFETY: getpid() is a niladic syscall with no preconditions and no invalid return.
            if parse_nonnegative_long(pid) == Some(unsafe { libc::getpid() as i64 }) =>
        {
            fd.parse::<c_int>().ok()
        }
        _ => None,
    }
}

fn trim_deleted_suffix(path: &str) -> &str {
    path.strip_suffix(" (deleted)").unwrap_or(path)
}

fn proc_fd_path(fd: c_int) -> String {
    format!("/proc/self/fd/{fd}")
}

fn duplicate_fd_file(fd: c_int) -> Option<File> {
    File::open(proc_fd_path(fd)).ok()
}

fn fd_target_is_mutable_root(fd: c_int) -> bool {
    let Ok(target) = fs::read_link(proc_fd_path(fd)) else {
        return false;
    };

    if let Ok(canonical) = fs::canonicalize(&target) {
        return resolved_path_is_mutable_root(&canonical.to_string_lossy());
    }

    resolved_path_is_mutable_root(trim_deleted_suffix(&target.to_string_lossy()))
}

fn collect_cstring_array(ptr: *const *const c_char) -> Vec<String> {
    let mut values = Vec::new();
    if ptr.is_null() {
        return values;
    }

    let mut index = 0usize;
    loop {
        // SAFETY: ptr is a non-null, NUL-sentinel-terminated char** (exec ABI / libc environ); index walks up to the sentinel checked below.
        let current = unsafe { *ptr.add(index) };
        if current.is_null() {
            break;
        }
        // SAFETY: current is a valid NUL-terminated C string element of the exec argv/envp/environ array.
        let entry = unsafe { CStr::from_ptr(current) }
            .to_string_lossy()
            .into_owned();
        values.push(entry);
        index += 1;
    }
    values
}

fn effective_env_ptr(envp: *const *const c_char) -> *const *const c_char {
    if envp.is_null() {
        // SAFETY: environ is libc-initialized; read in the calling thread with no concurrent setenv/putenv.
        unsafe { environ.cast() }
    } else {
        envp
    }
}

fn path_from_env_entries(env_entries: &[String]) -> Option<String> {
    env_entries
        .iter()
        .find_map(|entry| entry.strip_prefix("PATH=").map(ToOwned::to_owned))
        .or_else(|| env::var("PATH").ok())
}

fn resolve_command_via_path_value(command: &str, path_value: Option<&str>) -> Option<String> {
    if command.is_empty() || command.contains('/') {
        return None;
    }

    let path_value = path_value?;
    for segment in path_value.split(':').filter(|segment| !segment.is_empty()) {
        let candidate = format!("{segment}/{command}");
        let Ok(cstring) = CString::new(candidate.as_str()) else {
            continue;
        };

        // SAFETY: cstring is a live NUL-terminated CString valid for the call; access only reads the path.
        let executable = unsafe { libc::access(cstring.as_ptr(), libc::X_OK) == 0 };
        if !executable {
            continue;
        }

        if let Ok(canonical) = fs::canonicalize(&candidate) {
            return Some(canonical.to_string_lossy().into_owned());
        }
        return Some(candidate);
    }

    None
}

fn file_descriptor_is_native_elf(fd: c_int) -> bool {
    let Some(mut file) = duplicate_fd_file(fd) else {
        return false;
    };

    let mut header = [0u8; 4];
    matches!(file.read_exact(&mut header), Ok(())) && header == [0x7f, b'E', b'L', b'F']
}

fn file_descriptor_is_mutable_native_exec(fd: c_int) -> bool {
    current_mode_blocks_mutable_native_exec()
        && fd >= 0
        && fd_target_is_mutable_root(fd)
        && file_descriptor_is_native_elf(fd)
}

fn next_shebang_token(cursor: &mut &str) -> Option<String> {
    *cursor = cursor.trim_start_matches([' ', '\t']);
    let end = cursor.find([' ', '\t', '\r', '\n']).unwrap_or(cursor.len());
    if end == 0 {
        return None;
    }
    let token = cursor[..end].to_string();
    *cursor = &cursor[end..];
    Some(token)
}

fn token_basename(token: &str) -> &str {
    token.rsplit('/').next().unwrap_or(token)
}

fn token_uses_env_interpreter(token: &str) -> bool {
    token_basename(token) == "env"
}

fn token_is_shell_interpreter(token: &str) -> bool {
    matches!(
        token_basename(token),
        "sh" | "bash" | "dash" | "zsh" | "ksh" | "fish"
    )
}

fn shell_option_executes_command(option: &str) -> bool {
    option.starts_with('-') && option[1..].contains('c')
}

fn env_command_targets_protected_runtime(cursor: &str, env_entries: &[String]) -> bool {
    let mut scan = cursor;
    let mut path_override: Option<String> = None;

    while let Some(token) = next_shebang_token(&mut scan) {
        if token == "-u" {
            let _ = next_shebang_token(&mut scan);
            continue;
        }

        if let Some(path) = token.strip_prefix("PATH=") {
            path_override = Some(path.to_owned());
        }

        if token.starts_with('-') || token.contains('=') {
            continue;
        }

        let token_path = resolve_command_via_path_value(
            &token,
            path_override
                .as_deref()
                .or(path_from_env_entries(env_entries).as_deref()),
        )
        .unwrap_or(token.clone());

        if token_is_shell_interpreter(&token_path)
            && let Some(target) = next_shebang_token(&mut scan)
            && shell_option_executes_command(&target)
        {
            return true;
        }

        if classify_protected_runtime_path(&token_path) != ProtectedRuntime::None {
            return true;
        }

        if !is_dynamic_loader_path(&token_path) {
            return false;
        }

        let Some(target) = next_shebang_token(&mut scan) else {
            return false;
        };
        return classify_protected_runtime_path(&target) != ProtectedRuntime::None;
    }

    false
}

fn buffer_targets_protected_runtime_via_shebang(buffer: &str, env_entries: &[String]) -> bool {
    if !buffer.starts_with("#!") {
        return false;
    }

    let mut cursor = &buffer[2..];
    let Some(interpreter) = next_shebang_token(&mut cursor) else {
        return false;
    };

    if classify_protected_runtime_path(&interpreter) != ProtectedRuntime::None {
        return true;
    }

    if token_uses_env_interpreter(&interpreter) {
        return env_command_targets_protected_runtime(cursor, env_entries);
    }

    if !is_dynamic_loader_path(&interpreter) {
        return false;
    }

    let Some(target) = next_shebang_token(&mut cursor) else {
        return false;
    };
    classify_protected_runtime_path(&target) != ProtectedRuntime::None
}

fn file_descriptor_targets_protected_runtime_via_shebang(
    fd: c_int,
    env_entries: &[String],
) -> bool {
    let Some(mut file) = duplicate_fd_file(fd) else {
        return false;
    };

    let mut buffer = [0u8; 511];
    let Ok(bytes) = file.read(&mut buffer) else {
        return false;
    };
    if bytes == 0 {
        return false;
    }

    buffer_targets_protected_runtime_via_shebang(
        &String::from_utf8_lossy(&buffer[..bytes]),
        env_entries,
    )
}

fn file_descriptor_is_mutable_shebang_to_protected_runtime(
    fd: c_int,
    env_entries: &[String],
) -> bool {
    fd >= 0
        && fd_target_is_mutable_root(fd)
        && file_descriptor_targets_protected_runtime_via_shebang(fd, env_entries)
}

fn canonicalize_existing_path(path: &str) -> Option<String> {
    fs::canonicalize(path)
        .ok()
        .map(|path| path.to_string_lossy().into_owned())
}

fn path_is_mutable_native_exec(path: &str) -> bool {
    if !current_mode_blocks_mutable_native_exec() || path.is_empty() {
        return false;
    }

    let Some(resolved) = canonicalize_existing_path(path) else {
        return false;
    };
    if !resolved_path_is_mutable_root(&resolved) {
        return false;
    }

    let Ok(mut file) = File::open(&resolved) else {
        return false;
    };
    let mut header = [0u8; 4];
    matches!(file.read_exact(&mut header), Ok(())) && header == [0x7f, b'E', b'L', b'F']
}

fn path_is_mutable_shebang_to_protected_runtime(path: &str, env_entries: &[String]) -> bool {
    let Some(resolved) = canonicalize_existing_path(path) else {
        return false;
    };
    if !resolved_path_is_mutable_root(&resolved) {
        return false;
    }

    let Ok(mut file) = File::open(&resolved) else {
        return false;
    };
    let mut buffer = [0u8; 511];
    let Ok(bytes) = file.read(&mut buffer) else {
        return false;
    };
    if bytes == 0 {
        return false;
    }

    buffer_targets_protected_runtime_via_shebang(
        &String::from_utf8_lossy(&buffer[..bytes]),
        env_entries,
    )
}

fn current_process_wrapper_env_is_clean() -> bool {
    let ld_preload = env::var("LD_PRELOAD").ok();

    env::var_os("BASH_ENV").is_none()
        && env::var_os("ENV").is_none()
        && env::var_os("LD_AUDIT").is_none()
        && env::var_os("LD_LIBRARY_PATH").is_none()
        && env::var_os("SSL_CERT_FILE").is_none()
        && env::var_os("SSL_CERT_DIR").is_none()
        && ld_preload
            .as_deref()
            .is_none_or(|value| value.is_empty() || value == ALLOWED_LD_PRELOAD)
}

fn env_entry_value<'a>(entry: &'a str, key: &str) -> Option<&'a str> {
    let (entry_key, value) = entry.split_once('=')?;
    if entry_key == key { Some(value) } else { None }
}

fn env_has_unsafe_workcell_launcher_loader_override(env_entries: &[String]) -> bool {
    env_entries.iter().any(|entry| {
        env_entry_value(entry, "LD_AUDIT").is_some_and(|value| !value.is_empty())
            || env_entry_value(entry, "LD_LIBRARY_PATH").is_some_and(|value| !value.is_empty())
            || env_entry_value(entry, "LD_TRACE_LOADED_OBJECTS")
                .is_some_and(|value| !value.is_empty())
            || env_entry_value(entry, "LD_PRELOAD")
                .is_some_and(|value| !value.is_empty() && value != ALLOWED_LD_PRELOAD)
    })
}

fn path_is_workcell_native_launcher(path: &str) -> bool {
    !path.is_empty() && path_matches_any_same_file(Path::new(path), APPROVED_NATIVE_LAUNCHERS)
}

fn stat_signature_matches_any_same_file(signature: &StatSignature, candidates: &[&str]) -> bool {
    candidates.iter().any(|candidate| {
        canonicalize_existing_path(candidate)
            .and_then(|resolved| fs::metadata(resolved).ok())
            .map(|metadata| metadata_signature_from_metadata(&metadata))
            .is_some_and(|candidate_sig| {
                signature.dev == candidate_sig.dev && signature.ino == candidate_sig.ino
            })
    })
}

fn fd_matches_any_same_file(fd: c_int, candidates: &[&str]) -> bool {
    duplicate_fd_file(fd)
        .and_then(|file| file.metadata().ok())
        .map(|metadata| {
            stat_signature_matches_any_same_file(
                &metadata_signature_from_metadata(&metadata),
                candidates,
            )
        })
        .unwrap_or(false)
}

fn fd_matches_workcell_native_launcher(fd: c_int) -> bool {
    fd_matches_any_same_file(fd, APPROVED_NATIVE_LAUNCHERS)
}

fn should_block_workcell_launcher_loader_env(path: &str, env_entries: &[String]) -> bool {
    path_is_workcell_native_launcher(path)
        && env_has_unsafe_workcell_launcher_loader_override(env_entries)
}

fn should_block_workcell_launcher_fd_loader_env(fd: c_int, env_entries: &[String]) -> bool {
    fd_matches_workcell_native_launcher(fd)
        && env_has_unsafe_workcell_launcher_loader_override(env_entries)
}

fn path_matches_any_same_file(path: &Path, candidates: &[&str]) -> bool {
    let Ok(metadata) = fs::metadata(path) else {
        return false;
    };
    let signature = metadata_signature_from_metadata(&metadata);

    stat_signature_matches_any_same_file(&signature, candidates)
}

fn current_process_executable_is_approved_wrapper_launcher() -> bool {
    let Ok(current_exe) = fs::read_link("/proc/self/exe") else {
        return false;
    };

    path_matches_any_same_file(&current_exe, APPROVED_WRAPPER_LAUNCHERS)
}

fn approved_wrapper_requires_native_launcher_parent(wrapper: ApprovedWrapper) -> bool {
    matches!(
        wrapper,
        ApprovedWrapper::Git | ApprovedWrapper::Node | ApprovedWrapper::Provider
    )
}

fn current_process_parent_is_approved_native_launcher() -> bool {
    // SAFETY: getppid() is a niladic syscall with no preconditions.
    let parent_pid = unsafe { libc::getppid() };
    if parent_pid < 1 {
        return false;
    }

    let parent_exe_path = format!("/proc/{parent_pid}/exe");
    let Ok(parent_exe) = fs::read_link(parent_exe_path) else {
        return false;
    };

    path_matches_any_same_file(&parent_exe, APPROVED_NATIVE_LAUNCHERS)
}

fn current_process_approved_wrapper() -> ApprovedWrapper {
    if !current_process_wrapper_env_is_clean() {
        return ApprovedWrapper::None;
    }

    let Ok(cmdline) = fs::read("/proc/self/cmdline") else {
        return ApprovedWrapper::None;
    };
    let args: Vec<String> = cmdline
        .split(|byte| *byte == 0)
        .filter(|entry| !entry.is_empty())
        .map(|entry| String::from_utf8_lossy(entry).into_owned())
        .collect();

    if !current_process_executable_is_approved_wrapper_launcher() {
        return ApprovedWrapper::None;
    }

    let Some(candidate) = args.get(1) else {
        return ApprovedWrapper::None;
    };

    let wrapper = APPROVED_WRAPPER_SCRIPTS
        .iter()
        .find_map(|(kind, approved)| {
            if candidate == approved
                || fs::canonicalize(candidate)
                    .ok()
                    .is_some_and(|resolved| resolved.to_string_lossy() == *approved)
            {
                Some(*kind)
            } else {
                None
            }
        })
        .unwrap_or(ApprovedWrapper::None);

    if approved_wrapper_requires_native_launcher_parent(wrapper)
        && !current_process_parent_is_approved_native_launcher()
    {
        return ApprovedWrapper::None;
    }

    wrapper
}

fn approved_wrapper_allows_runtime(wrapper: ApprovedWrapper, kind: ProtectedRuntime) -> bool {
    match wrapper {
        ApprovedWrapper::Provider => matches!(
            kind,
            ProtectedRuntime::Codex
                | ProtectedRuntime::Claude
                | ProtectedRuntime::Copilot
                | ProtectedRuntime::Node
        ),
        ApprovedWrapper::Git => kind == ProtectedRuntime::Git,
        ApprovedWrapper::Node => kind == ProtectedRuntime::Node,
        ApprovedWrapper::Development | ApprovedWrapper::None => false,
    }
}

fn read_all(file: &mut File) -> Option<Vec<u8>> {
    let mut buffer = Vec::new();
    file.read_to_end(&mut buffer).ok()?;
    Some(buffer)
}

fn compare_open_files(left: &mut File, right: &mut File) -> bool {
    match (read_all(left), read_all(right)) {
        (Some(left), Some(right)) => left == right,
        _ => false,
    }
}

#[cfg(target_os = "macos")]
fn file_type_bits() -> u32 {
    u32::from(libc::S_IFMT)
}

#[cfg(not(target_os = "macos"))]
fn file_type_bits() -> u32 {
    libc::S_IFMT
}

#[cfg(target_os = "macos")]
fn regular_file_mode() -> u32 {
    u32::from(libc::S_IFREG)
}

#[cfg(not(target_os = "macos"))]
fn regular_file_mode() -> u32 {
    libc::S_IFREG
}

fn metadata_signature_from_metadata(metadata: &fs::Metadata) -> StatSignature {
    StatSignature {
        dev: metadata.dev(),
        ino: metadata.ino(),
        size: metadata.size() as i64,
        mode: metadata.mode(),
    }
}

fn stat_signature_from_stat(stat_buf: &libc::stat) -> Option<StatSignature> {
    #[cfg(target_os = "macos")]
    let dev = u64::try_from(stat_buf.st_dev).ok()?;
    #[cfg(not(target_os = "macos"))]
    let dev = stat_buf.st_dev;

    #[cfg(target_os = "macos")]
    let mode = u32::from(stat_buf.st_mode);
    #[cfg(not(target_os = "macos"))]
    let mode = stat_buf.st_mode;

    Some(StatSignature {
        dev,
        ino: stat_buf.st_ino,
        size: stat_buf.st_size,
        mode,
    })
}

fn protected_runtime_signatures() -> &'static Vec<(ProtectedRuntime, StatSignature)> {
    PROTECTED_RUNTIME_SIGS.get_or_init(|| {
        PROTECTED_RUNTIME_PATHS
            .iter()
            .filter_map(|(kind, path)| {
                fs::metadata(path)
                    .ok()
                    .map(|metadata| (*kind, metadata_signature_from_metadata(&metadata)))
            })
            .collect()
    })
}

fn protected_git_signatures() -> &'static Vec<StatSignature> {
    PROTECTED_GIT_SIGS.get_or_init(|| {
        PROTECTED_GIT_PATHS
            .iter()
            .filter_map(|path| {
                fs::metadata(path)
                    .ok()
                    .map(|metadata| metadata_signature_from_metadata(&metadata))
            })
            .collect()
    })
}

fn dynamic_loader_candidate_paths() -> Vec<String> {
    let mut paths = Vec::new();

    for path in [
        "/lib64/ld-linux-x86-64.so.2",
        "/lib/ld-linux-aarch64.so.1",
        "/lib/ld-linux-armhf.so.3",
    ] {
        if fs::metadata(path).is_ok() {
            paths.push(path.to_owned());
        }
    }

    for dir in ["/lib", "/lib64"] {
        let Ok(entries) = fs::read_dir(dir) else {
            continue;
        };
        for entry in entries.flatten() {
            let path = entry.path();
            let Some(path_string) = path.to_str().map(str::to_owned) else {
                continue;
            };
            if path
                .file_name()
                .and_then(|name| name.to_str())
                .is_some_and(is_dynamic_loader_path)
                && !paths.iter().any(|candidate| candidate == &path_string)
            {
                paths.push(path_string);
            }
        }
    }

    paths
}

fn dynamic_loader_signatures() -> &'static Vec<(String, StatSignature)> {
    DYNAMIC_LOADER_SIGS.get_or_init(|| {
        dynamic_loader_candidate_paths()
            .into_iter()
            .filter_map(|path| {
                fs::metadata(&path)
                    .ok()
                    .map(|metadata| (path, metadata_signature_from_metadata(&metadata)))
            })
            .collect()
    })
}

fn stat_matches_dynamic_loader(candidate: &StatSignature) -> bool {
    dynamic_loader_signatures()
        .iter()
        .any(|(_, loader)| loader.dev == candidate.dev && loader.ino == candidate.ino)
}

fn candidate_size_matches_dynamic_loader(candidate: &StatSignature) -> bool {
    (candidate.mode & file_type_bits()) == regular_file_mode()
        && dynamic_loader_signatures()
            .iter()
            .any(|(_, loader)| loader.size == candidate.size)
}

fn file_matches_dynamic_loader_by_contents(
    candidate: &mut File,
    candidate_signature: &StatSignature,
) -> bool {
    if (candidate_signature.mode & file_type_bits()) != regular_file_mode() {
        return false;
    }

    for (path, loader_signature) in dynamic_loader_signatures() {
        if loader_signature.size != candidate_signature.size {
            continue;
        }

        let Ok(mut loader_file) = File::open(path) else {
            continue;
        };
        let Ok(mut candidate_clone) = candidate.try_clone() else {
            continue;
        };
        if compare_open_files(&mut candidate_clone, &mut loader_file) {
            return true;
        }
    }

    false
}

fn stat_matches_protected_runtime(candidate: &StatSignature) -> ProtectedRuntime {
    protected_runtime_signatures()
        .iter()
        .find_map(|(kind, protected)| {
            if protected.dev == candidate.dev && protected.ino == candidate.ino {
                Some(*kind)
            } else {
                None
            }
        })
        .unwrap_or(ProtectedRuntime::None)
}

fn candidate_size_matches_protected_runtime(candidate: &StatSignature) -> bool {
    (candidate.mode & file_type_bits()) == regular_file_mode()
        && protected_runtime_signatures()
            .iter()
            .any(|(_, protected)| protected.size == candidate.size)
}

fn file_matches_protected_runtime_by_contents(
    candidate: &mut File,
    candidate_signature: &StatSignature,
) -> ProtectedRuntime {
    if (candidate_signature.mode & file_type_bits()) != regular_file_mode() {
        return ProtectedRuntime::None;
    }

    for (kind, path) in PROTECTED_RUNTIME_PATHS {
        let Ok(metadata) = fs::metadata(path) else {
            continue;
        };
        let protected_signature = metadata_signature_from_metadata(&metadata);
        if protected_signature.size != candidate_signature.size {
            continue;
        }

        let Ok(mut protected_file) = File::open(path) else {
            continue;
        };
        let Ok(mut candidate_clone) = candidate.try_clone() else {
            continue;
        };
        if compare_open_files(&mut candidate_clone, &mut protected_file) {
            return *kind;
        }
    }

    ProtectedRuntime::None
}

fn classify_protected_runtime_path(path: &str) -> ProtectedRuntime {
    if path.is_empty() {
        return ProtectedRuntime::None;
    }

    if let Ok(metadata) = fs::metadata(path) {
        let candidate = metadata_signature_from_metadata(&metadata);
        let kind = stat_matches_protected_runtime(&candidate);
        if kind != ProtectedRuntime::None {
            return kind;
        }
        if !candidate_size_matches_protected_runtime(&candidate) {
            return ProtectedRuntime::None;
        }
    }

    let Ok(mut file) = File::open(path) else {
        return ProtectedRuntime::None;
    };
    let Ok(metadata) = file.metadata() else {
        return ProtectedRuntime::None;
    };
    let signature = metadata_signature_from_metadata(&metadata);
    file_matches_protected_runtime_by_contents(&mut file, &signature)
}

fn classify_protected_runtime_fd(fd: c_int) -> ProtectedRuntime {
    let Some(mut file) = duplicate_fd_file(fd) else {
        return ProtectedRuntime::None;
    };
    let Ok(metadata) = file.metadata() else {
        return ProtectedRuntime::None;
    };
    let candidate = metadata_signature_from_metadata(&metadata);
    let kind = stat_matches_protected_runtime(&candidate);
    if kind != ProtectedRuntime::None {
        return kind;
    }
    file_matches_protected_runtime_by_contents(&mut file, &candidate)
}

fn is_dynamic_loader_path(path: &str) -> bool {
    let base = Path::new(path)
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or(path);
    base.starts_with("ld-linux-") || base.starts_with("ld-musl-")
}

fn path_points_to_dynamic_loader(path: &str) -> bool {
    if is_dynamic_loader_path(path) {
        return true;
    }
    if let Ok(target) = fs::read_link(path) {
        let target_string = target.to_string_lossy();
        if is_dynamic_loader_path(trim_deleted_suffix(&target_string)) {
            return true;
        }
    }
    if fs::canonicalize(path)
        .ok()
        .is_some_and(|target| is_dynamic_loader_path(&target.to_string_lossy()))
    {
        return true;
    }

    let Ok(mut file) = File::open(path) else {
        return false;
    };
    let Ok(metadata) = file.metadata() else {
        return false;
    };
    let candidate = metadata_signature_from_metadata(&metadata);
    stat_matches_dynamic_loader(&candidate)
        || (candidate_size_matches_dynamic_loader(&candidate)
            && file_matches_dynamic_loader_by_contents(&mut file, &candidate))
}

fn fd_is_dynamic_loader(fd: c_int) -> bool {
    if fs::read_link(proc_fd_path(fd)).ok().is_some_and(|target| {
        let target_string = target.to_string_lossy();
        path_points_to_dynamic_loader(trim_deleted_suffix(&target_string))
    }) {
        return true;
    }

    let Some(mut file) = duplicate_fd_file(fd) else {
        return false;
    };
    let Ok(metadata) = file.metadata() else {
        return false;
    };
    let candidate = metadata_signature_from_metadata(&metadata);
    stat_matches_dynamic_loader(&candidate)
        || (candidate_size_matches_dynamic_loader(&candidate)
            && file_matches_dynamic_loader_by_contents(&mut file, &candidate))
}

fn classify_loader_args(args: &[String]) -> ProtectedRuntime {
    args.iter()
        .skip(1)
        .map(|target| classify_protected_runtime_path(target))
        .find(|kind| *kind != ProtectedRuntime::None)
        .unwrap_or(ProtectedRuntime::None)
}

fn classify_loader_target(path: &str, args: &[String]) -> ProtectedRuntime {
    if !path_points_to_dynamic_loader(path) {
        return ProtectedRuntime::None;
    }
    classify_loader_args(args)
}

fn classify_loader_fd_target(fd: c_int, args: &[String]) -> ProtectedRuntime {
    if fd_is_dynamic_loader(fd) {
        classify_loader_args(args)
    } else {
        ProtectedRuntime::None
    }
}

fn loader_arg_targets_mutable_native_exec(target: &str) -> bool {
    if let Some(proc_fd) = path_is_current_process_fd_path(target) {
        return file_descriptor_is_mutable_native_exec(proc_fd);
    }
    path_is_mutable_native_exec(target)
}

fn loader_targets_mutable_native_exec(path: &str, args: &[String]) -> bool {
    path_points_to_dynamic_loader(path)
        && args
            .iter()
            .skip(1)
            .any(|target| loader_arg_targets_mutable_native_exec(target))
}

fn loader_fd_targets_mutable_native_exec(fd: c_int, args: &[String]) -> bool {
    fd_is_dynamic_loader(fd)
        && args
            .iter()
            .skip(1)
            .any(|target| loader_arg_targets_mutable_native_exec(target))
}

fn protected_runtime_exec_blocked(
    kind: ProtectedRuntime,
    approved_wrapper: ApprovedWrapper,
) -> bool {
    kind != ProtectedRuntime::None && !approved_wrapper_allows_runtime(approved_wrapper, kind)
}

fn should_block_protected_runtime_kind(kind: ProtectedRuntime) -> bool {
    if kind == ProtectedRuntime::None {
        return false;
    }
    protected_runtime_exec_blocked(kind, current_process_approved_wrapper())
}

fn should_block_protected_runtime_exec(
    path: &str,
    args: &[String],
    env_entries: &[String],
) -> bool {
    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        let kind = classify_protected_runtime_fd(proc_fd);
        if kind != ProtectedRuntime::None {
            return should_block_protected_runtime_kind(kind);
        }
        let kind = classify_loader_fd_target(proc_fd, args);
        if kind != ProtectedRuntime::None {
            return should_block_protected_runtime_kind(kind);
        }
        return file_descriptor_is_mutable_shebang_to_protected_runtime(proc_fd, env_entries);
    }

    let mut kind = classify_protected_runtime_path(path);
    if kind == ProtectedRuntime::None {
        kind = classify_loader_target(path, args);
    }
    if kind != ProtectedRuntime::None {
        return should_block_protected_runtime_kind(kind);
    }

    path_is_mutable_shebang_to_protected_runtime(path, env_entries)
}

fn should_block_mutable_native_exec(path: &str, args: &[String]) -> bool {
    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        return file_descriptor_is_mutable_native_exec(proc_fd)
            || loader_fd_targets_mutable_native_exec(proc_fd, args);
    }

    path_is_mutable_native_exec(path) || loader_targets_mutable_native_exec(path, args)
}

fn resolve_exec_search_target(file: &str, env_entries: &[String]) -> String {
    resolve_command_via_path_value(file, path_from_env_entries(env_entries).as_deref())
        .unwrap_or_else(|| file.to_owned())
}

fn git_config_key_has_prefix_and_suffix(key: &str, prefix: &str, suffix: &str) -> bool {
    // `prefix`/`suffix` are ASCII, so slice with char-boundary-safe `get`: a git
    // config key with a multibyte character straddling the byte offset returns
    // `None` (correctly no match) instead of panicking on a non-boundary index.
    key.len() > prefix.len() + suffix.len()
        && key
            .get(..prefix.len())
            .is_some_and(|head| head.eq_ignore_ascii_case(prefix))
        && key
            .get(key.len() - suffix.len()..)
            .is_some_and(|tail| tail.eq_ignore_ascii_case(suffix))
}

fn git_config_key_is_blocked(key: &str) -> bool {
    matches!(
        key.to_ascii_lowercase().as_str(),
        "core.askpass"
            | "core.editor"
            | "core.fsmonitor"
            | "core.hookspath"
            | "core.pager"
            | "core.sshcommand"
            | "core.worktree"
            | "credential.helper"
            | "diff.external"
            | "include.path"
            | "sequence.editor"
    ) || git_config_key_has_prefix_and_suffix(key, "credential.", ".helper")
        || git_config_key_has_prefix_and_suffix(key, "includeif.", ".path")
        || (key.len() > 6
            && key
                .get(..6)
                .is_some_and(|head| head.eq_ignore_ascii_case("pager.")))
}

fn git_config_spec_is_blocked(spec: &str) -> bool {
    let key = spec.split_once('=').map(|(key, _)| key).unwrap_or(spec);
    let value = spec.split_once('=').map(|(_, value)| value);
    if let Some(value) = value
        && git_config_spec_value_is_explicit_safe(key, value)
    {
        return false;
    }
    !key.is_empty() && git_config_key_is_blocked(key)
}

fn git_config_spec_value_is_explicit_safe(key: &str, value: &str) -> bool {
    key.eq_ignore_ascii_case("core.fsmonitor")
        && matches!(
            value.to_ascii_lowercase().as_str(),
            "" | "false" | "0" | "no" | "off"
        )
}

fn stat_matches_protected_git(candidate: &StatSignature) -> bool {
    protected_git_signatures()
        .iter()
        .any(|protected| protected.dev == candidate.dev && protected.ino == candidate.ino)
}

fn is_git_path(path: &str) -> bool {
    if path.is_empty() {
        return false;
    }

    if Path::new(path)
        .file_name()
        .and_then(|name| name.to_str())
        .is_some_and(|name| name == "git")
    {
        return true;
    }

    fs::metadata(path)
        .ok()
        .map(|metadata| stat_matches_protected_git(&metadata_signature_from_metadata(&metadata)))
        .unwrap_or(false)
}

#[cfg(test)]
fn should_block(path: &str, args: &[String]) -> bool {
    should_block_reason(path, args).is_some()
}

fn should_block_reason(path: &str, args: &[String]) -> Option<&'static str> {
    if !is_git_path(path) || args.is_empty() {
        return None;
    }

    let mut saw_commit = false;
    let mut expect_config_arg = false;
    let mut expect_path_override_arg: Option<GitPathOverrideKind> = None;
    let mut expect_chdir_arg = false;

    for arg in args.iter().skip(1) {
        if expect_chdir_arg {
            expect_chdir_arg = false;
            continue;
        }

        if let Some(kind) = expect_path_override_arg.take() {
            if !git_path_override_is_allowed(kind, arg) {
                return Some(git_path_override_reason(kind));
            }
            continue;
        }

        if expect_config_arg {
            expect_config_arg = false;
            if git_config_spec_is_blocked(arg) {
                return Some("unsafe-inline-config");
            }
            continue;
        }

        if arg == "--" {
            break;
        }
        if saw_commit && git_commit_short_arg_invokes_no_verify(arg) {
            return Some("unsafe-commit-short-no-verify");
        }

        match arg.as_str() {
            "-c" | "--config-env" => {
                expect_config_arg = true;
                continue;
            }
            "-C" => {
                expect_chdir_arg = true;
                continue;
            }
            "--exec-path" => return Some("unsafe-exec-path"),
            "--no-verify" => return Some("unsafe-no-verify"),
            "--git-dir" => {
                expect_path_override_arg = Some(GitPathOverrideKind::GitDir);
                continue;
            }
            "--work-tree" => {
                expect_path_override_arg = Some(GitPathOverrideKind::WorkTree);
                continue;
            }
            "commit" => saw_commit = true,
            _ => {}
        }

        if let Some(spec) = arg.strip_prefix("--config-env=")
            && git_config_spec_is_blocked(spec)
        {
            return Some("unsafe-config-env");
        }
        if arg.starts_with("--exec-path=") {
            return Some("unsafe-exec-path");
        }
        if let Some(value) = arg.strip_prefix("--git-dir=")
            && !git_path_override_is_allowed(GitPathOverrideKind::GitDir, value)
        {
            return Some("unsafe-git-dir");
        }
        if let Some(value) = arg.strip_prefix("--work-tree=")
            && !git_path_override_is_allowed(GitPathOverrideKind::WorkTree, value)
        {
            return Some("unsafe-work-tree");
        }
    }

    None
}

#[derive(Clone, Copy)]
enum GitPathOverrideKind {
    GitDir,
    WorkTree,
}

fn git_path_override_reason(kind: GitPathOverrideKind) -> &'static str {
    match kind {
        GitPathOverrideKind::GitDir => "unsafe-git-dir",
        GitPathOverrideKind::WorkTree => "unsafe-work-tree",
    }
}

fn git_path_override_is_allowed(kind: GitPathOverrideKind, value: &str) -> bool {
    let value = value.trim_end_matches('/');
    match kind {
        GitPathOverrideKind::GitDir => value == "/workspace/.git",
        GitPathOverrideKind::WorkTree => value == "/workspace",
    }
}

fn git_commit_short_arg_invokes_no_verify(arg: &str) -> bool {
    if !arg.starts_with('-') || arg.starts_with("--") || arg.len() <= 1 {
        return false;
    }

    for ch in arg[1..].chars() {
        match ch {
            'n' => return true,
            'm' | 'F' | 'C' | 'c' | 't' | 'u' | 'S' => return false,
            _ => {}
        }
    }

    false
}

fn env_has_unsafe_git_override(env_entries: &[String]) -> bool {
    let mut count = 0usize;

    for entry in env_entries {
        // Inline argv config may disable fsmonitor with explicit false-ish
        // values, but env-sourced Git config stays fail-closed because it is
        // inherited ambiently.
        if let Some(value) = entry.strip_prefix("GIT_CONFIG_PARAMETERS=") {
            let lower = value.to_ascii_lowercase();
            if lower.contains("core.askpass")
                || lower.contains("core.editor")
                || lower.contains("core.fsmonitor")
                || lower.contains("core.hookspath")
                || lower.contains("core.pager")
                || lower.contains("core.sshcommand")
                || lower.contains("core.worktree")
                || lower.contains("credential.helper")
                || lower.contains("diff.external")
                || lower.contains("include.path")
                || lower.contains("includeif.")
                || lower.contains("pager.")
                || lower.contains("sequence.editor")
            {
                return true;
            }
        }

        if entry.starts_with("GIT_DIR=")
            || entry.starts_with("GIT_WORK_TREE=")
            || entry.starts_with("GIT_COMMON_DIR=")
            || entry.starts_with("GIT_EXEC_PATH=")
            || entry.starts_with("GIT_OBJECT_DIRECTORY=")
            || entry.starts_with("GIT_ALTERNATE_OBJECT_DIRECTORIES=")
            || entry.starts_with("GIT_INDEX_FILE=")
            || entry.starts_with("GIT_ASKPASS=")
            || entry.starts_with("GIT_EDITOR=")
            || entry.starts_with("GIT_EXTERNAL_DIFF=")
            || entry.starts_with("GIT_CONFIG_SYSTEM=")
            || entry.starts_with("GIT_PAGER=")
            || entry.starts_with("GIT_SEQUENCE_EDITOR=")
            || entry.starts_with("GIT_SSH=")
            || entry.starts_with("GIT_SSH_COMMAND=")
            || entry.starts_with("SSH_ASKPASS=")
            || entry.starts_with("EDITOR=")
            || entry.starts_with("PAGER=")
            || entry.starts_with("VISUAL=")
        {
            return true;
        }

        if let Some(value) = entry.strip_prefix("GIT_CONFIG_GLOBAL=")
            && value != "/dev/null"
        {
            return true;
        }

        if let Some(value) = entry.strip_prefix("GIT_CONFIG_NOSYSTEM=")
            && value != "1"
        {
            return true;
        }

        if let Some(value) = entry.strip_prefix("GIT_CONFIG_COUNT=") {
            count = value.parse::<usize>().unwrap_or(0);
        }
    }

    if count == 0 {
        return false;
    }

    env_entries.iter().any(|entry| {
        entry
            .strip_prefix("GIT_CONFIG_KEY_")
            .and_then(|value| value.split_once('=').map(|(_, key)| key))
            .is_some_and(git_config_key_is_blocked)
    })
}

fn report(message: &str) {
    // SAFETY: message is a live &str valid for len bytes (write is read-only); errno_location() returns libc's valid errno slot.
    unsafe {
        libc::write(
            libc::STDERR_FILENO,
            message.as_ptr().cast::<c_void>(),
            message.len(),
        );
        *errno_location() = libc::EPERM;
    }
}

#[cfg(target_os = "linux")]
unsafe fn errno_location() -> *mut c_int {
    // SAFETY: __errno_location() takes no arguments and always returns a valid per-thread errno pointer.
    unsafe { libc::__errno_location() }
}

#[cfg(target_os = "macos")]
unsafe fn errno_location() -> *mut c_int {
    // SAFETY: __error() takes no arguments and always returns a valid per-thread errno pointer.
    unsafe { libc::__error() }
}

fn report_env_block() {
    report(ENV_BLOCK_MESSAGE);
}

fn report_arg_block(reason: &str) {
    report(ARG_BLOCK_MESSAGE_PREFIX);
    report(reason);
    report(ARG_BLOCK_MESSAGE_SUFFIX);
}

fn report_protected_runtime_block() {
    report(PROTECTED_RUNTIME_BLOCK_MESSAGE);
}

fn report_mutable_native_exec_block() {
    report(MUTABLE_NATIVE_EXEC_BLOCK_MESSAGE);
}

fn report_workcell_launcher_loader_env_block() {
    report(WORKCELL_LAUNCHER_LOADER_ENV_BLOCK_MESSAGE);
}

unsafe fn load_symbol<T: Copy>(name: &CStr) -> T {
    // SAFETY: name is a valid NUL-terminated c-string literal; dlsym reads it and returns the RTLD_NEXT symbol address or null.
    let symbol = unsafe { libc::dlsym(libc::RTLD_NEXT, name.as_ptr().cast()) };
    assert!(!symbol.is_null(), "missing required symbol {:?}", name);
    // SAFETY: symbol is non-null (asserted) and pointer-sized; T is a same-width extern "C" fn pointer, so reinterpreting the address is valid on POSIX.
    unsafe { mem::transmute_copy(&symbol) }
}

fn execve_fn() -> ExecveFn {
    // SAFETY: c"execve" is a valid C-string literal and matches the ExecveFn ABI resolved into this OnceLock.
    *EXECVE_FN.get_or_init(|| unsafe { load_symbol(c"execve") })
}

fn execv_fn() -> ExecvFn {
    // SAFETY: c"execv" is a valid C-string literal and matches the ExecvFn ABI resolved into this OnceLock.
    *EXECV_FN.get_or_init(|| unsafe { load_symbol(c"execv") })
}

fn execvp_fn() -> ExecvpFn {
    // SAFETY: c"execvp" is a valid C-string literal and matches the ExecvpFn ABI resolved into this OnceLock.
    *EXECVP_FN.get_or_init(|| unsafe { load_symbol(c"execvp") })
}

fn execvpe_fn() -> ExecvpeFn {
    // SAFETY: c"execvpe" is a valid C-string literal and matches the ExecvpeFn ABI resolved into this OnceLock.
    *EXECVPE_FN.get_or_init(|| unsafe { load_symbol(c"execvpe") })
}

fn execveat_fn() -> ExecveatFn {
    // SAFETY: c"execveat" is a valid C-string literal and matches the ExecveatFn ABI resolved into this OnceLock.
    *EXECVEAT_FN.get_or_init(|| unsafe { load_symbol(c"execveat") })
}

fn fexecve_fn() -> FexecveFn {
    // SAFETY: c"fexecve" is a valid C-string literal and matches the FexecveFn ABI resolved into this OnceLock.
    *FEXECVE_FN.get_or_init(|| unsafe { load_symbol(c"fexecve") })
}

fn posix_spawn_fn() -> PosixSpawnFn {
    // SAFETY: c"posix_spawn" is a valid C-string literal and matches the PosixSpawnFn ABI resolved into this OnceLock.
    *POSIX_SPAWN_FN.get_or_init(|| unsafe { load_symbol(c"posix_spawn") })
}

fn posix_spawnp_fn() -> PosixSpawnpFn {
    // SAFETY: c"posix_spawnp" is a valid C-string literal and matches the PosixSpawnpFn ABI resolved into this OnceLock.
    *POSIX_SPAWNP_FN.get_or_init(|| unsafe { load_symbol(c"posix_spawnp") })
}

fn real_syscall_fn() -> SyscallFn {
    // SAFETY: c"syscall" is a valid C-string literal and matches the SyscallFn ABI resolved into this OnceLock.
    *REAL_SYSCALL_FN.get_or_init(|| unsafe { load_symbol(c"syscall") })
}

fn c_path_string(path: *const c_char) -> String {
    if path.is_null() {
        String::new()
    } else {
        // SAFETY: path is non-null (checked above) and a NUL-terminated C string from the exec ABI.
        unsafe { CStr::from_ptr(path) }
            .to_string_lossy()
            .into_owned()
    }
}

fn fd_matches_protected_git(fd: c_int) -> bool {
    duplicate_fd_file(fd)
        .and_then(|file| file.metadata().ok())
        .map(|metadata| stat_matches_protected_git(&metadata_signature_from_metadata(&metadata)))
        .unwrap_or(false)
}

fn is_git_execveat_target(dirfd: c_int, pathname: &str, flags: c_int) -> bool {
    if pathname.is_empty() && (flags & AT_EMPTY_PATH_FLAG) != 0 {
        return fd_matches_protected_git(dirfd);
    }

    if Path::new(pathname)
        .file_name()
        .and_then(|name| name.to_str())
        .is_some_and(|name| name == "git")
    {
        return true;
    }

    let Ok(c_path) = CString::new(pathname.as_bytes()) else {
        return false;
    };
    let mut stat_buf = MaybeUninit::<libc::stat>::uninit();
    // SAFETY: c_path is a live NUL-terminated CString; stat_buf.as_mut_ptr() is a valid writable stat buffer for fstatat.
    if unsafe { libc::fstatat(dirfd, c_path.as_ptr(), stat_buf.as_mut_ptr(), 0) } != 0 {
        return false;
    }
    // SAFETY: fstatat returned 0, so the kernel fully initialized stat_buf.
    let stat_buf = unsafe { stat_buf.assume_init() };

    let Some(signature) = stat_signature_from_stat(&stat_buf) else {
        return false;
    };

    stat_matches_protected_git(&signature)
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn workcell_syscall_shim(
    number: c_long,
    arg1: c_long,
    arg2: c_long,
    arg3: c_long,
    arg4: c_long,
    arg5: c_long,
    arg6: c_long,
) -> c_long {
    #[cfg(any(target_arch = "x86_64", target_arch = "aarch64"))]
    {
        if number == SYS_EXECVE {
            // SAFETY: number==SYS_execve, so arg1..arg3 are the (path, argv, envp) pointers of the execve ABI.
            return unsafe {
                execve(
                    arg1 as *const c_char,
                    arg2 as *const *const c_char,
                    arg3 as *const *const c_char,
                ) as c_long
            };
        }
        if number == SYS_EXECVEAT {
            // SAFETY: number==SYS_execveat, so arg1..arg5 are (dirfd, pathname, argv, envp, flags) per the execveat ABI.
            return unsafe {
                execveat(
                    arg1 as c_int,
                    arg2 as *const c_char,
                    arg3 as *const *const c_char,
                    arg4 as *const *const c_char,
                    arg5 as c_int,
                ) as c_long
            };
        }
    }

    // SAFETY: forwards the original syscall number and its 6 register args unchanged to the real libc syscall().
    unsafe { real_syscall_fn()(number, arg1, arg2, arg3, arg4, arg5, arg6) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execve(
    path: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let path_string = c_path_string(path);
    let args = collect_cstring_array(argv);
    let env_entries = collect_cstring_array(effective_env_ptr(envp));

    if should_block_workcell_launcher_loader_env(&path_string, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if should_block_protected_runtime_exec(&path_string, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&path_string, &args) {
        report_mutable_native_exec_block();
        return -1;
    }
    if is_git_path(&path_string) && env_has_unsafe_git_override(&env_entries) {
        report_env_block();
        return -1;
    }
    if let Some(reason) = should_block_reason(&path_string, &args) {
        report_arg_block(reason);
        return -1;
    }

    // SAFETY: forwards the caller's original, unmodified execve arguments to the real libc execve resolved via RTLD_NEXT.
    unsafe { execve_fn()(path, argv, envp) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execv(path: *const c_char, argv: *const *const c_char) -> c_int {
    let path_string = c_path_string(path);
    let args = collect_cstring_array(argv);
    // SAFETY: environ is libc-initialized; read in the calling thread with no concurrent setenv/putenv.
    let env_entries = collect_cstring_array(unsafe { environ.cast() });

    if should_block_workcell_launcher_loader_env(&path_string, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if should_block_protected_runtime_exec(&path_string, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&path_string, &args) {
        report_mutable_native_exec_block();
        return -1;
    }
    if is_git_path(&path_string) && env_has_unsafe_git_override(&env_entries) {
        report_env_block();
        return -1;
    }
    if let Some(reason) = should_block_reason(&path_string, &args) {
        report_arg_block(reason);
        return -1;
    }

    // SAFETY: forwards the caller's original, unmodified execv arguments to the real libc execv resolved via RTLD_NEXT.
    unsafe { execv_fn()(path, argv) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execvp(file: *const c_char, argv: *const *const c_char) -> c_int {
    let file_string = c_path_string(file);
    let args = collect_cstring_array(argv);
    // SAFETY: environ is libc-initialized; read in the calling thread with no concurrent setenv/putenv.
    let env_entries = collect_cstring_array(unsafe { environ.cast() });
    let effective_path = resolve_exec_search_target(&file_string, &env_entries);

    if should_block_workcell_launcher_loader_env(&effective_path, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if should_block_protected_runtime_exec(&effective_path, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&effective_path, &args) {
        report_mutable_native_exec_block();
        return -1;
    }
    if is_git_path(&file_string) && env_has_unsafe_git_override(&env_entries) {
        report_env_block();
        return -1;
    }
    if let Some(reason) = should_block_reason(&file_string, &args) {
        report_arg_block(reason);
        return -1;
    }

    // SAFETY: forwards the caller's original, unmodified execvp arguments to the real libc execvp resolved via RTLD_NEXT.
    unsafe { execvp_fn()(file, argv) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execvpe(
    file: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let file_string = c_path_string(file);
    let args = collect_cstring_array(argv);
    let env_entries = collect_cstring_array(effective_env_ptr(envp));
    let effective_path = resolve_exec_search_target(&file_string, &env_entries);

    if should_block_workcell_launcher_loader_env(&effective_path, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if should_block_protected_runtime_exec(&effective_path, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&effective_path, &args) {
        report_mutable_native_exec_block();
        return -1;
    }
    if is_git_path(&file_string) && env_has_unsafe_git_override(&env_entries) {
        report_env_block();
        return -1;
    }
    if let Some(reason) = should_block_reason(&file_string, &args) {
        report_arg_block(reason);
        return -1;
    }

    // SAFETY: forwards the caller's original, unmodified execvpe arguments to the real libc execvpe resolved via RTLD_NEXT.
    unsafe { execvpe_fn()(file, argv, envp) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execveat(
    dirfd: c_int,
    pathname: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
    flags: c_int,
) -> c_int {
    let pathname_string = c_path_string(pathname);
    let args = collect_cstring_array(argv);
    let env_entries = collect_cstring_array(effective_env_ptr(envp));
    let git_target = is_git_execveat_target(dirfd, &pathname_string, flags);
    let effective_path =
        if git_target && (flags & AT_EMPTY_PATH_FLAG) != 0 && pathname_string.is_empty() {
            "/usr/local/bin/git".to_owned()
        } else {
            pathname_string.clone()
        };

    let (
        protected_target,
        mutable_native_target,
        mutable_shebang_protected_target,
        native_launcher_target,
    ) = if (flags & AT_EMPTY_PATH_FLAG) != 0 && pathname_string.is_empty() {
        let mut protected_target = classify_protected_runtime_fd(dirfd);
        if protected_target == ProtectedRuntime::None {
            protected_target = classify_loader_fd_target(dirfd, &args);
        }
        (
            protected_target,
            file_descriptor_is_mutable_native_exec(dirfd)
                || loader_fd_targets_mutable_native_exec(dirfd, &args),
            file_descriptor_is_mutable_shebang_to_protected_runtime(dirfd, &env_entries),
            fd_matches_workcell_native_launcher(dirfd),
        )
    } else {
        let mut protected_target = ProtectedRuntime::None;
        let mut mutable_native_target = false;
        let mut mutable_shebang_target = false;
        let mut native_launcher_target = false;

        if let Ok(c_path) = CString::new(pathname_string.as_bytes()) {
            // SAFETY: c_path is a live NUL-terminated CString; assume_init runs only on fstatat==0; candidate_fd (O_CLOEXEC) is owned, checked >=0, and closed exactly once after use.
            unsafe {
                let mut stat_buf = MaybeUninit::<libc::stat>::uninit();
                if libc::fstatat(dirfd, c_path.as_ptr(), stat_buf.as_mut_ptr(), 0) == 0
                    && let stat_buf = stat_buf.assume_init()
                    && let Some(signature) = stat_signature_from_stat(&stat_buf)
                {
                    protected_target = stat_matches_protected_runtime(&signature);
                    native_launcher_target =
                        stat_signature_matches_any_same_file(&signature, APPROVED_NATIVE_LAUNCHERS);
                }

                let candidate_fd =
                    libc::openat(dirfd, c_path.as_ptr(), libc::O_RDONLY | libc::O_CLOEXEC);
                if candidate_fd >= 0 {
                    if protected_target == ProtectedRuntime::None {
                        protected_target = classify_loader_fd_target(candidate_fd, &args);
                    }
                    if !native_launcher_target {
                        native_launcher_target = fd_matches_workcell_native_launcher(candidate_fd);
                    }
                    mutable_native_target = file_descriptor_is_mutable_native_exec(candidate_fd);
                    if !mutable_native_target {
                        mutable_native_target =
                            loader_fd_targets_mutable_native_exec(candidate_fd, &args);
                    }
                    if !mutable_native_target {
                        mutable_shebang_target =
                            file_descriptor_is_mutable_shebang_to_protected_runtime(
                                candidate_fd,
                                &env_entries,
                            );
                    }
                    libc::close(candidate_fd);
                }
            }
        }

        if protected_target == ProtectedRuntime::None {
            protected_target = classify_loader_target(&pathname_string, &args);
        }
        if !mutable_native_target {
            mutable_native_target = loader_targets_mutable_native_exec(&pathname_string, &args);
        }

        (
            protected_target,
            mutable_native_target,
            mutable_shebang_target,
            native_launcher_target,
        )
    };

    if should_block_protected_runtime_kind(protected_target) || mutable_shebang_protected_target {
        report_protected_runtime_block();
        return -1;
    }
    let launcher_loader_env_blocked = if (flags & AT_EMPTY_PATH_FLAG) != 0
        && pathname_string.is_empty()
    {
        should_block_workcell_launcher_fd_loader_env(dirfd, &env_entries)
    } else {
        (native_launcher_target && env_has_unsafe_workcell_launcher_loader_override(&env_entries))
            || should_block_workcell_launcher_loader_env(&pathname_string, &env_entries)
    };
    if launcher_loader_env_blocked {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if mutable_native_target {
        report_mutable_native_exec_block();
        return -1;
    }
    if git_target && env_has_unsafe_git_override(&env_entries) {
        report_env_block();
        return -1;
    }
    if git_target && let Some(reason) = should_block_reason(&effective_path, &args) {
        report_arg_block(reason);
        return -1;
    }

    // SAFETY: forwards the caller's original, unmodified execveat arguments to the real libc execveat resolved via RTLD_NEXT.
    unsafe { execveat_fn()(dirfd, pathname, argv, envp, flags) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn fexecve(
    fd: c_int,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let args = collect_cstring_array(argv);
    let env_entries = collect_cstring_array(effective_env_ptr(envp));

    if should_block_workcell_launcher_fd_loader_env(fd, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if should_block_protected_runtime_kind(classify_protected_runtime_fd(fd))
        || should_block_protected_runtime_kind(classify_loader_fd_target(fd, &args))
    {
        report_protected_runtime_block();
        return -1;
    }
    if file_descriptor_is_mutable_shebang_to_protected_runtime(fd, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if file_descriptor_is_mutable_native_exec(fd)
        || loader_fd_targets_mutable_native_exec(fd, &args)
    {
        report_mutable_native_exec_block();
        return -1;
    }
    if fd_matches_protected_git(fd) && env_has_unsafe_git_override(&env_entries) {
        report_env_block();
        return -1;
    }
    if fd_matches_protected_git(fd)
        && let Some(reason) = should_block_reason("/usr/local/bin/git", &args)
    {
        report_arg_block(reason);
        return -1;
    }

    // SAFETY: forwards the caller's original, unmodified fexecve arguments to the real libc fexecve resolved via RTLD_NEXT.
    unsafe { fexecve_fn()(fd, argv, envp) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn posix_spawn(
    pid: *mut pid_t,
    path: *const c_char,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let path_string = c_path_string(path);
    let args = collect_cstring_array(argv);
    let env_entries = collect_cstring_array(effective_env_ptr(envp));

    if should_block_workcell_launcher_loader_env(&path_string, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return libc::EPERM;
    }
    if should_block_protected_runtime_exec(&path_string, &args, &env_entries) {
        report_protected_runtime_block();
        return libc::EPERM;
    }
    if should_block_mutable_native_exec(&path_string, &args) {
        report_mutable_native_exec_block();
        return libc::EPERM;
    }
    if is_git_path(&path_string) && env_has_unsafe_git_override(&env_entries) {
        report_env_block();
        return libc::EPERM;
    }
    if let Some(reason) = should_block_reason(&path_string, &args) {
        report_arg_block(reason);
        return libc::EPERM;
    }

    // SAFETY: forwards the caller's original, unmodified posix_spawn arguments to the real libc posix_spawn resolved via RTLD_NEXT.
    unsafe { posix_spawn_fn()(pid, path, file_actions, attrp, argv, envp) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn posix_spawnp(
    pid: *mut pid_t,
    file: *const c_char,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let file_string = c_path_string(file);
    let args = collect_cstring_array(argv);
    let env_entries = collect_cstring_array(effective_env_ptr(envp));
    let effective_path = resolve_exec_search_target(&file_string, &env_entries);

    if should_block_workcell_launcher_loader_env(&effective_path, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return libc::EPERM;
    }
    if should_block_protected_runtime_exec(&effective_path, &args, &env_entries) {
        report_protected_runtime_block();
        return libc::EPERM;
    }
    if should_block_mutable_native_exec(&effective_path, &args) {
        report_mutable_native_exec_block();
        return libc::EPERM;
    }
    if is_git_path(&file_string) && env_has_unsafe_git_override(&env_entries) {
        report_env_block();
        return libc::EPERM;
    }
    if let Some(reason) = should_block_reason(&file_string, &args) {
        report_arg_block(reason);
        return libc::EPERM;
    }

    // SAFETY: forwards the caller's original, unmodified posix_spawnp arguments to the real libc posix_spawnp resolved via RTLD_NEXT.
    unsafe { posix_spawnp_fn()(pid, file, file_actions, attrp, argv, envp) }
}

/// Fuzzing-only re-exports of the internal exec-guard classifiers and parsers.
///
/// Compiled only under `--cfg fuzzing`, which cargo-fuzz sets for the fuzz
/// build (see `fuzz/`), so this surface never exists in the shipped `cdylib`
/// nor in the normal `cargo build`/`cargo test` builds. Keeping the underlying
/// functions private everywhere else preserves the exec-guard's hardened API;
/// this module only widens visibility for the in-repo fuzz targets.
#[cfg(fuzzing)]
pub mod fuzz_api {
    //! Thin `pub` wrappers over the private classifiers/parsers. They forward to
    //! the internal functions (visible here because this module is a crate
    //! descendant) and reduce results to primitives, so the private
    //! `ProtectedRuntime` type is never leaked into the public interface. The
    //! fuzzer only needs the code paths exercised, not the classification value.

    /// Fuzz `classify_protected_runtime_path`; the classification is discarded.
    pub fn classify_protected_runtime_path(path: &str) {
        let _ = super::classify_protected_runtime_path(path);
    }

    /// Fuzz `classify_loader_target`; the classification is discarded.
    pub fn classify_loader_target(path: &str, args: &[String]) {
        let _ = super::classify_loader_target(path, args);
    }

    /// Fuzz `path_points_to_dynamic_loader`.
    pub fn path_points_to_dynamic_loader(path: &str) -> bool {
        super::path_points_to_dynamic_loader(path)
    }

    /// Fuzz `path_from_env_entries`.
    pub fn path_from_env_entries(env_entries: &[String]) -> Option<String> {
        super::path_from_env_entries(env_entries)
    }

    /// Fuzz `resolve_command_via_path_value`.
    pub fn resolve_command_via_path_value(
        command: &str,
        path_value: Option<&str>,
    ) -> Option<String> {
        super::resolve_command_via_path_value(command, path_value)
    }

    /// Fuzz `env_has_unsafe_git_override`.
    pub fn env_has_unsafe_git_override(env_entries: &[String]) -> bool {
        super::env_has_unsafe_git_override(env_entries)
    }

    /// Fuzz `git_config_spec_is_blocked`.
    pub fn git_config_spec_is_blocked(spec: &str) -> bool {
        super::git_config_spec_is_blocked(spec)
    }

    /// Fuzz `git_config_key_is_blocked`.
    pub fn git_config_key_is_blocked(key: &str) -> bool {
        super::git_config_key_is_blocked(key)
    }

    /// Fuzz `git_config_spec_value_is_explicit_safe`.
    pub fn git_config_spec_value_is_explicit_safe(key: &str, value: &str) -> bool {
        super::git_config_spec_value_is_explicit_safe(key, value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;
    use std::path::PathBuf;
    use std::process;
    use std::time::{SystemTime, UNIX_EPOCH};

    fn create_temp_test_dir(label: &str) -> PathBuf {
        let unique = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("current time")
            .as_nanos();
        let dir = env::temp_dir().join(format!("workcell-{label}-{}-{unique}", process::id()));
        fs::create_dir(&dir).expect("create temp test dir");
        dir
    }

    #[test]
    fn matches_non_strict_recognizes_only_non_empty_non_strict_values() {
        assert!(!matches_non_strict(None));
        assert!(!matches_non_strict(Some("")));
        assert!(!matches_non_strict(Some("strict")));
        assert!(!matches_non_strict(Some("STRICT")));
        assert!(matches_non_strict(Some("development")));
        assert!(matches_non_strict(Some("build")));
        assert!(matches_non_strict(Some("breakglass")));
    }

    #[test]
    fn path_has_root_prefix_requires_boundary_match() {
        assert!(path_has_root_prefix("/workspace", "/workspace"));
        assert!(path_has_root_prefix("/workspace/project", "/workspace"));
        assert!(!path_has_root_prefix("/workspace-elsewhere", "/workspace"));
        assert!(!path_has_root_prefix("/stateful", "/state"));
    }

    #[test]
    fn trim_deleted_suffix_only_removes_kernel_deleted_suffix() {
        assert_eq!(trim_deleted_suffix("/tmp/tool (deleted)"), "/tmp/tool");
        assert_eq!(trim_deleted_suffix("/tmp/tool"), "/tmp/tool");
    }

    #[test]
    fn git_commit_short_arg_invokes_no_verify_only_for_real_no_verify_flags() {
        assert!(git_commit_short_arg_invokes_no_verify("-n"));
        assert!(git_commit_short_arg_invokes_no_verify("-nm"));
        assert!(git_commit_short_arg_invokes_no_verify("-anm"));
        assert!(!git_commit_short_arg_invokes_no_verify("-mnote"));
        assert!(!git_commit_short_arg_invokes_no_verify("-uno"));
        assert!(!git_commit_short_arg_invokes_no_verify("--no-verify"));
        assert!(!should_block(
            "git",
            &[
                "git".to_string(),
                "commit".to_string(),
                "--".to_string(),
                "--no-verify".to_string(),
            ],
        ));
        assert!(!should_block(
            "git",
            &[
                "git".to_string(),
                "commit".to_string(),
                "--".to_string(),
                "-n".to_string(),
            ],
        ));
    }

    #[test]
    fn git_path_overrides_allow_only_managed_workspace_roots() {
        assert!(!should_block(
            "git",
            &[
                "git".to_string(),
                "--git-dir=/workspace/.git".to_string(),
                "--work-tree=/workspace".to_string(),
                "status".to_string(),
            ],
        ));
        assert!(!should_block(
            "git",
            &[
                "git".to_string(),
                "--git-dir".to_string(),
                "/workspace/.git".to_string(),
                "--work-tree".to_string(),
                "/workspace".to_string(),
                "status".to_string(),
            ],
        ));
        assert!(should_block(
            "git",
            &[
                "git".to_string(),
                "-C".to_string(),
                "/workspace".to_string(),
                "--git-dir=.git".to_string(),
                "--work-tree=.".to_string(),
                "status".to_string(),
            ],
        ));
        assert!(should_block(
            "git",
            &[
                "git".to_string(),
                "-C".to_string(),
                "/workspace".to_string(),
                "--git-dir=.git".to_string(),
                "-C".to_string(),
                "/tmp/evil".to_string(),
                "status".to_string(),
            ],
        ));
        assert!(should_block(
            "git",
            &[
                "git".to_string(),
                "--git-dir=/tmp/repo/.git".to_string(),
                "--work-tree=/tmp/repo".to_string(),
                "status".to_string(),
            ],
        ));
        assert!(!should_block(
            "git",
            &["git".to_string(), "--git-dir".to_string()],
        ));
    }

    #[test]
    fn path_is_current_process_fd_path_accepts_supported_fd_forms() {
        assert_eq!(
            path_is_current_process_fd_path("/dev/stdin"),
            Some(libc::STDIN_FILENO)
        );
        assert_eq!(path_is_current_process_fd_path("/proc/self/fd/9"), Some(9));
        assert_eq!(
            // SAFETY: getpid() has no preconditions (test-only).
            path_is_current_process_fd_path(&format!("/proc/{}/fd/4", unsafe { libc::getpid() })),
            Some(4)
        );
        assert_eq!(path_is_current_process_fd_path("/proc/999999/fd/4"), None);
    }

    #[test]
    fn env_has_unsafe_git_override_blocks_git_object_directory() {
        assert!(env_has_unsafe_git_override(&[
            "GIT_OBJECT_DIRECTORY=/attacker/objects".to_string()
        ]));
        assert!(!env_has_unsafe_git_override(&[
            "SOME_OTHER_VAR=value".to_string()
        ]));
    }

    #[test]
    fn env_has_unsafe_git_override_blocks_git_alternate_object_directories() {
        assert!(env_has_unsafe_git_override(&[
            "GIT_ALTERNATE_OBJECT_DIRECTORIES=/attacker/alt".to_string()
        ]));
    }

    #[test]
    fn env_has_unsafe_git_override_blocks_git_index_file() {
        assert!(env_has_unsafe_git_override(&[
            "GIT_INDEX_FILE=/attacker/index".to_string()
        ]));
    }

    #[test]
    fn git_config_key_is_blocked_blocks_core_fsmonitor() {
        assert!(git_config_key_is_blocked("core.fsmonitor"));
        assert!(git_config_key_is_blocked("Core.FsMonitor"));
    }

    #[test]
    fn git_config_key_checks_do_not_panic_on_multibyte_boundaries() {
        // Regression (found by the env_filtering fuzz target): git config keys
        // with a multibyte character straddling a prefix/suffix byte offset used
        // to panic on a non-char-boundary slice. They must classify without
        // panicking; a multibyte char where an ASCII prefix/suffix is expected
        // simply does not match.
        assert!(!git_config_key_is_blocked("pager\u{00e9}x")); // 'é' straddles byte 6
        assert!(!git_config_key_is_blocked("cr\u{00e9}dential.x.helper"));
        assert!(!git_config_key_is_blocked("includeif.\u{00e9}.pat\u{00e9}"));
        assert!(!git_config_spec_is_blocked("pager\u{00e9}x=1"));
        assert!(!env_has_unsafe_git_override(&[
            "GIT_CONFIG_PARAMETERS='pager\u{00e9}x=1'".to_string()
        ]));
        // The ASCII cases still classify correctly after the fix.
        assert!(git_config_key_is_blocked("pager.diff"));
        assert!(git_config_key_is_blocked("credential.example.helper"));
    }

    #[test]
    fn git_config_spec_allows_only_explicit_fsmonitor_disable_values() {
        assert!(!git_config_spec_is_blocked("core.fsmonitor=false"));
        assert!(!git_config_spec_is_blocked("core.fsmonitor="));
        assert!(git_config_spec_is_blocked("core.fsmonitor=/tmp/fsmonitor"));
    }

    #[test]
    fn env_has_unsafe_git_override_blocks_core_fsmonitor_parameters() {
        assert!(env_has_unsafe_git_override(&[
            "GIT_CONFIG_PARAMETERS='core.fsmonitor=/attacker/fsmonitor'".to_string()
        ]));
    }

    #[test]
    fn env_has_unsafe_git_override_blocks_all_sensitive_config_parameters() {
        for key in [
            "core.askpass",
            "core.editor",
            "core.pager",
            "core.sshCommand",
            "credential.helper",
            "diff.external",
            "pager.log",
            "sequence.editor",
        ] {
            assert!(
                env_has_unsafe_git_override(&[format!("GIT_CONFIG_PARAMETERS='{key}=unsafe'")]),
                "expected GIT_CONFIG_PARAMETERS {key} to be blocked"
            );
        }
    }

    #[test]
    fn approved_wrappers_are_runtime_specific() {
        assert!(!protected_runtime_exec_blocked(
            ProtectedRuntime::Copilot,
            ApprovedWrapper::Provider
        ));
        assert!(protected_runtime_exec_blocked(
            ProtectedRuntime::Copilot,
            ApprovedWrapper::Development
        ));
        assert!(protected_runtime_exec_blocked(
            ProtectedRuntime::Copilot,
            ApprovedWrapper::Node
        ));
        assert!(!protected_runtime_exec_blocked(
            ProtectedRuntime::Git,
            ApprovedWrapper::Git
        ));
        assert!(protected_runtime_exec_blocked(
            ProtectedRuntime::Git,
            ApprovedWrapper::Provider
        ));
        assert!(!protected_runtime_exec_blocked(
            ProtectedRuntime::Node,
            ApprovedWrapper::Node
        ));
        assert!(!protected_runtime_exec_blocked(
            ProtectedRuntime::Node,
            ApprovedWrapper::Provider
        ));
    }

    #[test]
    fn protected_runtime_wrappers_require_native_launcher_parent() {
        assert!(approved_wrapper_requires_native_launcher_parent(
            ApprovedWrapper::Provider
        ));
        assert!(approved_wrapper_requires_native_launcher_parent(
            ApprovedWrapper::Node
        ));
        assert!(approved_wrapper_requires_native_launcher_parent(
            ApprovedWrapper::Git
        ));
        assert!(!approved_wrapper_requires_native_launcher_parent(
            ApprovedWrapper::Development
        ));
        assert!(!approved_wrapper_requires_native_launcher_parent(
            ApprovedWrapper::None
        ));
    }

    #[test]
    fn workcell_launcher_loader_env_blocks_unsafe_dynamic_loader_overrides() {
        assert!(env_has_unsafe_workcell_launcher_loader_override(&[
            "LD_PRELOAD=/workspace/preload.so".to_string()
        ]));
        assert!(env_has_unsafe_workcell_launcher_loader_override(&[
            "LD_AUDIT=/workspace/audit.so".to_string()
        ]));
        assert!(env_has_unsafe_workcell_launcher_loader_override(&[
            "LD_TRACE_LOADED_OBJECTS=1".to_string()
        ]));
        assert!(!env_has_unsafe_workcell_launcher_loader_override(&[
            format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}")
        ]));
        assert!(!env_has_unsafe_workcell_launcher_loader_override(&[
            "LD_PRELOAD=".to_string()
        ]));
        assert!(env_has_unsafe_workcell_launcher_loader_override(&[
            format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}"),
            "LD_PRELOAD=/workspace/preload.so".to_string()
        ]));
        assert!(env_has_unsafe_workcell_launcher_loader_override(&[
            "LD_AUDIT=".to_string(),
            "LD_AUDIT=/workspace/audit.so".to_string()
        ]));
    }

    #[test]
    fn path_matches_any_same_file_requires_same_inode() {
        let dir = create_temp_test_dir("same-file");
        let original = dir.join("original");
        let hardlink = dir.join("hardlink");
        let symlink_path = dir.join("symlink");
        let copy = dir.join("copy");

        fs::write(&original, b"#!/bin/sh\n").expect("write fixture");
        fs::hard_link(&original, &hardlink).expect("create hardlink");
        symlink(&original, &symlink_path).expect("create symlink");
        fs::copy(&original, &copy).expect("copy fixture");

        assert!(path_matches_any_same_file(
            &original,
            &[hardlink.to_str().expect("hardlink path")]
        ));
        assert!(path_matches_any_same_file(
            &original,
            &[symlink_path.to_str().expect("symlink path")]
        ));
        assert!(!path_matches_any_same_file(
            &original,
            &[copy.to_str().expect("copy path")]
        ));

        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }
}
