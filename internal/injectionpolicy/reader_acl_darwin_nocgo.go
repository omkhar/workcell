// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build darwin && !cgo

package injectionpolicy

import "errors"

func rejectPolicyExtendedACL(int, bool) error {
	return errors.New("cannot inspect extended ACL without cgo")
}
