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
#[cfg(target_os = "linux")]
use std::os::unix::ffi::OsStrExt;
use std::os::unix::fs::MetadataExt;
use std::path::Path;
#[cfg(target_os = "linux")]
use std::path::PathBuf;
use std::sync::OnceLock;

mod gitpolicy;
use gitpolicy::{env_has_unsafe_git_override, should_block_reason};

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

// Every root a container process can write to and then execute from. The
// workspace and state binds are the obvious two; the rest are the writable
// kernel and runtime roots that exist in every image, so a target planted in
// one of them is no more trustworthy than one planted in the workspace.
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
// Bounds on the exec inputs this guard copies out of caller memory before it
// can classify them. Without these an attacker-sized argv/envp forces the guard
// to allocate without limit inside the interposed call. MAX_EXEC_ELEMENTS must
// stay equal to WORKCELL_MAX_EXEC_ELEMENTS in src/exec_variadic.c.
const MAX_EXEC_STRING_BYTES: usize = 128 * 1024;
const MAX_EXEC_AGGREGATE_BYTES: usize = 2 * 1024 * 1024;
const MAX_EXEC_ELEMENTS: usize = 65_536;
const MAX_EXEC_PATH_SEGMENT_BYTES: usize = MAX_EXEC_STRING_BYTES;
const MAX_EXEC_PATH_BYTES: usize = MAX_EXEC_AGGREGATE_BYTES;
const MAX_EXEC_PATH_SEGMENTS: usize = MAX_EXEC_ELEMENTS;
const DEFAULT_SEARCH_PATH: &str = "/bin:/usr/bin";
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

/// Paths the kernel resolves through a descriptor table rather than a
/// directory walk. Their target can be replaced between this classification and
/// the exec, and there is no pathname to pin, so they never count as trusted.
#[cfg(target_os = "linux")]
fn path_is_magic_exec_target(path: &str) -> bool {
    path == "/proc"
        || path.starts_with("/proc/")
        || path == "/dev/fd"
        || path.starts_with("/dev/fd/")
        || matches!(path, "/dev/stdin" | "/dev/stdout" | "/dev/stderr")
}

/// Answers whether this process can write the path with its effective
/// credentials, which is what decides whether it can replace an exec target.
///
/// A root runtime overrides ordinary permission checks, so `faccessat` answers
/// yes for every file and would make the whole filesystem mutable. For that
/// case the root-owned, not group/world-writable baseline is the answer
/// instead. Every other runtime goes through `faccessat` so an ACL grant is
/// part of the decision rather than invisible to it.
#[cfg(target_os = "linux")]
fn effective_access_can_write(path: &Path) -> bool {
    let Ok(metadata) = fs::metadata(path) else {
        return true;
    };
    // SAFETY: geteuid is a niladic syscall wrapper with no invalid inputs.
    let euid = unsafe { libc::geteuid() };
    if euid == 0 && metadata.uid() == 0 && metadata.mode() & (libc::S_IWGRP | libc::S_IWOTH) == 0 {
        return false;
    }
    let Ok(c_path) = CString::new(path.as_os_str().as_bytes()) else {
        return true;
    };
    // SAFETY: c_path is a live NUL-terminated CString valid for the call, and
    // faccessat only reads the path and this process's credentials.
    unsafe {
        libc::faccessat(
            libc::AT_FDCWD,
            c_path.as_ptr(),
            libc::W_OK,
            libc::AT_EACCESS,
        ) == 0
    }
}

/// A target is trusted only when neither it nor any directory on the way to it
/// can be replaced by this process: a regular root-owned file with no group or
/// world write bit, outside every mutable root, under trusted ancestors.
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

/// Walks from the directory to the root. A writable directory anywhere on that
/// walk lets this process rename the target out from under the kernel lookup
/// that follows, so the file's own mode is not enough.
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

/// Resolves `.` and `..` textually, without touching the filesystem. The
/// filesystem answer is what the kernel will use, but it is also what an
/// attacker can change; the lexical answer names the path the caller wrote, so
/// a target under a mutable root cannot be laundered through a symlink.
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

/// Rebuilds the absolute pathname the kernel will resolve, from a relative name
/// plus the directory it is relative to.
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

/// True when the name itself, or any directory leading to it, is one this
/// process can change before the kernel resolves it.
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

/// The lexical name lands in a mutable root, whatever it resolves to.
#[cfg(target_os = "linux")]
fn path_has_mutable_provenance(path: &str, dirfd: c_int) -> bool {
    resolved_path_is_mutable_root(&path_with_dirfd_base(path, dirfd).to_string_lossy())
}

