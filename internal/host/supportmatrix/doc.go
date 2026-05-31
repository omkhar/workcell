// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package supportmatrix carries the host-side support tier classification:
// which host OS, architecture, distro, distro version, runtime target, and
// assurance combinations policy/host-support-matrix.tsv expresses. The launch
// path uses this to fail-fast on unsupported hosts and to decide which
// assurance label a session entry record should carry.
package supportmatrix
