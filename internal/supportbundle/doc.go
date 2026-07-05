// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package supportbundle implements `workcell support-bundle`, the host-side
// diagnostics collector (roadmap item G2).
//
// It gathers six evidence classes needed to diagnose install, policy, target,
// provider, and runtime failures:
//
//   - install:        launcher presence/path, repo root, host os/arch, version
//   - policy:         repo policy inventory + user injection-policy presence
//   - target:         state-root layout, colima profile directories/markers
//   - providers:      per-provider adapter presence + credential container path
//   - sessions:       durable session-record summaries (never workspace bodies)
//   - audit_pointers: audit-log path metadata (path/size/mtime; never content)
//
// The bundle is a single deterministic JSON document: struct field order is
// stable, every slice is sorted, and the only time-varying field
// (generated_at) is injected through Config so golden tests are hermetic.
//
// Redaction is the security core (see redact.go). The collector NEVER reads
// credential-file contents, workspace files, or log bodies — it records paths,
// presence, and metadata only. As defense-in-depth every collected string is
// additionally passed through the secret redactor before it enters the bundle.
package supportbundle
