package testkit

import (
	"os"
	"path/filepath"
	"regexp"
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
	// The stub records into a file: on Linux, ld.so reports the missing
	// preload objects on stderr, which the probe's output also carries.
	dir := t.TempDir()
	bin, record := filepath.Join(dir, "bench"), filepath.Join(dir, "record")
	if err := os.WriteFile(bin, []byte("#!/bin/bash\nprintf 'preload=[%s]\\n' \"${LD_PRELOAD:-}\" >>\"${RECORD}\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	code, output := runBashProbe(t, string(function)+`run_bench x 0
run_bench x 1
`, map[string]string{"LD_PRELOAD": "/inherited.so", "BIN": bin, "SO_PATH": "/guard.so", "BENCH_PATH": "/usr/bin:/bin", "RECORD": record})
	recorded, err := os.ReadFile(record)
	if want := "preload=[]\npreload=[/guard.so]\n"; code != 0 || err != nil || string(recorded) != want {
		t.Fatalf("run_bench: exit=%d record=%q (%v), want %q\n%s", code, recorded, err, want, output)
	}
}
