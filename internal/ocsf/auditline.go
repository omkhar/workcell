// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

import (
	"fmt"
	"strings"

	"github.com/omkhar/workcell/internal/applecontainer"
)

// Audit records are written by TWO different encoders depending on which target
// ran the session, and each session's log is written by exactly one of them (the
// bash launcher's backends — colima/docker-desktop/aws-ec2-ssm/gcp-vm — never
// share a log with the Go AppleContainer target). F1 selects the decoder per
// session by the record's target_provider (auditEncodingForProvider):
//
//   - encodingBashQuote — scripts/workcell's append_audit_record_to_path writes
//     each token with bash `printf '%q '`, so a value with spaces or control
//     bytes is escaped (spaces → `\ `, newlines → the ANSI-C `$'...'` form). The
//     launch record's `endpoints=` field is a SPACE-DELIMITED allowlist
//     (ALLOW_ENDPOINTS), so a naive strings.Fields split would truncate it at the
//     first space. The authoritative encoder is bashQuote in
//     internal/publishpr/host_exec.go; splitQuotedTokens is its exact inverse and
//     is validated against real `bash printf %q` in the tests.
//   - encodingPercentPath — internal/applecontainer writes plain Sprintf lines
//     (single-space delimited) and percent-encodes only its path fields via
//     encodeAuditPathValue (spaces → %20), leaving a literal backslash in a legal
//     POSIX path UNCHANGED. Running such a line through the `%q` decoder would
//     treat `\` as an escape and DROP it (`/tmp/a\b` → `/tmp/ab`), corrupting the
//     workspace evidence. This path tokenizes with strings.Fields and percent-
//     decodes the encoded fields via the canonical applecontainer.DecodeAuditPathValue.
//
// If the source cannot be identified as AppleContainer, the bash `%q` decoder is
// used (the launcher backends are the common case); the percent decoder is only
// selected on the unambiguous apple-container provider.

// auditEncoding selects which on-disk audit encoding a record uses.
type auditEncoding int

const (
	// encodingBashQuote is scripts/workcell's bash `printf %q` encoding.
	encodingBashQuote auditEncoding = iota
	// encodingPercentPath is internal/applecontainer's percent-encoded path form.
	encodingPercentPath
)

// percentEncodedAuditFields are the audit field keys the AppleContainer writer
// percent-encodes (encodeAuditPathValue in internal/applecontainer/recovery.go).
// Only these are percent-decoded on read-back; every other field is raw Sprintf,
// so decoding it could corrupt a legitimate literal `%` — keep the inverse exact.
var percentEncodedAuditFields = map[string]struct{}{
	"workspace":        {},
	"workspace_origin": {},
}

// auditEncodingForProvider selects the audit decoder for a session from its
// record's target_provider. The Go AppleContainer target (provider
// "apple-container") writes percent-encoded path fields; every launcher backend
// writes bash `%q`. The apple-container match is exact and drift-proof (the
// canonical provider constant), and the safe default for anything else is the
// bash `%q` decoder.
func auditEncodingForProvider(provider string) auditEncoding {
	if strings.TrimSpace(provider) == applecontainer.Provider {
		return encodingPercentPath
	}
	return encodingBashQuote
}

