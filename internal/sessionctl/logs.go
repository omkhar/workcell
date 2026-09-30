// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/host/stateroot"
	"github.com/omkhar/workcell/internal/rootio"
)

// resolveLogPath is a test seam for the path check that precedes the open.
var resolveLogPath = hoststate.ResolveHostOutputCandidate

// LogsMain implements `workcell session logs --id SESSION_ID --kind KIND`,
// the Go translation of the bash session_logs_main function in
// scripts/workcell. The matching kind values (audit/debug/file-trace/
// transcript) and error messages are kept byte-identical so existing
// callers, including verify-invariants.sh, see the same output.
func LogsMain(args []string) error {
	return logsMain(args, os.Stdout)
}

func logsMain(args []string, stdout io.Writer) error {
	roots, rest := stateroot.ConsumeRootArgs(args)
	sessionID, kind, showHelp, err := parseLogsArgs(rest)
	if err != nil {
		return err
	}
	if showHelp {
		fmt.Print(UsageText())
		return nil
	}
	if sessionID == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session logs requires --id."}
	}
	if kind == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session logs requires --kind."}
	}
	if err := validateLogsKindName(kind); err != nil {
		return err
	}

	roots, lookupErr := rootsOrLookup(roots)
	if lookupErr != nil {
		return lookupErr
	}
	record, err := sessions.FindSessionRecordInRoots(roots, sessionID)
	if err != nil {
		return err
	}

	logPath := logPathForKind(record, kind)
	if logPath == "" {
		return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("No %s log is recorded for session %s.", kind, sessionID)}
	}

	resolved, err := resolveLogPath(logPath)
	if err != nil || resolved != logPath {
		return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("Workcell blocked host output path after launch: %s", logPath)}
	}

	file, err := openLogNoFollow(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("No %s log is recorded for session %s.", kind, sessionID)}
		}
		return err
	}
	defer file.Close()
	_, err = io.Copy(stdout, file)
	return err
}

// openLogNoFollow opens a recorded log through a verified parent handle and
// refuses a symlink leaf or a non-regular file, so a swap after the path check
// cannot redirect the read.
func openLogNoFollow(path string) (*os.File, error) {
	parent, cleaned, err := rootio.OpenParentDirectoryNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	name := filepath.Base(cleaned)
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), cleaned)
	info, err := file.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fmt.Errorf("log must be a regular file: %s", name)
	}
	if err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func parseLogsArgs(args []string) (sessionID, kind string, showHelp bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			value, next, perr := optionValueOrErrorStrict(args, i, "--id")
			if perr != nil {
				return "", "", false, perr
			}
			sessionID = value
			i = next
		case "--kind":
			value, next, perr := optionValueOrError(args, i, "--kind")
			if perr != nil {
				return "", "", false, perr
			}
			kind = value
			i = next
		case "-h", "--help":
			showHelp = true
		default:
			return "", "", false, unsupportedOption("session logs", args[i])
		}
	}
	return sessionID, kind, showHelp, nil
}

func validateLogsKindName(kind string) error {
	switch kind {
	case "audit", "debug", "file-trace", "transcript":
		return nil
	}
	return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("Unsupported log kind: %s\nUse --logs audit, --logs debug, --logs file-trace, or --logs transcript.", kind)}
}

func logPathForKind(record sessions.SessionRecord, kind string) string {
	switch kind {
	case "audit":
		return record.AuditLogPath
	case "debug":
		return record.DebugLogPath
	case "file-trace":
		return record.FileTraceLogPath
	case "transcript":
		return record.TranscriptLogPath
	}
	return ""
}
