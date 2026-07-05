// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportbundle

import (
	"strings"
	"testing"
)

// knownSecrets are representative live-credential shapes. Every redaction path
// in the package is proven against this table so a regression that lets any
// one through fails a test rather than shipping a leak.
var knownSecrets = []struct {
	name   string
	secret string
}{
	// Each fixture is assembled from fragments so the full token shape never
	// appears as a literal in source (GitHub push protection scans committed
	// literals); the concatenated runtime value still exercises each pattern.
	{"github classic token", "ghp_" + "0123456789abcdefghijklmnopqrstuvwx"},
	{"github oauth token", "gho_" + "0123456789abcdefghijklmnopqrstuvwx"},
	{"github pat", "github_pat_" + "11ABCDEFG0abcdefghijkl_mnopqrstuvwxyz0123456789ABCDEFGHIJ"},
	{"openai key", "sk-" + "proj-abcdefghijklmnopqrstuvwxyz0123456789ABCD"},
	{"google api key", "AIza" + "SyA1234567890abcdefghijklmnopqrstuv"},
	{"aws access key", "AKIA" + "IOSFODNN7EXAMPLE"},
	{"aws session key", "ASIA" + "IOSFODNN7EXAMPLE"},
	{"slack token", "xoxb-" + "1234567890-abcdefghijklmnop"},
	{"jwt", "eyJhbGciOiJIUzI1NiJ9." + "eyJzdWIiOiIxMjM0NSJ9.abcDEF-_1234567890xyz"},
}

func TestRedactorMasksKnownSecrets(t *testing.T) {
	r := NewRedactor("/Users/operator")
	for _, tc := range knownSecrets {
		t.Run(tc.name, func(t *testing.T) {
			for _, wrapper := range []string{
				"%s",
				"value is %s here",
				"token=%s",
				`{"credential":"%s"}`,
			} {
				in := strings.Replace(wrapper, "%s", tc.secret, 1)
				out := r.String(in)
				if strings.Contains(out, tc.secret) {
					t.Fatalf("secret leaked: input %q produced %q", in, out)
				}
			}
		})
	}
}

func TestRedactorMasksPEMPrivateKey(t *testing.T) {
	r := NewRedactor("")
	pem := "-----BEGIN RSA PRIVATE KEY-----\n" + "MIIEpAIBAAKCAQEA0secretkeymaterial\n" + "-----END RSA PRIVATE KEY-----"
	out := r.String("here is a key " + pem + " trailing")
	if strings.Contains(out, "secretkeymaterial") {
		t.Fatalf("private key material leaked: %q", out)
	}
	if !strings.Contains(out, "[REDACTED-PRIVATE-KEY]") {
		t.Fatalf("expected private-key marker, got %q", out)
	}
}

func TestRedactorMasksBearer(t *testing.T) {
	r := NewRedactor("")
	out := r.String("Authorization: Bearer abc.DEF-secret_token123")
	if strings.Contains(out, "secret_token123") {
		t.Fatalf("bearer token leaked: %q", out)
	}
	if !strings.Contains(out, "Bearer [REDACTED]") {
		t.Fatalf("expected bearer marker, got %q", out)
	}
}

