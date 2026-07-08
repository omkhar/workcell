// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

import (
	"fmt"
	"strings"
)

// Audit records are written by scripts/workcell's append_audit_record_to_path
// with bash `printf '%q '` per token, so a value containing spaces or control
// bytes is escaped (spaces → `\ `, newlines → the ANSI-C `$'...'` form). The
// launch record's `endpoints=` field is a SPACE-DELIMITED allowlist
// (ALLOW_ENDPOINTS in scripts/workcell), so a naive strings.Fields split would
// truncate it at the first space. The repo's authoritative encoder is
// bashQuote in internal/publishpr/host_exec.go (documented as the bash-3.2 form
// scripts/workcell runs under); no matching decoder existed because the other
// audit readers (auditLineEvent, auditLineHasSessionID) only inspect the
// space-free event/session_id keys. decodeAuditLine is the exact inverse of
// that encoder and is validated against real `bash printf %q` in the tests.
//
// NOTE: this reverses the bash `%q` encoding only. Path values written by the
// Go audit writer's percent-encoding (encodeAuditPathValue in
// internal/applecontainer/audit.go) pass through as literals — they are a
// separate writer's concern and out of F1's scope; conflating the two would
// risk double-decoding.

// auditField is one ordered key/value pair decoded from an audit record.
type auditField struct {
	key   string
	value string
}

// decodeAuditLine tokenizes a `%q`-encoded audit line into ordered, un-escaped
// key/value fields. It REJECTS a record that carries a duplicate key
// (fail-closed): a tampered line such as `session_id=A session_id=B` must be
// detected rather than silently mapped to first- or last-wins, because the OCSF
// export is evidence. Tokens without an '=' are tolerated and skipped, matching
// the existing readers' tolerance for torn crash lines.
func decodeAuditLine(line string) ([]auditField, error) {
	tokens, err := splitQuotedTokens(line)
	if err != nil {
		return nil, err
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
