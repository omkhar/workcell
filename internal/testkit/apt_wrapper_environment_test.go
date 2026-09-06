// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Exercise only each wrapper's environment preamble. Do not invoke apt or the broker.
func TestAptWrapperPreamblesExportFixedPath(t *testing.T) {
	for _, wrapper := range []struct{ file, boundary string }{
		{"sudo-wrapper.sh", "\nexec "},
		{"apt-wrapper.sh", "\ncommand_name="},
	} {
		t.Run(wrapper.file, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join(repoRoot(t), "runtime/container/bin", wrapper.file))
			if err != nil {
				t.Fatal(err)
			}
			preamble, _, found := strings.Cut(string(data), wrapper.boundary)
			if !found {
				t.Fatal("wrapper preamble boundary is missing")
			}
			probe := filepath.Join(t.TempDir(), "preamble.sh")
			if err := os.WriteFile(probe, []byte(preamble+"\nexec /usr/bin/env\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command("/usr/bin/env", "-i", "/bin/bash", "-p", probe).CombinedOutput()
			if err != nil {
				t.Fatalf("preamble failed: %v: %s", err, output)
			}
			if !strings.Contains("\n"+string(output), "\nPATH=/usr/local/bin:/usr/bin:/bin\n") {
				t.Fatalf("child did not receive the fixed PATH: %s", output)
			}
		})
	}
}
