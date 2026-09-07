// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package aptbroker

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestPreservedEnvironmentUsesOnlyRequestedApprovedNames(t *testing.T) {
	lookup := func(name string) (string, bool) {
		values := map[string]string{
			"APT_LISTCHANGES_FRONTEND":    "none",
			"DEBCONF_NONINTERACTIVE_SEEN": "true",
			"DEBIAN_FRONTEND":             "noninteractive",
		}
		value, ok := values[name]
		return value, ok
	}
	got, err := preservedEnvironment([]string{"DEBIAN_FRONTEND", "APT_LISTCHANGES_FRONTEND"}, lookup)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"DEBIAN_FRONTEND": "noninteractive", "APT_LISTCHANGES_FRONTEND": "none"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("preservedEnvironment() = %#v, want %#v", got, want)
	}
}

func TestPreservedEnvironmentRejectsUnsupportedAndDuplicateNames(t *testing.T) {
	lookup := func(string) (string, bool) { return "noninteractive", true }
	for name, preserve := range map[string][]string{
		"unsupported": {"PATH"},
		"duplicate":   {"DEBIAN_FRONTEND", "DEBIAN_FRONTEND"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := preservedEnvironment(preserve, lookup); err == nil {
				t.Fatal("preservedEnvironment() accepted invalid names")
			}
		})
	}
}

func TestRunClientRejectsMalformedRequestBeforeDial(t *testing.T) {
	response, status, err := RunClient(context.Background(), "/does/not/exist", nil, nil, func(string) (string, bool) { return "", false })
	if err == nil || status != 2 || !reflect.DeepEqual(response, Response{}) {
		t.Fatalf("RunClient() response=%#v status=%d error=%v", response, status, err)
	}
}

func TestRunClientCancelsWhileTheBrokerNeverReadsTheRequest(t *testing.T) {
	directory, err := os.MkdirTemp("", "wcbr")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(directory)
	socketPath := filepath.Join(directory, "s")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	// One request large enough to exceed any socket send buffer, so the write
	// blocks until the broker reads. It never does.
	args := make([]string, MaxArguments)
	for index := range args {
		args[index] = strings.Repeat("a", MaxRequestBytes/MaxArguments-8)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	done := make(chan int, 1)
	go func() {
		_, status, _ := RunClient(ctx, socketPath, args, nil, func(string) (string, bool) { return "", false })
		done <- status
	}()
	select {
	case status := <-done:
		if status != 1 {
			t.Fatalf("RunClient() status = %d, want 1", status)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("RunClient() ignored the cancelled context during the request write")
	}
	select {
	case conn := <-accepted:
		conn.Close()
	default:
	}
}

func TestRunClientRejectsPreservedInteractiveValueBeforeDial(t *testing.T) {
	lookup := func(string) (string, bool) { return "dialog", true }
	response, status, err := RunClient(context.Background(), "/does/not/exist", []string{"apt-get"}, []string{"DEBIAN_FRONTEND"}, lookup)
	if err == nil || status != 2 || !reflect.DeepEqual(response, Response{}) {
		t.Fatalf("RunClient() response=%#v status=%d error=%v", response, status, err)
	}
}
