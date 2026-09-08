// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// repoRoot moved to exec_fixture_dir.go (a non-test file), so that
// ExecFixtureDir's checkout-root candidate — needed by non-test callers in
// other packages too — can reuse the same one implementation instead of a
// third copy.
package testkit
