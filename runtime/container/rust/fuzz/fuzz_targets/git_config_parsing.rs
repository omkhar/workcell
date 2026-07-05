#![no_main]
// Fuzzes the exec-guard git-config parsing surface:
//   * git_config_spec_is_blocked — decides whether a `key=value` (or bare key)
//     `-c` / GIT_CONFIG_PARAMETERS spec must be blocked.
//   * git_config_key_is_blocked — the key-only blocklist check.
//   * git_config_spec_value_is_explicit_safe — the narrow allowlist that lets
//     an otherwise-blocked key through only for an explicitly safe value.
// Invariant asserted: for any UTF-8 input these parsers must not panic.

use libfuzzer_sys::fuzz_target;
use workcell_exec_guard::fuzz_api;

fuzz_target!(|data: &[u8]| {
    let Ok(spec) = std::str::from_utf8(data) else {
        return;
    };
    let _ = fuzz_api::git_config_spec_is_blocked(spec);

    // A git-config spec is `key=value`; split on the first `=` so the key-level
    // checks see the same key/value decomposition the spec parser derives.
    let (key, value) = match spec.split_once('=') {
        Some((k, v)) => (k, v),
        None => (spec, ""),
    };
    let _ = fuzz_api::git_config_key_is_blocked(key);
    let _ = fuzz_api::git_config_spec_value_is_explicit_safe(key, value);
});
