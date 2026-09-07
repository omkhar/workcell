// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//! Loader invocation-form fixture matrix for the exec guard.
//!
//! Every defect in the loader/exec bypass class had one shape: the guard's
//! argument walk missed a syntactic form of an invocation it already judged in
//! another form. Each fix was proven one form at a time. This file makes the
//! coverage a table instead: one row per invocation form, each row naming the
//! classification the guard must reach, driven through the guard's own
//! exported entry points rather than through its private helpers.
//!
//! A row states the message the guard must print, or that no form-specific
//! refusal may fire. The cleared-environment first hop is accepted in the
//! second case: a child environment without the approved preload is refused by
//! the fail-closed default, which is not a statement about the form under test.
//!
//! Rows this lane cannot answer are recorded as pending with a reason rather
//! than asserted green. `scripts/container-smoke.sh` replays the loader
//! argument forms against the real loader inside the runtime image, where the
//! mutable exec roots and the protected runtime signatures exist. Each pending
//! reason names the lane that holds the form, or states that no lane holds it.

use Call::{Execve, ExecveNullEnv, Execveat, Execvp};
use Expect::{NotRefused, Pending, Refused};
use libc::c_int;
use std::ffi::{CString, OsString};
use std::fs::{self, File};
use std::io::Read;
use std::os::fd::FromRawFd;
use std::os::unix::fs::{DirBuilderExt, PermissionsExt};
use std::path::{Path, PathBuf};
use std::ptr;

const MUTABLE_NATIVE: &str = "Workcell blocked direct native executable launch from mutable workspace/state paths on the strict profile.\n";
const LOADER_ENV: &str = "Workcell blocked unsafe dynamic-loader environment for native execution on the strict profile.\n";
const PROTECTED_RUNTIME: &str =
    "Workcell blocked direct protected runtime execution outside approved wrappers.\n";
const MISSING_GUARD: &str = "Workcell blocked child execution without the approved exec guard preload on the strict profile.\n";
const RELATIVE_SEARCH_PATH: &str =
    "Workcell rejected a command found only through a relative PATH entry.\n";
const OVERSIZED_INPUT: &str = "Workcell rejected oversized exec arguments.\n";

/// The refusals that answer the form under test. `MISSING_GUARD` is absent on
/// purpose: it is the fail-closed default for an unguarded child, not a
/// judgement about the invocation form.
const FORM_SPECIFIC_REFUSALS: &[&str] = &[
    MUTABLE_NATIVE,
    LOADER_ENV,
    PROTECTED_RUNTIME,
    RELATIVE_SEARCH_PATH,
    OVERSIZED_INPUT,
];

/// Matches `ALLOWED_LD_PRELOAD` in `src/lib.rs`. Rows that are not about the
/// child environment carry exactly this entry, so the fail-closed default
/// never masks the classification the row is about.
const GUARD_PRELOAD: &str = "LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so";
const GUARDED_ENV: &[&str] = &[GUARD_PRELOAD];

/// The fixture directory. `{fixture}` expands to it in every row.
const FIXTURE: &str = "{fixture}";
/// A pathname whose basename marks it as the dynamic loader, so the guard walks
/// the argument vector the way the loader would.
const LOADER_NAME: &str = "ld-linux-form-fixture.so.2";
const LOADER: &str = "{fixture}/ld-linux-form-fixture.so.2";
/// An option of unknown arity. The guard cannot locate the exec target past it,
/// so reaching it must refuse. Placing it where a value belongs turns "the
/// option's value was read as the exec target" into an observable refusal.
const UNKNOWN_OPTION: &str = "--workcell-not-a-real-option";
/// A regular file that is not an ELF and carries no shebang: the ENOEXEC
/// fallback target, and an exec target outside every mutable root.
const TARGET: &str = "{fixture}/not-an-exec-target";
const TARGET_ARGV: &[&str] = &["target"];
/// A bare name for the PATH-search rows. No search root holds it.
const PROBE: &str = "workcell-loader-form-probe";
/// An executable regular file that is neither an ELF nor a shebang. execve
/// answers ENOEXEC for it, and glibc's execvp runs /bin/sh from inside libc,
/// which does not re-enter the guard, so the guard must classify it first.
const ENOEXEC_TARGET: &str = "enoexec-fallback-target";
/// Bytes past `MAX_EXEC_PATH_SEGMENT_BYTES` in `src/lib.rs`.
const OVERLONG_SEGMENT_BYTES: usize = 128 * 1024 + 1;

