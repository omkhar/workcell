// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// structAttributes appends the dotted JSON path of every leaf field of t.
func structAttributes(t reflect.Type, prefix string, out *[]string) {
	for i := 0; i < t.NumField(); i++ {
		name := strings.Split(t.Field(i).Tag.Get("json"), ",")[0]
		ft := t.Field(i).Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct {
			structAttributes(ft, prefix+name+".", out)
			continue
		}
		*out = append(*out, prefix+name)
	}
}

// currentAttributes derives the emitted attribute set from the code: the Event
// struct tree, the session.* keys of a fully populated record, and the typed
// audit.* properties.
func currentAttributes(t *testing.T) []string {
	t.Helper()
	var attrs []string
	structAttributes(reflect.TypeOf(Event{}), "", &attrs)

	rec := sessions.SessionRecord{}
	rv := reflect.ValueOf(&rec).Elem()
	for i := 0; i < rv.NumField(); i++ {
		switch rv.Field(i).Kind() {
		case reflect.String:
			rv.Field(i).SetString("x")
		case reflect.Int:
			rv.Field(i).SetInt(1)
		}
	}
	events, err := Export(sessions.SessionExport{Session: rec}, Options{Now: fixedNow})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	for key := range events[0].Unmapped {
		attrs = append(attrs, "unmapped."+key)
	}
	for key := range knownAuditFields {
		attrs = append(attrs, "unmapped.audit."+key)
	}
	sort.Strings(attrs)
	return attrs
}

// TestAttributeSetPinnedPerMappingVersion fails when an attribute is added or
// removed without a MappingVersion bump: the golden file is keyed by version.
func TestAttributeSetPinnedPerMappingVersion(t *testing.T) {
	got := strings.Join(currentAttributes(t), "\n") + "\n"
	path := filepath.Join("testdata", fmt.Sprintf("attributes-v%s.txt", MappingVersion))
	if *updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no attribute golden for MappingVersion %q: %v (run with -update-golden after a bump)", MappingVersion, err)
	}
	if got != string(want) {
		t.Fatalf("attribute set changed without a MappingVersion bump; bump MappingVersion and add %s\nwant:\n%s\ngot:\n%s", path, want, got)
	}
}
