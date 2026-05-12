// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package pinnedinputs carries CheckPinnedInputs and its
// pull-request-target / YAML helpers that used to live in
// internal/metadatautil/validate.go. The parent metadatautil package
// keeps a thin var shim so existing in-package tests and external
// callers (cmd/workcell-metadatautil) continue to compile against
// metadatautil.CheckPinnedInputs and metadatautil.PinnedInputsConfig.
package pinnedinputs
