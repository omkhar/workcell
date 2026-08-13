// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSimpleEnvFileErrorDoesNotEchoRawLine(t *testing.T) {
	t.Parallel()

	secret := "gemini-secret-marker"
	path := filepath.Join(t.TempDir(), "gemini.env")
	if err := os.WriteFile(path, []byte("BROKEN "+secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := parseSimpleEnvFile(Path(path))
	if err == nil {
		t.Fatal("parseSimpleEnvFile returned nil error for malformed input")
	}
	message := err.Error()
	if strings.Contains(message, secret) {
		t.Fatalf("parseSimpleEnvFile error echoed secret marker: %q", message)
	}
	if !strings.Contains(message, path) || !strings.Contains(message, "line 1") {
		t.Fatalf("parseSimpleEnvFile error lost source or line context: %q", message)
	}
}

func TestParseEnvBooleanValueErrorDoesNotEchoRawValue(t *testing.T) {
	t.Parallel()

	secret := "gemini-secret-marker"
	values := map[string]string{"GOOGLE_GENAI_USE_GCA": secret}
	_, err := parseEnvBooleanValue(values, Path("gemini.env"), "GOOGLE_GENAI_USE_GCA")
	if err == nil {
		t.Fatal("parseEnvBooleanValue returned nil error for invalid input")
	}
	message := err.Error()
	if strings.Contains(message, secret) {
		t.Fatalf("parseEnvBooleanValue error echoed secret marker: %q", message)
	}
	if !strings.Contains(message, "gemini.env") || !strings.Contains(message, "GOOGLE_GENAI_USE_GCA") {
		t.Fatalf("parseEnvBooleanValue error lost source or key context: %q", message)
	}
}

func TestGeminiEnvErrorsDoNotEchoUntrustedKeys(t *testing.T) {
	t.Parallel()

	secret := "GeminiSecretMarker"
	for _, content := range []string{
		secret + "=value\n",
		secret + "='unterminated\n",
		secret + "=one\n" + secret + "=two\n",
		secret + "=\"bad\\q\"\n",
		secret + "=value'\n",
	} {
		path := filepath.Join(t.TempDir(), "gemini.env")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := validateGeminiEnvFile(Path(path))
		if err == nil {
			t.Fatal("validateGeminiEnvFile returned nil error for invalid input")
		}
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("Gemini env error echoed untrusted key: %q", err)
		}
	}
}

func TestValidateGeminiEnvFileAcceptsSupportedAssignment(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "gemini.env")
	if err := os.WriteFile(path, []byte("GEMINI_API_KEY=valid-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := validateGeminiEnvFile(Path(path))
	if err != nil {
		t.Fatalf("validateGeminiEnvFile returned an error: %v", err)
	}
	if got := config["selected_auth_type"]; got != "gemini-api-key" {
		t.Fatalf("selected_auth_type = %q, want gemini-api-key", got)
	}
}
