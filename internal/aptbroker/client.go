// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package aptbroker

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"
)

func RunClient(
	ctx context.Context,
	socketPath string,
	args []string,
	preserve []string,
	lookup func(string) (string, bool),
) (Response, int, error) {
	environment, err := preservedEnvironment(preserve, lookup)
	if err != nil {
		return Response{}, 2, err
	}
	request, err := requestFrame(Request{Args: args, Env: environment})
	if err != nil {
		return Response{}, 2, err
	}
	conn, err := dialBroker(ctx, socketPath)
	if err != nil {
		return Response{}, 1, err
	}
	defer conn.Close()
	if _, err := conn.Write(request); err != nil {
		return Response{}, 1, err
	}
	return readClientResponse(ctx, conn)
}

func preservedEnvironment(preserve []string, lookup func(string) (string, bool)) (map[string]string, error) {
	environment := make(map[string]string, len(preserve))
	seen := make(map[string]struct{}, len(preserve))
	for _, name := range preserve {
		if !isAllowedEnvironment(name) {
			return nil, fmt.Errorf("unsupported preserved environment variable: %s", name)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("duplicate preserved environment variable: %s", name)
		}
		seen[name] = struct{}{}
		if value, ok := lookup(name); ok {
			environment[name] = value
		}
	}
	return environment, nil
}

func dialBroker(ctx context.Context, socketPath string) (*net.UnixConn, error) {
	if socketPath == "" {
		socketPath = DefaultSocketPath
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = conn.Close()
		return nil, fmt.Errorf("apt broker did not return a Unix connection")
	}
	return unixConn, nil
}

func readClientResponse(ctx context.Context, conn *net.UnixConn) (Response, int, error) {
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	result := make(chan responseResult, 1)
	go func() {
		response, err := readResponse(conn)
		result <- responseResult{response: response, err: err}
	}()
	select {
	case value := <-result:
		return clientResult(value)
	case received := <-signals:
		_ = conn.Close()
		return Response{}, signalStatus(received), context.Canceled
	case <-ctx.Done():
		_ = conn.Close()
		return Response{}, 1, ctx.Err()
	}
}

type responseResult struct {
	response Response
	err      error
}

func clientResult(result responseResult) (Response, int, error) {
	if result.err != nil {
		return Response{}, 1, result.err
	}
	return result.response, result.response.Status, nil
}

func signalStatus(received os.Signal) int {
	if received == syscall.SIGTERM {
		return 143
	}
	return 130
}

func readResponse(reader io.Reader) (Response, error) {
	maxBody := uint32(responseBaseSize + 2*MaxOutputBytes)
	body, err := readFrame(reader, responseMagic, maxBody)
	if err != nil {
		return Response{}, err
	}
	return decodeResponse(body)
}
