// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/auditseal"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/host/stateroot"
	"github.com/omkhar/workcell/internal/ocsf"
	"github.com/omkhar/workcell/internal/shellproto"
)

// forkCountPattern accepts 1 to 8, the most children one `session fork` starts.
var forkCountPattern = regexp.MustCompile(`^[1-8]$`)
var (
	forkContainerCPUPattern    = regexp.MustCompile(`^(unmanaged|[0-9]+([.][0-9]+)?)$`)
	forkContainerMemoryPattern = regexp.MustCompile(`^[0-9]+([kKmMgGtTpP][iI]?[bB]?)?$`)
)
var forkVMResourcePattern = regexp.MustCompile(`^[1-9][0-9]{0,5}$`)

// forkExecutionPaths are the parent execution paths a child can reproduce
// from the signed launch record. The other lower-assurance paths need a dated
// operator acknowledgement that fork does not replay.
var forkExecutionPaths = map[string]bool{
	"managed-tier1":                 true,
	"lower-assurance-development":   true,
	"lower-assurance-debug-command": true,
}

type forkArgs struct {
	sessionID, snapshotID, count, signingDir, ackArbitrary string
	allowArbitrary, showHelp                               bool
	command                                                []string
}

// ForkMain implements the plan half of
// `workcell session fork --id SESSION_ID [--snapshot SNAPSHOT_ID] --count N`.
//
// The bash shim (session_fork_main in scripts/workcell) takes the snapshot,
// starts the children, and appends the session_fork audit record. ForkMain
// reads the snapshot commit and the parent launch settings only from audit
// records that the host seal covers, never from a ref in the snapshot store.
//
// Without --snapshot it checks the signed launch record of a sealed parent, or
// the recorded execution path of an unsealed one, then emits session_id=,
// needs_snapshot=1, and the acknowledgement. The shim checks the
// acknowledgement date, takes a snapshot, and calls ForkMain again with
// --snapshot.
func ForkMain(args []string) error {
	return forkMain(args, os.Stdout, os.Stderr)
}

func forkMain(args []string, stdout, stderr io.Writer) error {
	roots, rest := stateroot.ConsumeRootArgs(args)
	opts, err := parseForkArgs(rest)
	if err != nil {
		return err
	}
	if opts.showHelp {
		fmt.Fprint(stderr, UsageText())
		return nil
	}
	if err := validateForkArgs(opts); err != nil {
		return err
	}
	roots, err = rootsOrLookup(roots)
	if err != nil {
		return err
	}
	record, recordPath, err := sessions.FindSessionRecordWithPathInRoots(roots, opts.sessionID)
	if err != nil {
		return err
	}
	if !sessionIsDetached(record) || record.Profile == "" {
		return fmt.Errorf("session fork only works for detached sessions started with 'workcell session start': %s", opts.sessionID)
	}
	if record.TargetProvider != "colima" {
		return fmt.Errorf("session fork supports only the colima target: %s", opts.sessionID)
	}
	origin := sessions.SessionParallelGroupKey(record)
	if origin == "" {
		return fmt.Errorf("session fork record is missing a workspace origin: %s", opts.sessionID)
	}
	if opts.snapshotID == "" {
		// A sealed parent, such as a stopped one, has signed launch settings
		// that can refuse before a snapshot. A running parent has no seal yet,
		// so only the unsigned session record can refuse early there.
		_, sealErr := auditseal.ReadSeal(auditseal.SealPathForRecord(recordPath))
		switch {
		case sealErr == nil:
			launch, _, err := sealedForkRecords(opts, record, recordPath)
			if err != nil {
				return err
			}
			if _, err := validateForkLaunch(opts, record, origin, launch); err != nil {
				return err
			}
		case !errors.Is(sealErr, os.ErrNotExist):
			return fmt.Errorf("session fork requires a signed audit chain for %s: %w", opts.sessionID, sealErr)
		case record.ExecutionPath != "":
			if err := checkForkExecutionPath(opts, record.ExecutionPath); err != nil {
				return err
			}
		}
		return shellproto.WriteFields(stdout, []shellproto.Field{
			{Key: "session_id", Value: record.SessionID},
			{Key: "needs_snapshot", Value: "1"},
			{Key: "ack_arbitrary_command", Value: opts.ackArbitrary},
		})
	}

	launch, snapshot, err := sealedForkRecords(opts, record, recordPath)
	if err != nil {
		return err
	}
	containerMutability, err := validateForkLaunch(opts, record, origin, launch)
	if err != nil {
		return err
	}
	originHash := sha256.Sum256([]byte(origin))
	return shellproto.WriteFields(stdout, []shellproto.Field{
		{Key: "session_id", Value: record.SessionID},
		{Key: "profile", Value: record.Profile},
		{Key: "workspace_origin", Value: origin},
		{Key: "origin_hash", Value: hex.EncodeToString(originHash[:])},
		{Key: "agent", Value: launch["agent"]},
		{Key: "mode", Value: launch["mode"]},
		{Key: "agent_autonomy", Value: launch["agent_autonomy"]},
		{Key: "container_mutability", Value: containerMutability},
		{Key: "launch_head", Value: launch["workspace_head"]},
		{Key: "vm_cpu", Value: launch["vm_cpu"]},
		{Key: "vm_memory_gib", Value: launch["vm_memory_gib"]},
		{Key: "vm_disk_gib", Value: launch["vm_disk_gib"]},
		{Key: "container_cpu", Value: launch["container_cpu"]},
		{Key: "container_memory", Value: launch["container_memory"]},
		{Key: "injection_policy_sha256", Value: launch["injection_policy_sha256"]},
		{Key: "snapshot_id", Value: opts.snapshotID},
		{Key: "commit", Value: snapshot["commit"]},
		{Key: "tree", Value: snapshot["tree"]},
		{Key: "count", Value: opts.count},
		{Key: "ack_arbitrary_command", Value: opts.ackArbitrary},
	})
}

