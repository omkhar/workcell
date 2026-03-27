#![allow(clippy::missing_safety_doc)]

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
use std::mem;
use std::os::unix::fs::MetadataExt;
use std::path::Path;
use std::sync::OnceLock;

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
];

const APPROVED_WRAPPER_SCRIPTS: &[&str] = &[
    "/usr/local/libexec/workcell/git-wrapper.sh",
    "/usr/local/libexec/workcell/node-wrapper.sh",
    "/usr/local/libexec/workcell/provider-wrapper.sh",
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

const ARG_BLOCK_MESSAGE: &str = "Workcell blocked git control-plane override: remove --no-verify, git commit -n, --exec-path, --git-dir, --work-tree, or inline hook/include overrides.\n";
const ENV_BLOCK_MESSAGE: &str = "Workcell blocked git control-plane override: remove GIT_CONFIG_*, GIT_CONFIG_GLOBAL, GIT_CONFIG_SYSTEM, GIT_DIR, GIT_WORK_TREE, GIT_COMMON_DIR, GIT_EXEC_PATH, GIT_ASKPASS, GIT_EDITOR, GIT_SEQUENCE_EDITOR, GIT_SSH, GIT_SSH_COMMAND, SSH_ASKPASS, EDITOR, PAGER, or VISUAL overrides.\n";
const PROTECTED_RUNTIME_BLOCK_MESSAGE: &str =
    "Workcell blocked direct protected runtime execution outside approved wrappers.\n";
const MUTABLE_NATIVE_EXEC_BLOCK_MESSAGE: &str =
    "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile.\n";

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
            if parse_nonnegative_long(pid) == Some(unsafe { libc::getpid() as i64 }) =>
        {
            fd.parse::<c_int>().ok()
        }
        ["proc", "self", "task", _tid, "fd", fd] => fd.parse::<c_int>().ok(),
        ["proc", pid, "task", _tid, "fd", fd]
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
        let current = unsafe { *ptr.add(index) };
        if current.is_null() {
            break;
        }
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

        if token_is_shell_interpreter(&token_path) {
            if let Some(target) = next_shebang_token(&mut scan) {
                if shell_option_executes_command(&target) {
                    return true;
                }
            }
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

fn current_process_uses_approved_wrapper() -> bool {
    if !current_process_wrapper_env_is_clean() {
        return false;
    }

    let Ok(cmdline) = fs::read("/proc/self/cmdline") else {
        return false;
    };
    let args: Vec<String> = cmdline
        .split(|byte| *byte == 0)
        .filter(|entry| !entry.is_empty())
        .map(|entry| String::from_utf8_lossy(entry).into_owned())
        .collect();

    let Some(candidate) = args.get(1) else {
        return false;
    };

    APPROVED_WRAPPER_SCRIPTS.iter().any(|approved| {
        candidate == approved
            || fs::canonicalize(candidate)
                .ok()
                .is_some_and(|resolved| resolved.to_string_lossy() == *approved)
    })
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

fn protected_runtime_signatures() -> Vec<(ProtectedRuntime, StatSignature)> {
    PROTECTED_RUNTIME_PATHS
        .iter()
        .filter_map(|(kind, path)| {
            fs::metadata(path)
                .ok()
                .map(|metadata| (*kind, metadata_signature_from_metadata(&metadata)))
        })
        .collect()
}

fn protected_git_signatures() -> Vec<StatSignature> {
    PROTECTED_GIT_PATHS
        .iter()
        .filter_map(|path| {
            fs::metadata(path)
                .ok()
                .map(|metadata| metadata_signature_from_metadata(&metadata))
        })
        .collect()
}

fn stat_matches_protected_runtime(candidate: &StatSignature) -> ProtectedRuntime {
    protected_runtime_signatures()
        .into_iter()
        .find_map(|(kind, protected)| {
            if protected.dev == candidate.dev && protected.ino == candidate.ino {
                Some(kind)
            } else {
                None
            }
        })
        .unwrap_or(ProtectedRuntime::None)
}

fn candidate_size_matches_protected_runtime(candidate: &StatSignature) -> bool {
    (candidate.mode & file_type_bits()) == regular_file_mode()
        && protected_runtime_signatures()
            .into_iter()
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

fn classify_loader_target(path: &str, args: &[String]) -> ProtectedRuntime {
    if !is_dynamic_loader_path(path) {
        return ProtectedRuntime::None;
    }
    args.get(1)
        .map(|target| classify_protected_runtime_path(target))
        .unwrap_or(ProtectedRuntime::None)
}

fn loader_targets_mutable_native_exec(path: &str, args: &[String]) -> bool {
    is_dynamic_loader_path(path)
        && args
            .get(1)
            .is_some_and(|target| path_is_mutable_native_exec(target))
}

fn should_block_protected_runtime_exec(
    path: &str,
    args: &[String],
    env_entries: &[String],
) -> bool {
    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        let kind = classify_protected_runtime_fd(proc_fd);
        if kind != ProtectedRuntime::None {
            return !current_process_uses_approved_wrapper();
        }
        return file_descriptor_is_mutable_shebang_to_protected_runtime(proc_fd, env_entries);
    }

    let mut kind = classify_protected_runtime_path(path);
    if kind == ProtectedRuntime::None {
        kind = classify_loader_target(path, args);
    }
    if kind != ProtectedRuntime::None {
        return !current_process_uses_approved_wrapper();
    }

    path_is_mutable_shebang_to_protected_runtime(path, env_entries)
}

fn should_block_mutable_native_exec(path: &str, args: &[String]) -> bool {
    if let Some(proc_fd) = path_is_current_process_fd_path(path) {
        return file_descriptor_is_mutable_native_exec(proc_fd);
    }

    path_is_mutable_native_exec(path) || loader_targets_mutable_native_exec(path, args)
}

fn resolve_exec_search_target(file: &str, env_entries: &[String]) -> String {
    resolve_command_via_path_value(file, path_from_env_entries(env_entries).as_deref())
        .unwrap_or_else(|| file.to_owned())
}

fn git_config_key_has_prefix_and_suffix(key: &str, prefix: &str, suffix: &str) -> bool {
    key.len() > prefix.len() + suffix.len()
        && key[..prefix.len()].eq_ignore_ascii_case(prefix)
        && key[key.len() - suffix.len()..].eq_ignore_ascii_case(suffix)
}

fn git_config_key_is_blocked(key: &str) -> bool {
    matches!(
        key.to_ascii_lowercase().as_str(),
        "core.askpass"
            | "core.editor"
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
        || key.len() > 6 && key[..6].eq_ignore_ascii_case("pager.")
}

fn git_config_spec_is_blocked(spec: &str) -> bool {
    let key = spec.split_once('=').map(|(key, _)| key).unwrap_or(spec);
    !key.is_empty() && git_config_key_is_blocked(key)
}

fn stat_matches_protected_git(candidate: &StatSignature) -> bool {
    protected_git_signatures()
        .into_iter()
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

fn should_block(path: &str, args: &[String]) -> bool {
    if !is_git_path(path) || args.is_empty() {
        return false;
    }

    let mut saw_commit = false;
    let mut expect_config_arg = false;

    for arg in args.iter().skip(1) {
        if expect_config_arg {
            expect_config_arg = false;
            if git_config_spec_is_blocked(arg) {
                return true;
            }
            continue;
        }

        match arg.as_str() {
            "-c" | "--config-env" => {
                expect_config_arg = true;
                continue;
            }
            "--exec-path" | "--git-dir" | "--work-tree" | "--no-verify" => return true,
            "commit" => saw_commit = true,
            _ => {}
        }

        if let Some(spec) = arg.strip_prefix("--config-env=") {
            if git_config_spec_is_blocked(spec) {
                return true;
            }
        }
        if arg.starts_with("--exec-path=")
            || arg.starts_with("--git-dir=")
            || arg.starts_with("--work-tree=")
        {
            return true;
        }
    }

    saw_commit
        && args
            .iter()
            .skip(1)
            .any(|arg| git_commit_short_arg_invokes_no_verify(arg))
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
        if let Some(value) = entry.strip_prefix("GIT_CONFIG_PARAMETERS=") {
            let lower = value.to_ascii_lowercase();
            if lower.contains("core.hookspath")
                || lower.contains("core.worktree")
                || lower.contains("include.path")
                || lower.contains("includeif.")
            {
                return true;
            }
        }

        if entry.starts_with("GIT_DIR=")
            || entry.starts_with("GIT_WORK_TREE=")
            || entry.starts_with("GIT_COMMON_DIR=")
            || entry.starts_with("GIT_EXEC_PATH=")
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

        if let Some(value) = entry.strip_prefix("GIT_CONFIG_GLOBAL=") {
            if value != "/dev/null" {
                return true;
            }
        }

        if let Some(value) = entry.strip_prefix("GIT_CONFIG_NOSYSTEM=") {
            if value != "1" {
                return true;
            }
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
    libc::__errno_location()
}

#[cfg(target_os = "macos")]
unsafe fn errno_location() -> *mut c_int {
    libc::__error()
}

fn report_block(env_bypass: bool) {
    report(if env_bypass {
        ENV_BLOCK_MESSAGE
    } else {
        ARG_BLOCK_MESSAGE
    });
}

fn report_protected_runtime_block() {
    report(PROTECTED_RUNTIME_BLOCK_MESSAGE);
}

fn report_mutable_native_exec_block() {
    report(MUTABLE_NATIVE_EXEC_BLOCK_MESSAGE);
}

unsafe fn load_symbol<T: Copy>(name: &[u8]) -> T {
    let symbol = libc::dlsym(libc::RTLD_NEXT, name.as_ptr().cast());
    assert!(!symbol.is_null(), "missing required symbol {:?}", name);
    mem::transmute_copy(&symbol)
}

fn execve_fn() -> ExecveFn {
    *EXECVE_FN.get_or_init(|| unsafe { load_symbol(b"execve\0") })
}

fn execv_fn() -> ExecvFn {
    *EXECV_FN.get_or_init(|| unsafe { load_symbol(b"execv\0") })
}

fn execvp_fn() -> ExecvpFn {
    *EXECVP_FN.get_or_init(|| unsafe { load_symbol(b"execvp\0") })
}

fn execvpe_fn() -> ExecvpeFn {
    *EXECVPE_FN.get_or_init(|| unsafe { load_symbol(b"execvpe\0") })
}

fn execveat_fn() -> ExecveatFn {
    *EXECVEAT_FN.get_or_init(|| unsafe { load_symbol(b"execveat\0") })
}

fn fexecve_fn() -> FexecveFn {
    *FEXECVE_FN.get_or_init(|| unsafe { load_symbol(b"fexecve\0") })
}

fn posix_spawn_fn() -> PosixSpawnFn {
    *POSIX_SPAWN_FN.get_or_init(|| unsafe { load_symbol(b"posix_spawn\0") })
}

fn posix_spawnp_fn() -> PosixSpawnpFn {
    *POSIX_SPAWNP_FN.get_or_init(|| unsafe { load_symbol(b"posix_spawnp\0") })
}

fn real_syscall_fn() -> SyscallFn {
    *REAL_SYSCALL_FN.get_or_init(|| unsafe { load_symbol(b"syscall\0") })
}

fn c_path_string(path: *const c_char) -> String {
    if path.is_null() {
        String::new()
    } else {
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
    let mut stat_buf = unsafe { mem::zeroed::<libc::stat>() };
    if unsafe { libc::fstatat(dirfd, c_path.as_ptr(), &mut stat_buf, 0) } != 0 {
        return false;
    }

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
            return execve(
                arg1 as *const c_char,
                arg2 as *const *const c_char,
                arg3 as *const *const c_char,
            ) as c_long;
        }
        if number == SYS_EXECVEAT {
            return execveat(
                arg1 as c_int,
                arg2 as *const c_char,
                arg3 as *const *const c_char,
                arg4 as *const *const c_char,
                arg5 as c_int,
            ) as c_long;
        }
    }

    real_syscall_fn()(number, arg1, arg2, arg3, arg4, arg5, arg6)
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

    if should_block_protected_runtime_exec(&path_string, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&path_string, &args) {
        report_mutable_native_exec_block();
        return -1;
    }
    if is_git_path(&path_string) && env_has_unsafe_git_override(&env_entries) {
        report_block(true);
        return -1;
    }
    if should_block(&path_string, &args) {
        report_block(false);
        return -1;
    }

    execve_fn()(path, argv, envp)
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execv(path: *const c_char, argv: *const *const c_char) -> c_int {
    let path_string = c_path_string(path);
    let args = collect_cstring_array(argv);
    let env_entries = collect_cstring_array(environ.cast());

    if should_block_protected_runtime_exec(&path_string, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&path_string, &args) {
        report_mutable_native_exec_block();
        return -1;
    }
    if is_git_path(&path_string) && env_has_unsafe_git_override(&env_entries) {
        report_block(true);
        return -1;
    }
    if should_block(&path_string, &args) {
        report_block(false);
        return -1;
    }

    execv_fn()(path, argv)
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn execvp(file: *const c_char, argv: *const *const c_char) -> c_int {
    let file_string = c_path_string(file);
    let args = collect_cstring_array(argv);
    let env_entries = collect_cstring_array(environ.cast());
    let effective_path = resolve_exec_search_target(&file_string, &env_entries);

    if should_block_protected_runtime_exec(&effective_path, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&effective_path, &args) {
        report_mutable_native_exec_block();
        return -1;
    }
    if is_git_path(&file_string) && env_has_unsafe_git_override(&env_entries) {
        report_block(true);
        return -1;
    }
    if should_block(&file_string, &args) {
        report_block(false);
        return -1;
    }

    execvp_fn()(file, argv)
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

    if should_block_protected_runtime_exec(&effective_path, &args, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if should_block_mutable_native_exec(&effective_path, &args) {
        report_mutable_native_exec_block();
        return -1;
    }
    if is_git_path(&file_string) && env_has_unsafe_git_override(&env_entries) {
        report_block(true);
        return -1;
    }
    if should_block(&file_string, &args) {
        report_block(false);
        return -1;
    }

    execvpe_fn()(file, argv, envp)
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

    let (protected_target, mutable_native_target, mutable_shebang_protected_target) =
        if (flags & AT_EMPTY_PATH_FLAG) != 0 && pathname_string.is_empty() {
            (
                classify_protected_runtime_fd(dirfd),
                file_descriptor_is_mutable_native_exec(dirfd),
                file_descriptor_is_mutable_shebang_to_protected_runtime(dirfd, &env_entries),
            )
        } else {
            let mut protected_target = ProtectedRuntime::None;
            let mut mutable_native_target = false;
            let mut mutable_shebang_target = false;

            if let Ok(c_path) = CString::new(pathname_string.as_bytes()) {
                let mut stat_buf = mem::zeroed::<libc::stat>();
                if libc::fstatat(dirfd, c_path.as_ptr(), &mut stat_buf, 0) == 0 {
                    if let Some(signature) = stat_signature_from_stat(&stat_buf) {
                        protected_target = stat_matches_protected_runtime(&signature);
                    }
                }

                let candidate_fd =
                    libc::openat(dirfd, c_path.as_ptr(), libc::O_RDONLY | libc::O_CLOEXEC);
                if candidate_fd >= 0 {
                    mutable_native_target = file_descriptor_is_mutable_native_exec(candidate_fd);
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

            if protected_target == ProtectedRuntime::None
                && is_dynamic_loader_path(&pathname_string)
                && args.get(1).is_some()
            {
                protected_target = args
                    .get(1)
                    .map(|target| classify_protected_runtime_path(target))
                    .unwrap_or(ProtectedRuntime::None);
            }
            if !mutable_native_target
                && is_dynamic_loader_path(&pathname_string)
                && args.get(1).is_some()
            {
                mutable_native_target = args
                    .get(1)
                    .is_some_and(|target| path_is_mutable_native_exec(target));
            }

            (
                protected_target,
                mutable_native_target,
                mutable_shebang_target,
            )
        };

    if (protected_target != ProtectedRuntime::None && !current_process_uses_approved_wrapper())
        || mutable_shebang_protected_target
    {
        report_protected_runtime_block();
        return -1;
    }
    if mutable_native_target {
        report_mutable_native_exec_block();
        return -1;
    }
    if git_target && env_has_unsafe_git_override(&env_entries) {
        report_block(true);
        return -1;
    }
    if git_target && should_block(&effective_path, &args) {
        report_block(false);
        return -1;
    }

    execveat_fn()(dirfd, pathname, argv, envp, flags)
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn fexecve(
    fd: c_int,
    argv: *const *const c_char,
    envp: *const *const c_char,
) -> c_int {
    let args = collect_cstring_array(argv);
    let env_entries = collect_cstring_array(effective_env_ptr(envp));

    if classify_protected_runtime_fd(fd) != ProtectedRuntime::None
        && !current_process_uses_approved_wrapper()
    {
        report_protected_runtime_block();
        return -1;
    }
    if file_descriptor_is_mutable_shebang_to_protected_runtime(fd, &env_entries) {
        report_protected_runtime_block();
        return -1;
    }
    if file_descriptor_is_mutable_native_exec(fd) {
        report_mutable_native_exec_block();
        return -1;
    }
    if fd_matches_protected_git(fd) && env_has_unsafe_git_override(&env_entries) {
        report_block(true);
        return -1;
    }
    if fd_matches_protected_git(fd) && should_block("/usr/local/bin/git", &args) {
        report_block(false);
        return -1;
    }

    fexecve_fn()(fd, argv, envp)
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

    if should_block_protected_runtime_exec(&path_string, &args, &env_entries) {
        report_protected_runtime_block();
        return libc::EPERM;
    }
    if should_block_mutable_native_exec(&path_string, &args) {
        report_mutable_native_exec_block();
        return libc::EPERM;
    }
    if is_git_path(&path_string) && env_has_unsafe_git_override(&env_entries) {
        report_block(true);
        return libc::EPERM;
    }
    if should_block(&path_string, &args) {
        report_block(false);
        return libc::EPERM;
    }

    posix_spawn_fn()(pid, path, file_actions, attrp, argv, envp)
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

    if should_block_protected_runtime_exec(&effective_path, &args, &env_entries) {
        report_protected_runtime_block();
        return libc::EPERM;
    }
    if should_block_mutable_native_exec(&effective_path, &args) {
        report_mutable_native_exec_block();
        return libc::EPERM;
    }
    if is_git_path(&file_string) && env_has_unsafe_git_override(&env_entries) {
        report_block(true);
        return libc::EPERM;
    }
    if should_block(&file_string, &args) {
        report_block(false);
        return libc::EPERM;
    }

    posix_spawnp_fn()(pid, file, file_actions, attrp, argv, envp)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn matches_non_strict_recognizes_only_non_empty_non_strict_values() {
        assert!(!matches_non_strict(None));
        assert!(!matches_non_strict(Some("")));
        assert!(!matches_non_strict(Some("strict")));
        assert!(!matches_non_strict(Some("STRICT")));
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
    }

    #[test]
    fn path_is_current_process_fd_path_accepts_supported_fd_forms() {
        assert_eq!(
            path_is_current_process_fd_path("/dev/stdin"),
            Some(libc::STDIN_FILENO)
        );
        assert_eq!(path_is_current_process_fd_path("/proc/self/fd/9"), Some(9));
        assert_eq!(
            path_is_current_process_fd_path(&format!("/proc/{}/fd/4", unsafe { libc::getpid() })),
            Some(4)
        );
        assert_eq!(path_is_current_process_fd_path("/proc/999999/fd/4"), None);
    }
}
