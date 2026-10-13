// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package ocsf

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/sessions"
)

// jsonPaths appends the dotted path of every leaf in v; array elements share
// their parent's path.
func jsonPaths(v any, prefix string, seen map[string]struct{}) {
	switch v := v.(type) {
	case map[string]any:
		for key, child := range v {
			jsonPaths(child, prefix+key+".", seen)
		}
	case []any:
		for _, child := range v {
			jsonPaths(child, prefix, seen)
		}
	default:
		seen[strings.TrimSuffix(prefix, ".")] = struct{}{}
	}
}

// currentAttributes derives the attribute set from what Export emits, not from
// the Event type: a fully populated session record plus one audit record per
// known event name and one unrecognized event, each carrying every known field.
// A mapping that stops populating an optional field changes the pin.
func currentAttributes(t *testing.T) []string {
	t.Helper()
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
	// Every other value is x: it matches rec.SessionID and is no known event name.
	var fields []string
	for key := range knownAuditFields {
		if key != "event" {
			fields = append(fields, key+"=x")
		}
	}
	var records []string
	for name := range knownAuditEvents {
		records = append(records, "event="+name+" "+strings.Join(fields, " "))
	}
	records = append(records, "event=x "+strings.Join(fields, " "))
	events, err := Export(sessions.SessionExport{Session: rec, AuditRecords: records}, Options{Now: fixedNow})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	seen := map[string]struct{}{}
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		jsonPaths(v, "", seen)
	}
	var attrs []string
	for key := range seen {
		attrs = append(attrs, key)
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