func parseForkArgs(args []string) (forkArgs, error) {
	opts := forkArgs{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--":
			opts.command = args[i+1:]
			return opts, nil
		case arg == "--id" || arg == "--snapshot" || arg == "--count":
			v, ni, err := optionValueOrErrorStrict(args, i, arg)
			if err != nil {
				return opts, err
			}
			switch arg {
			case "--id":
				opts.sessionID = v
			case "--snapshot":
				opts.snapshotID = v
			default:
				opts.count = v
			}
			i = ni
		case strings.HasPrefix(arg, "--signing-dir="):
			if opts.signingDir != "" {
				return opts, &cliexit.ExitCodeError{Code: 2, Message: "workcell session fork accepts --signing-dir only once."}
			}
			opts.signingDir = strings.TrimPrefix(arg, "--signing-dir=")
		case arg == "--allow-arbitrary-command":
			opts.allowArbitrary = true
		case strings.HasPrefix(arg, "--ack-arbitrary-command="):
			opts.ackArbitrary = strings.TrimPrefix(arg, "--ack-arbitrary-command=")
		case arg == "-h" || arg == "--help":
			opts.showHelp = true
		default:
			return opts, unsupportedOption("session fork", arg)
		}
	}
	return opts, nil
}

func validateForkArgs(opts forkArgs) error {
	if opts.sessionID == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session fork requires --id."}
	}
	if !forkCountPattern.MatchString(opts.count) {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session fork requires --count from 1 to 8."}
	}
	if opts.signingDir == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session fork requires --signing-dir."}
	}
	for flag, value := range map[string]string{"--id": opts.sessionID, "--snapshot": opts.snapshotID, "--ack-arbitrary-command": opts.ackArbitrary} {
		if err := rejectControlChars("session fork", flag, value); err != nil {
			return err
		}
	}
	if opts.snapshotID != "" && !snapshotNamePattern.MatchString(opts.snapshotID) {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session fork --snapshot is not a snapshot id."}
	}
	return nil
}

// sealedForkRecords returns the parent launch record and the named snapshot
// record from the signed prefix of the parent audit chain.
func sealedForkRecords(opts forkArgs, record sessions.SessionRecord, recordPath string) (launch, snapshot map[string]string, err error) {
	auditLog, ok := canonicalSessionAuditLog(record, recordPath)
	if !ok {
		return nil, nil, fmt.Errorf("session fork: recorded audit log path does not match the profile's canonical location: %s", opts.sessionID)
	}
	seal, err := auditseal.ReadSeal(auditseal.SealPathForRecord(recordPath))
	if err != nil {
		return nil, nil, fmt.Errorf("session fork requires a signed audit chain for %s: %w", opts.sessionID, err)
	}
	records, err := auditseal.SealedSessionRecords(opts.signingDir, auditLog, record.TargetProvider, opts.sessionID, seal)
	if err != nil {
		return nil, nil, fmt.Errorf("session fork: audit verification failed for %s: %w", opts.sessionID, err)
	}
	for _, fields := range records {
		values := auditFieldMap(fields)
		switch {
		case values["event"] == "launch" && launch == nil:
			launch = values
		case values["event"] == "session_snapshot" && values["snapshot_id"] == opts.snapshotID:
			snapshot = values
		}
	}
	if launch == nil || launch["agent"] == "" || launch["mode"] == "" {
		return nil, nil, fmt.Errorf("session fork: no signed launch record for %s", opts.sessionID)
	}
	if opts.snapshotID != "" && (snapshot == nil || !gitObjectIDPattern.MatchString(snapshot["commit"]) || !gitObjectIDPattern.MatchString(snapshot["tree"])) {
		return nil, nil, fmt.Errorf("session fork: no signed session_snapshot record %s for %s", opts.snapshotID, opts.sessionID)
	}
	return launch, snapshot, nil
}

