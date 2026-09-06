// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

#![allow(clippy::missing_safety_doc)]
#![deny(unsafe_op_in_unsafe_fn)]

#[cfg(target_os = "linux")]
use libc::c_uint;
use libc::{c_char, c_int, c_long, c_void, pid_t};
use std::env;
use std::ffi::{CStr, CString};
use std::fs::{self, File};
#[cfg(target_os = "linux")]
use std::io::Write;
use std::io::{Read, Seek, SeekFrom};
use std::mem::{self, MaybeUninit};
#[cfg(target_os = "linux")]
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::MetadataExt;
#[cfg(all(target_os = "linux", test))]
use std::os::unix::fs::PermissionsExt;
#[cfg(target_os = "linux")]
use std::os::unix::io::{AsRawFd, FromRawFd, IntoRawFd};
use std::path::Path;
#[cfg(target_os = "linux")]
use std::path::PathBuf;
use std::sync::OnceLock;

mod gitpolicy;
use gitpolicy::{env_has_unsafe_git_override, should_block_reason};

// SAFETY: matches libc's process-global char **environ; reads assume no concurrent setenv/putenv mutation.
unsafe extern "C" {
    static mut environ: *mut *mut c_char;

    #[cfg(target_os = "linux")]
    fn faccessat(dirfd: c_int, pathname: *const c_char, mode: c_int, flags: c_int) -> c_int;
}

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
#[cfg(target_os = "linux")]
const APPROVED_CONTROL_SCRIPTS: &[&str] = &[
    "/usr/local/libexec/workcell/entrypoint.sh",
    "/usr/local/libexec/workcell/development-wrapper.sh",
    "/usr/local/libexec/workcell/git-wrapper.sh",
    "/usr/local/libexec/workcell/node-wrapper.sh",
    "/usr/local/libexec/workcell/provider-wrapper.sh",
];
const APPROVED_WRAPPER_LAUNCHERS: &[&str] = &["/bin/bash"];
const APPROVED_NATIVE_LAUNCHERS: &[&str] = &[
    "/usr/local/libexec/workcell/core/launcher",
    "/usr/local/libexec/workcell/core/git",
];

const MUTABLE_EXEC_ROOTS: &[&str] = &[
    "/workspace",
    "/state",
    "/tmp",
    "/var/tmp",
    "/run",
    "/dev/shm",
    "/dev/mqueue",
];
const ALLOWED_LD_PRELOAD: &str = "/usr/local/lib/libworkcell_exec_guard.so";
const MAX_EXEC_STRING_BYTES: usize = 128 * 1024;
const MAX_EXEC_AGGREGATE_BYTES: usize = 2 * 1024 * 1024;
const MAX_EXEC_ELEMENTS: usize = 65_536;
const MAX_EXEC_PATH_SEGMENT_BYTES: usize = MAX_EXEC_STRING_BYTES;
const MAX_EXEC_PATH_BYTES: usize = MAX_EXEC_AGGREGATE_BYTES;
const MAX_EXEC_PATH_SEGMENTS: usize = MAX_EXEC_ELEMENTS;
#[cfg(target_os = "linux")]
const MAX_MUTABLE_EXEC_SNAPSHOT_BYTES: u64 = 64 * 1024 * 1024;
#[cfg(target_os = "linux")]
const MUTABLE_EXEC_COPY_CHUNK_BYTES: usize = 64 * 1024;
#[cfg(target_os = "linux")]
const MFD_EXEC_FLAG: c_uint = 0x0010;
#[cfg(target_os = "linux")]
const AT_EACCESS_FLAG: c_int = 0x0200;
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
const MUTABLE_NATIVE_EXEC_BLOCK_MESSAGE: &str = "Workcell blocked direct native executable launch from mutable runtime paths on the strict profile.\n";
const WORKCELL_LAUNCHER_LOADER_ENV_BLOCK_MESSAGE: &str =
    "Workcell blocked unsafe dynamic-loader environment for Workcell launcher execution.\n";
const NATIVE_LOADER_ENV_BLOCK_MESSAGE: &str = "Workcell blocked unsafe dynamic-loader environment for native execution on the strict profile.\n";
const MISSING_GUARD_ENV_BLOCK_MESSAGE: &str = "Workcell blocked child execution without the approved exec guard preload on the strict profile.\n";

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
#[cfg(not(target_os = "linux"))]
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
#[cfg(not(target_os = "linux"))]
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