// knownAuditFields is the set of audit field NAMES the writers actually emit.
// The OCSF mapping only turns these into typed `audit.<key>` unmapped
// properties; any other key (an audit line is mutable text, so a tampered record
// could carry a secret-shaped key) is bucketed under one fixed redacted property
// instead of becoming a JSON property name of its own.
//
// Derived from the AUDIT writers only, not hand-curated. Regenerate from the
// bash audit-append functions (NOT all of scripts/workcell — the whole file also
// contains write_session_record's git_branch/git_head/git_base SessionRecord
// fields and the core.*/git_* alias-guard substrings at ~6775-6778, none of
// which are audit-line keys) plus the apple-container Go writers:
//
//	{ awk '/^append_(launch|exit|session_control)_audit_record\(\)/{f=1} f&&/^}/{f=0}
//	      f' scripts/workcell | grep -hoE '"[a-z_0-9]+=' | sed 's/"//;s/=//';
//	  grep -hoE '[a-z_]+=%s' internal/applecontainer/recovery.go \
//	    internal/applecontainer/target_session.go | sed 's/=%s//';
//	  printf 'timestamp\nts\nprev_digest\nrecord_digest\nmaterialization_id\naccess_model\nbootstrap_id\nimage_ref\nv\n'; } |
//	  sort -u | grep -vE '^(0|1)$'
//
// (the last printf adds the framing keys the append_audit_record_to_path wrapper
// stamps plus the apple-container schema sentinel `v` from auditLineSentinel).
// A new legitimate writer key not yet listed degrades safely to the redacted
// bucket, not a leak.
var knownAuditFields = map[string]struct{}{
	"access_model":                          {},
	"agent":                                 {},
	"agent_autonomy":                        {},
	"argv":                                  {},
	"audit_log_path":                        {},
	"autonomy_assurance":                    {},
	"bootstrap_applied":                     {},
	"bootstrap_endpoints":                   {},
	"bootstrap_id":                          {},
	"cache_assurance":                       {},
	"cache_profile":                         {},
	"codex_rules_assurance_configured":      {},
	"codex_rules_assurance_effective_final": {},
	"codex_rules_assurance_effective_initial":  {},
	"codex_rules_mutability_configured":        {},
	"codex_rules_mutability_effective_final":   {},
	"codex_rules_mutability_effective_initial": {},
	"command":                            {},
	"container_assurance":                {},
	"container_name":                     {},
	"current_assurance":                  {},
	"debug_log_enabled":                  {},
	"debug_log_path":                     {},
	"endpoints":                          {},
	"event":                              {},
	"execution_path":                     {},
	"exit_status":                        {},
	"file_trace_log_path":                {},
	"final_assurance":                    {},
	"finished_at":                        {},
	"force":                              {},
	"github_auth_present":                {},
	"image_ref":                          {},
	"initial_assurance":                  {},
	"injection_credential_keys":          {},
	"injection_policy_sha256":            {},
	"injection_secret_copy_targets":      {},
	"injection_ssh_enabled":              {},
	"live_status":                        {},
	"log_level":                          {},
	"materialization_id":                 {},
	"mode":                               {},
	"monitor_pid":                        {},
	"network_policy":                     {},
	"observability_assurance":            {},
	"observed_at":                        {},
	"package_mutation_downgraded":        {},
	"prepare":                            {},
	"prev_digest":                        {},
	"profile":                            {},
	"provider_auth_mode":                 {},
	"provider_auth_modes":                {},
	"provider_native_sandbox_configured": {},
	"provider_native_sandbox_effective":  {},
	"provider_native_sandbox_reason":     {},
	"reason":                             {},
	"record_digest":                      {},
	"runtime_api":                        {},
	"session_assurance_final":            {},
	"session_assurance_initial":          {},
	"session_audit_dir":                  {},
	"session_id":                         {},
	"shared_auth_modes":                  {},
	"source":                             {},
	"ssh_config_assurance":               {},
	"started_at":                         {},
	"status":                             {},
	"stdin_mode":                         {},
	"target_assurance_class":             {},
	"target_id":                          {},
	"target_kind":                        {},
	"target_provider":                    {},
	"timestamp":                          {},
	"transcript_log_path":                {},
	"transcript_logging":                 {},
	"transport_status":                   {},
	"ts":                                 {},
	"ui":                                 {},
	// v is the AppleContainer schema-version sentinel (auditLineSentinel = " v=1"
	// in internal/applecontainer/session_helpers.go), appended to EVERY rendered
	// apple-container audit line — a legitimate field, not a tampered key.
	"v":                       {},
	"workspace":               {},
	"workspace_control_plane": {},
	"workspace_origin":        {},
	"workspace_root":          {},
	"workspace_transport":     {},
	"worktree_path":           {},
}

// auditField is one ordered key/value pair decoded from an audit record.
type auditField struct {
	key   string
	value string
}

// decodeAuditLine tokenizes an audit line into ordered, un-escaped key/value
// fields using the encoding the session's writer produced. It REJECTS a record
// that carries a duplicate key (fail-closed) under either encoding: a tampered
// line such as `session_id=A session_id=B` must be detected rather than silently
// mapped to first- or last-wins, because the OCSF export is evidence. Tokens
// without an '=' are tolerated and skipped, matching the existing readers'
// tolerance for torn crash lines.
func decodeAuditLine(line string, enc auditEncoding) ([]auditField, error) {
	var tokens []string
	if enc == encodingPercentPath {
		// Percent-encoded lines never carry an unescaped space (spaces become
		// %20), so plain whitespace splitting is a faithful tokenizer and a
		// literal backslash in a path is preserved verbatim.
		tokens = strings.Fields(line)
	} else {
		var err error
		tokens, err = splitQuotedTokens(line)
		if err != nil {
			return nil, err
		}
	}
	fields := make([]auditField, 0, len(tokens))
	seen := make(map[string]struct{}, len(tokens))
	for _, tok := range tokens {
		key, value, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		if _, dup := seen[key]; dup {
			return nil, fmt.Errorf("audit record has duplicate key %q", key)
		}
		seen[key] = struct{}{}
		if enc == encodingPercentPath {
			if _, isPath := percentEncodedAuditFields[key]; isPath {
				value = applecontainer.DecodeAuditPathValue(value)
			}
		}
		fields = append(fields, auditField{key: key, value: value})
	}
	return fields, nil
}

