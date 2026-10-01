// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/host/stateroot"
	"github.com/omkhar/workcell/internal/shellproto"
)

// gitObjectIDPattern accepts a full SHA-1 or SHA-256 object name. The
// recorded git_head reaches `git commit-tree -p` as an argument, so a value
// that is not a plain object name (for example one that starts with `-`) is
// refused here.
var gitObjectIDPattern = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64})$`)

// SnapshotMain implements the option-parsing and record-validation half of
// `workcell session snapshot --id SESSION_ID`.
//
// The bash shim (session_snapshot_main in scripts/workcell) owns the side
// effects: docker pause/unpause, the git capture into the host-owned
// snapshot store, and the signed audit append. SnapshotMain emits the plan:
//
//	session_id=<id>
//	profile=<profile>
//	container_name=<container>
//	workspace=<workspace>
//	git_head=<object id>
//	origin_hash=<sha256 hex of the parallel-session origin key>
//	pause=0|1
//
// pause=1 means the record is live (starting or running), so the shim must
// freeze the container while it reads the workspace. A terminal record gets
// pause=0. Any other live status (for example stopping) is refused.
func SnapshotMain(args []string) error {
	return snapshotMain(args, os.Stdout, os.Stderr)
}

func snapshotMain(args []string, stdout, stderr io.Writer) error {
	roots, rest := stateroot.ConsumeRootArgs(args)
	sessionID, showHelp, err := parseSnapshotArgs(rest)
	if err != nil {
		return err
	}
	if showHelp {
		fmt.Fprint(stderr, UsageText())
		return nil
	}
	if sessionID == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session snapshot requires --id."}
	}
	if err := rejectControlChars("session snapshot", "--id", sessionID); err != nil {
		return err
	}

	roots, err = rootsOrLookup(roots)
	if err != nil {
		return err
	}
	record, err := sessions.FindSessionRecordInRoots(roots, sessionID)
	if err != nil {
		return err
	}
	if record.Profile == "" {
		return fmt.Errorf("session snapshot record is missing a profile: %s", sessionID)
	}
	if !sessionIsDetached(record) {
		return fmt.Errorf("session snapshot only works for detached sessions started with 'workcell session start': %s", sessionID)
	}
	if record.ContainerName == "" {
		return fmt.Errorf("session snapshot record is missing a container name: %s", sessionID)
	}
	if record.Workspace == "" {
		return fmt.Errorf("session snapshot record is missing a workspace path: %s", sessionID)
	}
	if !gitObjectIDPattern.MatchString(record.GitHead) {
		return fmt.Errorf("session snapshot requires a recorded git head commit: %s", sessionID)
	}

	pause := ""
	switch sessions.SessionDisplayLiveStatus(record) {
	case "starting", "running":
		pause = "1"
	case "stopped", "exited", "failed", "aborted":
		pause = "0"
	default:
		return fmt.Errorf("session snapshot requires a running or terminal detached session: %s", sessionID)
	}

	originHash := sha256.Sum256([]byte(sessions.SessionParallelGroupKey(record)))
	return shellproto.WriteFields(stdout, []shellproto.Field{
		{Key: "session_id", Value: record.SessionID},
		{Key: "profile", Value: record.Profile},
		{Key: "container_name", Value: record.ContainerName},
		{Key: "workspace", Value: record.Workspace},
		{Key: "git_head", Value: record.GitHead},
		{Key: "origin_hash", Value: hex.EncodeToString(originHash[:])},
		{Key: "pause", Value: pause},
	})
}

func parseSnapshotArgs(args []string) (sessionID string, showHelp bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			v, ni, perr := optionValueOrErrorStrict(args, i, "--id")
			if perr != nil {
				return "", false, perr
			}
			sessionID = v
			i = ni
		case "-h", "--help":
			showHelp = true
		default:
			return "", false, unsupportedOption("session snapshot", args[i])
		}
	}
	return sessionID, showHelp, nil
}
