// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeIdentitySourceOmitsSudoersGrant(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "runtime/container/runtime-user.sh"))
	if err != nil {
		t.Fatal(err)
	}
	body := extractShellFunction(t, string(data), "workcell_prepare_runtime_identity")
	for _, obsolete := range []string{"/etc/sudoers", "NOPASSWD"} {
		if strings.Contains(body, obsolete) {
			t.Errorf("runtime identity still contains obsolete sudoers grant literal %q", obsolete)
		}
	}
}

// Check the canonical launch function, not arbitrary shell semantics or live propagation.
func TestRuntimeBrokerSourceUsesFixedLaunchEnvironment(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(repoRoot(t), "runtime/container/runtime-user.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "workcell_start_apt_broker() {") != 1 {
		t.Fatal("runtime-user.sh must define workcell_start_apt_broker exactly once")
	}
	want := `workcell_start_apt_broker() {
  /usr/bin/env -i \
    PATH=/usr/local/bin:/usr/bin:/bin LC_ALL=C \
    LD_PRELOAD=/usr/local/lib/libworkcell_exec_guard.so \
    WORKCELL_APT_BROKER_HELPER_TIMEOUT_SECONDS="${WORKCELL_APT_BROKER_HELPER_TIMEOUT_SECONDS:-300}" \
    /usr/local/libexec/workcell/workcell-apt-broker-server --start --peer-uid "$1"
}`
	if got := extractShellFunction(t, string(data), "workcell_start_apt_broker"); got != want {
		t.Fatalf("broker launch source differs from the fixed environment contract:\n%s", got)
	}
}
