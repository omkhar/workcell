// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

//go:build !linux

package aptbroker

import (
	"context"
	"fmt"
	"net"
)

func runOwnedHelper(context.Context, *net.UnixConn, Request, helperConfig) (Response, error) {
	return Response{}, fmt.Errorf("apt broker server is unsupported on this platform")
}
