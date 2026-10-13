package testkit

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestExecGuardBenchRefusesAnActivePreload runs the exec-guard bench driver
// with LD_PRELOAD already set, as in a Workcell session: it must stop before
// it measures, since the preload would skew the unhooked baseline and the
// loaded guard would reject the baseline's guard-free launch.
func TestExecGuardBenchRefusesAnActivePreload(t *testing.T) {
	script := filepath.Join(repoRoot(t), "scripts", "bench", "run-exec-guard-bench.sh")
	output, err := execRetryETXTBSY(func() *exec.Cmd {
		cmd := exec.Command("bash", script)
		cmd.Env = []string{"PATH=/usr/bin:/bin", "LD_PRELOAD=/inherited.so"}
		return cmd
	})
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 2 || !strings.Contains(string(output), "run outside a Workcell session") {
		t.Fatalf("run-exec-guard-bench.sh with LD_PRELOAD set: err=%v\n%s", err, output)
	}
}
