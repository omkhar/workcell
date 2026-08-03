// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type colimaProcessIdentity struct {
	pid     int
	started string
}

type colimaProcessReaperDependencies struct {
	list      func(context.Context) ([]byte, error)
	started   func(int) (string, error)
	state     func(int) (string, error)
	signal    func(int, syscall.Signal) error
	sleep     func(context.Context, time.Duration) error
	termDelay time.Duration
	termPolls int
	killDelay time.Duration
	killPolls int
}

// ReapColimaProfileProcesses terminates only processes whose command and
// start-time identity belong to profile, then proves those identities and any
// newly observed profile processes are absent.
func ReapColimaProfileProcesses(ctx context.Context, profile string) error {
	deps := colimaProcessReaperDependencies{
		list: func(ctx context.Context) ([]byte, error) {
			output, err := exec.CommandContext(ctx, trustedPSPath(), "-axo", "pid=,command=").Output()
			if err != nil {
				return nil, fmt.Errorf("list host processes: %w", err)
			}
			return output, nil
		},
		started:   ProcessStartTime,
		state:     processState,
		signal:    syscall.Kill,
		sleep:     sleepWithContext,
		termDelay: time.Second,
		termPolls: 5,
		killDelay: 100 * time.Millisecond,
		killPolls: 10,
	}
	return reapColimaProfileProcesses(ctx, profile, deps)
}

func reapColimaProfileProcesses(
	ctx context.Context,
	profile string,
	deps colimaProcessReaperDependencies,
) error {
	owned, err := captureColimaProcessIdentities(ctx, profile, deps)
	if err != nil {
		return err
	}
	for _, identity := range owned {
		if err := signalCurrentProcessIdentity(ctx, profile, identity, syscall.SIGTERM, deps); err != nil {
			return err
		}
	}
	survivors := owned
	for poll := 0; poll < deps.termPolls && len(survivors) > 0; poll++ {
		if err := deps.sleep(ctx, deps.termDelay); err != nil {
			return err
		}
		survivors, err = currentProcessIdentities(survivors, deps)
		if err != nil {
			return err
		}
	}
	fresh, err := captureColimaProcessIdentities(ctx, profile, deps)
	if err != nil {
		return err
	}
	for _, identity := range fresh {
		if !slices.Contains(survivors, identity) {
			survivors = append(survivors, identity)
		}
	}
	for _, identity := range survivors {
		if err := signalCurrentProcessIdentity(ctx, profile, identity, syscall.SIGKILL, deps); err != nil {
			return err
		}
	}
	for poll := 0; poll < deps.killPolls && len(survivors) > 0; poll++ {
		if err := deps.sleep(ctx, deps.killDelay); err != nil {
			return err
		}
		survivors, err = currentProcessIdentities(survivors, deps)
		if err != nil {
			return err
		}
	}

	fresh, err = captureColimaProcessIdentities(ctx, profile, deps)
	if err != nil {
		return err
	}
	if len(survivors)+len(fresh) != 0 {
		return fmt.Errorf("colima profile %s still has owned processes", profile)
	}
	return nil
}

