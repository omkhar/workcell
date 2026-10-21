// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package auditseal

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/applecontainer"
)

// appleContainerSession runs one apple-container session through its public
// lifecycle and returns its audit log path and session id. The source path has
// a space, so the digest must cover the decoded workspace_origin value.
func appleContainerSession(t *testing.T) (string, string) {
	t.Helper()
	source := filepath.Join(t.TempDir(), "my src")
	if err := os.MkdirAll(filepath.Join(source, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	target, err := applecontainer.NewAppleContainerTarget(applecontainer.Contract{})
	if err != nil {
		t.Fatal(err)
	}
	c := applecontainer.DefaultConformanceCase(t.TempDir(), source)
	result, err := applecontainer.RunConformance(context.Background(), target, applecontainer.DefaultContract(), c)
	if err != nil {
		t.Fatalf("apple-container session: %v", err)
	}
	return result.Finished.AuditLogPath, c.SessionID
}

func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

// TestAppleContainerSessionSignsAndVerifies: the apple-container writers chain
// every record, so the A5 signer seals the session and verify passes; a
// tampered, deleted or reordered record fails verify.
func TestAppleContainerSessionSignsAndVerifies(t *testing.T) {
	logPath, sessionID := appleContainerSession(t)
	lines := readLines(t, logPath)
	if len(lines) != 4 {
		t.Fatalf("got %d audit lines, want 4:\n%s", len(lines), strings.Join(lines, "\n"))
	}
	signingDir := filepath.Join(t.TempDir(), "signing")
	seal, err := SignSessionHead(signingDir, logPath, applecontainer.Provider, sessionID, "t")
	if err != nil {
		t.Fatalf("sign apple-container session: %v", err)
	}
	if _, err := VerifySessionSeal(signingDir, logPath, applecontainer.Provider, sessionID, seal); err != nil {
		t.Fatalf("verify apple-container session: %v", err)
	}

	tampered := append([]string{}, lines...)
	tampered[3] = strings.Replace(tampered[3], "exit_status=0", "exit_status=1", 1)
	deleted := append(append([]string{}, lines[:1]...), lines[2:]...)
	reordered := []string{lines[1], lines[0], lines[2], lines[3]}
	for name, mutated := range map[string][]string{"tampered": tampered, "deleted": deleted, "reordered": reordered} {
		t.Run(name, func(t *testing.T) {
			if strings.Join(mutated, "\n") == strings.Join(lines, "\n") {
				t.Fatal("mutation did not change the log")
			}
			path := writeLog(t, t.TempDir(), mutated)
			if _, err := VerifySessionSeal(signingDir, path, applecontainer.Provider, sessionID, seal); err == nil {
				t.Fatalf("%s apple-container log verified", name)
			}
		})
	}
}

// TestLegacyAppleContainerLogFailsVerify: a log written before the chain (the
// same lines without prev_digest/record_digest) cannot be signed, and a seal
// over the chained log does not verify against it.
func TestLegacyAppleContainerLogFailsVerify(t *testing.T) {
	logPath, sessionID := appleContainerSession(t)
	signingDir := filepath.Join(t.TempDir(), "signing")
	seal, err := SignSessionHead(signingDir, logPath, applecontainer.Provider, sessionID, "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	var legacy []string
	for _, line := range readLines(t, logPath) {
		legacy = append(legacy, strings.Split(line, " prev_digest=")[0])
		legacy[len(legacy)-1] = strings.Split(legacy[len(legacy)-1], " record_digest=")[0]
	}
	path := writeLog(t, t.TempDir(), legacy)
	if _, err := SignSessionHead(signingDir, path, applecontainer.Provider, sessionID, "t"); err == nil || !strings.Contains(err.Error(), "no digest chain") {
		t.Fatalf("legacy apple-container log must fail signing with no digest chain, got %v", err)
	}
	if _, err := VerifySessionSeal(signingDir, path, applecontainer.Provider, sessionID, seal); err == nil || !strings.Contains(err.Error(), "no digest chain") {
		t.Fatalf("legacy apple-container log must fail verify with no digest chain, got %v", err)
	}
}
