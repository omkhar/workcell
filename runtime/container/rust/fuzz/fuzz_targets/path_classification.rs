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
    // NUL-split into an argv vector and classify with the FIRST component as the
    // executed loader path (as the guard sees it), so realistic
    // `ld.so <interp> <program>` shapes are actually exercised instead of the
    // whole blob (whose trailing component would hide the loader).
    let args: Vec<String> = s.split('\0').map(String::from).collect();
    let loader_path = args.first().map_or("", String::as_str);
    fuzz_api::classify_loader_target(loader_path, &args);
});
