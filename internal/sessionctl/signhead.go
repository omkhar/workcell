// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/host/auditseal"
)

// SignHeadMain implements the host-side signing hook invoked from
// scripts/workcell's finalize_session_audit after the terminal audit record is
// appended and the durable session record is finalized. It recomputes the
// session's audit-chain head from the authoritative log and writes a signed
// seal beside the durable record.
//
// It is deliberately NOT fatal to session teardown: signing failures are
// surfaced to the caller (which logs a warning and continues) so a signing
// problem never wedges container cleanup. The absence of a valid seal makes
// `session verify` fail closed, which is the correct fail-closed posture.
//
// Flags (all required except --signed-at):
//
//	--signing-dir=DIR      per-host signing key/pubkey directory
//	--audit-log=PATH       authoritative profile audit log
//	--session-id=ID        session whose head is signed
//	--record-path=PATH     durable session record (the seal is written beside it)
//	--provider=NAME        target provider (selects the audit-line decoder)
//	--signed-at=RFC3339    signature timestamp (defaults to now, UTC)
func SignHeadMain(args []string) error {
	var signingDir, auditLog, sessionID, recordPath, provider, signedAt string
	for _, arg := range args {
		switch {
		case strings.HasPrefix(arg, "--signing-dir="):
			signingDir = strings.TrimPrefix(arg, "--signing-dir=")
		case strings.HasPrefix(arg, "--audit-log="):
			auditLog = strings.TrimPrefix(arg, "--audit-log=")
		case strings.HasPrefix(arg, "--session-id="):
			sessionID = strings.TrimPrefix(arg, "--session-id=")
		case strings.HasPrefix(arg, "--record-path="):
			recordPath = strings.TrimPrefix(arg, "--record-path=")
		case strings.HasPrefix(arg, "--provider="):
			provider = strings.TrimPrefix(arg, "--provider=")
		case strings.HasPrefix(arg, "--signed-at="):
			signedAt = strings.TrimPrefix(arg, "--signed-at=")
		default:
			return &cliexit.ExitCodeError{Code: 2, Message: fmt.Sprintf("Unsupported workcell session sign-head option: %s", arg)}
		}
	}
	if signingDir == "" || auditLog == "" || sessionID == "" || recordPath == "" {
		return &cliexit.ExitCodeError{Code: 2, Message: "session sign-head requires --signing-dir, --audit-log, --session-id, and --record-path."}
	}
	if signedAt == "" {
		signedAt = time.Now().UTC().Format(time.RFC3339)
	}

	seal, err := auditseal.SignSessionHead(signingDir, auditLog, provider, sessionID, signedAt)
	if err != nil {
		if errors.Is(err, auditseal.ErrUnsupportedAuditChain) {
			// A provider with no digest chain (e.g. the preview-only
			// apple-container target) is scoped out of signing: leave the
			// session unsigned rather than emit a seal that can never verify.
			// `session verify` reports it as unsigned, fail-closed.
			return nil
		}
		return err
	}
	return auditseal.WriteSeal(auditseal.SealPathForRecord(recordPath), seal)
}
