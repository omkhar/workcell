// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

// authUsageText is the canonical help text for `workcell auth <cmd>`,
// migrated verbatim from scripts/workcell's auth_usage() function.
const authUsageText = `Usage: workcell auth init [options]
       workcell auth set [options]
       workcell auth unset [options]
       workcell auth status [options]

Commands:
  init
    --injection-policy PATH     Policy file to create or validate (default: ~/.config/workcell/injection-policy.toml)
    --managed-root PATH         Managed credential root (default: ~/.config/workcell/credentials)

  set
    --injection-policy PATH     Policy file to update (default: ~/.config/workcell/injection-policy.toml)
    --managed-root PATH         Managed credential root (default: ~/.config/workcell/credentials)
    --agent codex|claude|gemini Agent used for validation and default shared-credential scoping
    --credential KEY            Credential key to configure
    --source PATH               Copy PATH into the managed Workcell credential store
    --resolver NAME             Configure a built-in host resolver
    --ack-host-resolver         Required with --resolver

  unset
    --injection-policy PATH     Policy file to update (default: ~/.config/workcell/injection-policy.toml)
    --managed-root PATH         Managed credential root (default: ~/.config/workcell/credentials)
    --credential KEY            Credential key to remove

  status
    --injection-policy PATH     Policy file to inspect (default: ~/.config/workcell/injection-policy.toml)
    --agent codex|claude|gemini Filter to the selected agent
    --mode strict|development|build|breakglass
    --workspace PATH            Accepted for CLI symmetry; ignored by host status

Notes:
  - auth commands run on the host and do not start the Workcell runtime.
  - auth set/unset rewrite only the entrypoint policy file; if a credential is
    declared by an included fragment, update that fragment directly.
  - resolver-backed credentials remain host-side preprocessing and do not
    grant Keychain or provider-home access inside Tier 1.
  - auth status reports provider_bootstrap_* summary fields for the selected
    agent on the reviewed host-owned path.
`

// policyUsageText is the canonical help text for `workcell policy <cmd>`,
// migrated verbatim from scripts/workcell's policy_usage() function.
const policyUsageText = `Usage: workcell policy show [options]
       workcell policy validate [options]
       workcell policy diff [options]

Commands:
  show
    --injection-policy PATH     Policy file to render after include expansion

  validate
    --injection-policy PATH     Policy file to validate

  diff
    --injection-policy PATH     Policy file to compare against the canonical merged view

Notes:
  - policy commands run on the host and do not start the Workcell runtime.
  - when --injection-policy is omitted, commands use the host default policy path.
  - show renders the merged effective policy after include expansion.
  - diff compares the selected entrypoint policy file against that canonical
    merged effective view.
  - validate fails closed when the selected policy file is missing or invalid.
  - validate checks source-backed credential paths, but resolver-backed launch
    readiness remains deferred to the launch path.
`

// AuthUsageText returns the canonical `workcell auth` help string.
func AuthUsageText() string {
	return authUsageText
}

// PolicyUsageText returns the canonical `workcell policy` help string.
func PolicyUsageText() string {
	return policyUsageText
}