func TestRedactorMasksSecretNamedKeyValues(t *testing.T) {
	r := NewRedactor("")
	// Each case pairs an input with the sensitive fragments that must not survive.
	// The single-quoted and spaced double-quoted forms are regressions for a
	// value group that previously skipped quoted values or masked only the first
	// word of a quoted passphrase.
	cases := []struct {
		in    string
		leaks []string
	}{
		{"password=hunter2trailing", []string{"hunter2trailing"}},
		{"api_key: myplaintextapikeyvalue", []string{"myplaintextapikeyvalue"}},
		{"client_secret = s3cr3tvalueforclient", []string{"s3cr3tvalueforclient"}},
		{`"session_key":"abcdef012345sessionvalue"`, []string{"abcdef012345sessionvalue"}},
		{"password='hunter2single'", []string{"hunter2single"}},
		{`password="two words here"`, []string{"two words here", "words here", "words"}},
		{"token='multi word secret'", []string{"multi word secret", "word secret", "secret"}},
		// secret-bearing stem anywhere in the key name (common env/config fields).
		{"secret_key=plainsecretvalue", []string{"plainsecretvalue"}},
		{"SECRET_KEY_BASE=basesecretvalue", []string{"basesecretvalue"}},
		{"app_password_field: fieldsecretvalue", []string{"fieldsecretvalue"}},
		{"refresh_token=refreshsecretvalue", []string{"refreshsecretvalue"}},
		// escaped quote inside a quoted value must not leak the suffix.
		{`password="abc\"defsecret"`, []string{"defsecret", `abc\"defsecret`}},
	}
	for _, tc := range cases {
		out := r.String(tc.in)
		if !strings.Contains(out, "[REDACTED]") {
			t.Fatalf("expected redaction of %q, got %q", tc.in, out)
		}
		for _, leak := range tc.leaks {
			if strings.Contains(out, leak) {
				t.Fatalf("secret value leaked from %q: %q", tc.in, out)
			}
		}
	}
}

func TestRedactorKeepsNonSecretFieldsLegible(t *testing.T) {
	r := NewRedactor("")
	// These key names are not secret-bearing; their values must survive so
	// diagnostics stay readable (no over-redaction from the broadened rule).
	legible := []string{
		"auth_status=ok",
		"public_key=id-abc123",
		"cache_key=warm",
		"sort_key=ascending",
	}
	for _, in := range legible {
		if out := r.String(in); strings.Contains(out, "[REDACTED]") {
			t.Fatalf("over-redacted a non-secret field %q -> %q", in, out)
		}
	}
}

func TestRedactorRewritesHome(t *testing.T) {
	r := NewRedactor("/Users/operator")
	out := r.String("/Users/operator/.local/state/workcell/x")
	if strings.Contains(out, "operator") {
		t.Fatalf("home username leaked: %q", out)
	}
	if out != "~/.local/state/workcell/x" {
		t.Fatalf("home rewrite = %q, want ~/.local/state/workcell/x", out)
	}
}

func TestRedactorHomeTrailingSlashNormalized(t *testing.T) {
	r := NewRedactor("/Users/operator/")
	if got := r.String("/Users/operator/x"); got != "~/x" {
		t.Fatalf("home rewrite with trailing slash = %q, want ~/x", got)
	}
}

// TestRedactorPreservesDiagnosticText guards against over-redaction: fields
// that matter for diagnosis (sha digests, enum values, ordinary paths) must
// survive so the bundle stays useful.
func TestRedactorPreservesDiagnosticText(t *testing.T) {
	r := NewRedactor("/Users/operator")
	keep := []string{
		"sha256:3b1c9e0f2a4d5b6c7e8f9a0b1c2d3e4f5a6b7c8d9e0f1a2b3c4d5e6f7a8b9c0d",
		"auth_status=none",
		"colima",
		"target_kind=vm",
		"~/.colima/wcl-strict",
	}
	for _, in := range keep {
		if got := r.String(in); got != in {
			t.Fatalf("diagnostic text over-redacted: %q became %q", in, got)
		}
	}
}

func TestRedactorEmptyString(t *testing.T) {
	if got := NewRedactor("/Users/operator").String(""); got != "" {
		t.Fatalf("empty string became %q", got)
	}
}

func TestRedactorStrings(t *testing.T) {
	r := NewRedactor("/Users/operator")
	in := []string{"/Users/operator/x", "ghp_" + "0123456789abcdefghijklmnopqrstuvwx"}
	out := r.Strings(in)
	if out[0] != "~/x" {
		t.Fatalf("Strings[0] = %q", out[0])
	}
	if strings.Contains(out[1], "ghp_") {
		t.Fatalf("Strings did not redact token: %q", out[1])
	}
	if r.Strings(nil) != nil {
		t.Fatalf("Strings(nil) should be nil")
	}
}