/// The resolved target lands in a mutable root, whatever the name looked like.
#[cfg(target_os = "linux")]
fn path_resolves_to_mutable_root(path: &str, dirfd: c_int) -> bool {
    let Ok(resolved) = fs::canonicalize(path_with_dirfd_base(path, dirfd)) else {
        return false;
    };
    resolved_path_is_mutable_root(&resolved.to_string_lossy())
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

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct ExecInputTooLarge;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ExecSearchError {
    InputTooLarge,
    PermissionDenied,
    RelativeSearchPath,
    LookupFailed(c_int),
}

// The errors libc records and walks past while searching PATH. Any other error
// ends its search, so it must end the guard's search too.
const SEARCH_CONTINUES_ON: &[c_int] = &[
    libc::ENOENT,
    libc::ENOTDIR,
    libc::ESTALE,
    libc::ENODEV,
    libc::ETIMEDOUT,
];

fn bounded_c_string(path: *const c_char) -> Result<String, ExecInputTooLarge> {
    bounded_c_string_with_limit(path, MAX_EXEC_STRING_BYTES)
}

fn bounded_c_string_with_limit(
    path: *const c_char,
    limit: usize,
) -> Result<String, ExecInputTooLarge> {
    if path.is_null() {
        return Ok(String::new());
    }

    // SAFETY: path is supplied by the exec ABI. strnlen limits the read before
    // the bytes are copied into an owned string.
    let length = unsafe { libc::strnlen(path, limit + 1) };
    if length > limit {
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

// A NULL envp gives the child an empty environment, so it asks exactly the
// question the missing-guard classifier answers about an empty environment.
// The other classifiers read `effective_env_ptr`, which substitutes the
// caller's own environment for NULL and would therefore see a preload the
// child never gets; this check has to run on the raw pointer, before that
// substitution.
fn should_block_null_explicit_env(envp: *const *const c_char) -> bool {
    envp.is_null() && should_block_missing_guard_env(&[])
}

// libc consults only PATH from the caller's environment when it searches, so
// bound that one entry instead of copying the whole environment: an unrelated
// oversized variable must not turn a searched exec into E2BIG.
fn current_process_path_value() -> Result<Option<String>, ExecInputTooLarge> {
    const PREFIX: &str = "PATH=";
    // clearenv() leaves environ null on glibc, and a bare-name search is still
    // valid there: libc falls back to its default search path.
    // SAFETY: environ is libc-initialized; read in the calling thread with no concurrent setenv/putenv.
    let entries = unsafe { environ.cast::<*const c_char>() };
    if entries.is_null() {
        return Ok(None);
    }
    let mut index = 0usize;
    loop {
        // SAFETY: entries is a non-null, NUL-sentinel-terminated char**; index walks up to the sentinel below.
        let current = unsafe { *entries.add(index) };
        if current.is_null() {
            return Ok(None);
        }
        // The sentinel is read before the bound is applied, so an environment of
        // exactly MAX_EXEC_ELEMENTS entries is accepted here as it is by
        // collect_cstring_array.
        if index == MAX_EXEC_ELEMENTS {
            return Err(ExecInputTooLarge);
        }
        // SAFETY: current is a valid NUL-terminated C string; strncmp reads at most PREFIX bytes of it.
        if unsafe { libc::strncmp(current, c"PATH=".as_ptr(), PREFIX.len()) } == 0 {
            // A PATH value gets the PATH budget, not the per-string one:
            // validate_path_value below is what enforces MAX_EXEC_PATH_BYTES and
            // the per-segment limit, and it can only do that if the whole entry
            // was read.
            return bounded_c_string_with_limit(current, PREFIX.len() + MAX_EXEC_PATH_BYTES)
                .map(|entry| Some(entry[PREFIX.len()..].to_owned()));
        }
        index += 1;
    }
}

fn path_from_env_entries(env_entries: &[String]) -> Option<String> {
    env_entries
        .iter()
        .find_map(|entry| entry.strip_prefix("PATH=").map(ToOwned::to_owned))
        .or_else(|| env::var("PATH").ok())
        // POSIX libc falls back to a system default search path when PATH is
        // absent, so the guard must search the same places libc would.
        .or_else(|| Some(DEFAULT_SEARCH_PATH.to_owned()))
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
    let mut relative_segment = false;
    for segment in path_value.split(':') {
        // An empty or otherwise relative PATH entry names a working directory
        // the guard cannot pin: it can change between this resolution and the
        // exec (posix_spawn chdir file actions do exactly that, and glibc runs
        // its own search in the child after applying them), and the caller can
        // write to it. The guard cannot classify a target it cannot name, so a
        // relative entry never yields a candidate.
        if !segment.starts_with('/') {
            relative_segment = true;
            continue;
        }
        let candidate = format!("{segment}/{command}");
        let Ok(cstring) = CString::new(candidate.as_str()) else {
            continue;
        };

        // SAFETY: cstring is a live NUL-terminated CString valid for the call;
        // faccessat only reads the path and this process's credentials.
        if unsafe {
            libc::faccessat(
                libc::AT_FDCWD,
                cstring.as_ptr(),
                libc::X_OK,
                libc::AT_EACCESS,
            )
        } != 0
        {
            // Read the error from the lookup that failed: a candidate under an
            // unsearchable directory answers EACCES to an existence probe too,
            // so probing for existence would read it as absent.
            // SAFETY: errno_location() returns the current thread's valid errno slot.
            let lookup_errno = unsafe { *errno_location() };
            if lookup_errno == libc::EACCES {
                // libc remembers EACCES and keeps searching, then reports it
                // when no later entry holds the command.
                permission_denied = true;
            } else if !SEARCH_CONTINUES_ON.contains(&lookup_errno) {
                // libc stops here and reports this error, so the guard must not
                // walk on and classify a candidate libc will never reach.
                return Err(ExecSearchError::LookupFailed(lookup_errno));
            }
            continue;
        }
        // X_OK also succeeds for a searchable directory, and the kernel refuses
        // to execute anything that is not a regular file, reporting EACCES.
        // libc keeps searching on that too, so a directory must not end the
        // guard's search either -- otherwise the guard classifies the directory
        // while libc goes on to execute a later, unclassified entry.
        if !fs::metadata(&candidate).is_ok_and(|metadata| metadata.is_file()) {
            permission_denied = true;
            continue;
        }

        // Return the pathname libc would exec, not what it resolves to today.
        // The classifiers decide on provenance, and a resolved name hides the
        // mutable component the caller actually wrote.
        return Ok(Some(candidate));
    }

    if permission_denied {
        Err(ExecSearchError::PermissionDenied)
    } else if relative_segment {
        // No absolute entry held the command, so libc would fall back to the
        // relative entry the guard skipped. Refuse rather than let it run
        // unclassified.
        Err(ExecSearchError::RelativeSearchPath)
    } else {
        Ok(None)
    }
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

fn token_is_loader_environment_assignment(token: &str) -> bool {
    let Some((name, _value)) = token.split_once('=') else {
        return false;
    };
    token_is_loader_environment_name(name)
}

fn token_is_loader_environment_name(token: &str) -> bool {
    matches!(
        token,
        "LD_PRELOAD" | "LD_AUDIT" | "LD_LIBRARY_PATH" | "LD_TRACE_LOADED_OBJECTS"
    )
}

/// The short `-uNAME` form, which carries its variable name in the same token.
/// The long forms are matched by prefix in the scanner, since env accepts
/// abbreviations of them.
fn token_is_loader_environment_unset_option(token: &str) -> bool {
    token
        .strip_prefix("-u")
        .filter(|suffix| !suffix.is_empty())
        .map(|name| name.strip_prefix('=').unwrap_or(name))
        .is_some_and(token_is_loader_environment_name)
}

/// env(1) accepts any unambiguous abbreviation of a long option, so a long
/// option is matched by prefix rather than by its full name.  An abbreviation
/// that is ambiguous makes env exit without running anything, so treating
/// every prefix as the option it names never lets a target through.
fn env_long_option_name(token: &str) -> Option<&str> {
    let name = token.strip_prefix("--")?;
    let name = name.split('=').next().unwrap_or(name);
    (!name.is_empty()).then_some(name)
}

/// env(1) short options cluster into one token, so a bare `-S` match is not
/// enough: `-iS` reaches both of the options that defeat the guard.  `-S`
/// re-splits the rest of the line into tokens this scan cannot follow, and
/// `-i` drops the guard's own preload.  Scanning stops at an option that
/// consumes the rest of the token as its value.
fn token_clusters_loader_environment_control(token: &str) -> bool {
    let Some(cluster) = token.strip_prefix('-') else {
        return false;
    };
    if cluster.starts_with('-') {
        return false;
    }
    cluster
        .chars()
        .take_while(|option| !matches!(option, 'u' | 'C'))
        .any(|option| matches!(option, 'S' | 'i'))
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
        // env can rewrite the loader environment before the target runs, so the
        // options that add, drop or replace it end the scan with a refusal.
        if let Some(name) = env_long_option_name(&token) {
            if "split-string".starts_with(name) || "ignore-environment".starts_with(name) {
                return true;
            }
            if "unset".starts_with(name) {
                let unset = token
                    .split_once('=')
                    .map(|(_, value)| value.to_owned())
                    .or_else(|| next_shebang_token(&mut scan));
                if unset.is_some_and(|name| token_is_loader_environment_name(&name)) {
                    return true;
                }
                continue;
            }
        }
        if token == "-" || token_clusters_loader_environment_control(&token) {
            return true;
        }
        if token == "-u" {
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
            // Fail closed: an unclassifiable interpreter is treated as blocked.
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

/// Decides whether a native exec target is one this process could have planted
/// or could still replace.
///
/// The former test was "does it resolve into a mutable root": a target could be
/// reached through a writable directory anywhere else on the filesystem and
/// still pass. This asks the two questions that matter instead. Is the target
/// itself trusted-immutable, and is the pathname the caller wrote free of any
/// component this process can change before the kernel resolves it? Either
/// answer being no is a block, so a target that does not exist yet but whose
/// directory is writable is refused rather than admitted.
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
        // The name does not resolve. Only a regular file can be executed, so a
        // name that is not one is left to the kernel's own errno.
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

fn env_has_unsafe_loader_override(env_entries: &[String]) -> bool {
    env_entries.iter().any(|entry| {
        env_entry_value(entry, "LD_AUDIT").is_some_and(|value| !value.is_empty())
            || env_entry_value(entry, "LD_LIBRARY_PATH").is_some_and(|value| !value.is_empty())
            // The loader enters trace mode on the presence of this variable,
            // whatever its value, so an empty one is an override too.
            || env_entry_value(entry, "LD_TRACE_LOADED_OBJECTS").is_some()
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
    path_is_workcell_native_launcher(path) && env_has_unsafe_loader_override(env_entries)
}

fn should_block_workcell_launcher_fd_loader_env(fd: c_int, env_entries: &[String]) -> bool {
    fd_matches_workcell_native_launcher(fd) && env_has_unsafe_loader_override(env_entries)
}

/// True when a loader environment override would change how the target runs.
///
/// Every regular file qualifies, whatever its contents. An ELF is loaded by
/// ld.so directly and a shebang loads its interpreter the same way. A regular
/// file that is neither still counts, because execve returns ENOEXEC for it
/// and glibc's execvp and execvpe answer that by launching /bin/sh with the
/// caller's environment from inside libc, which does not re-enter this guard.
/// Nothing else is executable at all, so a target that is not a regular file
/// cannot reach the loader. A target that cannot be stat'ed stays
/// unclassified and is treated as sensitive.
#[cfg(target_os = "linux")]
fn file_descriptor_is_loader_sensitive(fd: c_int) -> bool {
    duplicate_fd_file(fd)
        .and_then(|file| file.metadata().ok())
        .is_none_or(|metadata| (metadata.mode() & file_type_bits()) == regular_file_mode())
}

#[cfg(target_os = "linux")]
fn path_is_loader_sensitive(path: &str) -> bool {
    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        return file_descriptor_is_loader_sensitive(proc_fd);
    }
    match fs::metadata(path) {
        Ok(metadata) => (metadata.mode() & file_type_bits()) == regular_file_mode(),
        // A target the guard cannot stat stays unclassified.
        Err(_) => true,
    }
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

// The child environment must carry the guard and nothing else in LD_PRELOAD.
// The loader honours every entry it is given, so a second one loads an
// attacker-chosen library alongside the guard: "contains the guard" is not the
// same question as "is exactly the guard", and only the second one is safe to
// answer yes to.
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

// No exemption: an approved launcher would have to be recognised by pathname,
// and the pathname checked is not the file the kernel later runs. The launcher
// restores the preload before it execs, so there is nothing left for an
// exemption to cover.
#[cfg(target_os = "linux")]
fn should_block_missing_guard_env(env_entries: &[String]) -> bool {
    current_mode_blocks_mutable_native_exec() && !env_has_approved_guard_preload(env_entries)
}

#[cfg(not(target_os = "linux"))]
fn should_block_missing_guard_env(_env_entries: &[String]) -> bool {
    false
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
    // A loader argument is a name the loader looks up later, in its own
    // process, after this guard has returned. A relative name resolves against
    // a working directory this process can change, and a magic descriptor path
    // resolves against a table it can rewrite, so neither can be approved on
    // the strength of what it names now.
    #[cfg(target_os = "linux")]
    if current_mode_blocks_mutable_native_exec()
        && (!Path::new(target).is_absolute() || path_is_magic_exec_target(target))
    {
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
        // The loader reads this argument as a library path or a target, and
        // resolves it after the exec. A mutable name is a lookup race whatever
        // it currently points at.
        return true;
    }
    path_is_mutable_native_exec(target)
}

/// Walks a dynamic loader argument vector the way the loader does, to find the
/// exec target: everything past that target belongs to the target's own argv.
///
/// The options that name libraries are refused outright rather than parsed.
/// Deciding whether such a value stays out of a mutable root means reproducing
/// the loader's own reading of it: it accepts colons, semicolons and
/// whitespace as separators, and expands `$ORIGIN`, `$LIB` and `$PLATFORM`
/// against the target before use. A guard that reimplements that is a guard
/// that is wrong in some corner of it, so an explicit loader invocation that
/// sets a library path is refused on the strict profile instead. Programs are
/// normally executed directly rather than through ld.so, and a loader
/// environment reaching a target is covered separately by the environment
/// check.
fn loader_args_target_mutable_native_exec(args: &[String]) -> bool {
    let mut index = 1usize;
    while index < args.len() {
        let argument = &args[index];
        // Only an option carries an inline value. The exec target is a
        // pathname, and a pathname may contain an equals sign, so splitting
        // every argument would check a truncated prefix of the real target.
        let (option, inline_value) = if argument.starts_with('-') {
            argument
                .split_once('=')
                .map_or((argument.as_str(), None), |(option, value)| {
                    (option, Some(value))
                })
        } else {
            (argument.as_str(), None)
        };

        match option {
            "--preload" | "--audit" | "--library-path" => {
                return current_mode_blocks_mutable_native_exec();
            }
            // Options that consume a value which is not a library search list.
            // Missing one here would read its value as the exec target.
            "--argv0"
            | "--inhibit-rpath"
            | "--glibc-hwcaps-mask"
            | "--glibc-hwcaps-prepend"
            | "--hwcap-mask" => {
                if inline_value.is_none() {
                    index += 1;
                }
            }
            // Options that consume no value.
            "--list" | "--list-tunables" | "--list-diagnostics" | "--verify"
            | "--inhibit-cache" | "--help" | "--version" => {}
            "--" => {
                return args
                    .get(index + 1)
                    .is_some_and(|target| loader_arg_targets_mutable_native_exec(target));
            }
            // An option this parser does not know may or may not consume the
            // next argument, so the exec target cannot be located. Refuse
            // rather than guess and skip past it.
            option if option.starts_with('-') => return true,
            target => {
                return loader_arg_targets_mutable_native_exec(target);
            }
        }
        index += 1;
    }
    false
}

fn loader_targets_mutable_native_exec(path: &str, args: &[String]) -> bool {
    if !path_points_to_dynamic_loader(path) {
        return false;
    }
    // A loader named through /proc or /dev/fd can be swapped before it performs
    // its own target lookup, and a loader pathname cannot be pinned to a
    // descriptor without changing the loader ABI.
    #[cfg(target_os = "linux")]
    if current_mode_blocks_mutable_native_exec() && path_is_magic_exec_target(path) {
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

fn should_block_mutable_native_exec(path: &str, args: &[String]) -> bool {
    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        return file_descriptor_is_mutable_native_exec(proc_fd)
            || loader_fd_targets_mutable_native_exec(proc_fd, args);
    }

    path_is_mutable_native_exec(path) || loader_targets_mutable_native_exec(path, args)
}

// execvp, execvpe and posix_spawnp all search PATH from the caller's own
// environment rather than the envp handed to the child, so the guard reads the
// same source libc will. A name containing a slash is not searched at all, so
// nothing is read from the caller environment unless a search happens.
fn resolve_exec_search_target(file: &str) -> Result<Option<String>, ExecSearchError> {
    if file.contains('/') {
        return Ok(Some(file.to_owned()));
    }
    let path_value = current_process_path_value()
        .map_err(|_| ExecSearchError::InputTooLarge)?
        // POSIX libc falls back to a system default search path when PATH is
        // absent, so the guard must search the same places libc would.
        .unwrap_or_else(|| DEFAULT_SEARCH_PATH.to_owned());
    resolve_command_via_path_value(file, Some(&path_value))
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

fn report_native_loader_env_block() {
    report(NATIVE_LOADER_ENV_BLOCK_MESSAGE);
}

fn report_missing_guard_env_block() {
    report(MISSING_GUARD_ENV_BLOCK_MESSAGE);
}

fn report_workcell_launcher_loader_env_block() {
    report(WORKCELL_LAUNCHER_LOADER_ENV_BLOCK_MESSAGE);
}

fn report_exec_input_block() {
    report_with_errno("Workcell rejected oversized exec arguments.\n", libc::E2BIG);
}

fn report_relative_search_path_block() {
    report_with_errno(
        "Workcell rejected a command found only through a relative PATH entry.\n",
        libc::EACCES,
    );
}

// Fail closed at every interposed entry point: exec inputs the guard refuses to
// copy are also inputs it cannot classify, so the call is rejected rather than
// forwarded unchecked. `$failure` is the entry point's own error convention
// (-1 for the exec family, an errno value for posix_spawn/posix_spawnp).
macro_rules! bounded_exec_input {
    ($parse:expr, $failure:expr) => {
        match $parse {
            Ok(value) => value,
            Err(ExecInputTooLarge) => {
                report_exec_input_block();
                return $failure;
            }
        }
    };
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

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_execve(
    path: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let path_string = bounded_exec_input!(bounded_c_string(path), -1);
    let args = bounded_exec_input!(collect_cstring_array(argv), -1);
    let env_entries = bounded_exec_input!(collect_cstring_array(effective_env_ptr(envp)), -1);

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

    // Last of the refusals: each one above names a more specific reason, and
    // this is the fail-closed default for a child that would run unguarded.
    if should_block_null_explicit_env(envp) || should_block_missing_guard_env(&env_entries) {
        report_missing_guard_env_block();
        return -1;
    }

    // SAFETY: forwards the caller's original, unmodified execve arguments to the real libc execve resolved via RTLD_NEXT.
    unsafe { execve_fn()(path, argv, envp) }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_execv(path: *const c_char, argv: *const *const c_char) -> c_int {
    let path_string = bounded_exec_input!(bounded_c_string(path), -1);
    let args = bounded_exec_input!(collect_cstring_array(argv), -1);
    let env_entries = bounded_exec_input!(current_process_env_entries(), -1);

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

    // Last of the refusals: each one above names a more specific reason, and
    // this is the fail-closed default for a child that would run unguarded.
    if should_block_missing_guard_env(&env_entries) {
        report_missing_guard_env_block();
        return -1;
    }

    // SAFETY: forwards the caller's original, unmodified execv arguments to the real libc execv resolved via RTLD_NEXT.
    unsafe { execv_fn()(path, argv) }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_execvp(file: *const c_char, argv: *const *const c_char) -> c_int {
    let file_string = bounded_exec_input!(bounded_c_string(file), -1);
    let args = bounded_exec_input!(collect_cstring_array(argv), -1);
    let env_entries = bounded_exec_input!(current_process_env_entries(), -1);
    let effective_path = match resolve_exec_search_target(&file_string) {
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
        Err(ExecSearchError::RelativeSearchPath) => {
            report_relative_search_path_block();
            return -1;
        }
        Err(ExecSearchError::LookupFailed(errno)) => {
            set_errno(errno);
            return -1;
        }
    };

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

    // Last of the refusals: each one above names a more specific reason, and
    // this is the fail-closed default for a child that would run unguarded.
    if should_block_missing_guard_env(&env_entries) {
        report_missing_guard_env_block();
        return -1;
    }

    // SAFETY: forwards the caller's original, unmodified execvp arguments to the real libc execvp resolved via RTLD_NEXT.
    unsafe { execvp_fn()(file, argv) }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_execvpe(
    file: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let file_string = bounded_exec_input!(bounded_c_string(file), -1);
    let args = bounded_exec_input!(collect_cstring_array(argv), -1);
    let env_entries = bounded_exec_input!(collect_cstring_array(effective_env_ptr(envp)), -1);
    let effective_path = match resolve_exec_search_target(&file_string) {
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
        Err(ExecSearchError::RelativeSearchPath) => {
            report_relative_search_path_block();
            return -1;
        }
        Err(ExecSearchError::LookupFailed(errno)) => {
            set_errno(errno);
            return -1;
        }
    };

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

    // Last of the refusals: each one above names a more specific reason, and
    // this is the fail-closed default for a child that would run unguarded.
    if should_block_null_explicit_env(envp) || should_block_missing_guard_env(&env_entries) {
        report_missing_guard_env_block();
        return -1;
    }

    // SAFETY: forwards the caller's original, unmodified execvpe arguments to the real libc execvpe resolved via RTLD_NEXT.
    unsafe { execvpe_fn()(file, argv, envp) }
}

#[unsafe(no_mangle)]
unsafe extern "C" fn guarded_execveat(
    dirfd: c_int,
    pathname: *const c_char,
    argv: *const *const c_char,
    envp: *const *const c_char,
    flags: c_int,
) -> c_int {
    let pathname_string = bounded_exec_input!(bounded_c_string(pathname), -1);
    let args = bounded_exec_input!(collect_cstring_array(argv), -1);
    let env_entries = bounded_exec_input!(collect_cstring_array(effective_env_ptr(envp)), -1);
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
        native_loader_env_blocked,
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
            should_block_loader_env_for_fd(dirfd, &env_entries),
        )
    } else {
        let mut protected_target = ProtectedRuntime::None;
        let mut mutable_native_target = false;
        let mut mutable_shebang_target = false;
        // A relative pathname resolves against dirfd, not the working
        // directory, so loader sensitivity is read from the descriptor the
        // kernel will execute. When that descriptor cannot be opened the
        // target stays unclassified, so refuse it whenever the environment
        // carries a loader override at all.
        let mut native_loader_env_blocked = current_mode_blocks_mutable_native_exec()
            && env_has_unsafe_loader_override(&env_entries);
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
                    native_loader_env_blocked =
                        should_block_loader_env_for_fd(candidate_fd, &env_entries);
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
            native_loader_env_blocked,
        )
    };

    if should_block_protected_runtime_kind(protected_target) || mutable_shebang_protected_target {
        report_protected_runtime_block();
        return -1;
    }
    let launcher_loader_env_blocked =
        if (flags & AT_EMPTY_PATH_FLAG) != 0 && pathname_string.is_empty() {
            should_block_workcell_launcher_fd_loader_env(dirfd, &env_entries)
        } else {
            (native_launcher_target && env_has_unsafe_loader_override(&env_entries))
                || should_block_workcell_launcher_loader_env(&pathname_string, &env_entries)
        };
    if launcher_loader_env_blocked {
        report_workcell_launcher_loader_env_block();
        return -1;
    }
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

    // Last of the refusals: each one above names a more specific reason, and
    // this is the fail-closed default for a child that would run unguarded.
    if should_block_null_explicit_env(envp) || should_block_missing_guard_env(&env_entries) {
        report_missing_guard_env_block();
        return -1;
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
    let args = bounded_exec_input!(collect_cstring_array(argv), -1);
    let env_entries = bounded_exec_input!(collect_cstring_array(effective_env_ptr(envp)), -1);

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

    // Last of the refusals: each one above names a more specific reason, and
    // this is the fail-closed default for a child that would run unguarded.
    if should_block_null_explicit_env(envp) || should_block_missing_guard_env(&env_entries) {
        report_missing_guard_env_block();
        return -1;
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
    let path_string = bounded_exec_input!(bounded_c_string(path), libc::E2BIG);
    let args = bounded_exec_input!(collect_cstring_array(argv), libc::E2BIG);
    let env_entries =
        bounded_exec_input!(collect_cstring_array(effective_env_ptr(envp)), libc::E2BIG);

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

    // Last of the refusals: each one above names a more specific reason, and
    // this is the fail-closed default for a child that would run unguarded.
    if should_block_null_explicit_env(envp) || should_block_missing_guard_env(&env_entries) {
        report_missing_guard_env_block();
        return libc::EPERM;
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
    let file_string = bounded_exec_input!(bounded_c_string(file), libc::E2BIG);
    let args = bounded_exec_input!(collect_cstring_array(argv), libc::E2BIG);
    let env_entries =
        bounded_exec_input!(collect_cstring_array(effective_env_ptr(envp)), libc::E2BIG);
    let effective_path = match resolve_exec_search_target(&file_string) {
        Ok(Some(path)) => path,
        Ok(None) => return libc::ENOENT,
        Err(ExecSearchError::InputTooLarge) => {
            report_exec_input_block();
            return libc::E2BIG;
        }
        Err(ExecSearchError::PermissionDenied) => return libc::EACCES,
        Err(ExecSearchError::RelativeSearchPath) => {
            report_relative_search_path_block();
            return libc::EACCES;
        }
        Err(ExecSearchError::LookupFailed(errno)) => return errno,
    };

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

    // Last of the refusals: each one above names a more specific reason, and
    // this is the fail-closed default for a child that would run unguarded.
    if should_block_null_explicit_env(envp) || should_block_missing_guard_env(&env_entries) {
        report_missing_guard_env_block();
        return libc::EPERM;
    }

    // SAFETY: forwards the caller's original, unmodified posix_spawnp arguments to the real libc posix_spawnp resolved via RTLD_NEXT.
    unsafe { posix_spawnp_fn()(pid, file, file_actions, attrp, argv, envp) }
}

// glibc resolves execl/execlp/execle internally, so interposing execv/execvp/
// execve alone leaves the variadic forms outside the guard. Rust cannot express
// a C variadic definition, so each name is a bare jump into the C adapter in
// src/exec_variadic.c, which flattens the argument list and re-enters the guard
// through the guarded_* implementations above.
#[cfg(all(target_os = "linux", target_arch = "x86_64"))]
macro_rules! define_variadic_exec_trampoline {
    ($name:ident, $target:ident) => {
        #[unsafe(naked)]
        #[unsafe(no_mangle)]
        pub unsafe extern "C" fn $name() {
            core::arch::naked_asm!(concat!("jmp ", stringify!($target)));
        }
    };
}

#[cfg(all(target_os = "linux", target_arch = "aarch64"))]
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
    use std::os::unix::fs::{PermissionsExt, symlink};
    #[cfg(target_os = "linux")]
    use std::os::unix::io::AsRawFd;
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
    fn loader_env_override_detection_allows_only_the_approved_preload() {
        assert!(env_has_unsafe_loader_override(&[
            "LD_PRELOAD=/workspace/preload.so".to_string()
        ]));
        assert!(env_has_unsafe_loader_override(&[
            "LD_AUDIT=/workspace/audit.so".to_string()
        ]));
        assert!(env_has_unsafe_loader_override(&[
            "LD_TRACE_LOADED_OBJECTS=1".to_string()
        ]));
        // The loader enters trace mode on presence, not on value.
        assert!(env_has_unsafe_loader_override(&[
            "LD_TRACE_LOADED_OBJECTS=".to_string()
        ]));
        assert!(!env_has_unsafe_loader_override(&[format!(
            "LD_PRELOAD={ALLOWED_LD_PRELOAD}"
        )]));
        assert!(!env_has_unsafe_loader_override(
            &["LD_PRELOAD=".to_string()]
        ));
        assert!(env_has_unsafe_loader_override(&[
            format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}"),
            "LD_PRELOAD=/workspace/preload.so".to_string()
        ]));
        assert!(env_has_unsafe_loader_override(&[
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

    /// Holds the variadic entry points to the guard.
    ///
    /// `execl`/`execlp`/`execle` are the naked trampolines defined above, and
    /// `build.rs` links the C adapter into test binaries as well as the preload
    /// shared object, so these calls take the same route a preloaded child
    /// takes. The target is absolute and nonexistent: the policy rejects it on
    /// the `git` basename before any exec, while a regression that drops a
    /// trampoline, its adapter, or an architecture arm falls through to libc,
    /// which cannot exec the path and reports `ENOENT` instead of `EPERM`.
    #[cfg(target_os = "linux")]
    #[test]
    fn variadic_exec_entry_points_stay_behind_the_guard() {
        const TARGET: &[u8] = b"/nonexistent/workcell-variadic-probe/git\0";
        const ARG0: &[u8] = b"git\0";
        const FLAG: &[u8] = b"-c\0";
        const SPEC: &[u8] = b"core.pager=sh -c id\0";

        let path = TARGET.as_ptr().cast::<c_char>();
        let arg0 = ARG0.as_ptr().cast::<c_char>();
        let flag = FLAG.as_ptr().cast::<c_char>();
        let spec = SPEC.as_ptr().cast::<c_char>();
        let end = std::ptr::null::<c_char>();
        let envp: [*const c_char; 1] = [std::ptr::null()];

        fn last_errno() -> c_int {
            std::io::Error::last_os_error()
                .raw_os_error()
                .expect("errno after a failed exec")
        }

        // SAFETY: every pointer is a NUL-terminated literal that outlives the
        // call, and the argument list carries the NULL sentinel execl requires.
        let execl_result = unsafe { libc::execl(path, arg0, flag, spec, end) };
        let execl_errno = last_errno();
        // SAFETY: as above; the path contains a slash, so execlp performs no
        // PATH search and cannot reach a different binary on a regression.
        let execlp_result = unsafe { libc::execlp(path, arg0, flag, spec, end) };
        let execlp_errno = last_errno();
        // SAFETY: as above, plus the environment pointer execle reads after the
        // sentinel, which is an empty NULL-terminated array valid for the call.
        let execle_result = unsafe { libc::execle(path, arg0, flag, spec, end, envp.as_ptr()) };
        let execle_errno = last_errno();

        for (name, result, errno) in [
            ("execl", execl_result, execl_errno),
            ("execlp", execlp_result, execlp_errno),
            ("execle", execle_result, execle_errno),
        ] {
            assert_eq!(result, -1, "{name} must refuse the blocked target");
            assert_eq!(errno, libc::EPERM, "{name} must be refused by the guard");
        }
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

        // An oversized PATH is a search failure the caller can distinguish, not
        // a silent fall-through to an unclassified target.
        assert_eq!(
            resolve_command_via_path_value("true", Some(&over_segments)),
            Err(ExecSearchError::InputTooLarge)
        );

        // A PATH value is read against the PATH budget, not the per-string one,
        // so validate_path_value is what decides -- a value between the two
        // limits must reach it rather than being refused by the reader.
        let between_limits = CString::new(vec![b'/'; MAX_EXEC_STRING_BYTES + 1])
            .expect("path value between the string and PATH limits");
        assert_eq!(
            bounded_c_string(between_limits.as_ptr()),
            Err(ExecInputTooLarge)
        );
        assert_eq!(
            bounded_c_string_with_limit(between_limits.as_ptr(), MAX_EXEC_PATH_BYTES)
                .expect("PATH budget accepts it")
                .len(),
            MAX_EXEC_STRING_BYTES + 1
        );
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn guard_environment_requires_the_exact_preload() {
        assert!(env_has_approved_guard_preload(&[format!(
            "LD_PRELOAD={ALLOWED_LD_PRELOAD}"
        )]));
        assert!(!env_has_approved_guard_preload(&[]));
        assert!(!env_has_approved_guard_preload(
            &["LD_PRELOAD=".to_string()]
        ));
        assert!(!env_has_approved_guard_preload(&[
            "LD_PRELOAD=/workspace/guard.so".to_string()
        ]));
        // The loader honours every LD_PRELOAD entry it is given, so an extra
        // one alongside the approved guard is still an attacker-chosen library.
        assert!(!env_has_approved_guard_preload(&[
            "LD_PRELOAD=".to_string(),
            format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}"),
        ]));
        assert!(!env_has_approved_guard_preload(&[
            format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}"),
            format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}"),
        ]));
    }

    #[test]
    fn null_child_environment_is_treated_as_a_missing_guard_preload() {
        let entry = CString::new(format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}")).expect("entry");
        let envp: [*const c_char; 2] = [entry.as_ptr(), std::ptr::null()];

        assert!(!should_block_null_explicit_env(envp.as_ptr()));
        // A NULL envp hands the child an empty environment, which is exactly
        // the environment the missing-guard classifier refuses.
        assert_eq!(
            should_block_null_explicit_env(std::ptr::null()),
            should_block_missing_guard_env(&[])
        );
        // It cannot be asked of the classifiers, because this substitution
        // would report the caller's own environment as the child's.
        assert!(!effective_env_ptr(std::ptr::null::<*const c_char>()).is_null());
    }

    #[test]
    fn path_search_walks_past_a_directory_that_shadows_the_command() {
        let dir = create_temp_test_dir("path-search");
        let shadow = dir.join("shadow");
        let real = dir.join("real");
        fs::create_dir(&shadow).expect("shadow dir");
        fs::create_dir(&real).expect("real dir");
        // A searchable directory named like the command answers access(X_OK),
        // but libc keeps searching past it, so the guard must too.
        fs::create_dir(shadow.join("probe")).expect("shadow probe dir");
        let target = real.join("probe");
        fs::write(&target, "#!/bin/sh\nexit 0\n").expect("real probe");
        fs::set_permissions(&target, fs::Permissions::from_mode(0o755)).expect("probe mode");

        let path_value = format!("{}:{}", shadow.display(), real.display());
        // The search reports the pathname libc would exec, not what it resolves
        // to: the classifiers decide on the provenance of the name the caller
        // wrote, and canonicalising it here would hide a mutable component.
        assert_eq!(
            resolve_command_via_path_value("probe", Some(&path_value)),
            Ok(Some(target.to_string_lossy().into_owned()))
        );

        // With only the directory on PATH the search reports the same
        // permission failure libc reports, never the directory itself.
        assert_eq!(
            resolve_command_via_path_value("probe", Some(&shadow.display().to_string())),
            Err(ExecSearchError::PermissionDenied)
        );
        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[test]
    fn path_search_reports_permission_denied_like_libc() {
        let dir = create_temp_test_dir("path-eacces");
        let plain = dir.join("plain");
        fs::create_dir(&plain).expect("plain dir");
        let unreadable = dir.join("unreadable");
        fs::create_dir(&unreadable).expect("unreadable dir");

        // A present but non-executable file: libc gets EACCES from execve,
        // remembers it, and reports it when nothing later matches.
        fs::write(plain.join("probe"), "").expect("plain probe");
        assert_eq!(
            resolve_command_via_path_value("probe", Some(&plain.display().to_string())),
            Err(ExecSearchError::PermissionDenied)
        );

        // A candidate under a directory the caller cannot search answers EACCES
        // to an existence probe as well, so it must be read from the failed
        // executable lookup rather than mistaken for an absent file.
        fs::write(unreadable.join("probe"), "").expect("unreadable probe");
        fs::set_permissions(&unreadable, fs::Permissions::from_mode(0o600)).expect("dir mode");
        // SAFETY: geteuid takes no arguments and cannot fail.
        if unsafe { libc::geteuid() } != 0 {
            assert_eq!(
                resolve_command_via_path_value("probe", Some(&unreadable.display().to_string())),
                Err(ExecSearchError::PermissionDenied)
            );
        }
        fs::set_permissions(&unreadable, fs::Permissions::from_mode(0o700)).expect("restore mode");
        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[test]
    fn path_search_stops_where_libc_stops() {
        let dir = create_temp_test_dir("path-eloop");
        let looped = dir.join("looped");
        fs::create_dir(&looped).expect("looped dir");
        symlink("probe", looped.join("probe")).expect("self-referential symlink");

        // libc reports ELOOP and stops; walking on would let the guard classify
        // a later candidate that libc never reaches.
        assert_eq!(
            resolve_command_via_path_value("probe", Some(&looped.display().to_string())),
            Err(ExecSearchError::LookupFailed(libc::ELOOP))
        );
        let with_later_entry = format!("{}:/usr/bin", looped.display());
        assert_eq!(
            resolve_command_via_path_value("probe", Some(&with_later_entry)),
            Err(ExecSearchError::LookupFailed(libc::ELOOP))
        );
        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[test]
    fn relative_path_entries_never_resolve_a_command() {
        let dir = create_temp_test_dir("relative-path");
        let target = dir.join("probe");
        fs::write(&target, "#!/bin/sh\nexit 0\n").expect("probe");
        fs::set_permissions(&target, fs::Permissions::from_mode(0o755)).expect("probe mode");
        let absolute = dir.display().to_string();

        // An absolute entry still resolves, so a trailing or leading empty
        // segment does not break an ordinary PATH.
        for path_value in [
            format!(":{absolute}"),
            format!("{absolute}:"),
            absolute.clone(),
        ] {
            assert!(
                matches!(
                    resolve_command_via_path_value("probe", Some(&path_value)),
                    Ok(Some(_))
                ),
                "{path_value} must still resolve the absolute entry"
            );
        }

        // A command reachable only through a relative entry is refused rather
        // than resolved against a working directory the guard cannot pin.
        for path_value in ["", ".", ":", "relative/bin"] {
            assert_eq!(
                resolve_command_via_path_value("probe", Some(path_value)),
                Err(ExecSearchError::RelativeSearchPath),
                "{path_value} must not resolve a command"
            );
        }
        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[test]
    fn env_interpreter_tokens_name_loader_environment_controls() {
        assert!(token_is_loader_environment_assignment(
            "LD_PRELOAD=/tmp/x.so"
        ));
        assert!(token_is_loader_environment_assignment("LD_AUDIT="));
        assert!(!token_is_loader_environment_assignment("PATH=/tmp"));
        assert!(!token_is_loader_environment_assignment("LD_PRELOAD"));

        assert!(token_is_loader_environment_name("LD_LIBRARY_PATH"));
        assert!(token_is_loader_environment_name("LD_TRACE_LOADED_OBJECTS"));
        assert!(!token_is_loader_environment_name("PATH"));

        assert!(token_is_loader_environment_unset_option("-uLD_PRELOAD"));
        assert!(token_is_loader_environment_unset_option("-u=LD_AUDIT"));
        assert!(!token_is_loader_environment_unset_option("-uPATH"));
        // A bare -u carries its name in the next token, handled by the scanner.
        assert!(!token_is_loader_environment_unset_option("-u"));
        // The long forms are matched by prefix in the scanner instead.
        assert!(!token_is_loader_environment_unset_option(
            "--unset=LD_PRELOAD"
        ));

        // env accepts any unambiguous abbreviation of a long option.
        assert_eq!(
            env_long_option_name("--split-string=X"),
            Some("split-string")
        );
        assert_eq!(env_long_option_name("--spl"), Some("spl"));
        assert_eq!(env_long_option_name("--unset=LD_PRELOAD"), Some("unset"));
        assert_eq!(env_long_option_name("--"), None);
        assert_eq!(env_long_option_name("-u"), None);
        assert_eq!(env_long_option_name("PATH=/bin"), None);

        // Short options cluster, so -S and -i must be found anywhere in the
        // cluster, not only at its head.
        assert!(token_clusters_loader_environment_control("-S"));
        assert!(token_clusters_loader_environment_control("-i"));
        assert!(token_clusters_loader_environment_control("-iS"));
        assert!(token_clusters_loader_environment_control("-0i"));
        assert!(token_clusters_loader_environment_control("-vS"));
        // -u and -C consume the rest of the token as a value, so a variable
        // name or a directory containing i or S is not an option.
        assert!(!token_clusters_loader_environment_control("-uSHELL"));
        assert!(!token_clusters_loader_environment_control("-C/tmp/dir"));
        assert!(!token_clusters_loader_environment_control("-0"));
        assert!(!token_clusters_loader_environment_control("-"));
        assert!(!token_clusters_loader_environment_control("--split-string"));
        assert!(!token_clusters_loader_environment_control("PATH=/bin"));
    }

    #[test]
    fn env_interpreter_shebangs_reject_loader_environment_control() {
        // env -S smuggles assignments the plain token scan would skip, and
        // -i / -u / - drop the guard's own preload before the target runs.
        for cursor in [
            " -S LD_PRELOAD=/tmp/x.so /bin/sh",
            " -iS LD_PRELOAD=/tmp/x.so /bin/sh",
            " --split-string=LD_AUDIT=/tmp/a.so /bin/sh",
            // env accepts unambiguous long-option abbreviations.
            " --split=-u LD_PRELOAD /bin/sh",
            " --s=LD_PRELOAD=/tmp/x.so /bin/sh",
            " --ignore-env /bin/sh",
            " --un=LD_PRELOAD /bin/sh",
            " --unset LD_AUDIT /bin/sh",
            " -u LD_PRELOAD /bin/sh",
            " --unset=LD_LIBRARY_PATH /bin/sh",
            " -uLD_PRELOAD /bin/sh",
            " -i /bin/sh",
            " --ignore-environment /bin/sh",
            " - /bin/sh",
            " LD_PRELOAD=/tmp/x.so /bin/true",
        ] {
            assert!(
                env_command_targets_protected_runtime(cursor, &[]),
                "env{cursor} must be refused"
            );
        }

        // Unrelated variables and unsets stay allowed.
        for cursor in [" -u FOO /bin/true", " FOO=bar /bin/true"] {
            assert!(
                !env_command_targets_protected_runtime(cursor, &[]),
                "env{cursor} must stay allowed"
            );
        }
    }

    #[test]
    fn every_writable_runtime_root_counts_as_mutable() {
        for path in [
            "/workspace/tool",
            "/state/tool",
            "/tmp/tool",
            "/var/tmp/tool",
            "/run/tool",
            "/dev/shm/tool",
            "/dev/mqueue/tool",
        ] {
            assert!(resolved_path_is_mutable_root(path), "{path}");
        }
        // A longer directory name that only starts with a root name is a
        // different directory, and /usr/bin stays trusted.
        assert!(!resolved_path_is_mutable_root("/tmpfoo/tool"));
        assert!(!resolved_path_is_mutable_root("/usr/bin/tool"));
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
    fn mutable_path_provenance_is_lexical_and_survives_parent_components() {
        // The lexical answer names what the caller wrote. Resolving first would
        // let a parent component walk out of a mutable root and back in.
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
        assert!(!path_has_mutable_provenance(
            "/usr/bin/tool",
            libc::AT_FDCWD
        ));
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn untrusted_provenance_reads_the_directory_not_the_root_list() {
        // The former test was membership in a fixed root list. A writable
        // directory anywhere else is the same risk, so provenance answers for
        // it too.
        assert!(!path_has_untrusted_provenance("/bin/true", libc::AT_FDCWD));

        let dir = create_temp_test_dir("provenance");
        let target = dir.join("probe");
        fs::write(&target, "probe").expect("write probe");
        assert!(path_has_untrusted_provenance(
            target.to_str().expect("probe path"),
            libc::AT_FDCWD
        ));
        // The target does not have to exist: a writable directory means the
        // name can be filled in after this decision.
        assert!(path_has_untrusted_provenance(
            dir.join("absent").to_str().expect("absent path"),
            libc::AT_FDCWD
        ));

        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn magic_and_relative_loader_targets_are_rejected() {
        assert!(path_is_magic_exec_target("/proc/self/fd/9"));
        assert!(path_is_magic_exec_target("/proc/self/cwd/bin/true"));
        assert!(path_is_magic_exec_target("/dev/fd/9"));
        assert!(!path_is_magic_exec_target("/usr/bin/true"));
        // A loader argument is looked up after this guard returns, so a name
        // that resolves against a working directory or a descriptor table is
        // refused whatever it names now.
        assert!(loader_arg_targets_mutable_native_exec("relative-target"));
        assert!(loader_arg_targets_mutable_native_exec(
            "/proc/self/cwd/bin/true"
        ));
        assert!(!loader_arg_targets_mutable_native_exec("/bin/true"));
    }

    #[test]
    fn loader_control_options_reject_mutable_values() {
        // Every library-naming option is refused without reading its value,
        // because deciding a value means reproducing the loader's separator
        // set and its $ORIGIN, $LIB and $PLATFORM expansion.
        let loader = "/lib64/ld-linux-x86-64.so.2".to_string();
        for arguments in [
            vec![loader.clone(), "--preload".to_string()],
            vec![loader.clone(), "--preload=".to_string(), "/bin/true".into()],
            // An immutable-looking value is refused too: the loader would
            // expand it before use, so its text does not decide where it
            // resolves.
            vec![
                loader.clone(),
                "--library-path".to_string(),
                "/usr/lib".to_string(),
                "/bin/true".into(),
            ],
            vec![
                loader.clone(),
                "--library-path=/usr/lib;/workspace/lib".to_string(),
                "/bin/true".into(),
            ],
            vec![
                loader.clone(),
                "--preload".to_string(),
                "$ORIGIN/../../workspace/evil.so".to_string(),
                "/bin/true".into(),
            ],
            vec![loader.clone(), "--audit=/usr/lib:".to_string()],
            // An option this parser does not know may consume the next
            // argument, so the exec target cannot be located.
            vec![
                loader.clone(),
                "--not-a-real-option".to_string(),
                "/bin/true".into(),
            ],
        ] {
            assert!(
                loader_args_target_mutable_native_exec(&arguments),
                "{arguments:?} must be refused"
            );
        }

        for arguments in [
            vec![loader.clone(), "/bin/true".to_string()],
            // --argv0 takes a value that is not an exec target.
            vec![
                loader.clone(),
                "--argv0".to_string(),
                "sh".to_string(),
                "/bin/true".into(),
            ],
            vec![
                loader.clone(),
                "--argv0=sh".to_string(),
                "/bin/true".to_string(),
            ],
            // The other value-taking loader options must not have their value
            // read as the exec target.
            vec![
                loader.clone(),
                "--inhibit-rpath".to_string(),
                "/usr/lib".to_string(),
                "/bin/true".into(),
            ],
            vec![
                loader.clone(),
                "--glibc-hwcaps-mask".to_string(),
                "x86-64-v3".to_string(),
                "/bin/true".into(),
            ],
            // A valueless option leaves the next argument as the target.
            vec![
                loader.clone(),
                "--inhibit-cache".to_string(),
                "/bin/true".to_string(),
            ],
            vec![loader.clone(), "--".to_string(), "/bin/true".to_string()],
            // Arguments after the target belong to the target, not the loader.
            vec![
                loader.clone(),
                "/bin/true".to_string(),
                "/usr/lib:".to_string(),
            ],
            // A target pathname may contain an equals sign. It must be checked
            // whole rather than truncated at the first one, so this target
            // reaches path_is_mutable_native_exec intact and does not exist.
            vec![loader.clone(), "/bin/true=x".to_string()],
        ] {
            assert!(
                !loader_args_target_mutable_native_exec(&arguments),
                "{arguments:?} must stay allowed"
            );
        }
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn loader_environment_is_refused_for_loader_sensitive_targets() {
        let dir = create_temp_test_dir("loader-sensitive");
        let elf = dir.join("elf");
        fs::write(&elf, [0x7f, b'E', b'L', b'F', 2, 1, 1, 0]).expect("elf");
        let script = dir.join("script");
        fs::write(&script, "#!/bin/sh\nexit 0\n").expect("script");
        let data = dir.join("data");
        fs::write(&data, "plain text, not an exec target\n").expect("data");

        let elf_path = elf.display().to_string();
        let data_path = data.display().to_string();
        assert!(path_is_loader_sensitive(&elf_path));
        assert!(path_is_loader_sensitive(&script.display().to_string()));
        // A regular file that is neither ELF nor a shebang still counts:
        // execve answers it with ENOEXEC and glibc's execvp and execvpe then
        // launch /bin/sh with the caller's environment from inside libc,
        // without re-entering this guard.
        assert!(path_is_loader_sensitive(&data_path));
        // Nothing that is not a regular file is executable at all.
        assert!(!path_is_loader_sensitive(&dir.display().to_string()));
        // A target the guard cannot stat is refused rather than trusted.
        assert!(path_is_loader_sensitive(
            &dir.join("missing").display().to_string()
        ));

        let unsafe_env = ["LD_AUDIT=/tmp/audit.so".to_string()];
        assert!(should_block_loader_env_for_path(&elf_path, &unsafe_env));
        assert!(should_block_loader_env_for_path(&data_path, &unsafe_env));
        // A clean environment leaves every target alone.
        assert!(!should_block_loader_env_for_path(&elf_path, &[]));
        assert!(!should_block_loader_env_for_path(&data_path, &[]));
        assert!(!should_block_loader_env_for_path(
            &elf_path,
            &[format!("LD_PRELOAD={ALLOWED_LD_PRELOAD}")]
        ));
        assert!(!should_block_loader_env_for_path(
            &dir.display().to_string(),
            &unsafe_env
        ));

        let file = File::open(&elf).expect("open elf");
        let elf_fd = file.as_raw_fd();
        assert!(file_descriptor_is_loader_sensitive(elf_fd));
        assert!(should_block_loader_env_for_fd(elf_fd, &unsafe_env));
        assert!(!should_block_loader_env_for_fd(elf_fd, &[]));

        let data_file = File::open(&data).expect("open data");
        assert!(should_block_loader_env_for_fd(
            data_file.as_raw_fd(),
            &unsafe_env
        ));
        // A closed descriptor cannot be inspected, so it is refused.
        assert!(file_descriptor_is_loader_sensitive(-1));

        fs::remove_dir_all(&dir).expect("cleanup temp test dir");
    }
}
