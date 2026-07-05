#![no_main]
// Fuzzes the exec-guard path validation / classification surface:
//   * classify_protected_runtime_path — maps a resolved path to the protected
//     runtime it represents.
//   * path_points_to_dynamic_loader — recognises the ld.so dynamic loader.
//   * classify_loader_target — classifies `ld.so <interp> <program>` argv
//     shapes so the loader cannot be used to launch a protected runtime.
// Invariant asserted: for any UTF-8 input these classifiers must not panic; a
// "not protected" / `false` answer is the expected outcome for junk input.

use libfuzzer_sys::fuzz_target;
use workcell_exec_guard::fuzz_api;

fuzz_target!(|data: &[u8]| {
    let Ok(s) = std::str::from_utf8(data) else {
        return;
    };
    fuzz_api::classify_protected_runtime_path(s);
    let _ = fuzz_api::path_points_to_dynamic_loader(s);
    // NUL-split the same bytes into an argv vector so the loader classifier
    // sees realistic multi-arg `ld.so` invocations.
    let args: Vec<String> = s.split('\0').map(String::from).collect();
    fuzz_api::classify_loader_target(s, &args);
});
