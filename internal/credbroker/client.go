// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package credbroker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"time"
)

// DenyError is a refusal from the broker; its value is one of the Err* codes.
type DenyError string

func (e DenyError) Error() string { return "credential broker refused: " + string(e) }

// Lookup asks the broker at socketPath for the value to send in header to host.
func Lookup(ctx context.Context, socketPath, token, host, header string) (string, error) {
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(ioTimeout)
	}
	_ = connection.SetDeadline(deadline)
	if err := json.NewEncoder(connection).Encode(Request{Token: token, Host: host, Header: header}); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(io.LimitReader(connection, maxLineBytes)).ReadBytes('\n')
	if err != nil {
		return "", err
	}
	var response Response
	if err := json.Unmarshal(line, &response); err != nil {
		return "", err
	}
	if response.Error != "" {
		return "", DenyError(response.Error)
	}
	if response.Value == "" {
		return "", errors.New("credential broker returned an empty value")
	}
	return response.Value, nil
}