const EQUALS_SIGN_PENDING: &str = "the split-at-equals defect is only observable when the truncated prefix resolves inside a mutable exec root; the container smoke row target-with-equals-sign holds it";
const ENV_CLUSTER_PENDING: &str = "the cluster is classified only when the shebang scan reaches a protected runtime, whose stat signatures exist in the runtime image; the container smoke fixture .workcell-env-cluster-node-shebang holds it";
const RAW_SYSCALL_PENDING: &str = "no lane holds this form: the built cdylib exports workcell_syscall_shim and does not export syscall, so nothing interposes a raw syscall(SYS_execve, ...) and a row over it would report a false pass; the missing export is pre-existing on main and is recorded for the trampoline unit of the series";

enum Call {
    /// `execve(path, argv, envp)` with an explicit child environment.
    Execve(
        &'static str,
        &'static [&'static str],
        &'static [&'static str],
    ),
    /// `execve(path, argv, NULL)`. A NULL envp gives the child an empty
    /// environment, which no other row can ask about.
    ExecveNullEnv(&'static str),
    /// `execveat(dirfd, name, argv, envp, 0)` with `name` relative to the
    /// fixture directory, so the target is the descriptor the kernel resolves.
    Execveat(
        &'static str,
        &'static [&'static str],
        &'static [&'static str],
    ),
    /// `execvp(file, argv)` with `PATH` and one more variable set on the
    /// caller. `execvp` reads the caller's own environment for both the search
    /// and the loader-override check, never the child environment. An empty
    /// third field sets no extra variable.
    Execvp(&'static str, &'static str, &'static str),
}