func auditFieldMap(fields []ocsf.AuditField) map[string]string {
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		values[field.Key] = field.Value
	}
	return values
}

// checkForkExecutionPath keeps each child on the parent execution path. A
// debug-command parent needs the operator to acknowledge the arbitrary
// command again and to give it after --; any other parent refuses those flags.
func checkForkExecutionPath(opts forkArgs, executionPath string) error {
	if !forkExecutionPaths[executionPath] {
		return fmt.Errorf("session fork does not support the parent execution path %q: %s", executionPath, opts.sessionID)
	}
	gaveCommand := opts.allowArbitrary || opts.ackArbitrary != "" || len(opts.command) > 0
	if executionPath != "lower-assurance-debug-command" {
		if gaveCommand {
			return &cliexit.ExitCodeError{Code: 2, Message: "workcell session fork accepts --allow-arbitrary-command only when the parent ran an arbitrary command."}
		}
		return nil
	}
	if !opts.allowArbitrary || opts.ackArbitrary == "" || len(opts.command) == 0 {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session fork of an arbitrary-command parent requires --allow-arbitrary-command, --ack-arbitrary-command=YYYY-MM-DD, and a command after --."}
	}
	return nil
}

// validateForkLaunch checks the signed launch record against the durable
// session record and the fork request. It returns the container mutability a
// child replays.
func validateForkLaunch(opts forkArgs, record sessions.SessionRecord, origin string, launch map[string]string) (string, error) {
	if err := checkForkExecutionPath(opts, launch["execution_path"]); err != nil {
		return "", err
	}
	// The child launches with --target colima, so the signed launch must name
	// colima and agree with the durable record.
	if launch["target_provider"] != "colima" || record.TargetProvider != "colima" {
		return "", fmt.Errorf("session fork supports only a parent that the signed launch record shows on the colima target: %s", opts.sessionID)
	}
	// The unsigned record picks the clone source, so the signed launch record
	// must name the same origin. A child does not replay provider arguments, so
	// a parent that ran with any is refused instead of forked with the default.
	if launch["workspace_origin"] != origin {
		return "", fmt.Errorf("session fork: the session record workspace origin does not match the signed launch record: %s", opts.sessionID)
	}
	if launch["provider_arg_count"] != "0" {
		return "", fmt.Errorf("session fork does not support a parent that ran with provider arguments: %s", opts.sessionID)
	}
	// The child reuses this profile, so it comes from the signed record. A
	// durable session record that names another profile is refused.
	if launch["profile"] != record.Profile {
		return "", fmt.Errorf("session fork: the session record profile does not match the signed launch record: %s", opts.sessionID)
	}
	// The snapshot step already captured the unsigned workspace and git head,
	// so they must match the signed launch before any child starts.
	if launch["workspace"] != record.Workspace || launch["workspace_head"] != record.GitHead || !gitObjectIDPattern.MatchString(launch["workspace_head"]) {
		return "", fmt.Errorf("session fork: the session record workspace or git head does not match the signed launch record: %s", opts.sessionID)
	}
	// A child replays the parent rootfs posture. Without it a readonly parent
	// forks into the ephemeral default, a wider posture.
	var containerMutability string
	switch launch["container_assurance"] {
	case "managed-readonly":
		containerMutability = "readonly"
	case "managed-mutable":
		containerMutability = "ephemeral"
	default:
		return "", fmt.Errorf("session fork does not support the parent container assurance %q: %s", launch["container_assurance"], opts.sessionID)
	}
	// A child reuses the parent profile. Resource flags that differ from the
	// profile make a launch delete the shared VM, so a child replays the parent
	// values, and a parent without them is refused.
	for _, key := range []string{"vm_cpu", "vm_memory_gib", "vm_disk_gib"} {
		if !forkVMResourcePattern.MatchString(launch[key]) {
			return "", fmt.Errorf("session fork needs the parent VM resources in the signed launch record (%s): %s", key, opts.sessionID)
		}
	}
	// Container limits matter the same way: up to eight children share the VM.
	if !forkContainerCPUPattern.MatchString(launch["container_cpu"]) || !forkContainerMemoryPattern.MatchString(launch["container_memory"]) {
		return "", fmt.Errorf("session fork needs the parent container limits in the signed launch record: %s", opts.sessionID)
	}
	return containerMutability, nil
}
