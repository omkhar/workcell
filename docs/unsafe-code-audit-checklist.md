# Unsafe-Code Pre-Audit Checklist

Workcell's security boundary depends on a small, deliberately contained body of
Rust `unsafe` code: the container-side exec/syscall interception shim
(`runtime/container/rust/src/lib.rs`) and the launcher binaries
(`runtime/container/rust/src/bin/`). This checklist is the pre-audit reference
for reviewing that surface — before a release, a dependency bump that touches
libc, or an external security audit.

## Enforcement in place

- Every `unsafe {}` block carries a `// SAFETY:` comment stating the invariant
  that makes it sound. This is enforced by
  `undocumented_unsafe_blocks = "deny"` in
  `runtime/container/rust/Cargo.toml`, checked by
  `cargo clippy --all-targets --locked --offline -- -D warnings` in
  `scripts/validate-repo.sh`. A new undocumented `unsafe` block fails CI.
- `#![deny(unsafe_op_in_unsafe_fn)]` (`lib.rs`) forces every operation inside an
  `unsafe fn` into an explicit `unsafe {}` block, so no unsafe operation is
  implicitly covered by the function signature.

## Unsafe surface inventory

The unsafe code falls into a few invariant classes. An auditor should confirm
each block still belongs to its class and that the class invariant holds.

| Class | What it does | Invariant to confirm |
|---|---|---|
| Niladic syscalls (`getpid`, `getppid`, `fork`) | no-argument syscalls | no preconditions; return value handled |
| C-string ABI reads (`CStr::from_ptr`, `*ptr.add`) | reading intercepted `argv`/`envp`/path pointers | pointer is null-checked and points to a NUL-terminated string / NUL-sentinel array per the exec ABI |
| `static mut environ` reads | reading libc's global env when no `envp` given | read in the calling thread with no concurrent `setenv`/`putenv` |
| `dlsym(RTLD_NEXT)` + `transmute_copy` | resolving the next-chain libc symbol into a fn pointer | `T` is a pointer-sized `extern "C"` fn whose ABI matches the named symbol |
| Syscall trampoline (`workcell_syscall_shim`) | entered from hand-written `global_asm!` | asm preserves the `syscall(long, ...)` ABI; per-number arg meanings |
| LD_PRELOAD interposers + tail forwards | intercept then forward original args to real libc | args obey the interposed function's C ABI; forwarded unmodified |
| Env mutation (`env::set_var`/`remove_var`) | Rust 2024 unsafe env writes | runs single-threaded before any thread/child spawns |
| Signal-handler `kill` / `sigaction` setup | async-signal-safe teardown | only async-signal-safe calls; PID read via atomic |

## High-scrutiny items (subtle invariants — review these first)

1. **`workcell_syscall_shim` (`lib.rs`)** — safety is upheld by the
   `global_asm!` trampoline, not by any Rust caller, and it reads all six
   register args regardless of the variadic count. A change to the asm or to the
   per-syscall arg handling must be re-audited against the kernel ABI.
2. **`load_symbol` `transmute_copy` (`lib.rs`)** — `transmute_copy` skips the
   size check; soundness rests on every instantiation of `T` being a
   pointer-sized `extern "C"` fn pointer matching the named C symbol. Adding a
   new interposer means adding a matching `Fn` type alias + `c"name"` pair.
3. **`static mut environ` reads** — a shared mutable global; sound only absent
   concurrent `setenv`/`putenv`, consistent with libc `getenv` semantics.
4. **`env::set_var`/`remove_var` in the launchers** — the "no data race"
   invariant holds only if these run before any thread is spawned. Confirm caller
   ordering; note the default multithreaded test runner is the weak spot for the
   `#[cfg(test)]` uses.

## Pre-audit checklist

- [ ] `cargo clippy … -D warnings` is green (zero `undocumented_unsafe_blocks`).
- [ ] `#![deny(unsafe_op_in_unsafe_fn)]` is still present in `lib.rs`.
- [ ] Every `unsafe {}` block's `// SAFETY:` comment still matches the code it
      guards (no block was edited without updating its invariant).
- [ ] No new `unsafe` construct falls outside the classes above; if it does, it
      is documented and added to this inventory.
- [ ] The high-scrutiny items are unchanged, or their changes were re-audited
      against the relevant ABI (kernel syscall, libc symbol, signal-safety).
- [ ] Any new LD_PRELOAD interposer pairs a `c"symbol"` literal with a matching
      `extern "C" fn` type alias, and forwards the caller's original arguments
      unmodified.
- [ ] `libc` crate version bumps are reviewed for changed signatures on the
      intercepted symbols.
