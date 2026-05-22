// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package injectionpolicy holds the types and helpers shared across
// the workcell injection-policy engines (internal/authpolicy,
// internal/authresolve, internal/injection).  Historically each
// package maintained its own copy of PolicySource and a handful of
// load/parse helpers; drift between the copies caused at least one
// silent-empty-hash regression (see commit 2d4c27f in PR #267).  This
// package is the single source of truth for the cross-package types.
//
// Per-package implementations of policy loading, validation, and
// bundle rendering remain in their own packages — they have subtle
// differences (e.g. Path-typed paths in internal/injection vs raw
// strings in internal/authpolicy) that are not load-bearing enough
// to force a full unification yet.  An issue tracks the follow-up
// function-dedup work; this package is the type-level first step.
package injectionpolicy

// PolicySource is the per-policy-file metadata that every
// injection-policy reader emits.  Field names + JSON tags here are
// the canonical form; package-local types in authpolicy/injection/
// authresolve must alias this type rather than re-declare it.
type PolicySource struct {
	// Path is the logical (entrypoint-relative when known, otherwise
	// absolute) path of the policy file.
	Path string `json:"path"`
	// Sha256 is the "sha256:<hex>" content digest of the policy file.
	// The "sha256:" prefix is part of the value; downstream consumers
	// rely on the prefix to distinguish the digest from a bare hex
	// string.
	Sha256 string `json:"sha256"`
}