// splitQuotedTokens splits a bash `%q`-encoded line into decoded tokens,
// breaking on UNESCAPED whitespace. Backslash escapes the next rune literally
// (so `\ ` is a non-splitting space); `$'...'` is decoded as an ANSI-C block;
// the bare two-single-quote empty-string form yields an empty token. It is the inverse of
// bashQuote.
func splitQuotedTokens(line string) ([]string, error) {
	var (
		tokens  []string
		cur     strings.Builder
		inToken bool
	)
	runes := []rune(line)
	for i := 0; i < len(runes); {
		r := runes[i]
		switch {
		case r == ' ' || r == '\t':
			if inToken {
				tokens = append(tokens, cur.String())
				cur.Reset()
				inToken = false
			}
			i++
		case r == '\\':
			inToken = true
			if i+1 >= len(runes) {
				// Trailing backslash on a torn line: keep it literally.
				cur.WriteRune('\\')
				i++
				continue
			}
			cur.WriteRune(runes[i+1])
			i += 2
		case r == '$' && i+1 < len(runes) && runes[i+1] == '\'':
			decoded, next, err := decodeAnsiC(runes, i+2)
			if err != nil {
				return nil, err
			}
			cur.WriteString(decoded)
			inToken = true
			i = next
		case r == '\'' && i+1 < len(runes) && runes[i+1] == '\'':
			// bash `%q` empty-string form: an empty token.
			inToken = true
			i += 2
		default:
			cur.WriteRune(r)
			inToken = true
			i++
		}
	}
	if inToken {
		tokens = append(tokens, cur.String())
	}
	return tokens, nil
}

// decodeAnsiC decodes the body of a bash ANSI-C `$'...'` block starting at
// runes[start], returning the decoded string and the index just past the
// closing quote. It handles the ANSI-C escapes bash `%q` emits for control
// bytes: the named forms \a \b \e \f \n \r \t \v, the literal \\ and \', and the
// three-digit octal \ooo used for any other non-printable byte. (Without the
// named-control cases, a byte such as BEL would arrive as `$'\a'` and decode to
// the literal letter "a", silently corrupting the exported evidence.) An
// unterminated block is an error (a corrupt record), keeping decode fail-closed.
func decodeAnsiC(runes []rune, start int) (string, int, error) {
	var b strings.Builder
	for i := start; i < len(runes); {
		r := runes[i]
		if r == '\'' {
			return b.String(), i + 1, nil
		}
		if r == '\\' && i+1 < len(runes) {
			n := runes[i+1]
			switch n {
			case 'a':
				b.WriteRune('\a') // BEL 0x07
				i += 2
			case 'b':
				b.WriteRune('\b') // BS 0x08
				i += 2
			case 'e', 'E':
				b.WriteRune('\x1b') // ESC 0x1b (bash emits \e; \E accepted too)
				i += 2
			case 'f':
				b.WriteRune('\f') // FF 0x0c
				i += 2
			case 'n':
				b.WriteRune('\n')
				i += 2
			case 'r':
				b.WriteRune('\r')
				i += 2
			case 't':
				b.WriteRune('\t')
				i += 2
			case 'v':
				b.WriteRune('\v') // VT 0x0b
				i += 2
			case '\\':
				b.WriteRune('\\')
				i += 2
			case '\'':
				b.WriteRune('\'')
				i += 2
			default:
				if isOctal(n) && i+3 < len(runes) && isOctal(runes[i+2]) && isOctal(runes[i+3]) {
					val := (int(n)-'0')*64 + (int(runes[i+2])-'0')*8 + (int(runes[i+3]) - '0')
					// Octal escapes are raw BYTES, not runes: under LC_ALL=C bash %q
					// emits a non-ASCII value as one \ooo per UTF-8 byte (é → \303\251).
					// WriteByte reassembles the original multibyte sequence; WriteRune
					// would re-encode each byte 0xC3/0xA9 as its own rune ("Ã©").
					b.WriteByte(byte(val))
					i += 4
				} else {
					b.WriteRune(n)
					i += 2
				}
			}
			continue
		}
		b.WriteRune(r)
		i++
	}
	return "", 0, fmt.Errorf("unterminated $'...' quote in audit record")
}

func isOctal(r rune) bool { return r >= '0' && r <= '7' }
