package testkit

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestExecGuardBenchBaselineDropsAnInheritedPreload runs run_bench from the
// exec-guard bench driver with an LD_PRELOAD already set, as in a Workcell
// session: the unhooked baseline must run without it, and the hooked run
// must carry only the guard.
func TestExecGuardBenchBaselineDropsAnInheritedPreload(t *testing.T) {
	script, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "bench", "run-exec-guard-bench.sh"))
	if err != nil {
		t.Fatal(err)
	}
	function := regexp.MustCompile(`(?ms)^run_bench\(\) \{\n.*?^\}\n`).Find(script)
	if function == nil {
		t.Fatal("run_bench is missing from run-exec-guard-bench.sh")
	}
	bin := filepath.Join(t.TempDir(), "bench")
	if err := os.WriteFile(bin, []byte("#!/bin/bash\nprintf 'preload=[%s]\\n' \"${LD_PRELOAD:-}\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	code, output := runBashProbe(t, string(function)+`run_bench x 0
run_bench x 1
`, map[string]string{"LD_PRELOAD": "/inherited.so", "BIN": bin, "SO_PATH": "/guard.so", "BENCH_PATH": "/usr/bin:/bin"})
	if want := "preload=[]\npreload=[/guard.so]"; code != 0 || strings.TrimSpace(output) != want {
		t.Fatalf("run_bench: exit=%d output=%q, want %q", code, output, want)
	}
}
