// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportbundle

import (
	"regexp"
	"strings"
)

// RedactionPolicyVersion versions the documented redaction rule-set. Bump it
// whenever a rule in redactionRules (or the patterns below) changes so a bundle
// self-describes the guarantees it was produced under.
const RedactionPolicyVersion = "1"

// redactionRules is the human-readable rule-set embedded in every bundle and
// mirrored in SUPPORT.md. Each string is one guarantee the collector upholds.
var redactionRules = []string{
	"credential file contents are never read; only path, presence, and size/mtime metadata are recorded",
	"workspace and agent output are never collected; log bodies are referenced by pointer only",
	"token, key, password, secret, and credential material is masked by pattern (JWT, GitHub/OpenAI/Google/AWS/Slack tokens, PEM private keys, Bearer headers) and by secret-named key=value pairs",
	"the operator home-directory prefix is rewritten to ~ so the local username never leaks through paths",
	"only structured, enumerated diagnostic fields are emitted; no raw environment dumps or command output blobs",
}

// RedactionRules returns a copy of the documented redaction rule-set.
func RedactionRules() []string {
	out := make([]string, len(redactionRules))
	copy(out, redactionRules)
	return out
}

// The token/secret patterns. Order in secretReplacers is load-bearing: the
// multi-line PEM block and the secret-named key=value rule run before the
// single-token formats so a `token=ghp_...` value is masked as a unit and the
// residue never re-matches a narrower rule.
var (
	pemPrivateKeyRe = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	bearerRe        = regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/-]+=*`)
	// keyValueSecretRe masks the value after a secret-named key. The key name
	// must strongly indicate a secret; bare "auth" is intentionally excluded
	// so diagnostic fields like auth_status stay legible.
	// Key names: the secret-bearing stems (password/secret/token/credential/
	// passphrase) may appear anywhere in the identifier, so compound names like
	// secret_key, SECRET_KEY_BASE, or app_password_field match; plus the explicit
	// *_key forms whose stem is not itself secret-named. Bare "auth" and generic
	// "*_key" (e.g. public_key, cache_key) are deliberately excluded so
	// diagnostic fields stay legible.
	// Values: a double- or single-quoted string (honoring backslash escapes, so
	// password="abc\"def" is masked whole) or a bare unquoted token.
	keyValueSecretRe = regexp.MustCompile(`(?i)((?:[a-z0-9_.-]*(?:password|passwd|passphrase|secret|token|credential)[a-z0-9_.-]*|(?:api|access|private|session)[_-]?key)"?\s*[:=]\s*)("(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|[^\s"',]+)`)
	jwtRe            = regexp.MustCompile(`eyJ[A-Za-z0-9_=-]+\.[A-Za-z0-9_=-]+\.[A-Za-z0-9_=-]*`)
	githubTokenRe    = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,255}`)
	githubPatRe      = regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,255}`)
	slackTokenRe     = regexp.MustCompile(`(?:xox[baprs]|xapp)-[A-Za-z0-9-]{10,}`)
	awsKeyRe         = regexp.MustCompile(`(?:AKIA|ASIA)[0-9A-Z]{16}`)
	openaiKeyRe      = regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)
	googleKeyRe      = regexp.MustCompile(`AIza[0-9A-Za-z_-]{20,}`)
)

type secretReplacer struct {
	re   *regexp.Regexp
	repl string
}

// secretReplacers with a static replacement string (no capture groups).
// pemPrivateKeyRe is NOT here: it runs before the key=value rule in String()
// because a PEM block spans spaces/newlines that the key=value value group would
// otherwise truncate.
var secretReplacers = []secretReplacer{
	{bearerRe, "Bearer [REDACTED]"},
	{githubTokenRe, "[REDACTED-TOKEN]"},
	{githubPatRe, "[REDACTED-TOKEN]"},
	{slackTokenRe, "[REDACTED-TOKEN]"},
	{awsKeyRe, "[REDACTED-TOKEN]"},
	{openaiKeyRe, "[REDACTED-TOKEN]"},
	{googleKeyRe, "[REDACTED-TOKEN]"},
	{jwtRe, "[REDACTED-JWT]"},
}

// Redactor rewrites home-directory prefixes and masks secret material. A zero
// Redactor (empty home) still masks every secret pattern; only the home
// rewrite is skipped when Home is empty.
type Redactor struct {
	// Home is the absolute operator home directory; every occurrence is
	// rewritten to "~" before secret masking runs.
	Home string
}

// NewRedactor returns a Redactor rooted at home.
func NewRedactor(home string) Redactor {
	return Redactor{Home: strings.TrimRight(home, "/")}
}

// String applies the full redaction pipeline: home rewrite, then the
// secret-named key=value rule, then every single-token pattern.
func (r Redactor) String(s string) string {
	if s == "" {
		return s
	}
	if r.Home != "" {
		s = strings.ReplaceAll(s, r.Home, "~")
	}
	// PEM private-key blocks first: they span spaces and newlines, so the
	// key=value rule below would truncate the header (e.g. private_key=-----BEGIN
	// ...) and defeat pemPrivateKeyRe if it ran afterward.
	s = pemPrivateKeyRe.ReplaceAllString(s, "[REDACTED-PRIVATE-KEY]")
	// Secret-named key=value next so the whole value is masked as a unit.
	s = keyValueSecretRe.ReplaceAllString(s, `${1}[REDACTED]`)
	for _, sr := range secretReplacers {
		s = sr.re.ReplaceAllString(s, sr.repl)
	}
	return s
}

// Strings redacts each element of in, returning a new slice.
func (r Redactor) Strings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = r.String(s)
	}
	return out
}
