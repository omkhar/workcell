# Unsafe-Code Pre-Audit Checklist

This checklist covers Rust `unsafe` code in the syscall shim and launcher
binaries.

Use it before a release, a relevant `libc` update, or an external audit.

## Scope

The first-party Rust surface is in these paths:

- `runtime/container/rust/src/lib.rs`
- `runtime/container/rust/src/bin/`

Each `unsafe {}` block and `unsafe extern` block must have a safety comment.
The manual audit must map every unsafe site to one class below.

## Automated checks

Run the locked offline Clippy command:

```sh
cargo clippy --manifest-path runtime/container/rust/Cargo.toml \
  --all-targets --locked --offline -- -D warnings
```

`runtime/container/rust/Cargo.toml` sets
`undocumented_unsafe_blocks = "deny"`. Thus, Clippy rejects an `unsafe {}` block
that has no `// SAFETY:` comment.

Clippy does not check `unsafe extern "C"` blocks. `scripts/validate-repo.sh`
requires a `// SAFETY:` comment immediately before each block.

`lib.rs` also sets `#![deny(unsafe_op_in_unsafe_fn)]`. Each unsafe operation in
an unsafe function must use an explicit unsafe block.

These checks do not confirm that the inventory is complete. The manual review
must map each first-party unsafe site to one class below.

## Unsafe classes

| Class | Operations | Required invariant |
|---|---|---|
| Niladic system call | `getpid`, `getppid` | The call has no pointer input. The code handles its result. |
| C pointer and string | `CStr::from_ptr`, pointer offsets, `getauxval` result | The code checks required pointers. Each string ends with NUL. Each array has a NUL sentinel. |
| Path access | `access` | The path is a live NUL-terminated string. The code handles the result. |
| FFI output buffer | `fstatat`, `MaybeUninit::assume_init` | The C call succeeds before Rust reads initialized output. |
| File-descriptor lifecycle | `openat`, `close` | The code checks the new descriptor and closes it exactly once. |
| Process lifecycle FFI | `execve`, `fork`, `_exit`, `waitpid` | The launcher is single-threaded at `fork`. Arguments match the C ABI. Parent and child paths handle ownership correctly. |
| Error report FFI | `write`, `__errno_location`, `__error` | The buffer is valid. The code sets the thread-local error value to `EPERM`. |
| Global environment read | `static mut environ` | No concurrent code changes the process environment. |
| Dynamic symbol load | `dlsym(RTLD_NEXT)`, `transmute_copy` | The function type has pointer size and matches the named C symbol. |
| Unsafe export | `#[unsafe(no_mangle)]` | The symbol has an intended export or interposition target. Its ABI matches that target. No unintended collision exists. |
| System-call trampoline | `workcell_syscall_shim`, `global_asm!` | Assembly preserves the Linux `syscall(long, ...)` ABI. Each system-call number uses the correct arguments. |
| Preload interposer | LD_PRELOAD wrappers and tail calls | The wrapper matches the C ABI. It passes the original arguments to libc. |
| Environment change | `env::set_var`, `env::remove_var` | The launcher changes the environment before it creates a thread or child process. |
| Test environment change | Test-only `env::remove_var` | The test has no concurrent environment access. |
| Signal setup and teardown | `kill`, `sigaction`, `sigemptyset`, `std::mem::zeroed` | The handler uses async-signal-safe calls. Structures and handlers match the C ABI. |

This map assigns each first-party site group to an inventory class:

| File and function group | Inventory class |
|---|---|
| `lib.rs`: process path and file-descriptor helpers | Niladic system call, path access, C pointer and string, FFI output buffer |
| `lib.rs`: `report` and `errno_location` | Error report FFI |
| `lib.rs`: `load_symbol` and symbol accessors | Dynamic symbol load |
| `lib.rs`: `workcell_syscall_shim` | Unsafe export, system-call trampoline |
| `lib.rs`: exported `exec*` and `posix_spawn*` functions | Unsafe export, preload interposer, C pointer and string, global environment read, FFI output buffer, file-descriptor lifecycle |
| `bin/common/launcher_common.rs`: environment helpers | Environment change, global environment read |
| `bin/common/launcher_common.rs`: `exec_request` | Process lifecycle FFI, global environment read |
| `bin/common/launcher_common.rs`: signal helpers | Signal setup and teardown |
| `bin/common/launcher_common.rs`: `spawn_and_wait_request` | Process lifecycle FFI, global environment read, signal setup and teardown |
| `bin/workcell-launcher.rs`: `current_execfn` | C pointer and string |
| `bin/workcell-launcher.rs`: environment cleanup test | Test environment change |

Give special attention to these sites:

- `workcell_syscall_shim` reads all six register arguments. Review the assembly
  and system-call behavior together.
- `load_symbol` uses `transmute_copy`. Each type must have pointer size and
  match the named C function signature.
- `static mut environ` is shared mutable state. Confirm that no concurrent code
  calls `setenv` or `putenv`.
- Launcher environment changes must occur before thread or child creation.
- `fstatat` output stays uninitialized until the C call succeeds.
- `fork` creates two control paths. Confirm all `_exit`, `execve`, and `waitpid`
  results and ownership rules.
- The post-`fork` child formats errors before `_exit`. Confirm the launcher stays
  single-threaded and no unsafe shared state is active at `fork`.

## Manual release checklist

- [ ] Run the locked offline Clippy command. Confirm exit code 0.
- [ ] Confirm that each `unsafe {}` block has an accurate `// SAFETY:` comment.
- [ ] Confirm that each `unsafe extern` block has a `// SAFETY:` comment immediately before it.
- [ ] Confirm that `#![deny(unsafe_op_in_unsafe_fn)]` remains in `lib.rs`.
- [ ] List each first-party unsafe site with `rg -n '\bunsafe\b' runtime/container/rust/src`.
- [ ] Map each unsafe site to one class in this inventory.
- [ ] Add each new unsafe class to this inventory.
- [ ] Compare each changed interposer signature with the libc signature for that symbol.
- [ ] Confirm that each interposer passes the original arguments to libc.
- [ ] Review changes to the system-call trampoline against the Linux ABI.
- [ ] Review each `libc` version change for affected signatures.
- [ ] Confirm that each launcher changes environment variables before it creates a thread.
- [ ] Confirm that each environment test has no concurrent environment access.
- [ ] Confirm that Rust reads FFI output buffers only after the C call initializes them successfully.
- [ ] Confirm that each process lifecycle path handles errors and child ownership.