enum Expect {
    /// The guard must print this message.
    Refused(&'static str),
    /// No form-specific refusal may fire.
    NotRefused,
    /// Not answerable on this branch. Recorded, not asserted.
    Pending(&'static str),
}

struct Form {
    name: &'static str,
    call: Call,
    expect: Expect,
    /// The loader-environment and descriptor classifiers are Linux-only.
    linux_only: bool,
}

/// Table constructor, so a row reads as one line of matrix rather than four.
const fn form(name: &'static str, call: Call, expect: Expect, linux_only: bool) -> Form {
    Form {
        name,
        call,
        expect,
        linux_only,
    }
}

/// `linux_only` values, named for the table.
const ANY: bool = false;
const LINUX: bool = true;

const FORMS: &[Form] = &[
    // The loader argument walk. A row that must not refuse puts UNKNOWN_OPTION
    // where the option's value belongs: if the walk reads that value as the
    // exec target it reaches an option of unknown arity and refuses, so the
    // absence of a refusal is the proof that the value was consumed.
    //
    // The opposite error needs the opposite placement. An attached value is
    // already in its option's token, so a walk that also consumes the next
    // argument steps over the exec target and refuses nothing. A row for an
    // attached form therefore puts UNKNOWN_OPTION in the exec-target position
    // and demands a refusal: the refusal is the proof that the walk stopped
    // there rather than past it.
    form(
        "bare exec target",
        Execve(LOADER, &["ld", TARGET], GUARDED_ENV),
        NotRefused,
        ANY,
    ),
    form(
        "option of unknown arity refuses rather than skips",
        Execve(LOADER, &["ld", UNKNOWN_OPTION, TARGET], GUARDED_ENV),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "option abbreviation is not a known option",
        Execve(LOADER, &["ld", "--argv", TARGET], GUARDED_ENV),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "valueless option before an unknown option still refuses",
        Execve(
            LOADER,
            &["ld", "--list-tunables", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--argv0 separate value is not the exec target",
        Execve(
            LOADER,
            &["ld", "--argv0", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        NotRefused,
        ANY,
    ),
    form(
        "--argv0=value attached form",
        Execve(
            LOADER,
            &["ld", "--argv0=--workcell-not-a-real-option", TARGET],
            GUARDED_ENV,
        ),
        NotRefused,
        ANY,
    ),
    form(
        "--argv0=value consumes no further argument",
        Execve(LOADER, &["ld", "--argv0=x", UNKNOWN_OPTION], GUARDED_ENV),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--inhibit-rpath= empty attached value",
        Execve(LOADER, &["ld", "--inhibit-rpath=", TARGET], GUARDED_ENV),
        NotRefused,
        ANY,
    ),
    form(
        "--inhibit-rpath= empty attached value consumes no further argument",
        Execve(
            LOADER,
            &["ld", "--inhibit-rpath=", UNKNOWN_OPTION],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--inhibit-rpath separate value",
        Execve(
            LOADER,
            &["ld", "--inhibit-rpath", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        NotRefused,
        ANY,
    ),
    form(
        "--glibc-hwcaps-mask separate value",
        Execve(
            LOADER,
            &["ld", "--glibc-hwcaps-mask", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        NotRefused,
        ANY,
    ),
    form(
        "--glibc-hwcaps-prepend separate value",
        Execve(
            LOADER,
            &["ld", "--glibc-hwcaps-prepend", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        NotRefused,
        ANY,
    ),
    form(
        "--hwcap-mask separate value",
        Execve(
            LOADER,
            &["ld", "--hwcap-mask", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        NotRefused,
        ANY,
    ),
    form(
        "valueless option leaves the next argument as the target",
        Execve(LOADER, &["ld", "--inhibit-cache", TARGET], GUARDED_ENV),
        NotRefused,
        ANY,
    ),
    // One row per remaining valueless option. Each puts UNKNOWN_OPTION where a
    // value would sit: a walk that reads it as this option's value skips it and
    // refuses nothing, so the refusal is the proof that the option consumed
    // nothing.
    form(
        "--list consumes no value",
        Execve(
            LOADER,
            &["ld", "--list", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--list-diagnostics consumes no value",
        Execve(
            LOADER,
            &["ld", "--list-diagnostics", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--verify consumes no value",
        Execve(
            LOADER,
            &["ld", "--verify", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--inhibit-cache consumes no value",
        Execve(
            LOADER,
            &["ld", "--inhibit-cache", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--help consumes no value",
        Execve(
            LOADER,
            &["ld", "--help", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--version consumes no value",
        Execve(
            LOADER,
            &["ld", "--version", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "-- ends option parsing",
        Execve(LOADER, &["ld", "--", TARGET], GUARDED_ENV),
        NotRefused,
        ANY,
    ),
    form(
        "--library-path is refused rather than read, colon separators",
        Execve(
            LOADER,
            &["ld", "--library-path", "/usr/lib:/state/lib", TARGET],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--library-path attached value, semicolon separators",
        Execve(
            LOADER,
            &["ld", "--library-path=/usr/lib;/state/lib", TARGET],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--preload is refused rather than expanded",
        Execve(
            LOADER,
            &["ld", "--preload", "$ORIGIN/../evil.so", TARGET],
            GUARDED_ENV,
        ),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "--audit= empty attached value is still refused",
        Execve(LOADER, &["ld", "--audit=", TARGET], GUARDED_ENV),
        Refused(MUTABLE_NATIVE),
        ANY,
    ),
    form(
        "arguments after the exec target belong to the target",
        Execve(
            LOADER,
            &["ld", TARGET, "--preload", "/state/evil.so"],
            GUARDED_ENV,
        ),
        NotRefused,
        ANY,
    ),
    form(
        "exec target containing an equals sign is not truncated",
        Execve(
            LOADER,
            &["ld", "{fixture}/not-an-exec-target=x"],
            GUARDED_ENV,
        ),
        Pending(EQUALS_SIGN_PENDING),
        ANY,
    ),
    // The loader environment on a native exec target.
    form(
        "LD_LIBRARY_PATH colon separators",
        Execve(
            TARGET,
            TARGET_ARGV,
            &[GUARD_PRELOAD, "LD_LIBRARY_PATH=/usr/lib:/state/lib"],
        ),
        Refused(LOADER_ENV),
        LINUX,
    ),
    form(
        "LD_LIBRARY_PATH semicolon separators",
        Execve(
            TARGET,
            TARGET_ARGV,
            &[GUARD_PRELOAD, "LD_LIBRARY_PATH=/usr/lib;/state/lib"],
        ),
        Refused(LOADER_ENV),
        LINUX,
    ),
    form(
        "LD_PRELOAD space separator beside the approved guard",
        Execve(
            TARGET,
            TARGET_ARGV,
            &["LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so /state/evil.so"],
        ),
        Refused(LOADER_ENV),
        LINUX,
    ),
    form(
        "LD_PRELOAD colon separator beside the approved guard",
        Execve(
            TARGET,
            TARGET_ARGV,
            &["LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so:/state/evil.so"],
        ),
        Refused(LOADER_ENV),
        LINUX,
    ),
    form(
        "LD_PRELOAD newline separator beside the approved guard",
        Execve(
            TARGET,
            TARGET_ARGV,
            &["LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so\n/state/evil.so"],
        ),
        Refused(LOADER_ENV),
        LINUX,
    ),
    form(
        "a non-ELF regular file target is loader interpreted",
        Execve(
            TARGET,
            TARGET_ARGV,
            &[GUARD_PRELOAD, "LD_AUDIT=/state/evil.so"],
        ),
        Refused(LOADER_ENV),
        LINUX,
    ),
    form(
        "ENOEXEC fallback target found through the execvp search",
        Execvp(
            ENOEXEC_TARGET,
            FIXTURE,
            "LD_LIBRARY_PATH=/usr/lib:/state/lib",
        ),
        Refused(LOADER_ENV),
        LINUX,
    ),
    form(
        "a directory target cannot reach the loader",
        Execve(
            FIXTURE,
            TARGET_ARGV,
            &[GUARD_PRELOAD, "LD_AUDIT=/state/evil.so"],
        ),
        NotRefused,
        LINUX,
    ),
    form(
        "null environ is an empty child environment",
        ExecveNullEnv(FIXTURE),
        Refused(MISSING_GUARD),
        LINUX,
    ),
    // Descriptor-relative resolution.
    form(
        "execveat resolves a dirfd-relative loader name",
        Execveat(LOADER_NAME, &["ld", UNKNOWN_OPTION, TARGET], GUARDED_ENV),
        Refused(MUTABLE_NATIVE),
        LINUX,
    ),
    form(
        "execveat dirfd-relative loader skips a value-taking option's value",
        Execveat(
            LOADER_NAME,
            &["ld", "--argv0", UNKNOWN_OPTION, TARGET],
            GUARDED_ENV,
        ),
        NotRefused,
        LINUX,
    ),
    // PATH resolution, read from the caller's own environment.
    form(
        "relative PATH entry is refused rather than searched",
        Execvp(PROBE, "relative/dir:/nonexistent-workcell-search-root", ""),
        Refused(RELATIVE_SEARCH_PATH),
        ANY,
    ),
    form(
        "chdir before PATH resolution: a dot entry is relative too",
        Execvp(PROBE, ".:/nonexistent-workcell-search-root", ""),
        Refused(RELATIVE_SEARCH_PATH),
        ANY,
    ),
    form(
        "overlong PATH component",
        Execvp(PROBE, "{overlong}", ""),
        Refused(OVERSIZED_INPUT),
        ANY,
    ),
    form(
        "empty name is not searched",
        Execvp("", "/nonexistent-workcell-search-root", ""),
        NotRefused,
        ANY,
    ),
    // Forms this lane cannot answer. Recorded, never asserted green.
    form(
        "env(1) short-option cluster ahead of a protected runtime",
        Execve("/usr/bin/env", &["env", "-iS", "codex"], GUARDED_ENV),
        Pending(ENV_CLUSTER_PENDING),
        ANY,
    ),
    form(
        "raw syscall(SYS_execve) entry",
        Execve(LOADER, &["ld", UNKNOWN_OPTION, TARGET], GUARDED_ENV),
        Pending(RAW_SYSCALL_PENDING),
        ANY,
    ),
];

/// Keeps a pending row from being added without being counted.
const PENDING_FORMS: usize = 3;

#[test]
fn loader_invocation_forms_reach_the_stated_classification() {
    let fixture = prepare_fixture();

    // Believe the matrix only after it observes a form the guard is known to
    // refuse. Without this, a harness that reaches nothing reports every row as
    // unrefused and the whole table passes for the wrong reason.
    let control = Execve(LOADER, &["ld", UNKNOWN_OPTION, TARGET], GUARDED_ENV);
    let observed = run(&control, &fixture);
    assert!(
        observed.contains(MUTABLE_NATIVE),
        "harness control: the guard must refuse an unknown loader option, saw {observed:?}"
    );

    let mut pending = 0usize;
    for form in FORMS {
        if let Pending(reason) = form.expect {
            println!("pending form {}: {reason}", form.name);
            pending += 1;
            continue;
        }
        if form.linux_only && !cfg!(target_os = "linux") {
            println!("skipping Linux-only form {}", form.name);
            continue;
        }

        let stderr = run(&form.call, &fixture);
        match form.expect {
            Refused(message) => assert!(
                stderr.contains(message),
                "form {}: expected {message:?}, saw {stderr:?}",
                form.name
            ),
            NotRefused => {
                if let Some(refusal) = FORM_SPECIFIC_REFUSALS
                    .iter()
                    .find(|message| stderr.contains(**message))
                {
                    panic!(
                        "form {}: expected no form-specific refusal, saw {refusal:?}",
                        form.name
                    );
                }
            }
            Pending(_) => unreachable!("pending rows are skipped above"),
        }
    }

    assert_eq!(
        pending, PENDING_FORMS,
        "a pending row was added or removed without updating PENDING_FORMS"
    );
    fs::remove_dir_all(&fixture).expect("remove the fixture directory");
}

/// The fixture directory holds files the rows execute, in a directory every
/// user can write. A name another user can predict is a check/use gap: it can
/// be pre-created, and the writes below would then land through symlinks that
/// user planted. The name carries 128 bits from the kernel, and the directory
/// is created exclusively and owner-only, so it is this process's alone and no
/// other user can place an entry inside it for a write to follow.
fn prepare_fixture() -> PathBuf {
    let mut random = [0u8; 16];
    File::open("/dev/urandom")
        .expect("open the kernel random source")
        .read_exact(&mut random)
        .expect("read the fixture directory name");
    let name: String = random.iter().map(|byte| format!("{byte:02x}")).collect();
    let fixture = std::env::temp_dir().join(format!("workcell-loader-forms-{name}"));
    fs::DirBuilder::new()
        .mode(0o700)
        .create(&fixture)
        .expect("create the fixture directory exclusively");
    fs::write(fixture.join("ld-linux-form-fixture.so.2"), []).expect("write the loader fixture");
    fs::write(
        fixture.join("not-an-exec-target"),
        "neither an ELF nor a shebang\n",
    )
    .expect("write the exec target fixture");
    let enoexec = fixture.join(ENOEXEC_TARGET);
    fs::write(&enoexec, "neither an ELF nor a shebang\n").expect("write the ENOEXEC fixture");
    // The PATH search only yields a candidate that answers X_OK.
    fs::set_permissions(&enoexec, fs::Permissions::from_mode(0o755))
        .expect("make the ENOEXEC fixture executable");
    fixture
}

fn expand(value: &str, fixture: &Path) -> String {
    let value = if value.contains("{overlong}") {
        value.replace(
            "{overlong}",
            &format!("/{}", "a".repeat(OVERLONG_SEGMENT_BYTES)),
        )
    } else {
        value.to_owned()
    };
    value.replace("{fixture}", &fixture.display().to_string())
}

fn c_string(value: &str, fixture: &Path) -> CString {
    CString::new(expand(value, fixture)).expect("fixture strings carry no interior NUL")
}

fn c_strings(values: &[&str], fixture: &Path) -> Vec<CString> {
    values
        .iter()
        .map(|value| c_string(value, fixture))
        .collect()
}

fn c_pointers(values: &[CString]) -> Vec<*const libc::c_char> {
    values
        .iter()
        .map(|value| value.as_ptr())
        .chain(std::iter::once(ptr::null()))
        .collect()
}

fn run(call: &Call, fixture: &Path) -> String {
    match call {
        Execve(path, argv, env) => {
            let path = c_string(path, fixture);
            let argv = c_strings(argv, fixture);
            let env = c_strings(env, fixture);
            let argv = c_pointers(&argv);
            let env = c_pointers(&env);
            capture_stderr(|| {
                // SAFETY: path is a live NUL-terminated CString; argv and env are live NULL-terminated pointer arrays that outlive the call.
                unsafe {
                    workcell_exec_guard::execve(path.as_ptr(), argv.as_ptr(), env.as_ptr());
                }
            })
        }
        ExecveNullEnv(path) => {
            let path = c_string(path, fixture);
            let argv = c_strings(TARGET_ARGV, fixture);
            let argv = c_pointers(&argv);
            capture_stderr(|| {
                // SAFETY: path is a live NUL-terminated CString; argv is a live NULL-terminated pointer array; a NULL envp is the documented empty-environment form.
                unsafe {
                    workcell_exec_guard::execve(path.as_ptr(), argv.as_ptr(), ptr::null());
                }
            })
        }
        Execveat(name, argv, env) => {
            let directory = c_string(FIXTURE, fixture);
            // SAFETY: directory is a live NUL-terminated CString; open only reads it.
            let dirfd = unsafe {
                libc::open(
                    directory.as_ptr(),
                    libc::O_RDONLY | libc::O_DIRECTORY | libc::O_CLOEXEC,
                )
            };
            assert!(dirfd >= 0, "open the fixture directory");
            let name = c_string(name, fixture);
            let argv = c_strings(argv, fixture);
            let env = c_strings(env, fixture);
            let argv = c_pointers(&argv);
            let env = c_pointers(&env);
            let stderr = capture_stderr(|| {
                // SAFETY: dirfd is an open directory descriptor; name, argv and env are live and NULL-terminated for the call.
                unsafe {
                    workcell_exec_guard::execveat(
                        dirfd,
                        name.as_ptr(),
                        argv.as_ptr(),
                        env.as_ptr(),
                        0,
                    );
                }
            });
            // SAFETY: dirfd was opened above, is still open, and is closed once.
            unsafe { libc::close(dirfd) };
            stderr
        }
        Execvp(file, path_value, extra_env) => {
            let (extra_key, extra_value) = extra_env.split_once('=').unwrap_or(("", ""));
            let restore = [
                ("PATH", std::env::var_os("PATH")),
                (extra_key, std::env::var_os(extra_key)),
            ];
            set_env("PATH", Some(expand(path_value, fixture).into()));
            set_env(extra_key, Some(extra_value.into()));
            let file = c_string(file, fixture);
            let argv = c_strings(&["probe"], fixture);
            let argv = c_pointers(&argv);
            let stderr = capture_stderr(|| {
                // SAFETY: file is a live NUL-terminated CString; argv is a live NULL-terminated pointer array that outlives the call.
                unsafe {
                    workcell_exec_guard::execvp(file.as_ptr(), argv.as_ptr());
                }
            });
            for (key, value) in restore {
                set_env(key, value);
            }
            stderr
        }
    }
}

/// The search rows have to change the caller's own environment: `execvp` reads
/// the caller's `PATH` and the caller's loader variables, never the child ones.
/// An empty key is the no-extra-variable case and is ignored.
fn set_env(key: &str, value: Option<OsString>) {
    if key.is_empty() {
        return;
    }
    // SAFETY: .cargo/config.toml forces RUST_TEST_THREADS=1 for this crate, so no other thread reads or writes the environment while this runs.
    unsafe {
        match value {
            Some(value) => std::env::set_var(key, value),
            None => std::env::remove_var(key),
        }
    }
}

/// The guard reports every refusal by writing to file descriptor 2 and returns
/// -1, so the message is the only thing that says which classification fired.
fn capture_stderr(body: impl FnOnce()) -> String {
    let mut pipe_fds = [0 as c_int; 2];
    // SAFETY: pipe_fds is a live two-element array; pipe writes both descriptors and nothing else.
    let created = unsafe { libc::pipe(pipe_fds.as_mut_ptr()) };
    assert_eq!(created, 0, "create a pipe");
    // SAFETY: STDERR_FILENO is open; dup returns a new descriptor owned here.
    let saved = unsafe { libc::dup(libc::STDERR_FILENO) };
    assert!(saved >= 0, "duplicate stderr");
    // SAFETY: both descriptors are open and owned here.
    let redirected = unsafe { libc::dup2(pipe_fds[1], libc::STDERR_FILENO) };
    assert!(redirected >= 0, "redirect stderr");

    body();

    // SAFETY: saved and the write end are open descriptors owned here, each closed once.
    unsafe {
        libc::dup2(saved, libc::STDERR_FILENO);
        libc::close(saved);
        libc::close(pipe_fds[1]);
    }
    // SAFETY: the read end is open, owned here, and handed to File exactly once.
    let mut read_end = unsafe { File::from_raw_fd(pipe_fds[0]) };
    let mut captured = String::new();
    read_end
        .read_to_string(&mut captured)
        .expect("read the captured stderr");
    captured
}
