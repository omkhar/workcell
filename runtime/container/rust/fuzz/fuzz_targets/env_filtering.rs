#![no_main]
// Fuzzes the exec-guard environment-filtering surface:
//   * path_from_env_entries — extracts the effective PATH from a `KEY=VALUE`
//     environ snapshot.
//   * env_has_unsafe_git_override — fail-closed scan for ambient Git overrides
//     (GIT_CONFIG_PARAMETERS, GIT_OBJECT_DIRECTORY, core.fsmonitor, ...).
//   * resolve_command_via_path_value — resolves a bare command against a PATH
//     value the way execvp would.
// Invariant asserted: for any UTF-8 input these parsers must not panic.

use libfuzzer_sys::fuzz_target;
use workcell_exec_guard::fuzz_api;

fuzz_target!(|data: &[u8]| {
    let Ok(s) = std::str::from_utf8(data) else {
        return;
    };
    // NUL is the real environ entry separator, so split on it to reconstruct a
    // `KEY=VALUE` environ snapshot from the fuzz bytes.
    let env_entries: Vec<String> = s.split('\0').map(String::from).collect();

    let path_value = fuzz_api::path_from_env_entries(&env_entries);
    let _ = fuzz_api::env_has_unsafe_git_override(&env_entries);

    // Derive a command to resolve from the first entry and feed the extracted
    // PATH back in, exercising resolve_command_via_path_value end to end.
    let command = env_entries.first().map(String::as_str).unwrap_or("");
    let _ = fuzz_api::resolve_command_via_path_value(command, path_value.as_deref());
});