func captureColimaProcessIdentities(
	ctx context.Context,
	profile string,
	deps colimaProcessReaperDependencies,
) ([]colimaProcessIdentity, error) {
	output, err := deps.list(ctx)
	if err != nil {
		return nil, fmt.Errorf("capture Colima profile processes: %w", err)
	}
	pids, err := ColimaProfileProcessPIDs(output, profile)
	if err != nil {
		return nil, err
	}
	identities := make([]colimaProcessIdentity, 0, len(pids))
	observedPIDs := make(map[int]struct{}, len(pids))
	for _, pid := range pids {
		observedPIDs[pid] = struct{}{}
		started, err := deps.started(pid)
		if IsProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("capture Colima profile process %d identity: %w", pid, err)
		}
		if started == "" {
			return nil, fmt.Errorf("capture Colima profile process %d identity: empty start time", pid)
		}
		state, err := deps.state(pid)
		if IsProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("capture Colima profile process %d state: %w", pid, err)
		}
		if isZombieProcessState(state) {
			continue
		}
		identities = append(identities, colimaProcessIdentity{pid: pid, started: started})
	}

	stableOutput, err := deps.list(ctx)
	if err != nil {
		return nil, fmt.Errorf("revalidate Colima profile processes: %w", err)
	}
	stablePIDs, err := ColimaProfileProcessPIDs(stableOutput, profile)
	if err != nil {
		return nil, err
	}
	stableSet := make(map[int]struct{}, len(stablePIDs))
	for _, pid := range stablePIDs {
		stableSet[pid] = struct{}{}
	}
	stableIdentities := make([]colimaProcessIdentity, 0, len(identities))
	for _, identity := range identities {
		current, err := currentProcessIdentity(identity, deps)
		if _, exists := stableSet[identity.pid]; !exists {
			if err == nil && !current {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("revalidate Colima profile process %d identity: %w", identity.pid, err)
			}
			return nil, errors.New("colima profile process command identity changed during capture")
		}
		if err != nil {
			return nil, fmt.Errorf("revalidate Colima profile process %d identity: %w", identity.pid, err)
		}
		if !current {
			continue
		}
		stableIdentities = append(stableIdentities, identity)
	}
	for _, pid := range stablePIDs {
		if _, exists := observedPIDs[pid]; !exists {
			return nil, errors.New("colima profile process inventory changed during identity capture")
		}
	}
	return stableIdentities, nil
}

func signalCurrentProcessIdentity(
	ctx context.Context,
	profile string,
	identity colimaProcessIdentity,
	signal syscall.Signal,
	deps colimaProcessReaperDependencies,
) error {
	started, err := deps.started(identity.pid)
	if IsProcessGone(err) || (err == nil && started != identity.started) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("revalidate Colima profile process %d: %w", identity.pid, err)
	}
	output, err := deps.list(ctx)
	if err != nil {
		return fmt.Errorf("revalidate Colima profile process command: %w", err)
	}
	pids, err := ColimaProfileProcessPIDs(output, profile)
	if err != nil {
		return err
	}
	if !slices.Contains(pids, identity.pid) {
		started, err := deps.started(identity.pid)
		if IsProcessGone(err) || (err == nil && started != identity.started) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("revalidate Colima profile process %d: %w", identity.pid, err)
		}
		return fmt.Errorf("colima profile process %d command identity changed before signal", identity.pid)
	}
	started, err = deps.started(identity.pid)
	if IsProcessGone(err) || (err == nil && started != identity.started) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("revalidate Colima profile process %d immediately before signal: %w", identity.pid, err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := deps.signal(identity.pid, signal); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal Colima profile process %d: %w", identity.pid, err)
	}
	return nil
}

func currentProcessIdentities(
	identities []colimaProcessIdentity,
	deps colimaProcessReaperDependencies,
) ([]colimaProcessIdentity, error) {
	var current []colimaProcessIdentity
	for _, identity := range identities {
		isCurrent, err := currentProcessIdentity(identity, deps)
		if err != nil {
			return nil, fmt.Errorf("prove Colima profile process %d absent: %w", identity.pid, err)
		}
		if isCurrent {
			current = append(current, identity)
		}
	}
	return current, nil
}

func currentProcessIdentity(
	identity colimaProcessIdentity,
	deps colimaProcessReaperDependencies,
) (bool, error) {
	started, err := deps.started(identity.pid)
	if IsProcessGone(err) || (err == nil && started != identity.started) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	state, err := deps.state(identity.pid)
	if IsProcessGone(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if isZombieProcessState(state) {
		return false, nil
	}
	return true, nil
}

func processState(pid int) (string, error) {
	cmd := exec.Command(trustedPSPath(), "-o", "stat=", "-p", strconv.Itoa(pid))
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(strings.TrimSpace(string(output))) == 0 && len(strings.TrimSpace(string(exitErr.Stderr))) == 0 {
			return "", processGoneErr{pid: pid}
		}
		return "", err
	}
	fields := strings.Fields(string(output))
	if len(fields) == 0 {
		return "", processGoneErr{pid: pid}
	}
	if len(fields) != 1 {
		return "", fmt.Errorf("unexpected process %d state output", pid)
	}
	return fields[0], nil
}

func isZombieProcessState(state string) bool {
	return strings.HasPrefix(state, "Z")
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
