// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package egressproxy

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"regexp"
	"slices"
	"strings"
)

// denyLine is one deny log line: the running count of denials for one
// (host, port, reason). The proxy writes it and DenyAuditRecords reads it back.
type denyLine struct {
	Host   string `json:"host"`
	Port   uint16 `json:"port"`
	Reason string `json:"reason"`
	Count  uint64 `json:"count"`
}

const (
	// MaxDenyLogBytes is above the sidecar's 10 MiB json-file log limit.
	MaxDenyLogBytes  = 16 << 20
	maxDenyLineBytes = 4096
	// maxAuditDenyKeys bounds the egress_deny records of one session. The
	// summary still counts every key.
	maxAuditDenyKeys = 32
	invalidHost      = "(invalid)"
)

var (
	auditHost   = regexp.MustCompile(`^[A-Za-z0-9._-]{1,253}$`)
	auditReason = regexp.MustCompile(`^[a-z_]{1,32}$`)
)

// DenyAuditRecords folds a deny log into audit records, one per line: up to
// maxAuditDenyKeys "egress_deny host= port= reason= count=" records with the
// largest counts, then one "egress_summary" record. The log comes from the
// sidecar, so each line is untrusted: a line that is not one strict deny
// object is counted in malformed_lines, never dropped in silence. A host
// outside the DNS name characters is recorded as (invalid), so no value holds
// a space or a control character.
func DenyAuditRecords(log []byte) []string {
	counts := map[denyKey]uint64{}
	malformed := 0
	lines := strings.Split(string(log), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		d, ok := parseDenyLine(line)
		key := denyKey{d.Host, d.Port, d.Reason}
		if _, seen := counts[key]; ok && !seen && len(counts) >= 2*maxDenyKeys {
			ok = false // more keys than the proxy can write
		}
		if !ok {
			malformed++
			continue
		}
		counts[key] = max(counts[key], d.Count) // the running count only grows
	}
	keys := slices.SortedFunc(maps.Keys(counts), func(a, b denyKey) int {
		return cmp.Or(cmp.Compare(counts[b], counts[a]), cmp.Compare(a.host, b.host),
			cmp.Compare(a.port, b.port), cmp.Compare(a.reason, b.reason))
	})
	var total uint64
	records := make([]string, 0, min(len(keys), maxAuditDenyKeys)+1)
	for i, key := range keys {
		if sum := total + counts[key]; sum >= total {
			total = sum
		} else {
			total = math.MaxUint64 // saturate instead of wrapping
		}
		if i >= maxAuditDenyKeys {
			continue
		}
		host := key.host
		if host != "" && host != overflowHost && !auditHost.MatchString(host) {
			host = invalidHost
		}
		records = append(records, fmt.Sprintf("egress_deny host=%s port=%d reason=%s count=%d", host, key.port, key.reason, counts[key]))
	}
	status := "complete"
	if malformed > 0 {
		status = "malformed"
	} else if len(keys) > maxAuditDenyKeys {
		status = "truncated"
	}
	return append(records, fmt.Sprintf("egress_summary status=%s deny_keys=%d deny_total=%d malformed_lines=%d", status, len(keys), total, malformed))
}

// parseDenyLine accepts one JSON object with only the deny fields, a port and
// count above zero, a bounded host and a lower-case reason, and nothing after it.
func parseDenyLine(line string) (denyLine, bool) {
	var d denyLine
	if len(line) > maxDenyLineBytes {
		return d, false
	}
	dec := json.NewDecoder(strings.NewReader(line))
	dec.DisallowUnknownFields()
	if dec.Decode(&d) != nil || dec.InputOffset() != int64(len(line)) {
		return d, false
	}
	return d, d.Port != 0 && d.Count != 0 && len(d.Host) <= maxLogHost && auditReason.MatchString(d.Reason)
}
