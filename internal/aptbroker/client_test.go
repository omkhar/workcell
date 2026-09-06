// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package aptbroker

import (
	"context"
	"reflect"
	"testing"
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

func TestRunClientRejectsPreservedInteractiveValueBeforeDial(t *testing.T) {
	lookup := func(string) (string, bool) { return "dialog", true }
	response, status, err := RunClient(context.Background(), "/does/not/exist", []string{"apt-get"}, []string{"DEBIAN_FRONTEND"}, lookup)
	if err == nil || status != 2 || !reflect.DeepEqual(response, Response{}) {
		t.Fatalf("RunClient() response=%#v status=%d error=%v", response, status, err)
	}
}
