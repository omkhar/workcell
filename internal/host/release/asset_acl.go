// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package release

import (
	"errors"

	"github.com/omkhar/workcell/internal/rootio"
)

// rejectExtendedACL classifies an extended ACL on fd as a publisher input
// error; every other inspection failure passes through unchanged.
func rejectExtendedACL(fd int) error {
	err := rootio.RejectExtendedACL(fd)
	if errors.Is(err, rootio.ErrExtendedACL) {
		return inputErrorf("%v", rootio.ErrExtendedACL)
	}
	return err
}
