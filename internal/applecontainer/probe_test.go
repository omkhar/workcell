// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

// lightweightVMBootCeiling is a robust upper bound proving the per-container
// lightweight-VM model. Steady-state warm boot on an idle macOS 26 host is
// sub-second (~0.77s, see the strict test below), but wall-clock boot inflates
// under the CPU contention of a parallel `go test ./...` run. The ceiling stays
// an order of magnitude below Colima's shared-VM cold boot (tens of seconds)
// while remaining non-flaky under that contention.
const lightweightVMBootCeiling = 15 * time.Second

func TestProbeAppleContainer(t *testing.T) {
	// Not parallel: booting VMs is resource-sensitive.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	evidence, err := ProbeAppleContainer(ctx)
	if errors.Is(err, ErrAppleContainerUnavailable) {
		t.Skipf("apple container unavailable, skipping live probe: %v", err)
	}
	if err != nil {
		t.Fatalf("ProbeAppleContainer() error = %v", err)
	}

	if !evidence.Available {
		t.Fatalf("evidence.Available = false, want true")
	}
	if len(evidence.BootSamples) == 0 {
		t.Fatalf("no boot samples captured")
	}
	if evidence.MedianBoot <= 0 || evidence.MedianBoot >= lightweightVMBootCeiling {
		t.Fatalf("median boot = %v, want positive and < %v (lightweight per-container VM)", evidence.MedianBoot, lightweightVMBootCeiling)
	}
	if !evidence.PerVMKernel {
		t.Fatalf("PerVMKernel = false (guest kernel %q, host kernel %q), want own Linux kernel", evidence.GuestKernel, evidence.HostKernel)
	}
	if !evidence.PerVMHostname {
		t.Fatalf("PerVMHostname = false (guest %q, host %q), want per-VM hostname", evidence.GuestHostname, evidence.HostHostname)
	}
	if !evidence.PerVMNIC {
		t.Fatalf("PerVMNIC = false, want own VM NIC (192.168.64.x); raw:\n%s", evidence.Raw)
	}
	if !evidence.PerVMBlockDevice {
		t.Fatalf("PerVMBlockDevice = false, want ext4 /dev/vd* rootfs; raw:\n%s", evidence.Raw)
	}
	t.Logf("apple container %s: median boot %v (min %v) over %d samples; guest kernel %s (host %s); guest hostname %s",
		evidence.ContainerVersion, evidence.MedianBoot, minDuration(evidence.BootSamples), len(evidence.BootSamples), evidence.GuestKernel, evidence.HostKernel, evidence.GuestHostname)
}

// TestProbeAppleContainerSubSecondBoot asserts the sub-second steady-state boot
// claim. It is opt-in (set WORKCELL_APPLECONTAINER_STRICT_BOOT=1) and meant to
// run serially on an otherwise-idle host, since wall-clock boot latency is
// dominated by host CPU contention and would flake inside a parallel suite.
func TestProbeAppleContainerSubSecondBoot(t *testing.T) {
	if os.Getenv("WORKCELL_APPLECONTAINER_STRICT_BOOT") == "" {
		t.Skip("set WORKCELL_APPLECONTAINER_STRICT_BOOT=1 to assert sub-second boot on an idle host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	evidence, err := ProbeAppleContainer(ctx)
	if errors.Is(err, ErrAppleContainerUnavailable) {
		t.Skipf("apple container unavailable: %v", err)
	}
	if err != nil {
		t.Fatalf("ProbeAppleContainer() error = %v", err)
	}
	best := minDuration(evidence.BootSamples)
	if best <= 0 || best >= time.Second {
		t.Fatalf("fastest warm boot = %v, want sub-second", best)
	}
	t.Logf("sub-second boot confirmed: fastest warm boot %v, median %v", best, evidence.MedianBoot)
}

func minDuration(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	best := samples[0]
	for _, s := range samples[1:] {
		if s < best {
			best = s
		}
	}
	return best
}
