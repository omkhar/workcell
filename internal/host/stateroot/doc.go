// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package stateroot resolves and validates the durable workcell
// state-root directory layout (the parent of session-record files,
// audit dirs, profile lock dirs, and injection bundle caches).  It
// keeps the path-derivation contract in one place so cleanup paths
// (internal/host/hoststate) and the live launch path agree on which
// directories to touch.
package stateroot
