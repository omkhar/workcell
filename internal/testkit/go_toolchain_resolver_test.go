package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveExactToolchainGoBinReturnsTheSwitchedGo covers an older trusted
// go that reaches the go.mod toolchain only through the GOTOOLCHAIN switch.
// Callers build with GOTOOLCHAIN=local, so the resolver must hand on the
// switched toolchain's own go, not the older bootstrap go.
func TestResolveExactToolchainGoBinReturnsTheSwitchedGo(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n\ngo 1.27\n\ntoolchain go1.27.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	switched := filepath.Join(dir, "switched")
	for path, body := range map[string]string{
		// The bootstrap go is older; only the switch reaches go1.27.1.
		filepath.Join(dir, "bootstrap", "bin", "go"): `case "${GOTOOLCHAIN}:$2" in
  go1.27.1:GOROOT) printf '%s\n' "${FAKE_SWITCHED}" ;;
  go1.27.1:GOVERSION) echo go1.27.1 ;;
  *) echo go1.26.0 ;;
esac`,
		filepath.Join(switched, "bin", "go"): `echo go1.27.1`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/bash\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// accept_go_bin maps the fixed /usr/local/go candidate to the bootstrap
	// go and accepts any other existing go as is. Paths reach the script
	// through the environment, since a hostile TMPDIR may hold $ or `.
	code, output := runBashProbe(t, `set -euo pipefail
source "${FAKE_LIB}"
accept_go_bin() {
  case "$1" in
    /usr/local/go/bin/go) printf '%s\n' "${FAKE_DIR}/bootstrap/bin/go" ;;
    "${FAKE_DIR}"/*) printf '%s\n' "$1" ;;
    *) return 1 ;;
  esac
}
resolve_exact_toolchain_go_bin "${FAKE_DIR}"
`, map[string]string{
		"FAKE_DIR":      dir,
		"FAKE_LIB":      filepath.Join(repoRoot(t), "scripts", "lib", "go-run-env.sh"),
		"FAKE_SWITCHED": switched,
	})
	if want := filepath.Join(switched, "bin", "go"); code != 0 || strings.TrimSpace(output) != want {
		t.Fatalf("resolve_exact_toolchain_go_bin: exit=%d output=%q, want %q", code, output, want)
	}
}
