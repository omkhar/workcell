// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
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
	signal    func(int, syscall.Signal) error
	sleep     func(context.Context, time.Duration) error
	termDelay time.Duration
	killDelay time.Duration
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
		signal:    syscall.Kill,
		sleep:     sleepWithContext,
		termDelay: time.Second,
		killDelay: 100 * time.Millisecond,
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
	if len(owned) > 0 {
		if err := deps.sleep(ctx, deps.termDelay); err != nil {
			return err
		}
	}

	survivors, err := currentProcessIdentities(owned, deps)
	if err != nil {
		return err
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
	if len(survivors) > 0 {
		if err := deps.sleep(ctx, deps.killDelay); err != nil {
			return err
		}
	}

	remaining, err := currentProcessIdentities(survivors, deps)
	if err != nil {
		return err
	}
	fresh, err = captureColimaProcessIdentities(ctx, profile, deps)
	if err != nil {
		return err
	}
	if len(remaining)+len(fresh) != 0 {
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
	for _, pid := range pids {
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
	identitySet := make(map[int]struct{}, len(identities))
	stableIdentities := make([]colimaProcessIdentity, 0, len(identities))
	for _, identity := range identities {
		identitySet[identity.pid] = struct{}{}
		started, err := deps.started(identity.pid)
		if _, exists := stableSet[identity.pid]; !exists {
			if IsProcessGone(err) || (err == nil && started != identity.started) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("revalidate Colima profile process %d identity: %w", identity.pid, err)
			}
			return nil, errors.New("colima profile process command identity changed during capture")
		}
		if IsProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("revalidate Colima profile process %d identity: %w", identity.pid, err)
		}
		if started != identity.started {
			return nil, errors.New("colima profile process identity changed during capture")
		}
		stableIdentities = append(stableIdentities, identity)
	}
	for _, pid := range stablePIDs {
		if _, exists := identitySet[pid]; !exists {
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
		started, err := deps.started(identity.pid)
		if IsProcessGone(err) || (err == nil && started != identity.started) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("prove Colima profile process %d absent: %w", identity.pid, err)
		}
		current = append(current, identity)
	}
	return current, nil
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
