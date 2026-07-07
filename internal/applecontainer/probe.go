// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ErrAppleContainerUnavailable is the skip sentinel returned by
// ProbeAppleContainer when the `container` CLI is absent or its system service
// is not running. Callers (and CI on non-macOS-26 hosts) should treat it as a
// graceful skip, not a failure.
var ErrAppleContainerUnavailable = errors.New("apple container runtime unavailable")

// probeImage is the small Linux image booted to exercise per-VM isolation.
const probeImage = "alpine"

// bootSampleCount is the number of warm timed boots taken for latency evidence.
const bootSampleCount = 3

// Evidence is the structured result of a live Apple-container probe.
type Evidence struct {
	Available        bool            `json:"available"`
	ContainerVersion string          `json:"container_version"`
	BootSamples      []time.Duration `json:"boot_samples"`
	MedianBoot       time.Duration   `json:"median_boot"`
	GuestKernel      string          `json:"guest_kernel"`
	GuestHostname    string          `json:"guest_hostname"`
	HostKernel       string          `json:"host_kernel"`
	HostHostname     string          `json:"host_hostname"`
	PerVMKernel      bool            `json:"per_vm_kernel"`
	PerVMHostname    bool            `json:"per_vm_hostname"`
	PerVMNIC         bool            `json:"per_vm_nic"`
	PerVMBlockDevice bool            `json:"per_vm_block_device"`
	Raw              string          `json:"raw"`
}

var (
	inetRE       = regexp.MustCompile(`inet\s+192\.168\.64\.\d+`)
	rootMountRE  = regexp.MustCompile(`/dev/vd\w+\s+on\s+/\s+type\s+ext4`)
	kernelLineRE = regexp.MustCompile(`^\d+\.\d+\.\d+`)
)

// ProbeAppleContainer shells out to the live `container` CLI to gather boot
// latency and per-VM isolation evidence. It first fails closed via RequireMacOS26
// (returning the guard error WITHOUT exec'ing the CLI) on any host outside the
// Apple-Silicon + macOS-26 support constraint, then returns
// ErrAppleContainerUnavailable (wrapped) when the runtime is absent or its
// service is stopped, so it skips gracefully on hosts without Apple container.
func ProbeAppleContainer(ctx context.Context) (Evidence, error) {
	return probeAppleContainer(ctx, RequireMacOS26)
}

// probeAppleContainer is the testable core of ProbeAppleContainer: guard is the host-support gate,
// checked (fail closed) BEFORE any exec, so a host outside the constraint never shells out.
func probeAppleContainer(ctx context.Context, guard func() error) (Evidence, error) {
	if err := guard(); err != nil {
		return Evidence{}, err
	}
	bin, err := exec.LookPath("container")
	if err != nil {
		return Evidence{}, fmt.Errorf("%w: container CLI not found in PATH", ErrAppleContainerUnavailable)
	}

	if err := requireAppleContainerRunning(ctx, bin); err != nil {
		return Evidence{}, err
	}

	evidence := Evidence{Available: true}
	evidence.ContainerVersion = containerVersion(ctx, bin)
	evidence.HostKernel = hostKernel()
	evidence.HostHostname, _ = os.Hostname()

	// Warm the image+kernel cache so boot samples measure steady-state latency
	// rather than a one-off image fetch.
	if _, err := runContainer(ctx, bin, "run", "--rm", probeImage, "true"); err != nil {
		return Evidence{}, fmt.Errorf("warm-up `container run` failed: %w", err)
	}

	for i := 0; i < bootSampleCount; i++ {
		start := time.Now()
		if _, err := runContainer(ctx, bin, "run", "--rm", probeImage, "true"); err != nil {
			return Evidence{}, fmt.Errorf("boot sample `container run` failed: %w", err)
		}
		evidence.BootSamples = append(evidence.BootSamples, time.Since(start))
	}
	evidence.MedianBoot = medianDuration(evidence.BootSamples)

	inspect, err := runContainer(ctx, bin, "run", "--rm", probeImage, "sh", "-c",
		"uname -r; echo HOSTNAME=$(hostname); ip addr; echo ---MOUNT---; mount")
	if err != nil {
		return Evidence{}, fmt.Errorf("inspection `container run` failed: %w", err)
	}
	evidence.Raw = inspect
	populateIsolation(&evidence, inspect)

	return evidence, nil
}

// requireAppleContainerRunning verifies the apiserver is actually running.
// It prefers the structured `container system status --format json` output and
// reads the exact `status` field, falling back to a precise field match on the
// tabular output only when JSON is unavailable (e.g. an older CLI). A stopped or
// unregistered service (whose message contains the substring "not running")
// yields ErrAppleContainerUnavailable rather than being mistaken for running.
func requireAppleContainerRunning(ctx context.Context, bin string) error {
	if jsonOut, err := runContainer(ctx, bin, "system", "status", "--format", "json"); err == nil {
		if running, parsed := appleContainerStatusJSONRunning(jsonOut); parsed {
			if running {
				return nil
			}
			return fmt.Errorf("%w: `container system status` reported not running", ErrAppleContainerUnavailable)
		}
	}
	textOut, err := runContainer(ctx, bin, "system", "status")
	if err != nil || !appleContainerStatusTextRunning(textOut) {
		return fmt.Errorf("%w: `container system status` did not report running", ErrAppleContainerUnavailable)
	}
	return nil
}

// appleContainerStatusJSONRunning parses `container system status --format json`
// and reports whether the `status` field is exactly "running". The second
// return value reports whether the payload parsed as JSON at all.
func appleContainerStatusJSONRunning(out string) (running bool, parsed bool) {
	var payload struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		return false, false
	}
	return strings.EqualFold(strings.TrimSpace(payload.Status), "running"), true
}

// appleContainerStatusTextRunning parses the tabular `container system status`
// output and reports whether its `status` field is exactly "running". It matches
// the field, not a bare substring, so a stopped-service message like
// "apiserver is not running and not registered with launchd" is not read as
// running.
func appleContainerStatusTextRunning(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "status" {
			return strings.EqualFold(fields[1], "running")
		}
	}
	return false
}

// populateIsolation parses one inspection run's stdout into the isolation
// booleans and guest identity fields.
func populateIsolation(evidence *Evidence, inspect string) {
	lines := strings.Split(inspect, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch {
		case evidence.GuestKernel == "" && kernelLineRE.MatchString(line):
			evidence.GuestKernel = line
		case strings.HasPrefix(line, "HOSTNAME="):
			evidence.GuestHostname = strings.TrimPrefix(line, "HOSTNAME=")
		}
	}
	// A guest kernel that differs from the macOS host kernel proves the container
	// ran its own Linux kernel rather than sharing the host's.
	evidence.PerVMKernel = evidence.GuestKernel != "" && evidence.GuestKernel != evidence.HostKernel
	evidence.PerVMHostname = evidence.GuestHostname != "" && evidence.GuestHostname != evidence.HostHostname
	evidence.PerVMNIC = inetRE.MatchString(inspect)
	evidence.PerVMBlockDevice = rootMountRE.MatchString(inspect)
}

func containerVersion(ctx context.Context, bin string) string {
	out, err := runContainer(ctx, bin, "--version")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(strings.SplitN(out, "\n", 2)[0])
}

// runContainer runs the container CLI and returns its stdout. Progress output is
// written by the CLI to stderr and is intentionally discarded.
func runContainer(ctx context.Context, bin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	out, err := cmd.Output()
	return string(out), err
}

func hostKernel() string {
	out, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func medianDuration(samples []time.Duration) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}