#[cfg(target_os = "linux")]
fn path_is_magic_exec_target(path: &str) -> bool {
    path == "/proc"
        || path.starts_with("/proc/")
        || path == "/dev/fd"
        || path.starts_with("/dev/fd/")
        || matches!(path, "/dev/stdin" | "/dev/stdout" | "/dev/stderr")
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

#[cfg(target_os = "linux")]
fn effective_access_can_write(path: &Path) -> bool {
    let Ok(metadata) = fs::metadata(path) else {
        return true;
    };
    // Root-owned files and directories without group/world write bits are the
    // immutable runtime baseline for a root runtime. Non-root runtimes still
    // use faccessat below so ACL grants are included in the decision.
    // SAFETY: geteuid is a niladic syscall wrapper with no invalid inputs.
    let euid = unsafe { libc::geteuid() };
    if euid == 0 && metadata.uid() == 0 && metadata.mode() & (libc::S_IWGRP | libc::S_IWOTH) == 0 {
        // Root-owned files and directories without group/world write bits are
        // the immutable runtime baseline, even when the process has root
        // capabilities that can override ordinary permission checks.
        return false;
    }
    let Ok(c_path) = CString::new(path.as_os_str().as_bytes()) else {
        return true;
    };
    // SAFETY: c_path is a valid NUL-terminated path. The call only checks the
    // effective credentials and does not modify the filesystem.
    unsafe { faccessat(libc::AT_FDCWD, c_path.as_ptr(), libc::W_OK, AT_EACCESS_FLAG) == 0 }
}

#[cfg(target_os = "linux")]
fn path_is_trusted_immutable(path: &Path) -> bool {
    let Ok(canonical) = fs::canonicalize(path) else {
        return false;
    };
    if resolved_path_is_mutable_root(&canonical.to_string_lossy()) {
        return false;
    }
    let Ok(metadata) = fs::metadata(&canonical) else {
        return false;
    };
    if (metadata.mode() & file_type_bits()) != regular_file_mode()
        || metadata.uid() != 0
        || metadata.mode() & (libc::S_IWGRP | libc::S_IWOTH) != 0
        || effective_access_can_write(&canonical)
    {
        return false;
    }

    path_ancestors_are_trusted_immutable(canonical.parent().unwrap_or(Path::new("/")))
}

#[cfg(target_os = "linux")]
fn fd_signature(fd: c_int) -> Option<StatSignature> {
    let mut stat_buf = MaybeUninit::<libc::stat>::uninit();
    // SAFETY: fd is borrowed for this call and stat_buf is writable storage.
    if unsafe { libc::fstat(fd, stat_buf.as_mut_ptr()) } != 0 {
        return None;
    }
    // SAFETY: fstat returned success, so stat_buf is initialized.
    let stat_buf = unsafe { stat_buf.assume_init() };
    stat_signature_from_stat(&stat_buf)
}

#[cfg(target_os = "linux")]
fn fd_target_is_trusted_immutable(fd: c_int) -> bool {
    let Ok(target) = fs::read_link(proc_fd_path(fd)) else {
        return false;
    };
    let target = target.to_string_lossy();
    if target.ends_with(" (deleted)")
        || target.starts_with("memfd:")
        || target.starts_with("/memfd:")
        || target.starts_with("anon_inode:")
        || target.starts_with("/anon_inode:")
    {
        return false;
    }
    let target = trim_deleted_suffix(&target);
    if !target.starts_with('/') {
        return false;
    }
    let target_path = Path::new(target);
    let Ok(canonical) = fs::canonicalize(target_path) else {
        return false;
    };
    if resolved_path_is_mutable_root(&canonical.to_string_lossy())
        || !path_is_trusted_immutable(&canonical)
    {
        return false;
    }
    let Some(fd_signature) = fd_signature(fd) else {
        return false;
    };
    fs::metadata(canonical)
        .ok()
        .map(|metadata| {
            let path_signature = metadata_signature_from_metadata(&metadata);
            fd_signature.dev == path_signature.dev && fd_signature.ino == path_signature.ino
        })
        .unwrap_or(false)
}

#[cfg(target_os = "linux")]
fn fd_target_is_untrusted_exec(fd: c_int) -> bool {
    !fd_target_is_trusted_immutable(fd)
}

#[cfg(not(target_os = "linux"))]
fn fd_target_is_mutable_root(fd: c_int) -> bool {
    let Ok(target) = fs::read_link(proc_fd_path(fd)) else {
        return false;
    };

    if let Ok(canonical) = fs::canonicalize(&target) {
        return resolved_path_is_mutable_root(&canonical.to_string_lossy());
    }

    resolved_path_is_mutable_root(trim_deleted_suffix(&target.to_string_lossy()))
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct ExecInputTooLarge;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ExecSearchError {
    InputTooLarge,
    PermissionDenied,
}

fn bounded_c_string(path: *const c_char) -> Result<String, ExecInputTooLarge> {
    if path.is_null() {
        return Ok(String::new());
    }

    // SAFETY: path is supplied by the exec ABI. strnlen limits the read before
    // the bytes are copied into an owned string.
    let length = unsafe { libc::strnlen(path, MAX_EXEC_STRING_BYTES + 1) };
    if length > MAX_EXEC_STRING_BYTES {
        return Err(ExecInputTooLarge);
    }
    // SAFETY: strnlen found the NUL terminator within the bounded region.
    let bytes = unsafe { std::slice::from_raw_parts(path.cast::<u8>(), length) };
    Ok(String::from_utf8_lossy(bytes).into_owned())
}

fn collect_cstring_array(ptr: *const *const c_char) -> Result<Vec<String>, ExecInputTooLarge> {
    let mut values = Vec::new();
    if ptr.is_null() {
        return Ok(values);
    }

    let mut total_bytes = 0usize;
    let mut index = 0usize;
    loop {
        if index > MAX_EXEC_ELEMENTS {
            return Err(ExecInputTooLarge);
        }
        // SAFETY: ptr is a non-null, NUL-sentinel-terminated char** (exec ABI / libc environ); index walks up to the sentinel checked below.
        let current = unsafe { *ptr.add(index) };
        if current.is_null() {
            break;
        }
        if index == MAX_EXEC_ELEMENTS {
            return Err(ExecInputTooLarge);
        }
        let entry = bounded_c_string(current)?;
        total_bytes = total_bytes
            .checked_add(entry.len().saturating_add(1))
            .ok_or(ExecInputTooLarge)?;
        if total_bytes > MAX_EXEC_AGGREGATE_BYTES {
            return Err(ExecInputTooLarge);
        }
        values.push(entry);
        index += 1;
    }
    Ok(values)
}

fn effective_env_ptr(envp: *const *const c_char) -> *const *const c_char {
    if envp.is_null() {
        // SAFETY: environ is libc-initialized; read in the calling thread with no concurrent setenv/putenv.
        unsafe { environ.cast() }
    } else {
        envp
    }
}

fn current_process_env_entries() -> Result<Vec<String>, ExecInputTooLarge> {
    // SAFETY: environ is libc-initialized; read in the calling thread with no concurrent setenv/putenv.
    collect_cstring_array(unsafe { environ.cast() })
}

fn should_block_null_explicit_env(envp: *const *const c_char) -> bool {
    envp.is_null() && current_mode_blocks_mutable_native_exec()
}

fn path_from_env_entries(env_entries: &[String]) -> Option<String> {
    env_entries
        .iter()
        .find_map(|entry| entry.strip_prefix("PATH=").map(ToOwned::to_owned))
        .or_else(|| env::var("PATH").ok())
        // POSIX libc uses a system default search path when PATH is absent.
        .or_else(|| Some("/bin:/usr/bin".to_owned()))
}

fn validate_path_value(path_value: &str) -> Result<(), ExecInputTooLarge> {
    if path_value.len() > MAX_EXEC_PATH_BYTES {
        return Err(ExecInputTooLarge);
    }
    let mut segment_count = 0usize;
    for segment in path_value.split(':') {
        segment_count = segment_count.checked_add(1).ok_or(ExecInputTooLarge)?;
        if segment_count > MAX_EXEC_PATH_SEGMENTS || segment.len() > MAX_EXEC_PATH_SEGMENT_BYTES {
            return Err(ExecInputTooLarge);
        }
    }
    Ok(())
}

fn resolve_command_via_path_value(
    command: &str,
    path_value: Option<&str>,
) -> Result<Option<String>, ExecSearchError> {
    if command.is_empty() || command.contains('/') {
        return Ok(None);
    }

    let Some(path_value) = path_value else {
        return Ok(None);
    };
    validate_path_value(path_value).map_err(|_| ExecSearchError::InputTooLarge)?;
    let mut permission_denied = false;
    for segment in path_value.split(':') {
        let candidate = if segment.is_empty() {
            format!("./{command}")
        } else {
            format!("{segment}/{command}")
        };
        let Ok(cstring) = CString::new(candidate.as_str()) else {
            continue;
        };

        // SAFETY: cstring is a live NUL-terminated CString valid for the call; access only reads the path.
        let executable = unsafe { libc::access(cstring.as_ptr(), libc::X_OK) == 0 };
        if !executable {
            // SAFETY: cstring is a live NUL-terminated CString valid for the call; access only reads the path.
            if unsafe { libc::access(cstring.as_ptr(), libc::F_OK) == 0 } {
                permission_denied = true;
            }
            continue;
        }

        return Ok(Some(candidate));
    }

    if permission_denied {
        Err(ExecSearchError::PermissionDenied)
    } else {
        Ok(None)
    }
}

#[cfg(target_os = "linux")]
fn file_descriptor_is_mutable_native_exec(fd: c_int) -> bool {
    if !current_mode_blocks_mutable_native_exec() || fd < 0 || !fd_target_is_untrusted_exec(fd) {
        return false;
    }
    let Some(mut file) = duplicate_fd_file(fd) else {
        return true;
    };
    let Ok(metadata) = file.metadata() else {
        return true;
    };
    if (metadata.mode() & file_type_bits()) != regular_file_mode() {
        return false;
    }
    let mut header = [0u8; 4];
    if file.read_exact(&mut header).is_err() {
        return true;
    }
    header == [0x7f, b'E', b'L', b'F']
}

#[cfg(not(target_os = "linux"))]
fn file_descriptor_is_mutable_native_exec(fd: c_int) -> bool {
    if !current_mode_blocks_mutable_native_exec() || fd < 0 || !fd_target_is_mutable_root(fd) {
        return false;
    }
    let Some(mut file) = duplicate_fd_file(fd) else {
        return true;
    };
    let Ok(metadata) = file.metadata() else {
        return true;
    };
    if (metadata.mode() & file_type_bits()) != regular_file_mode() {
        return false;
    }
    let mut header = [0u8; 4];
    if file.read_exact(&mut header).is_err() {
        return true;
    }
    header == [0x7f, b'E', b'L', b'F']
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

fn token_is_loader_environment_assignment(token: &str) -> bool {
    let Some((name, _value)) = token.split_once('=') else {
        return false;
    };
    matches!(
        name,
        "LD_PRELOAD" | "LD_AUDIT" | "LD_LIBRARY_PATH" | "LD_TRACE_LOADED_OBJECTS"
    )
}

fn token_is_loader_environment_name(token: &str) -> bool {
    matches!(
        token,
        "LD_PRELOAD" | "LD_AUDIT" | "LD_LIBRARY_PATH" | "LD_TRACE_LOADED_OBJECTS"
    )
}

fn token_is_loader_environment_unset_option(token: &str) -> bool {
    let name = token
        .strip_prefix("--unset=")
        .or_else(|| token.strip_prefix("-u").filter(|suffix| !suffix.is_empty()))
        .map(|name| name.strip_prefix('=').unwrap_or(name));
    name.is_some_and(token_is_loader_environment_name)
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
        if token == "-S"
            || token.starts_with("-S")
            || token == "--split-string"
            || token.starts_with("--split-string=")
        {
            return true;
        }
        if token == "-i" || token == "--ignore-environment" || token == "-" {
            return true;
        }
        if token == "-u" || token == "--unset" {
            if next_shebang_token(&mut scan)
                .is_some_and(|name| token_is_loader_environment_name(&name))
            {
                return true;
            }
            continue;
        }
        if token_is_loader_environment_unset_option(&token) {
            return true;
        }

        if token_is_loader_environment_assignment(&token) {
            return true;
        }

        if let Some(path) = token.strip_prefix("PATH=") {
            path_override = Some(path.to_owned());
        }

        if token.starts_with('-') || token.contains('=') {
            continue;
        }

        let token_path = match resolve_command_via_path_value(
            &token,
            path_override
                .as_deref()
                .or(path_from_env_entries(env_entries).as_deref()),
        ) {
            Ok(Some(path)) => path,
            Ok(None) => token.clone(),
            Err(_) => return true,
        };

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

fn shebang_path_is_untrusted_native_exec(path: &str) -> bool {
    if !current_mode_blocks_mutable_native_exec() || path.is_empty() {
        return false;
    }
    #[cfg(target_os = "linux")]
    if !Path::new(path).is_absolute() {
        return true;
    }
    #[cfg(target_os = "linux")]
    if path_is_magic_exec_target(path) {
        return true;
    }
    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        return file_descriptor_is_mutable_native_exec(proc_fd);
    }

    #[cfg(target_os = "linux")]
    if current_mode_blocks_mutable_native_exec()
        && path_has_untrusted_provenance(path, libc::AT_FDCWD)
    {
        // The kernel resolves a shebang interpreter after this process returns.
        // Do not approve an interpreter whose pathname can change before lookup.
        return true;
    }

    path_is_mutable_native_exec(path)
}

fn env_command_targets_untrusted_native_exec(cursor: &str, env_entries: &[String]) -> bool {
    let mut scan = cursor;
    let mut path_override: Option<String> = None;

    while let Some(token) = next_shebang_token(&mut scan) {
        if token == "-S"
            || token.starts_with("-S")
            || token == "--split-string"
            || token.starts_with("--split-string=")
        {
            return true;
        }
        if token == "-i" || token == "--ignore-environment" || token == "-" {
            return true;
        }
        if token == "-u" || token == "--unset" {
            if next_shebang_token(&mut scan)
                .is_some_and(|name| token_is_loader_environment_name(&name))
            {
                return true;
            }
            continue;
        }
        if token_is_loader_environment_unset_option(&token) {
            return true;
        }

        if token_is_loader_environment_assignment(&token) {
            return true;
        }

        if let Some(path) = token.strip_prefix("PATH=") {
            path_override = Some(path.to_owned());
        }

        if token.starts_with('-') || token.contains('=') {
            continue;
        }

        let token_path = match resolve_command_via_path_value(
            &token,
            path_override
                .as_deref()
                .or(path_from_env_entries(env_entries).as_deref()),
        ) {
            Ok(Some(path)) => path,
            Ok(None) => token,
            Err(_) => return true,
        };

        if is_dynamic_loader_path(&token_path) {
            return next_shebang_token(&mut scan)
                .is_some_and(|target| loader_arg_targets_mutable_native_exec(&target));
        }

        return shebang_path_is_untrusted_native_exec(&token_path);
    }

    false
}

fn buffer_targets_untrusted_native_via_shebang(buffer: &str, env_entries: &[String]) -> bool {
    if !buffer.starts_with("#!") {
        return false;
    }

    let mut cursor = &buffer[2..];
    let Some(interpreter) = next_shebang_token(&mut cursor) else {
        return false;
    };

    if token_uses_env_interpreter(&interpreter) {
        return env_command_targets_untrusted_native_exec(cursor, env_entries);
    }

    if is_dynamic_loader_path(&interpreter) {
        return next_shebang_token(&mut cursor)
            .is_some_and(|target| loader_arg_targets_mutable_native_exec(&target));
    }

    shebang_path_is_untrusted_native_exec(&interpreter)
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
    if fd < 0 {
        return false;
    }
    #[cfg(target_os = "linux")]
    let untrusted = fd_target_is_untrusted_exec(fd);
    #[cfg(not(target_os = "linux"))]
    let untrusted = fd_target_is_mutable_root(fd);
    untrusted && file_descriptor_targets_protected_runtime_via_shebang(fd, env_entries)
}

fn file_descriptor_is_untrusted_shebang_native_exec(fd: c_int, env_entries: &[String]) -> bool {
    if fd < 0 {
        return false;
    }
    let Some(mut file) = duplicate_fd_file(fd) else {
        return true;
    };
    let mut buffer = [0u8; 511];
    let Ok(bytes) = file.read(&mut buffer) else {
        return true;
    };
    buffer_targets_untrusted_native_via_shebang(
        &String::from_utf8_lossy(&buffer[..bytes]),
        env_entries,
    )
}

fn canonicalize_existing_path(path: &str) -> Option<String> {
    fs::canonicalize(path)
        .ok()
        .map(|path| path.to_string_lossy().into_owned())
}

#[cfg(target_os = "linux")]
fn path_is_mutable_native_exec(path: &str) -> bool {
    if !current_mode_blocks_mutable_native_exec() || path.is_empty() {
        return false;
    }

    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        return file_descriptor_is_mutable_native_exec(proc_fd);
    }

    let untrusted_provenance = path_has_untrusted_provenance(path, libc::AT_FDCWD);
    let Some(resolved) = canonicalize_existing_path(path) else {
        let Ok(metadata) = fs::metadata(path) else {
            return false;
        };
        return (metadata.mode() & file_type_bits()) == regular_file_mode() && untrusted_provenance;
    };
    let trusted = path_is_trusted_immutable(Path::new(&resolved));
    let Ok(mut file) = File::open(&resolved) else {
        return !trusted || untrusted_provenance;
    };
    match file.metadata() {
        Ok(metadata) if (metadata.mode() & file_type_bits()) == regular_file_mode() => {}
        _ => return false,
    }
    let mut header = [0u8; 4];
    if file.read_exact(&mut header).is_err() {
        return !trusted || untrusted_provenance;
    }
    header == [0x7f, b'E', b'L', b'F'] && (!trusted || untrusted_provenance)
}

#[cfg(not(target_os = "linux"))]
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
        return true;
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

fn path_is_untrusted_shebang_native_exec(path: &str, env_entries: &[String]) -> bool {
    if !current_mode_blocks_mutable_native_exec() || path.is_empty() {
        return false;
    }
    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        return file_descriptor_is_untrusted_shebang_native_exec(proc_fd, env_entries);
    }

    let Ok(mut file) = File::open(path) else {
        #[cfg(not(target_os = "linux"))]
        return false;
        #[cfg(target_os = "linux")]
        return path_has_untrusted_provenance(path, libc::AT_FDCWD);
    };
    let mut buffer = [0u8; 511];
    let Ok(bytes) = file.read(&mut buffer) else {
        return true;
    };
    buffer_targets_untrusted_native_via_shebang(
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

fn env_has_unsafe_loader_override(env_entries: &[String]) -> bool {
    env_has_unsafe_workcell_launcher_loader_override(env_entries)
}

#[cfg(target_os = "linux")]
fn env_has_approved_guard_preload(env_entries: &[String]) -> bool {
    let mut preload_entries = 0;
    for entry in env_entries {
        let Some(value) = env_entry_value(entry, "LD_PRELOAD") else {
            continue;
        };
        preload_entries += 1;
        if preload_entries > 1 || value != ALLOWED_LD_PRELOAD {
            return false;
        }
    }
    preload_entries == 1
}

#[cfg(target_os = "linux")]
fn current_process_is_approved_native_launcher() -> bool {
    fs::read_link("/proc/self/exe")
        .ok()
        .and_then(|path| path.to_str().map(str::to_owned))
        .is_some_and(|path| path_matches_any_same_file(Path::new(&path), APPROVED_NATIVE_LAUNCHERS))
}

#[cfg(target_os = "linux")]
fn path_is_approved_control_script(path: &str) -> bool {
    APPROVED_CONTROL_SCRIPTS
        .iter()
        .any(|candidate| path_matches_any_same_file(Path::new(path), &[*candidate]))
}

#[cfg(target_os = "linux")]
fn is_approved_launcher_control_transition(path: &str, args: &[String]) -> bool {
    current_process_is_approved_native_launcher()
        && path_matches_any_same_file(Path::new(path), APPROVED_WRAPPER_LAUNCHERS)
        && args
            .get(1)
            .is_some_and(|script| path_is_approved_control_script(script))
}

#[cfg(target_os = "linux")]
fn should_block_missing_guard_env(path: &str, args: &[String], env_entries: &[String]) -> bool {
    current_mode_blocks_mutable_native_exec()
        && !env_has_approved_guard_preload(env_entries)
        && !is_approved_launcher_control_transition(path, args)
}

#[cfg(not(target_os = "linux"))]
fn should_block_missing_guard_env(_path: &str, _args: &[String], _env_entries: &[String]) -> bool {
    false
}

#[cfg(target_os = "linux")]
fn file_descriptor_is_loader_sensitive(fd: c_int) -> bool {
    let Some(mut file) = duplicate_fd_file(fd) else {
        return true;
    };
    let Ok(metadata) = file.metadata() else {
        return true;
    };
    if (metadata.mode() & file_type_bits()) != regular_file_mode() {
        return false;
    }
    let mut prefix = [0u8; 2];
    if file.read_exact(&mut prefix).is_err() {
        return fd_target_is_untrusted_exec(fd);
    }
    prefix == [0x7f, b'E'] || prefix == *b"#!"
}

#[cfg(target_os = "linux")]
fn path_is_loader_sensitive(path: &str) -> bool {
    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        return file_descriptor_is_loader_sensitive(proc_fd);
    }
    let Ok(mut file) = File::open(path) else {
        return true;
    };
    let Ok(metadata) = file.metadata() else {
        return true;
    };
    if (metadata.mode() & file_type_bits()) != regular_file_mode() {
        return false;
    }
    let mut prefix = [0u8; 2];
    if file.read_exact(&mut prefix).is_err() {
        return path_has_untrusted_provenance(path, libc::AT_FDCWD);
    }
    prefix == [0x7f, b'E'] || prefix == *b"#!"
}

#[cfg(not(target_os = "linux"))]
fn path_is_loader_sensitive(_path: &str) -> bool {
    false
}

fn should_block_loader_env_for_path(path: &str, env_entries: &[String]) -> bool {
    current_mode_blocks_mutable_native_exec()
        && env_has_unsafe_loader_override(env_entries)
        && path_is_loader_sensitive(path)
}

#[cfg(target_os = "linux")]
fn should_block_loader_env_for_fd(fd: c_int, env_entries: &[String]) -> bool {
    current_mode_blocks_mutable_native_exec()
        && env_has_unsafe_loader_override(env_entries)
        && file_descriptor_is_loader_sensitive(fd)
}

#[cfg(not(target_os = "linux"))]
fn should_block_loader_env_for_fd(_fd: c_int, _env_entries: &[String]) -> bool {
    false
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

fn compare_open_files(left: &mut File, right: &mut File) -> bool {
    const COMPARE_CHUNK_BYTES: usize = 64 * 1024;
    if left.seek(SeekFrom::Start(0)).is_err() || right.seek(SeekFrom::Start(0)).is_err() {
        return false;
    }

    let mut left_buffer = [0u8; COMPARE_CHUNK_BYTES];
    let mut right_buffer = [0u8; COMPARE_CHUNK_BYTES];
    loop {
        let left_bytes = match left.read(&mut left_buffer) {
            Ok(bytes) => bytes,
            Err(_) => return false,
        };
        let right_bytes = match right.read(&mut right_buffer) {
            Ok(bytes) => bytes,
            Err(_) => return false,
        };
        if left_bytes != right_bytes || left_buffer[..left_bytes] != right_buffer[..right_bytes] {
            return false;
        }
        if left_bytes == 0 {
            return true;
        }
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

fn loader_path_list_targets_mutable_native_exec(value: &str) -> bool {
    if value.is_empty() {
        return true;
    }
    value
        .split(':')
        .any(|target| target.is_empty() || loader_arg_targets_mutable_native_exec(target))
}

fn loader_args_target_mutable_native_exec(args: &[String]) -> bool {
    let mut index = 1usize;
    while index < args.len() {
        let argument = &args[index];
        let (option, inline_value) = argument
            .split_once('=')
            .map_or((argument.as_str(), None), |(option, value)| {
                (option, Some(value))
            });

        match option {
            "--preload" | "--audit" | "--library-path" => {
                let value = inline_value.or_else(|| {
                    index += 1;
                    args.get(index).map(String::as_str)
                });
                if value.is_none_or(loader_path_list_targets_mutable_native_exec) {
                    return true;
                }
            }
            "--argv0" => {
                index += 1;
            }
            "--" => {
                return args
                    .get(index + 1)
                    .is_some_and(|target| loader_arg_targets_mutable_native_exec(target));
            }
            option if option.starts_with('-') => {}
            target => {
                return loader_arg_targets_mutable_native_exec(target);
            }
        }
        index += 1;
    }
    false
}

fn loader_arg_targets_mutable_native_exec(target: &str) -> bool {
    #[cfg(target_os = "linux")]
    if current_mode_blocks_mutable_native_exec() && !Path::new(target).is_absolute() {
        return true;
    }
    #[cfg(target_os = "linux")]
    if current_mode_blocks_mutable_native_exec() && path_is_magic_exec_target(target) {
        return true;
    }
    if let Some(proc_fd) = path_is_current_process_fd_path(target) {
        return file_descriptor_is_mutable_native_exec(proc_fd);
    }
    #[cfg(target_os = "linux")]
    if current_mode_blocks_mutable_native_exec()
        && (path_has_mutable_provenance(target, libc::AT_FDCWD)
            || path_resolves_to_mutable_root(target, libc::AT_FDCWD)
            || path_has_untrusted_provenance(target, libc::AT_FDCWD))
    {
        // Dynamic loaders receive pathname arguments and perform a later lookup.
        // Reject every mutable-runtime argument to prevent loader option and
        // target lookup races, including library paths and scripts.
        return true;
    }
    path_is_mutable_native_exec(target)
}

fn loader_targets_mutable_native_exec(path: &str, args: &[String]) -> bool {
    if !path_points_to_dynamic_loader(path) {
        return false;
    }
    #[cfg(target_os = "linux")]
    if current_mode_blocks_mutable_native_exec() && path_is_magic_exec_target(path) {
        // A loader supplied through /proc or /dev/fd can change before the
        // loader performs its own target lookup.  Loader paths cannot use the
        // descriptor replacement path, so reject them in strict mode.
        return true;
    }
    loader_args_target_mutable_native_exec(args)
}

fn loader_fd_targets_mutable_native_exec(fd: c_int, args: &[String]) -> bool {
    fd_is_dynamic_loader(fd) && loader_args_target_mutable_native_exec(args)
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

fn should_block_mutable_native_exec(path: &str, args: &[String], env_entries: &[String]) -> bool {
    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        #[cfg(target_os = "linux")]
        if current_mode_blocks_mutable_native_exec()
            && path_is_magic_exec_target(path)
            && fd_is_dynamic_loader(proc_fd)
        {
            // A dynamic loader reached through a magic descriptor path cannot
            // be replaced with a pinned target without changing loader ABI.
            return true;
        }
        return file_descriptor_is_mutable_native_exec(proc_fd)
            || loader_fd_targets_mutable_native_exec(proc_fd, args)
            || file_descriptor_is_untrusted_shebang_native_exec(proc_fd, env_entries);
    }

    path_is_mutable_native_exec(path)
        || loader_targets_mutable_native_exec(path, args)
        || path_is_untrusted_shebang_native_exec(path, env_entries)
}

fn resolve_exec_search_target(
    file: &str,
    env_entries: &[String],
) -> Result<Option<String>, ExecSearchError> {
    if file.contains('/') {
        return Ok(Some(file.to_owned()));
    }
    resolve_command_via_path_value(file, path_from_env_entries(env_entries).as_deref())
}

#[cfg(target_os = "linux")]
#[derive(Debug)]
enum MutableExecPreparation {
    NotMutable,
    Block,
    Pinned(c_int),
    Execute(c_int),
}

#[cfg(target_os = "linux")]
fn lexical_normalized_path(path: &Path) -> PathBuf {
    let mut result = PathBuf::new();
    for component in path.components() {
        match component {
            std::path::Component::CurDir => {}
            std::path::Component::ParentDir => {
                result.pop();
            }
            component => result.push(component.as_os_str()),
        }
    }
    result
}

#[cfg(target_os = "linux")]
fn path_with_dirfd_base(path: &str, dirfd: c_int) -> PathBuf {
    let input = Path::new(path);
    let base = if input.is_absolute() {
        PathBuf::new()
    } else if dirfd != libc::AT_FDCWD {
        fs::read_link(proc_fd_path(dirfd)).unwrap_or_default()
    } else {
        env::current_dir().unwrap_or_default()
    };
    lexical_normalized_path(&base.join(input))
}

#[cfg(target_os = "linux")]
fn path_ancestors_are_trusted_immutable(path: &Path) -> bool {
    let Ok(canonical) = fs::canonicalize(path) else {
        return false;
    };
    let mut ancestor = canonical.as_path();
    loop {
        let Ok(metadata) = fs::metadata(ancestor) else {
            return false;
        };
        if metadata.uid() != 0
            || metadata.mode() & (libc::S_IWGRP | libc::S_IWOTH) != 0
            || effective_access_can_write(ancestor)
        {
            return false;
        }
        if ancestor == Path::new("/") {
            return true;
        }
        let Some(parent) = ancestor.parent() else {
            return false;
        };
        ancestor = parent;
    }
}

#[cfg(target_os = "linux")]
fn path_has_untrusted_provenance(path: &str, dirfd: c_int) -> bool {
    let candidate = path_with_dirfd_base(path, dirfd);
    let candidate_string = candidate.to_string_lossy();
    if resolved_path_is_mutable_root(&candidate_string) {
        return true;
    }
    let Some(parent) = candidate.parent() else {
        return true;
    };
    !path_ancestors_are_trusted_immutable(parent)
}

#[cfg(target_os = "linux")]
fn path_has_mutable_provenance(path: &str, dirfd: c_int) -> bool {
    let candidate = path_with_dirfd_base(path, dirfd);
    let candidate = candidate.to_string_lossy();
    MUTABLE_EXEC_ROOTS
        .iter()
        .any(|root| path_has_root_prefix(&candidate, root))
}

#[cfg(target_os = "linux")]
fn path_resolves_to_mutable_root(path: &str, dirfd: c_int) -> bool {
    let candidate = path_with_dirfd_base(path, dirfd);
    let Ok(resolved) = fs::canonicalize(candidate) else {
        return false;
    };
    let resolved = resolved.to_string_lossy();
    MUTABLE_EXEC_ROOTS
        .iter()
        .any(|root| path_has_root_prefix(&resolved, root))
}

#[cfg(target_os = "linux")]
fn mutable_exec_preparation(
    path: &str,
    dirfd: c_int,
    open_flags: c_int,
    env_entries: &[String],
) -> MutableExecPreparation {
    if !current_mode_blocks_mutable_native_exec() || path.is_empty() {
        return MutableExecPreparation::NotMutable;
    }

    let lexical_mutable = path_has_mutable_provenance(path, dirfd);
    let pin_target = !Path::new(path).is_absolute() || path_is_magic_exec_target(path);

    let Ok(c_path) = CString::new(path.as_bytes()) else {
        return if lexical_mutable || pin_target {
            MutableExecPreparation::Block
        } else {
            MutableExecPreparation::NotMutable
        };
    };
    // SAFETY: c_path is a valid NUL-terminated path and dirfd is supplied by the execveat caller.
    // O_PATH obtains stable identity even when the target is execute-only; the duplicate below
    // performs the separate readable open required for a sealed snapshot.
    let fd = unsafe {
        libc::openat(
            dirfd,
            c_path.as_ptr(),
            libc::O_PATH | libc::O_CLOEXEC | open_flags,
        )
    };
    if fd < 0 {
        let mut stat_buf = MaybeUninit::<libc::stat>::uninit();
        // SAFETY: c_path is NUL-terminated and stat_buf is writable storage for fstatat.
        let stat_ok =
            unsafe { libc::fstatat(dirfd, c_path.as_ptr(), stat_buf.as_mut_ptr(), 0) == 0 };
        let resolved_mutable = path_resolves_to_mutable_root(path, dirfd);
        if (lexical_mutable || resolved_mutable || path_has_untrusted_provenance(path, dirfd))
            && stat_ok
        {
            // SAFETY: fstatat returned success, so stat_buf is initialized.
            let stat_buf = unsafe { stat_buf.assume_init() };
            let mode = stat_buf.st_mode;
            if (mode & file_type_bits()) == regular_file_mode()
                && mode & (libc::S_IXUSR | libc::S_IXGRP | libc::S_IXOTH) != 0
            {
                return MutableExecPreparation::Block;
            }
        }
        return if lexical_mutable
            || resolved_mutable
            || path_has_untrusted_provenance(path, dirfd)
            || pin_target
        {
            MutableExecPreparation::Block
        } else {
            MutableExecPreparation::NotMutable
        };
    }

    // SAFETY: fd is a newly opened descriptor owned by this function.
    let path_file = unsafe { File::from_raw_fd(fd) };
    let Ok(metadata) = path_file.metadata() else {
        return MutableExecPreparation::Block;
    };
    if (metadata.mode() & file_type_bits()) != regular_file_mode() {
        return MutableExecPreparation::Block;
    }
    // Keep the lexical trust decision from the original path.  A mutable
    // directory can alias a trusted inode, and a script can change after the
    // descriptor is opened.  Snapshot every target with untrusted provenance.
    let descriptor_mutable = lexical_mutable
        || path_has_untrusted_provenance(path, dirfd)
        || fd_target_is_untrusted_exec(fd);
    if !descriptor_mutable {
        if pin_target {
            return MutableExecPreparation::Pinned(path_file.into_raw_fd());
        }
        return MutableExecPreparation::NotMutable;
    }
    let Some(mut file) = duplicate_fd_file(fd) else {
        return MutableExecPreparation::Block;
    };
    let Ok(metadata) = file.metadata() else {
        return MutableExecPreparation::Block;
    };
    snapshot_mutable_file(&mut file, metadata.mode(), env_entries).map_or(
        MutableExecPreparation::Block,
        MutableExecPreparation::Execute,
    )
}

#[cfg(target_os = "linux")]
fn snapshot_mutable_file(source: &mut File, mode: u32, env_entries: &[String]) -> Option<c_int> {
    let source_metadata = source.metadata().ok()?;
    if source_metadata.len() > MAX_MUTABLE_EXEC_SNAPSHOT_BYTES {
        return None;
    }
    source.seek(SeekFrom::Start(0)).ok()?;

    // SAFETY: the name is a static NUL-terminated string and the flags are valid memfd flags.
    let mut fd = unsafe {
        libc::memfd_create(
            c"workcell-mutable-exec".as_ptr(),
            libc::MFD_ALLOW_SEALING | libc::MFD_CLOEXEC | MFD_EXEC_FLAG,
        )
    };
    if fd < 0 {
        // Older Linux kernels do not know MFD_EXEC. Retry only for EINVAL;
        // newer kernels still enforce their configured memfd execution policy.
        // SAFETY: errno_location returns this thread's valid errno slot.
        let error = unsafe { *errno_location() };
        if error == libc::EINVAL {
            // SAFETY: the name is a static NUL-terminated string and the fallback flags are valid.
            fd = unsafe {
                libc::memfd_create(
                    c"workcell-mutable-exec".as_ptr(),
                    libc::MFD_ALLOW_SEALING | libc::MFD_CLOEXEC,
                )
            };
        }
    }
    if fd < 0 {
        return None;
    }
    // SAFETY: fd is a newly created descriptor owned by this function.
    let mut snapshot = unsafe { File::from_raw_fd(fd) };
    (|| {
        // SAFETY: snapshot is an owned regular memfd and the mode contains only ordinary bits.
        if unsafe { libc::fchmod(snapshot.as_raw_fd(), (mode & 0o777) as libc::mode_t) } != 0 {
            return None;
        }

        let mut copied = 0u64;
        let mut buffer = [0u8; MUTABLE_EXEC_COPY_CHUNK_BYTES];
        loop {
            let read = source.read(&mut buffer).ok()?;
            if read == 0 {
                break;
            }
            copied = copied.checked_add(read as u64)?;
            if copied > MAX_MUTABLE_EXEC_SNAPSHOT_BYTES {
                return None;
            }
            snapshot.write_all(&buffer[..read]).ok()?;
        }

        snapshot.seek(SeekFrom::Start(0)).ok()?;
        let mut header = [0u8; 4];
        let header_bytes = snapshot.read(&mut header).ok()?;
        if header_bytes == header.len() && header == [0x7f, b'E', b'L', b'F'] {
            return None;
        }

        snapshot.seek(SeekFrom::Start(0)).ok()?;
        let mut prefix = [0u8; 511];
        let prefix_bytes = snapshot.read(&mut prefix).ok()?;
        if buffer_targets_protected_runtime_via_shebang(
            &String::from_utf8_lossy(&prefix[..prefix_bytes]),
            env_entries,
        ) || buffer_targets_untrusted_native_via_shebang(
            &String::from_utf8_lossy(&prefix[..prefix_bytes]),
            env_entries,
        ) || (env_has_unsafe_loader_override(env_entries)
            && prefix[..prefix_bytes].starts_with(b"#!"))
        {
            return None;
        }

        let seals =
            libc::F_SEAL_WRITE | libc::F_SEAL_SHRINK | libc::F_SEAL_GROW | libc::F_SEAL_SEAL;
        // SAFETY: snapshot is an owned memfd and the seal set is a valid fcntl command.
        if unsafe { libc::fcntl(snapshot.as_raw_fd(), libc::F_ADD_SEALS, seals) } != 0 {
            return None;
        }

        // SAFETY: snapshot is an owned memfd and F_GETFD returns its descriptor flags.
        let descriptor_flags = unsafe { libc::fcntl(snapshot.as_raw_fd(), libc::F_GETFD) };
        if descriptor_flags < 0
            // SAFETY: snapshot is an owned memfd and descriptor_flags came from F_GETFD.
            || unsafe {
                libc::fcntl(
                    snapshot.as_raw_fd(),
                    libc::F_SETFD,
                    descriptor_flags & !libc::FD_CLOEXEC,
                )
            } != 0
        {
            return None;
        }

        snapshot.seek(SeekFrom::Start(0)).ok()?;
        Some(snapshot.into_raw_fd())
    })()
}

#[cfg(target_os = "linux")]
fn mutable_fd_preparation(fd: c_int, env_entries: &[String]) -> MutableExecPreparation {
    if !current_mode_blocks_mutable_native_exec() || fd < 0 {
        return MutableExecPreparation::NotMutable;
    }
    let untrusted = fd_target_is_untrusted_exec(fd);
    let Some(pinned_fd) = duplicate_owned_descriptor(fd) else {
        return MutableExecPreparation::Block;
    };
    if !untrusted {
        return MutableExecPreparation::Pinned(pinned_fd);
    }
    let Some(mut source) = duplicate_fd_file(fd) else {
        close_prepared_fd(pinned_fd);
        return MutableExecPreparation::Block;
    };
    let Ok(metadata) = source.metadata() else {
        close_prepared_fd(pinned_fd);
        return MutableExecPreparation::Block;
    };
    if (metadata.mode() & file_type_bits()) != regular_file_mode() {
        close_prepared_fd(pinned_fd);
        return MutableExecPreparation::Block;
    }
    match snapshot_mutable_file(&mut source, metadata.mode(), env_entries) {
        Some(snapshot_fd) => {
            close_prepared_fd(pinned_fd);
            MutableExecPreparation::Execute(snapshot_fd)
        }
        None => {
            close_prepared_fd(pinned_fd);
            MutableExecPreparation::Block
        }
    }
}

#[cfg(target_os = "linux")]
fn fd_exec_path(fd: c_int) -> Option<CString> {
    CString::new(proc_fd_path(fd)).ok()
}

#[cfg(target_os = "linux")]
fn close_prepared_fd(fd: c_int) {
    // SAFETY: callers pass the owned descriptor returned by mutable_exec_preparation.
    unsafe { libc::close(fd) };
}

#[cfg(target_os = "linux")]
fn duplicate_owned_descriptor(fd: c_int) -> Option<c_int> {
    // SAFETY: fd is borrowed for fcntl and the result is a new descriptor owned by the caller.
    let duplicate = unsafe { libc::fcntl(fd, libc::F_DUPFD_CLOEXEC, 3) };
    (duplicate >= 0).then_some(duplicate)
}

#[cfg(target_os = "linux")]
fn descriptor_is_shebang(fd: c_int) -> bool {
    let Some(mut file) = duplicate_fd_file(fd) else {
        return false;
    };
    let mut prefix = [0u8; 2];
    file.read_exact(&mut prefix).is_ok() && prefix == *b"#!"
}

#[cfg(target_os = "linux")]
fn prepare_descriptor_for_exec(fd: c_int) -> bool {
    if !descriptor_is_shebang(fd) {
        return true;
    }
    // SAFETY: fd is an owned descriptor and F_GETFD returns its descriptor flags.
    let descriptor_flags = unsafe { libc::fcntl(fd, libc::F_GETFD) };
    if descriptor_flags < 0 {
        return false;
    }
    // SAFETY: fd is an owned descriptor and descriptor_flags came from F_GETFD.
    unsafe { libc::fcntl(fd, libc::F_SETFD, descriptor_flags & !libc::FD_CLOEXEC) == 0 }
}

#[cfg(target_os = "linux")]
fn close_prepared_fd_preserving_errno(fd: c_int, result: c_int) {
    if result < 0 {
        // SAFETY: errno_location returns this thread's valid errno slot.
        let saved_errno = unsafe { *errno_location() };
        close_prepared_fd(fd);
        // SAFETY: errno_location returns this thread's valid errno slot.
        unsafe { *errno_location() = saved_errno };
    } else {
        close_prepared_fd(fd);
    }
}

#[cfg(target_os = "linux")]
fn execute_snapshot_execveat(
    fd: c_int,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    if !prepare_descriptor_for_exec(fd) {
        close_prepared_fd(fd);
        set_errno(libc::EPERM);
        return -1;
    }
    let empty = c"";
    // SAFETY: fd is a sealed executable snapshot, and argv/envp are the caller's original exec ABI pointers.
    let result = unsafe { execveat_fn()(fd, empty.as_ptr(), argv, envp, libc::AT_EMPTY_PATH) };
    if result < 0 {
        // Some Linux execveat implementations return ENOENT for a script whose
        // interpreter resolves through the descriptor path. Retry through the
        // stable proc descriptor before returning the original failure.
        // SAFETY: errno_location() returns the current thread's valid errno slot.
        let exec_errno = unsafe { *errno_location() };
        if exec_errno == libc::ENOENT
            && let Some(fd_path) = fd_exec_path(fd)
        {
            // SAFETY: fd_path names the still-open sealed snapshot and argv/envp
            // remain valid for the duration of this execve call.
            let fallback = unsafe { execve_fn()(fd_path.as_ptr(), argv, envp) };
            close_prepared_fd_preserving_errno(fd, fallback);
            return fallback;
        }
    }
    close_prepared_fd_preserving_errno(fd, result);
    result
}

#[cfg(target_os = "linux")]
fn execute_snapshot_execveat_with_shell_fallback(
    fd: c_int,
    argv: *const *const c_char,
    envp: *const *const c_char,
    arg_count: usize,
) -> c_int {
    if !prepare_descriptor_for_exec(fd) {
        close_prepared_fd(fd);
        set_errno(libc::EPERM);
        return -1;
    }
    let empty = c"";
    // SAFETY: fd is a sealed executable snapshot, and argv/envp are the caller's original exec ABI pointers.
    let mut result = unsafe { execveat_fn()(fd, empty.as_ptr(), argv, envp, libc::AT_EMPTY_PATH) };
    if result < 0 {
        // SAFETY: errno_location() returns the current thread's valid errno slot.
        let mut exec_errno = unsafe { *errno_location() };
        if exec_errno == libc::ENOENT
            && let Some(fd_path) = fd_exec_path(fd)
        {
            // SAFETY: fd_path names the still-open sealed snapshot and argv/envp
            // remain valid for this retry.
            result = unsafe { execve_fn()(fd_path.as_ptr(), argv, envp) };
            if result < 0 {
                // SAFETY: errno_location() returns the current thread's valid errno slot.
                exec_errno = unsafe { *errno_location() };
            }
        }
        if result < 0 && exec_errno == libc::ENOEXEC {
            let shell = c"/bin/sh";
            let Ok(fd_path) = CString::new(proc_fd_path(fd)) else {
                close_prepared_fd(fd);
                set_errno(libc::ENOENT);
                return -1;
            };
            let mut shell_argv = Vec::with_capacity(arg_count.saturating_add(2));
            shell_argv.push(shell.as_ptr());
            shell_argv.push(fd_path.as_ptr());
            if arg_count > 1 && !argv.is_null() {
                // SAFETY: collect_cstring_array validated arg_count entries before this call.
                let original_args =
                    unsafe { std::slice::from_raw_parts(argv.add(1), arg_count - 1) };
                shell_argv.extend_from_slice(original_args);
            }
            shell_argv.push(std::ptr::null());

            // SAFETY: shell and shell_argv are valid for the duration of the real execve call;
            // fd remains open so /proc/self/fd/N names the sealed snapshot.
            let shell_result = unsafe { execve_fn()(shell.as_ptr(), shell_argv.as_ptr(), envp) };
            close_prepared_fd_preserving_errno(fd, shell_result);
            return shell_result;
        }
    }
    close_prepared_fd_preserving_errno(fd, result);
    result
}

#[cfg(target_os = "linux")]
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ExecveatPreparationTarget {
    PassThrough,
    Descriptor,
    Path(c_int),
}

#[cfg(target_os = "linux")]
fn execveat_preparation_target(path: &str, flags: c_int) -> ExecveatPreparationTarget {
    let supported_flags = libc::AT_EMPTY_PATH | libc::AT_SYMLINK_NOFOLLOW;
    if flags & !supported_flags != 0 {
        return ExecveatPreparationTarget::PassThrough;
    }
    if path.is_empty() {
        return if flags & libc::AT_EMPTY_PATH != 0 {
            ExecveatPreparationTarget::Descriptor
        } else {
            ExecveatPreparationTarget::PassThrough
        };
    }
    let open_flags = if flags & libc::AT_SYMLINK_NOFOLLOW != 0 {
        libc::O_NOFOLLOW
    } else {
        0
    };
    ExecveatPreparationTarget::Path(open_flags)
}

#[cfg(target_os = "linux")]
fn execute_prepared_execve(
    path: &str,
    argv: *const *const c_char,
    envp: *const *const c_char,
    env_entries: &[String],
) -> Option<c_int> {
    match mutable_exec_preparation(path, libc::AT_FDCWD, 0, env_entries) {
        MutableExecPreparation::NotMutable => None,
        MutableExecPreparation::Block => {
            report_mutable_native_exec_block();
            Some(-1)
        }
        MutableExecPreparation::Pinned(fd) | MutableExecPreparation::Execute(fd) => {
            Some(execute_snapshot_execveat(fd, argv, envp))
        }
    }
}

#[cfg(target_os = "linux")]
fn execute_prepared_search_execve(
    path: &str,
    argv: *const *const c_char,
    envp: *const *const c_char,
    env_entries: &[String],
    arg_count: usize,
) -> Option<c_int> {
    match mutable_exec_preparation(path, libc::AT_FDCWD, 0, env_entries) {
        MutableExecPreparation::NotMutable => None,
        MutableExecPreparation::Block => {
            report_mutable_native_exec_block();
            Some(-1)
        }
        MutableExecPreparation::Pinned(fd) | MutableExecPreparation::Execute(fd) => Some(
            execute_snapshot_execveat_with_shell_fallback(fd, argv, envp, arg_count),
        ),
    }
}

#[cfg(target_os = "linux")]
fn execute_prepared_execveat(
    path: &str,
    dirfd: c_int,
    flags: c_int,
    argv: *const *const c_char,
    envp: *const *const c_char,
    env_entries: &[String],
) -> Option<c_int> {
    match execveat_preparation_target(path, flags) {
        ExecveatPreparationTarget::PassThrough => None,
        ExecveatPreparationTarget::Descriptor => match mutable_fd_preparation(dirfd, env_entries) {
            MutableExecPreparation::NotMutable => None,
            MutableExecPreparation::Block => {
                report_mutable_native_exec_block();
                Some(-1)
            }
            MutableExecPreparation::Pinned(fd) | MutableExecPreparation::Execute(fd) => {
                Some(execute_snapshot_execveat(fd, argv, envp))
            }
        },
        ExecveatPreparationTarget::Path(open_flags) => {
            // Linux accepts AT_EMPTY_PATH with a non-empty pathname. The flag does
            // not change pathname resolution, so the target still needs a snapshot.
            match mutable_exec_preparation(path, dirfd, open_flags, env_entries) {
                MutableExecPreparation::NotMutable => None,
                MutableExecPreparation::Block => {
                    report_mutable_native_exec_block();
                    Some(-1)
                }
                MutableExecPreparation::Pinned(fd) | MutableExecPreparation::Execute(fd) => {
                    Some(execute_snapshot_execveat(fd, argv, envp))
                }
            }
        }
    }
}

#[cfg(target_os = "linux")]
fn execute_prepared_spawn(
    path: &str,
    pid: *mut pid_t,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *const c_char,
    envp: *const *const c_char,
    env_entries: &[String],
) -> Option<c_int> {
    if current_mode_blocks_mutable_native_exec()
        && !file_actions.is_null()
        && !Path::new(path).is_absolute()
    {
        report_mutable_native_exec_block();
        return Some(libc::EPERM);
    }
    match mutable_exec_preparation(path, libc::AT_FDCWD, 0, env_entries) {
        MutableExecPreparation::NotMutable => None,
        MutableExecPreparation::Block => {
            report_mutable_native_exec_block();
            Some(libc::EPERM)
        }
        MutableExecPreparation::Pinned(fd) | MutableExecPreparation::Execute(fd) => {
            if !file_actions.is_null() {
                // File actions are opaque here. They may close or replace the descriptor that
                // names the sealed snapshot before the child resolves /proc/self/fd/N.
                close_prepared_fd(fd);
                report_mutable_native_exec_block();
                return Some(libc::EPERM);
            }
            if !prepare_descriptor_for_exec(fd) {
                close_prepared_fd(fd);
                report_mutable_native_exec_block();
                return Some(libc::EPERM);
            }
            let Some(fd_path) = fd_exec_path(fd) else {
                close_prepared_fd(fd);
                report_mutable_native_exec_block();
                return Some(libc::EPERM);
            };
            // SAFETY: fd_path remains alive for the call; fd stays open so a script interpreter
            // can access `/proc/self/fd/N` after posix_spawn forks the child.
            let result =
                unsafe { posix_spawn_fn()(pid, fd_path.as_ptr(), file_actions, attrp, argv, envp) };
            close_prepared_fd(fd);
            Some(result)
        }
    }
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

fn report_with_errno(message: &str, errno: c_int) {
    // SAFETY: message is a live &str valid for len bytes (write is read-only); errno_location() returns libc's valid errno slot.
    unsafe {
        libc::write(
            libc::STDERR_FILENO,
            message.as_ptr().cast::<c_void>(),
            message.len(),
        );
        *errno_location() = errno;
    }
}

#[cfg(target_os = "linux")]
fn set_errno(errno: c_int) {
    // SAFETY: errno_location() returns the current thread's valid errno slot.
    unsafe { *errno_location() = errno };
}

fn report(message: &str) {
    report_with_errno(message, libc::EPERM);
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

fn report_native_loader_env_block() {
    report(NATIVE_LOADER_ENV_BLOCK_MESSAGE);
}

fn report_missing_guard_env_block() {
    report(MISSING_GUARD_ENV_BLOCK_MESSAGE);
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

#[cfg(not(target_os = "linux"))]
fn posix_spawnp_fn() -> PosixSpawnpFn {
    // SAFETY: c"posix_spawnp" is a valid C-string literal and matches the PosixSpawnpFn ABI resolved into this OnceLock.
    *POSIX_SPAWNP_FN.get_or_init(|| unsafe { load_symbol(c"posix_spawnp") })
}

fn real_syscall_fn() -> SyscallFn {
    // SAFETY: c"syscall" is a valid C-string literal and matches the SyscallFn ABI resolved into this OnceLock.
    *REAL_SYSCALL_FN.get_or_init(|| unsafe { load_symbol(c"syscall") })
}

fn c_path_string(path: *const c_char) -> Result<String, ExecInputTooLarge> {
    bounded_c_string(path)
}

fn report_exec_input_block() {
    report_with_errno("Workcell rejected oversized exec arguments.\n", libc::E2BIG);
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
                guarded_execve(
                    arg1 as *const c_char,
                    arg2 as *const *const c_char,
                    arg3 as *const *const c_char,
                ) as c_long
            };
        }
        if number == SYS_EXECVEAT {
            // SAFETY: number==SYS_execveat, so arg1..arg5 are (dirfd, pathname, argv, envp, flags) per the execveat ABI.
            return unsafe {
                guarded_execveat(
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

// The fixed six-argument ABI matches the integer-register calling convention
// used by libc's variadic syscall on the supported Linux targets. A public
// no_mangle symbol is required because cdylib export filtering can hide an
// assembly-only symbol from dynamic lookup by a preloaded caller.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn workcell_syscall(
    number: c_long,
    arg1: c_long,
    arg2: c_long,
    arg3: c_long,
    arg4: c_long,
    arg5: c_long,
    arg6: c_long,
) -> c_long {
    // SAFETY: the wrapper receives the raw syscall register values and forwards
    // them without dereferencing or changing their representation.
    unsafe { workcell_syscall_shim(number, arg1, arg2, arg3, arg4, arg5, arg6) }
}

#[cfg(target_os = "linux")]
#[unsafe(no_mangle)]
#[inline(never)]
pub unsafe extern "C" fn workcell_execve(
    path: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    // SAFETY: the C variadic adapter forwards the original execve pointer ABI.
    unsafe { guarded_execve(path, argv, envp) }
}

#[cfg(target_os = "linux")]
#[unsafe(no_mangle)]
#[inline(never)]
pub unsafe extern "C" fn workcell_execv(path: *const c_char, argv: *const *const c_char) -> c_int {
    // SAFETY: the C variadic adapter forwards a bounded argv array with a sentinel.
    unsafe { guarded_execv(path, argv) }
}

#[cfg(target_os = "linux")]
#[unsafe(no_mangle)]
#[inline(never)]
pub unsafe extern "C" fn workcell_execvp(file: *const c_char, argv: *const *const c_char) -> c_int {
    // SAFETY: the C variadic adapter forwards a bounded argv array with a sentinel.
    unsafe { guarded_execvp(file, argv) }
}

#[cfg(target_os = "linux")]
#[unsafe(no_mangle)]
#[inline(never)]
pub unsafe extern "C" fn workcell_execvpe(
    file: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    // SAFETY: the C ABI adapter forwards the original execvpe pointer ABI.
    unsafe { guarded_execvpe(file, argv, envp) }
}

#[cfg(target_os = "linux")]
#[unsafe(no_mangle)]
#[inline(never)]
pub unsafe extern "C" fn workcell_execveat(
    dirfd: c_int,
    pathname: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
    flags: c_int,
) -> c_int {
    // SAFETY: the C ABI adapter forwards the original execveat pointer ABI.
    unsafe { guarded_execveat(dirfd, pathname, argv, envp, flags) }
}

#[cfg(target_os = "linux")]
#[unsafe(no_mangle)]
#[inline(never)]
pub unsafe extern "C" fn workcell_fexecve(
    fd: c_int,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    // SAFETY: the C ABI adapter forwards the original fexecve pointer ABI.
    unsafe { guarded_fexecve(fd, argv, envp) }
}

#[cfg(target_os = "linux")]
#[unsafe(no_mangle)]
#[inline(never)]
pub unsafe extern "C" fn workcell_posix_spawn(
    pid: *mut pid_t,
    path: *const c_char,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    // SAFETY: the C ABI adapter forwards the original posix_spawn pointer ABI.
    unsafe { guarded_posix_spawn(pid, path, file_actions, attrp, argv, envp) }
}

#[cfg(target_os = "linux")]
#[unsafe(no_mangle)]
#[inline(never)]
pub unsafe extern "C" fn workcell_posix_spawnp(
    pid: *mut pid_t,
    file: *const c_char,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    // SAFETY: the C ABI adapter forwards the original posix_spawnp pointer ABI.
    unsafe { guarded_posix_spawnp(pid, file, file_actions, attrp, argv, envp) }
}

#[cfg(target_os = "linux")]
#[cfg(target_arch = "x86_64")]
macro_rules! define_variadic_exec_trampoline {
    ($name:ident, $target:ident) => {
        #[unsafe(naked)]
        #[unsafe(no_mangle)]
        pub unsafe extern "C" fn $name() {
            core::arch::naked_asm!(concat!("jmp ", stringify!($target)));
        }
    };
}

#[cfg(target_os = "linux")]
#[cfg(target_arch = "aarch64")]
macro_rules! define_variadic_exec_trampoline {
    ($name:ident, $target:ident) => {
        #[unsafe(naked)]
        #[unsafe(no_mangle)]
        pub unsafe extern "C" fn $name() {
            core::arch::naked_asm!(concat!("b ", stringify!($target)));
        }
    };
}

#[cfg(target_os = "linux")]
define_variadic_exec_trampoline!(execl, workcell_export_execl);
#[cfg(target_os = "linux")]
define_variadic_exec_trampoline!(execlp, workcell_export_execlp);
#[cfg(target_os = "linux")]
define_variadic_exec_trampoline!(execle, workcell_export_execle);

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_execve(
    path: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let path_string = match c_path_string(path) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    let args = match collect_cstring_array(argv) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    if should_block_null_explicit_env(envp) {
        report_missing_guard_env_block();
        return -1;
    }
    let env_entries = match collect_cstring_array(effective_env_ptr(envp)) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };

    if should_block_missing_guard_env(&path_string, &args, &env_entries) {
        report_missing_guard_env_block();
        return -1;
    }
    if should_block_workcell_launcher_loader_env(&path_string, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if should_block_loader_env_for_path(&path_string, &env_entries) {
        report_native_loader_env_block();
        return -1;
    }
    if should_block_protected_runtime_exec(&path_string, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&path_string, &args, &env_entries) {
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

    #[cfg(target_os = "linux")]
    if let Some(result) = execute_prepared_execve(&path_string, argv, envp, &env_entries) {
        return result;
    }

    // SAFETY: forwards the caller's original, unmodified execve arguments to the real libc execve resolved via RTLD_NEXT.
    unsafe { execve_fn()(path, argv, envp) }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_execv(path: *const c_char, argv: *const *const c_char) -> c_int {
    let path_string = match c_path_string(path) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    let args = match collect_cstring_array(argv) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    // SAFETY: environ is libc-initialized; read in the calling thread with no concurrent setenv/putenv.
    let env_entries = match current_process_env_entries() {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    if should_block_missing_guard_env(&path_string, &args, &env_entries) {
        report_missing_guard_env_block();
        return -1;
    }

    if should_block_workcell_launcher_loader_env(&path_string, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if should_block_loader_env_for_path(&path_string, &env_entries) {
        report_native_loader_env_block();
        return -1;
    }
    if should_block_protected_runtime_exec(&path_string, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&path_string, &args, &env_entries) {
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

    #[cfg(target_os = "linux")]
    // SAFETY: environ is libc-initialized and this launcher reads it without concurrent mutation.
    let current_env = unsafe { environ.cast() };
    #[cfg(target_os = "linux")]
    if let Some(result) = execute_prepared_execve(&path_string, argv, current_env, &env_entries) {
        return result;
    }

    // SAFETY: forwards the caller's original, unmodified execv arguments to the real libc execv resolved via RTLD_NEXT.
    unsafe { execv_fn()(path, argv) }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_execvp(file: *const c_char, argv: *const *const c_char) -> c_int {
    let file_string = match c_path_string(file) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    let args = match collect_cstring_array(argv) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    // SAFETY: environ is libc-initialized; read in the calling thread with no concurrent setenv/putenv.
    let env_entries = match current_process_env_entries() {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    #[cfg(target_os = "linux")]
    let effective_path = match resolve_exec_search_target(&file_string, &env_entries) {
        Ok(Some(path)) => path,
        Ok(None) => {
            set_errno(libc::ENOENT);
            return -1;
        }
        Err(ExecSearchError::InputTooLarge) => {
            report_exec_input_block();
            return -1;
        }
        Err(ExecSearchError::PermissionDenied) => {
            set_errno(libc::EACCES);
            return -1;
        }
    };
    #[cfg(not(target_os = "linux"))]
    let effective_path = resolve_exec_search_target(&file_string, &env_entries)
        .ok()
        .flatten()
        .unwrap_or_else(|| file_string.clone());

    if should_block_missing_guard_env(&effective_path, &args, &env_entries) {
        report_missing_guard_env_block();
        return -1;
    }
    if should_block_workcell_launcher_loader_env(&effective_path, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if should_block_loader_env_for_path(&effective_path, &env_entries) {
        report_native_loader_env_block();
        return -1;
    }
    if should_block_protected_runtime_exec(&effective_path, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&effective_path, &args, &env_entries) {
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

    #[cfg(target_os = "linux")]
    // SAFETY: environ is libc-initialized and this launcher reads it without concurrent mutation.
    let current_env = unsafe { environ.cast() };
    #[cfg(target_os = "linux")]
    if let Some(result) =
        execute_prepared_search_execve(&effective_path, argv, current_env, &env_entries, args.len())
    {
        return result;
    }

    #[cfg(target_os = "linux")]
    {
        let Ok(effective_path) = CString::new(effective_path.as_bytes()) else {
            set_errno(libc::EINVAL);
            return -1;
        };
        // SAFETY: the selected path contains a slash, so libc performs no second PATH search.
        unsafe { execvp_fn()(effective_path.as_ptr(), argv) }
    }
    #[cfg(not(target_os = "linux"))]
    {
        // SAFETY: forwards the caller's original, unmodified execvp arguments to the real libc execvp resolved via RTLD_NEXT.
        unsafe { execvp_fn()(file, argv) }
    }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_execvpe(
    file: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let file_string = match c_path_string(file) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    let args = match collect_cstring_array(argv) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    if should_block_null_explicit_env(envp) {
        report_missing_guard_env_block();
        return -1;
    }
    let env_entries = match collect_cstring_array(effective_env_ptr(envp)) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    let caller_env_entries = match current_process_env_entries() {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    #[cfg(target_os = "linux")]
    let effective_path = match resolve_exec_search_target(&file_string, &caller_env_entries) {
        Ok(Some(path)) => path,
        Ok(None) => {
            set_errno(libc::ENOENT);
            return -1;
        }
        Err(ExecSearchError::InputTooLarge) => {
            report_exec_input_block();
            return -1;
        }
        Err(ExecSearchError::PermissionDenied) => {
            set_errno(libc::EACCES);
            return -1;
        }
    };
    #[cfg(not(target_os = "linux"))]
    let effective_path = resolve_exec_search_target(&file_string, &caller_env_entries)
        .ok()
        .flatten()
        .unwrap_or_else(|| file_string.clone());

    if should_block_missing_guard_env(&effective_path, &args, &env_entries) {
        report_missing_guard_env_block();
        return -1;
    }
    if should_block_workcell_launcher_loader_env(&effective_path, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if should_block_loader_env_for_path(&effective_path, &env_entries) {
        report_native_loader_env_block();
        return -1;
    }
    if should_block_protected_runtime_exec(&effective_path, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&effective_path, &args, &env_entries) {
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

    #[cfg(target_os = "linux")]
    if let Some(result) =
        execute_prepared_search_execve(&effective_path, argv, envp, &env_entries, args.len())
    {
        return result;
    }

    #[cfg(target_os = "linux")]
    {
        let Ok(effective_path) = CString::new(effective_path.as_bytes()) else {
            set_errno(libc::EINVAL);
            return -1;
        };
        // SAFETY: the selected path contains a slash, so libc performs no second PATH search.
        unsafe { execvpe_fn()(effective_path.as_ptr(), argv, envp) }
    }
    #[cfg(not(target_os = "linux"))]
    {
        // SAFETY: forwards the caller's original, unmodified execvpe arguments to the real libc execvpe resolved via RTLD_NEXT.
        unsafe { execvpe_fn()(file, argv, envp) }
    }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_execveat(
    dirfd: c_int,
    pathname: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
    flags: c_int,
) -> c_int {
    let pathname_string = match c_path_string(pathname) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    let args = match collect_cstring_array(argv) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    if should_block_null_explicit_env(envp) {
        report_missing_guard_env_block();
        return -1;
    }
    let env_entries = match collect_cstring_array(effective_env_ptr(envp)) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    let git_target = is_git_execveat_target(dirfd, &pathname_string, flags);
    let effective_path =
        if git_target && (flags & AT_EMPTY_PATH_FLAG) != 0 && pathname_string.is_empty() {
            "/usr/local/bin/git".to_owned()
        } else {
            pathname_string.clone()
        };

    #[cfg(target_os = "linux")]
    {
        if pathname_string.is_empty() {
            if current_mode_blocks_mutable_native_exec()
                && !env_has_approved_guard_preload(&env_entries)
            {
                report_missing_guard_env_block();
                return -1;
            }
        } else if should_block_missing_guard_env(&effective_path, &args, &env_entries) {
            report_missing_guard_env_block();
            return -1;
        }
    }

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
                || loader_fd_targets_mutable_native_exec(dirfd, &args)
                || file_descriptor_is_untrusted_shebang_native_exec(dirfd, &env_entries),
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
                        mutable_native_target = file_descriptor_is_untrusted_shebang_native_exec(
                            candidate_fd,
                            &env_entries,
                        );
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
    let native_loader_env_blocked =
        if (flags & AT_EMPTY_PATH_FLAG) != 0 && pathname_string.is_empty() {
            should_block_loader_env_for_fd(dirfd, &env_entries)
        } else {
            should_block_loader_env_for_path(&pathname_string, &env_entries)
        };
    if native_loader_env_blocked {
        report_native_loader_env_block();
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

    #[cfg(target_os = "linux")]
    if let Some(result) =
        execute_prepared_execveat(&pathname_string, dirfd, flags, argv, envp, &env_entries)
    {
        return result;
    }

    // SAFETY: forwards the caller's original, unmodified execveat arguments to the real libc execveat resolved via RTLD_NEXT.
    unsafe { execveat_fn()(dirfd, pathname, argv, envp, flags) }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_fexecve(
    fd: c_int,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let args = match collect_cstring_array(argv) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };
    if should_block_null_explicit_env(envp) {
        report_missing_guard_env_block();
        return -1;
    }
    let env_entries = match collect_cstring_array(effective_env_ptr(envp)) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return -1;
        }
    };

    #[cfg(target_os = "linux")]
    if current_mode_blocks_mutable_native_exec() && !env_has_approved_guard_preload(&env_entries) {
        report_missing_guard_env_block();
        return -1;
    }

    if should_block_workcell_launcher_fd_loader_env(fd, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
    if should_block_loader_env_for_fd(fd, &env_entries) {
        report_native_loader_env_block();
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
        || file_descriptor_is_untrusted_shebang_native_exec(fd, &env_entries)
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

    #[cfg(target_os = "linux")]
    match mutable_fd_preparation(fd, &env_entries) {
        MutableExecPreparation::NotMutable => {}
        MutableExecPreparation::Block => {
            report_mutable_native_exec_block();
            return -1;
        }
        MutableExecPreparation::Pinned(snapshot_fd)
        | MutableExecPreparation::Execute(snapshot_fd) => {
            return execute_snapshot_execveat(snapshot_fd, argv, envp);
        }
    }

    // SAFETY: forwards the caller's original, unmodified fexecve arguments to the real libc fexecve resolved via RTLD_NEXT.
    unsafe { fexecve_fn()(fd, argv, envp) }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_posix_spawn(
    pid: *mut pid_t,
    path: *const c_char,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let path_string = match c_path_string(path) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return libc::E2BIG;
        }
    };
    let args = match collect_cstring_array(argv) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return libc::E2BIG;
        }
    };
    if should_block_null_explicit_env(envp) {
        report_missing_guard_env_block();
        return libc::EPERM;
    }
    let env_entries = match collect_cstring_array(effective_env_ptr(envp)) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return libc::E2BIG;
        }
    };
    if should_block_missing_guard_env(&path_string, &args, &env_entries) {
        report_missing_guard_env_block();
        return libc::EPERM;
    }

    if should_block_workcell_launcher_loader_env(&path_string, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return libc::EPERM;
    }
    if should_block_loader_env_for_path(&path_string, &env_entries) {
        report_native_loader_env_block();
        return libc::EPERM;
    }
    if should_block_protected_runtime_exec(&path_string, &args, &env_entries) {
        report_protected_runtime_block();
        return libc::EPERM;
    }
    if should_block_mutable_native_exec(&path_string, &args, &env_entries) {
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

    #[cfg(target_os = "linux")]
    if let Some(result) = execute_prepared_spawn(
        &path_string,
        pid,
        file_actions,
        attrp,
        argv,
        envp,
        &env_entries,
    ) {
        return result;
    }

    // SAFETY: forwards the caller's original, unmodified posix_spawn arguments to the real libc posix_spawn resolved via RTLD_NEXT.
    unsafe { posix_spawn_fn()(pid, path, file_actions, attrp, argv, envp) }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_posix_spawnp(
    pid: *mut pid_t,
    file: *const c_char,
    file_actions: *const libc::posix_spawn_file_actions_t,
    attrp: *const libc::posix_spawnattr_t,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let file_string = match c_path_string(file) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return libc::E2BIG;
        }
    };
    let args = match collect_cstring_array(argv) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return libc::E2BIG;
        }
    };
    if should_block_null_explicit_env(envp) {
        report_missing_guard_env_block();
        return libc::EPERM;
    }
    let env_entries = match collect_cstring_array(effective_env_ptr(envp)) {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return libc::E2BIG;
        }
    };
    let caller_env_entries = match current_process_env_entries() {
        Ok(value) => value,
        Err(_) => {
            report_exec_input_block();
            return libc::E2BIG;
        }
    };
    #[cfg(target_os = "linux")]
    let effective_path = match resolve_exec_search_target(&file_string, &caller_env_entries) {
        Ok(Some(path)) => path,
        Ok(None) => return libc::ENOENT,
        Err(ExecSearchError::InputTooLarge) => {
            report_exec_input_block();
            return libc::E2BIG;
        }
        Err(ExecSearchError::PermissionDenied) => return libc::EACCES,
    };
    #[cfg(not(target_os = "linux"))]
    let effective_path = resolve_exec_search_target(&file_string, &caller_env_entries)
        .ok()
        .flatten()
        .unwrap_or_else(|| file_string.clone());

    if should_block_missing_guard_env(&effective_path, &args, &env_entries) {
        report_missing_guard_env_block();
        return libc::EPERM;
    }
    if should_block_workcell_launcher_loader_env(&effective_path, &env_entries) {
        report_workcell_launcher_loader_env_block();
        return libc::EPERM;
    }
    if should_block_loader_env_for_path(&effective_path, &env_entries) {
        report_native_loader_env_block();
        return libc::EPERM;
    }
    if should_block_protected_runtime_exec(&effective_path, &args, &env_entries) {
        report_protected_runtime_block();
        return libc::EPERM;
    }
    if should_block_mutable_native_exec(&effective_path, &args, &env_entries) {
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

    #[cfg(target_os = "linux")]
    if let Some(result) = execute_prepared_spawn(
        &effective_path,
        pid,
        file_actions,
        attrp,
        argv,
        envp,
        &env_entries,
    ) {
        return result;
    }

    #[cfg(target_os = "linux")]
    {
        let Ok(effective_path) = CString::new(effective_path.as_bytes()) else {
            return libc::EINVAL;
        };
        // SAFETY: the selected path contains a slash, so libc performs no second PATH search.
        unsafe {
            posix_spawn_fn()(
                pid,
                effective_path.as_ptr(),
                file_actions,
                attrp,
                argv,
                envp,
            )
        }
    }
    #[cfg(not(target_os = "linux"))]
    {
        // SAFETY: forwards the caller's original, unmodified posix_spawnp arguments to the real libc posix_spawnp resolved via RTLD_NEXT.
        unsafe { posix_spawnp_fn()(pid, file, file_actions, attrp, argv, envp) }
    }
}

// Keep unversioned exports for callers that do not request a glibc symbol
// version. The versioned adapter in the container handles versioned callers.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn execve(
    path: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    // SAFETY: forwards the original execve ABI to the guard implementation.
    unsafe { guarded_execve(path, argv, envp) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execv(path: *const c_char, argv: *const *const c_char) -> c_int {
    // SAFETY: forwards the original execv ABI to the guard implementation.
    unsafe { guarded_execv(path, argv) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execvp(file: *const c_char, argv: *const *const c_char) -> c_int {
    // SAFETY: forwards the original execvp ABI to the guard implementation.
    unsafe { guarded_execvp(file, argv) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execvpe(
    file: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    // SAFETY: forwards the original execvpe ABI to the guard implementation.
    unsafe { guarded_execvpe(file, argv, envp) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execveat(
    dirfd: c_int,
    pathname: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
    flags: c_int,
) -> c_int {
    // SAFETY: forwards the original execveat ABI to the guard implementation.
    unsafe { guarded_execveat(dirfd, pathname, argv, envp, flags) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn fexecve(
    fd: c_int,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    // SAFETY: forwards the original fexecve ABI to the guard implementation.
    unsafe { guarded_fexecve(fd, argv, envp) }
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
    // SAFETY: forwards the original posix_spawn ABI to the guard implementation.
    unsafe { guarded_posix_spawn(pid, path, file_actions, attrp, argv, envp) }
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
    // SAFETY: forwards the original posix_spawnp ABI to the guard implementation.
    unsafe { guarded_posix_spawnp(pid, file, file_actions, attrp, argv, envp) }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn syscall(
    number: c_long,
    arg1: c_long,
    arg2: c_long,
    arg3: c_long,
    arg4: c_long,
    arg5: c_long,
    arg6: c_long,
) -> c_long {
    // SAFETY: forwards the raw syscall register values to the fixed ABI shim.
    unsafe { workcell_syscall(number, arg1, arg2, arg3, arg4, arg5, arg6) }
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
            .ok()
            .flatten()
    }

    /// Fuzz `env_has_unsafe_git_override`.
    pub fn env_has_unsafe_git_override(env_entries: &[String]) -> bool {
        crate::gitpolicy::env_has_unsafe_git_override(env_entries)
    }

    /// Fuzz `git_config_spec_is_blocked`.
    pub fn git_config_spec_is_blocked(spec: &str) -> bool {
        crate::gitpolicy::git_config_spec_is_blocked(spec)
    }

    /// Fuzz `git_config_key_is_blocked`.
    pub fn git_config_key_is_blocked(key: &str) -> bool {
        crate::gitpolicy::git_config_key_is_blocked(key)
    }

    /// Fuzz `git_config_spec_value_is_explicit_safe`.
    pub fn git_config_spec_value_is_explicit_safe(key: &str, value: &str) -> bool {
        crate::gitpolicy::git_config_spec_value_is_explicit_safe(key, value)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::gitpolicy::*;
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

    #[cfg(target_os = "linux")]
    #[test]
    fn mutable_snapshot_is_sealed_and_stable_after_source_rewrite() {
        let dir = create_temp_test_dir("mutable-snapshot");
        let source_path = dir.join("script");
        fs::write(&source_path, b"#!/bin/sh\nprintf original\n").expect("write source");
        let mut permissions = fs::metadata(&source_path)
            .expect("source metadata")
            .permissions();
        permissions.set_mode(0o755);
        fs::set_permissions(&source_path, permissions).expect("set executable mode");

        let mut source = File::open(&source_path).expect("open source");
        let snapshot_fd = snapshot_mutable_file(&mut source, 0o755, &[]).expect("snapshot");
        fs::write(&source_path, b"\x7fELF attacker\n").expect("rewrite source");

        // SAFETY: snapshot_fd is returned as an owned descriptor by snapshot_mutable_file.
        let mut snapshot = unsafe { File::from_raw_fd(snapshot_fd) };
        let mut bytes = Vec::new();
        snapshot.read_to_end(&mut bytes).expect("read snapshot");
        assert_eq!(bytes, b"#!/bin/sh\nprintf original\n");
        assert_eq!(
            // SAFETY: snapshot is an owned memfd returned by snapshot_mutable_file.
            unsafe { libc::fcntl(snapshot.as_raw_fd(), libc::F_GET_SEALS) },
            libc::F_SEAL_WRITE | libc::F_SEAL_SHRINK | libc::F_SEAL_GROW | libc::F_SEAL_SEAL
        );

        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn mutable_script_snapshot_executes_with_a_null_argument_vector() {
        let dir = create_temp_test_dir("mutable-script-exec");
        let source_path = dir.join("script");
        fs::write(&source_path, b"#!/bin/sh\nexit 37\n").expect("write source");
        let mut permissions = fs::metadata(&source_path)
            .expect("source metadata")
            .permissions();
        permissions.set_mode(0o755);
        fs::set_permissions(&source_path, permissions).expect("set executable mode");

        let mut source = File::open(&source_path).expect("open source");
        let snapshot_fd = snapshot_mutable_file(&mut source, 0o755, &[]).expect("snapshot");
        let preload =
            CString::new(format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}")).expect("preload environment");
        let envp = [preload.as_ptr(), std::ptr::null()];
        let _ = execveat_fn();

        // SAFETY: fork has no arguments and duplicates the current process.
        let child = unsafe { libc::fork() };
        assert!(child >= 0, "fork snapshot child");
        if child == 0 {
            let result = execute_snapshot_execveat(snapshot_fd, std::ptr::null(), envp.as_ptr());
            let exit_code = if result < 0 {
                // SAFETY: errno_location returns this thread's valid errno slot.
                100 + unsafe { *errno_location() }.clamp(0, 99)
            } else {
                99
            };
            // SAFETY: the child must not run test-harness destructors.
            unsafe { libc::_exit(exit_code) };
        }

        close_prepared_fd(snapshot_fd);
        let mut status = 0;
        // SAFETY: child is the positive PID returned by fork and status is writable.
        assert_eq!(unsafe { libc::waitpid(child, &mut status, 0) }, child);
        assert!(libc::WIFEXITED(status));
        assert_eq!(libc::WEXITSTATUS(status), 37);

        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn mutable_shebang_native_interpreters_and_loader_targets_are_rejected() {
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/workspace/native\n",
            &[]
        ));
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/tmp/ld-linux-x86-64.so.2 /tmp/native\n",
            &[]
        ));
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/usr/bin/env -S /workspace/native\n",
            &[]
        ));
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/usr/bin/env -S/workspace/native\n",
            &[]
        ));
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/usr/bin/env -S --unset=LD_PRELOAD /bin/sh\n",
            &[]
        ));
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/usr/bin/env -S -uLD_PRELOAD /bin/sh\n",
            &[]
        ));
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/usr/bin/env -S -u=LD_PRELOAD /bin/sh\n",
            &[]
        ));
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/usr/bin/env --split-string=/workspace/native\n",
            &[]
        ));
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/usr/bin/env -u LD_PRELOAD /bin/sh\n",
            &[]
        ));
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/usr/bin/env LD_PRELOAD=/tmp/evil.so /bin/sh\n",
            &[]
        ));
        assert!(buffer_targets_untrusted_native_via_shebang(
            "#!/usr/bin/env -i /bin/sh\n",
            &[]
        ));
        assert!(!buffer_targets_untrusted_native_via_shebang(
            "#!/bin/sh\n",
            &[]
        ));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn loader_control_options_reject_mutable_values() {
        assert!(loader_args_target_mutable_native_exec(&[
            "/lib64/ld-linux-x86-64.so.2".to_string(),
            "--preload=/tmp/evil.so".to_string(),
            "/bin/true".to_string(),
        ]));
        assert!(loader_args_target_mutable_native_exec(&[
            "/lib64/ld-linux-x86-64.so.2".to_string(),
            "--library-path".to_string(),
            "/tmp".to_string(),
            "/bin/true".to_string(),
        ]));
        assert!(loader_args_target_mutable_native_exec(&[
            "/lib64/ld-linux-x86-64.so.2".to_string(),
            "--audit=/tmp/audit.so".to_string(),
            "/bin/true".to_string(),
        ]));
        assert!(!loader_args_target_mutable_native_exec(&[
            "/lib64/ld-linux-x86-64.so.2".to_string(),
            "/bin/true".to_string(),
        ]));
        assert!(loader_arg_targets_mutable_native_exec("/proc/self/fd/9"));
        assert!(loader_arg_targets_mutable_native_exec(
            "/proc/self/cwd/bin/true"
        ));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn magic_and_relative_shebang_targets_are_rejected() {
        assert!(path_is_magic_exec_target("/proc/self/fd/9"));
        assert!(path_is_magic_exec_target("/proc/self/cwd/bin/true"));
        assert!(path_is_magic_exec_target("/dev/fd/9"));
        assert!(shebang_path_is_untrusted_native_exec(
            "relative-interpreter"
        ));
        assert!(shebang_path_is_untrusted_native_exec("/proc/self/fd/9"));
    }

    #[test]
    fn path_limits_accept_exact_values_and_reject_over_limits() {
        let exact_segment = "x".repeat(MAX_EXEC_PATH_SEGMENT_BYTES);
        assert!(validate_path_value(&exact_segment).is_ok());
        assert_eq!(
            validate_path_value(&"x".repeat(MAX_EXEC_PATH_SEGMENT_BYTES + 1)),
            Err(ExecInputTooLarge)
        );

        let exact_segments = std::iter::repeat_n("x", MAX_EXEC_PATH_SEGMENTS)
            .collect::<Vec<_>>()
            .join(":");
        assert!(validate_path_value(&exact_segments).is_ok());
        let over_segments = std::iter::repeat_n("x", MAX_EXEC_PATH_SEGMENTS + 1)
            .collect::<Vec<_>>()
            .join(":");
        assert_eq!(validate_path_value(&over_segments), Err(ExecInputTooLarge));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn guard_environment_requires_the_exact_preload() {
        assert!(env_has_approved_guard_preload(&[format!(
            "LD_PRELOAD={ALLOWED_LD_PRELOAD}"
        )]));
        assert!(!env_has_approved_guard_preload(
            &["LD_PRELOAD=".to_string()]
        ));
        assert!(!env_has_approved_guard_preload(&[
            "LD_PRELOAD=/workspace/guard.so".to_string()
        ]));
        assert!(!env_has_approved_guard_preload(&[
            "LD_PRELOAD=".to_string(),
            format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}"),
        ]));
        assert!(!env_has_approved_guard_preload(&[
            format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}"),
            format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}"),
        ]));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn mutable_spawn_with_file_actions_fails_closed_before_child_creation() {
        let dir = create_temp_test_dir("spawn-file-actions");
        let path = dir.join("script");
        fs::write(&path, b"#!/bin/sh\nexit 0\n").expect("write script");
        let mut permissions = fs::metadata(&path).expect("script metadata").permissions();
        permissions.set_mode(0o755);
        fs::set_permissions(&path, permissions).expect("set script mode");

        let result = execute_prepared_spawn(
            path.to_str().expect("script path"),
            std::ptr::null_mut(),
            std::ptr::dangling(),
            std::ptr::null(),
            std::ptr::null(),
            std::ptr::null(),
            &[format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}")],
        );
        assert_eq!(result, Some(libc::EPERM));
        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn nonempty_execveat_at_empty_path_still_prepares_the_path() {
        assert_eq!(
            execveat_preparation_target("/workspace/script", libc::AT_EMPTY_PATH),
            ExecveatPreparationTarget::Path(0)
        );
        assert_eq!(
            execveat_preparation_target(
                "/workspace/script",
                libc::AT_EMPTY_PATH | libc::AT_SYMLINK_NOFOLLOW,
            ),
            ExecveatPreparationTarget::Path(libc::O_NOFOLLOW)
        );
        assert_eq!(
            execveat_preparation_target("", libc::AT_EMPTY_PATH),
            ExecveatPreparationTarget::Descriptor
        );
        assert_eq!(
            execveat_preparation_target("/workspace/script", 1 << 30),
            ExecveatPreparationTarget::PassThrough
        );
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn mutable_path_provenance_is_lexical_and_survives_parent_components() {
        assert!(path_has_mutable_provenance(
            "/workspace/../state/tool",
            libc::AT_FDCWD
        ));
        assert!(path_has_mutable_provenance(
            "/state/../workspace/tool",
            libc::AT_FDCWD
        ));
        assert!(path_has_mutable_provenance(
            "/workspace/../tmp/tool",
            libc::AT_FDCWD
        ));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn outside_alias_into_mutable_runtime_is_blocked_before_snapshot() {
        let dir = create_temp_test_dir("mutable-alias");
        let source = dir.join("source");
        let alias = dir.join("alias");
        fs::copy("/bin/true", &source).expect("copy native executable");
        symlink(&source, &alias).expect("create alias");

        assert!(path_is_mutable_native_exec(
            alias.to_str().expect("alias path")
        ));
        assert!(matches!(
            mutable_exec_preparation(alias.to_str().expect("alias path"), libc::AT_FDCWD, 0, &[]),
            MutableExecPreparation::Block
        ));

        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn writable_runtime_native_paths_are_not_trusted() {
        for path in ["/tmp", "/var/tmp", "/run", "/dev/shm"] {
            assert!(resolved_path_is_mutable_root(path));
            assert!(!path_is_trusted_immutable(Path::new(path)));
        }
        assert!(path_is_trusted_immutable(Path::new("/bin/true")));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn anonymous_elf_descriptor_is_untrusted_and_blocked() {
        // SAFETY: the name is a static NUL-terminated string and the flags are valid memfd flags.
        let fd = unsafe {
            libc::memfd_create(
                c"workcell-test-elf".as_ptr(),
                libc::MFD_ALLOW_SEALING | libc::MFD_CLOEXEC,
            )
        };
        // SAFETY: errno_location returns this thread's valid errno slot.
        let error = unsafe { *errno_location() };
        assert!(fd >= 0, "memfd_create failed: {error}");
        // SAFETY: fd is newly created and owned by this test.
        let mut anonymous = unsafe { File::from_raw_fd(fd) };
        let mut source = File::open("/bin/true").expect("open trusted ELF");
        std::io::copy(&mut source, &mut anonymous).expect("copy ELF");
        anonymous.seek(SeekFrom::Start(0)).expect("rewind ELF");

        assert!(fd_target_is_untrusted_exec(fd));
        if current_mode_blocks_mutable_native_exec() {
            assert!(file_descriptor_is_mutable_native_exec(fd));
            assert!(loader_arg_targets_mutable_native_exec(&proc_fd_path(fd)));
            assert!(matches!(
                mutable_fd_preparation(fd, &[]),
                MutableExecPreparation::Block
            ));
            assert!(path_is_mutable_native_exec(&proc_fd_path(fd)));
        }
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn trusted_runtime_elf_descriptor_is_pass_through() {
        let trusted = File::open("/bin/true").expect("open trusted ELF");
        assert!(!fd_target_is_untrusted_exec(trusted.as_raw_fd()));
        assert!(!file_descriptor_is_mutable_native_exec(trusted.as_raw_fd()));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn mutable_root_descriptor_is_not_trusted_after_path_resolution() {
        let dir = create_temp_test_dir("mutable-fd-root");
        let path = dir.join("native");
        fs::copy("/bin/true", &path).expect("copy native executable");
        let file = File::open(&path).expect("open native executable");

        assert!(resolved_path_is_mutable_root(
            &fs::canonicalize(&path)
                .expect("canonical path")
                .to_string_lossy()
        ));
        assert!(fd_target_is_untrusted_exec(file.as_raw_fd()));
        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn non_executable_elf_in_writable_directory_is_blocked_for_loader_use() {
        let dir = create_temp_test_dir("non-executable-elf");
        let path = dir.join("native");
        fs::copy("/bin/true", &path).expect("copy native executable");
        let mut permissions = fs::metadata(&path).expect("native metadata").permissions();
        permissions.set_mode(0o600);
        fs::set_permissions(&path, permissions).expect("remove execute permission");

        assert!(path_is_mutable_native_exec(
            path.to_str().expect("native path")
        ));
        assert!(loader_arg_targets_mutable_native_exec(
            path.to_str().expect("native path")
        ));
        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn deleted_elf_descriptor_is_untrusted() {
        let dir = create_temp_test_dir("deleted-elf");
        let path = dir.join("native");
        fs::copy("/bin/true", &path).expect("copy native executable");
        let file = File::open(&path).expect("open native executable");
        fs::remove_file(&path).expect("unlink native executable");
        symlink("/bin/true", &path).expect("replace path with trusted symlink");

        assert!(fd_target_is_untrusted_exec(file.as_raw_fd()));
        if current_mode_blocks_mutable_native_exec() {
            assert!(file_descriptor_is_mutable_native_exec(file.as_raw_fd()));
            assert!(matches!(
                mutable_fd_preparation(file.as_raw_fd(), &[]),
                MutableExecPreparation::Block
            ));
        }
        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[test]
    fn exec_input_limits_accept_exact_values_and_reject_over_limits() {
        let exact_string = CString::new(vec![b'x'; MAX_EXEC_STRING_BYTES]).expect("exact string");
        assert_eq!(
            bounded_c_string(exact_string.as_ptr())
                .expect("exact string accepted")
                .len(),
            MAX_EXEC_STRING_BYTES
        );
        let oversized_string =
            CString::new(vec![b'x'; MAX_EXEC_STRING_BYTES + 1]).expect("oversized string");
        assert_eq!(
            bounded_c_string(oversized_string.as_ptr()),
            Err(ExecInputTooLarge)
        );

        let aggregate_item =
            CString::new(vec![b'a'; MAX_EXEC_STRING_BYTES - 1]).expect("aggregate item");
        let exact_aggregate_items: Vec<*const c_char> = (0..16)
            .map(|_| aggregate_item.as_ptr())
            .chain([std::ptr::null()])
            .collect();
        assert_eq!(
            collect_cstring_array(exact_aggregate_items.as_ptr())
                .expect("exact aggregate accepted")
                .len(),
            16
        );
        let aggregate_over_limit: Vec<*const c_char> = (0..17)
            .map(|_| aggregate_item.as_ptr())
            .chain([std::ptr::null()])
            .collect();
        assert_eq!(
            collect_cstring_array(aggregate_over_limit.as_ptr()),
            Err(ExecInputTooLarge)
        );

        let element = CString::new("x").expect("element");
        let exact_elements: Vec<*const c_char> = (0..MAX_EXEC_ELEMENTS)
            .map(|_| element.as_ptr())
            .chain([std::ptr::null()])
            .collect();
        assert_eq!(
            collect_cstring_array(exact_elements.as_ptr())
                .expect("exact element count accepted")
                .len(),
            MAX_EXEC_ELEMENTS
        );
        let element_over_limit: Vec<*const c_char> = (0..=MAX_EXEC_ELEMENTS)
            .map(|_| element.as_ptr())
            .chain([std::ptr::null()])
            .collect();
        assert_eq!(
            collect_cstring_array(element_over_limit.as_ptr()),
            Err(ExecInputTooLarge)
        );
    }

    #[test]
    fn streaming_file_comparison_seeks_and_bounds_memory() {
        let dir = create_temp_test_dir("stream-compare");
        let left_path = dir.join("left");
        let right_path = dir.join("right");
        let mut bytes = vec![b'a'; 128 * 1024 + 17];
        bytes.extend_from_slice(b"tail");
        fs::write(&left_path, &bytes).expect("write left");
        fs::write(&right_path, &bytes).expect("write right");

        let mut left = File::open(&left_path).expect("open left");
        let mut right = File::open(&right_path).expect("open right");
        let mut prefix = [0u8; 1];
        left.read_exact(&mut prefix).expect("read left prefix");
        right.read_exact(&mut prefix).expect("read right prefix");
        assert!(compare_open_files(&mut left, &mut right));

        fs::write(&right_path, *b"b").expect("rewrite right");
        let mut right = File::open(&right_path).expect("reopen right");
        assert!(!compare_open_files(&mut left, &mut right));
        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }
}
