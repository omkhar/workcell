// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package sessionctl

import (
	"fmt"

	"github.com/omkhar/workcell/internal/host/auditseal"
	"github.com/omkhar/workcell/internal/host/sessions"
	"github.com/omkhar/workcell/internal/ocsf"
)

// sealedSnapshotRecords returns the launch record, the named snapshot record,
// and every record from the signed prefix of the session audit chain. command
// names the caller in each error. An empty snapshotID skips the snapshot.
func sealedSnapshotRecords(command, signingDir, sessionID, snapshotID string, record sessions.SessionRecord, recordPath string) (launch, snapshot map[string]string, records []map[string]string, err error) {
	auditLog, ok := canonicalSessionAuditLog(record, recordPath)
	if !ok {
		return nil, nil, nil, fmt.Errorf("%s: recorded audit log path does not match the profile's canonical location: %s", command, sessionID)
	}
	seal, err := auditseal.ReadSeal(auditseal.SealPathForRecord(recordPath))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s requires a signed audit chain for %s: %w", command, sessionID, err)
	}
	sealed, err := auditseal.SealedSessionRecords(signingDir, auditLog, record.TargetProvider, sessionID, seal)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: audit verification failed for %s: %w", command, sessionID, err)
	}
	for _, fields := range sealed {
		values := auditFieldMap(fields)
		records = append(records, values)
		switch {
		case values["event"] == "launch" && launch == nil:
			launch = values
		case values["event"] == "session_snapshot" && values["snapshot_id"] == snapshotID:
			snapshot = values
		}
	}
	if launch == nil || launch["agent"] == "" || launch["mode"] == "" {
		return nil, nil, nil, fmt.Errorf("%s: no signed launch record for %s", command, sessionID)
	}
	if snapshotID != "" && (snapshot == nil || !gitObjectIDPattern.MatchString(snapshot["commit"]) || !gitObjectIDPattern.MatchString(snapshot["tree"])) {
		return nil, nil, nil, fmt.Errorf("%s: no signed session_snapshot record %s for %s", command, snapshotID, sessionID)
	}
	return launch, snapshot, records, nil
}

// recordMatchesSignedLaunch reports whether the unsigned session record names
// the workspace and git head of the signed launch record. A snapshot captures
// the record values, so they must be the signed ones.
func recordMatchesSignedLaunch(record sessions.SessionRecord, launch map[string]string) bool {
	return launch["workspace"] == record.Workspace && launch["workspace_head"] == record.GitHead &&
		gitObjectIDPattern.MatchString(launch["workspace_head"])
}

// snapshotMatchesSignedLaunch reports whether the signed snapshot record names
// the workspace and head of the signed launch record. The unsigned session
// record can change between a check and the capture.
func snapshotMatchesSignedLaunch(snapshot, launch map[string]string) bool {
	return snapshot["snapshot_workspace"] == launch["workspace"] && snapshot["snapshot_head"] == launch["workspace_head"]
}

func auditFieldMap(fields []ocsf.AuditField) map[string]string {
	values := make(map[string]string, len(fields))
	for _, field := range fields {
		values[field.Key] = field.Value
	}
	return values
}
