// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyPinnedPackageTree runs verify_pinned_package_tree, and through it
// verify_lock_package_tree, against a fixture package.json, lockfile and stub
// registry. The stub serves the metadata file named by
// the request path, so a request that does not escape a scoped name finds no
// file and fails.
func TestVerifyPinnedPackageTree(t *testing.T) {
	entry := func(name, integrity string) map[string]any {
		return map[string]any{
			"version":   "1.0.0",
			"resolved":  "https://registry.npmjs.org/" + name + "/-/x-1.0.0.tgz",
			"integrity": integrity,
		}
	}
	goodLock := func() map[string]map[string]any {
		root := entry("@demo/cli", "sha512-root")
		root["dependencies"] = map[string]any{"plain": "1.0.0"}
		root["optionalDependencies"] = map[string]any{"@demo/cli-linux-x64": "1.0.0"}
		return map[string]map[string]any{
			"":                                 {"name": "fixture"},
			"node_modules/@demo/cli":           root,
			"node_modules/plain":               entry("plain", "sha512-plain"),
			"node_modules/@demo/cli-linux-x64": entry("@demo/cli-linux-x64", "sha512-platform"),
		}
	}
	cases := map[string]struct {
		edit    func(map[string]map[string]any)
		pin     map[string]any
		wantErr string
	}{
		"a matching tree": {edit: func(map[string]map[string]any) {}},
		"a changed root integrity": {
			edit:    func(p map[string]map[string]any) { p["node_modules/@demo/cli"]["integrity"] = "sha512-other" },
			wantErr: "@demo/cli integrity mismatch",
		},
		"a changed scoped platform integrity": {
			edit:    func(p map[string]map[string]any) { p["node_modules/@demo/cli-linux-x64"]["integrity"] = "sha512-other" },
			wantErr: "@demo/cli-linux-x64 integrity mismatch",
		},
		"a missing dependency entry": {
			edit:    func(p map[string]map[string]any) { delete(p, "node_modules/plain") },
			wantErr: "has no top-level entry for plain",
		},
		"a missing pin": {
			edit:    func(map[string]map[string]any) {},
			pin:     map[string]any{"other": "1.0.0"},
			wantErr: "package.json must pin @demo/cli",
		},
		"a pin that differs from the lockfile": {
			edit:    func(map[string]map[string]any) {},
			pin:     map[string]any{"@demo/cli": "1.0.1"},
			wantErr: "@demo/cli package-lock version mismatch: package.json=1.0.1, package-lock=1.0.0",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			registry := filepath.Join(dir, "registry")
			if err := os.Mkdir(registry, 0o700); err != nil {
				t.Fatal(err)
			}
			for path, e := range goodLock() {
				if path == "" {
					continue
				}
				pkg := strings.TrimPrefix(path, "node_modules/")
				meta := map[string]any{"version": e["version"], "dist": map[string]any{"tarball": e["resolved"], "integrity": e["integrity"]}}
				writeJSONFixture(t, filepath.Join(registry, strings.Replace(pkg, "/", "%2f", 1)+"@1.0.0.json"), meta)
			}
			lock := goodLock()
			tc.edit(lock)
			lockPath := filepath.Join(dir, "package-lock.json")
			writeJSONFixture(t, lockPath, map[string]any{"packages": lock})
			pin := tc.pin
			if pin == nil {
				pin = map[string]any{"@demo/cli": "1.0.0"}
			}
			packageJSONPath := filepath.Join(dir, "package.json")
			writeJSONFixture(t, packageJSONPath, map[string]any{"dependencies": pin})

			script := `set -euo pipefail
source "$1"
registry="$2"
curl() {
  local url="" out=""
  while (($#)); do
    case "$1" in
      -o) out="$2"; shift ;;
      https://*) url="$1" ;;
    esac
    shift
  done
  local path="${url#https://registry.npmjs.org/}"
  cp "${registry}/${path%/*}@${path##*/}.json" "${out}"
}
verify_pinned_package_tree "$3" "$4" @demo/cli`
			cmd := exec.Command("bash", "-c", script, "bash",
				filepath.Join(repoRoot(t), "scripts/lib/npm-registry-verify.sh"), registry, packageJSONPath, lockPath)
			cmd.Env = append(os.Environ(), "TMPDIR="+dir)
			out, err := cmd.CombinedOutput()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("verify_pinned_package_tree: %v\n%s", err, out)
				}
				return
			}
			if err == nil || !strings.Contains(string(out), tc.wantErr) {
				t.Fatalf("verify_pinned_package_tree = %v, want %q\n%s", err, tc.wantErr, out)
			}
		})
	}
}

func writeJSONFixture(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
