// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestCheckValidatorAnchoringAcceptsThisRepository(t *testing.T) {
	if err := metadatautil.CheckValidatorAnchoring(filepath.Join("..", "..")); err != nil {
		t.Fatalf("CheckValidatorAnchoring() error = %v", err)
	}
}

func TestCheckValidatorAnchoring(t *testing.T) {
	anchor := "\tfor _, args := range ShellInvocations(script, \"tool run\") {\n"
	corpus := "\tRequireRejectsAllEvasions(t, artifact, anchor, want, validate)\n"
	cases := []struct {
		name, source, test, want string
	}{
		{"parity", anchor + anchor, corpus + corpus, ""},
		{"corpus run missing", anchor + anchor, corpus, "2 call(s) of ShellInvocations but 1 run(s)"},
		{"corpus run without an anchor", anchor, corpus + corpus, "1 call(s) of ShellInvocations but 2 run(s)"},
		{"no anchored validator", "", corpus, "lost its subject"},
		{"comment naming a call", anchor + "\t// " + corpus, "\t// " + anchor + corpus, ""},
		{"literal naming a call", anchor + "\t_ = `" + corpus + "`\n", "\t_ = \"ShellInvocations(\"\n" + corpus, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "internal", "example")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(name, body string) {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("package example\n\nfunc f() {\n"+body+"}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write("example.go", testCase.source)
			write("example_test.go", testCase.test)

			err := metadatautil.CheckValidatorAnchoring(root)
			if testCase.want == "" {
				if err != nil {
					t.Fatalf("CheckValidatorAnchoring() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("CheckValidatorAnchoring() error = %v, want %q", err, testCase.want)
			}
		})
	}
}
