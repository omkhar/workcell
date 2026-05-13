// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/sessions"
)

// LogsMain implements `workcell session logs --id SESSION_ID --kind KIND`,
// the Go translation of the bash session_logs_main function in
// scripts/workcell. The matching kind values (audit/debug/file-trace/
// transcript) and error messages are kept byte-identical so existing
// callers, including verify-invariants.sh, see the same output.
func LogsMain(args []string) error {
	return logsMain(args, os.Stdout)
}

func logsMain(args []string, stdout io.Writer) error {
	roots, rest := consumeRootArgs(args)
	sessionID, kind, showHelp, err := parseLogsArgs(rest)
	if err != nil {
		return err
	}
	if showHelp {
		fmt.Print(UsageText())
		return nil
	}
	if sessionID == "" {
		return errors.New("workcell session logs requires --id.")
	}
	if kind == "" {
		return errors.New("workcell session logs requires --kind.")
	}
	if err := validateLogsKindName(kind); err != nil {
		return err
	}

	if len(roots) == 0 {
		roots = lookupRoots()
	}
	record, err := sessions.FindSessionRecordInRoots(roots, sessionID)
	if err != nil {
		return err
	}

	logPath := logPathForKind(record, kind)
	if logPath == "" {
		return fmt.Errorf("No %s log is recorded for session %s.", kind, sessionID)
	}

	resolved, err := hoststate.ResolveHostOutputCandidate(logPath)
	if err != nil || resolved != logPath {
		return fmt.Errorf("Workcell blocked host output path after launch: %s", logPath)
	}

	data, err := os.ReadFile(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("No %s log is recorded for session %s.", kind, sessionID)
		}
		return err
	}
	_, err = stdout.Write(data)
	return err
}

func parseLogsArgs(args []string) (sessionID, kind string, showHelp bool, err error) {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--id":
			if i+1 >= len(args) || args[i+1] == "" {
				return "", "", false, fmt.Errorf("--id requires a non-empty value")
			}
			sessionID = args[i+1]
			i++
		case "--kind":
			if i+1 >= len(args) || args[i+1] == "" {
				return "", "", false, fmt.Errorf("--kind requires a non-empty value")
			}
			kind = args[i+1]
			i++
		case "-h", "--help":
			showHelp = true
		default:
			return "", "", false, fmt.Errorf("Unsupported workcell session logs option: %s", args[i])
		}
	}
	return sessionID, kind, showHelp, nil
}

func validateLogsKindName(kind string) error {
	switch kind {
	case "audit", "debug", "file-trace", "transcript":
		return nil
	}
	return fmt.Errorf("Unsupported log kind: %s\nUse --logs audit, --logs debug, --logs file-trace, or --logs transcript.", kind)
}

// consumeRootArgs strips any leading --root=PATH arguments. Empty values
// are dropped to mirror scripts/workcell's session_lookup_root_args,
// which emits --root= even when one of the env vars is unset.
func consumeRootArgs(args []string) (roots, rest []string) {
	for len(args) > 0 && strings.HasPrefix(args[0], "--root=") {
		if v := strings.TrimPrefix(args[0], "--root="); v != "" {
			roots = append(roots, v)
		}
		args = args[1:]
	}
	return roots, args
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
