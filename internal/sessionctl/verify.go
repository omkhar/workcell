// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/auditseal"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/host/stateroot"
	"github.com/omkhar/workcell/internal/supportbundle"
)

// VerifyMain implements `workcell session verify --id SESSION_ID`: it recomputes
// the session's audit hash-chain from the AUTHORITATIVE durable profile log and
// verifies the host-side signature over the chain head. It is fail-closed —
// any tamper (flipped byte, reorder, drop, duplicate key), a missing or
// mismatched signature, or an unknown signing key exits non-zero. Output is
// passed through the shared G2 support-bundle redactor so no secret leaks.
//
// Exit codes mirror the sibling read-only subcommands: 2 for usage errors, 1
// for a failed verification (tamper or missing/invalid seal), 0 when a genuine
// session verifies.
func VerifyMain(args []string) error {
	return verifyMain(args, os.Stdout)
}

func verifyMain(args []string, stdout io.Writer) error {
	roots, rest := stateroot.ConsumeRootArgs(args)
	sessionID, signingDir, realHome, showHelp, err := parseVerifyArgs(rest)
	if err != nil {
		return err
	}
	if showHelp {
		fmt.Fprint(stdout, UsageText())
		return nil
	}
	if sessionID == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session verify requires --id."}
	}
	if signingDir == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "workcell session verify requires --signing-dir."}
	}

	redactor := supportbundle.NewRedactor(realHome)

	roots, lookupErr := rootsOrLookup(roots)
	if lookupErr != nil {
		return lookupErr
	}
	record, recordPath, err := sessions.FindSessionRecordWithPathInRoots(roots, sessionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("No session record found for %s.", redactor.String(sessionID))}
		}
		// A malformed or unreadable record surfaces the absolute record path in the
		// lower-layer error; redact it (home prefix → ~, credentials masked) like
		// every other message here so verify honors its support-bundle-redacted
		// output contract even on the damaged-record paths operators inspect.
		return &cliexit.ExitCodeError{Code: 1, Message: redactor.String(fmt.Sprintf("Failed to read session record for %s: %v", sessionID, err))}
	}
	if record.AuditLogPath == "" {
		return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("Session %s has no recorded audit log to verify.", redactor.String(sessionID))}
	}

	// Derive the audit log path CANONICALLY from the trusted on-disk record
	// location, never from the free-form record.AuditLogPath field. recordPath is
	// where the profile-scoped lookup actually found the record —
	// <root>/targets/<kind>/<provider>/<profile>/sessions/<id>.json (or the legacy
	// <root>/<profile>/sessions/<id>.json) — so its grandparent is the profile
	// state dir, exactly where profile_audit_log_path/the signer place
	// workcell.audit.log. Recomputing the signed head over this canonical log
	// (not an attacker-supplied path) closes the offline tamper hole where a
	// rewritten AuditLogPath points verification at a pristine copy while the real
	// log is modified. This derivation is layout-agnostic (modern and legacy).
	canonicalAuditLog := filepath.Join(filepath.Dir(filepath.Dir(recordPath)), "workcell.audit.log")
	// Defense in depth: a record whose recorded path disagrees with its own
	// profile location is itself tampered — reject it fail-closed rather than
	// silently verifying against the canonical log.
	if filepath.Clean(record.AuditLogPath) != canonicalAuditLog {
		return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("Session %s FAILED audit verification: recorded audit log path does not match the profile's canonical location.", redactor.String(sessionID))}
	}

	sealPath := auditseal.SealPathForRecord(recordPath)
	seal, err := auditseal.ReadSeal(sealPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if !auditseal.HasSignableChain(canonicalAuditLog, record.TargetProvider, sessionID) {
				return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("Session %s uses a provider whose audit records have no signable digest chain (apple-container is preview-only); verification fails closed.", redactor.String(sessionID))}
			}
			return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("Session %s is not signed (no audit seal); verification fails closed.", redactor.String(sessionID))}
		}
		return &cliexit.ExitCodeError{Code: 1, Message: redactor.String(err.Error())}
	}

	head, err := auditseal.VerifySessionSeal(signingDir, canonicalAuditLog, record.TargetProvider, sessionID, seal)
	if err != nil {
		return &cliexit.ExitCodeError{Code: 1, Message: fmt.Sprintf("Session %s FAILED audit verification: %s", redactor.String(sessionID), redactor.String(err.Error()))}
	}

	fmt.Fprintf(stdout, "session_verify=verified\n")
	fmt.Fprintf(stdout, "session_id=%s\n", redactor.String(sessionID))
	fmt.Fprintf(stdout, "key_id=%s\n", redactor.String(seal.KeyID))
	fmt.Fprintf(stdout, "head_digest=%s\n", redactor.String(head))
	return nil
}

func parseVerifyArgs(args []string) (sessionID, signingDir, realHome string, showHelp bool, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--id":
			value, next, perr := optionValueOrErrorStrict(args, i, "--id")
			if perr != nil {
				return "", "", "", false, perr
			}
			sessionID = value
			i = next
		case strings.HasPrefix(arg, "--signing-dir="):
			signingDir = strings.TrimPrefix(arg, "--signing-dir=")
		case strings.HasPrefix(arg, "--real-home="):
			realHome = strings.TrimPrefix(arg, "--real-home=")
		case arg == "-h", arg == "--help":
			showHelp = true
		default:
			return "", "", "", false, unsupportedOption("session verify", arg)
		}
	}
	return sessionID, signingDir, realHome, showHelp, nil
}
