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
		{"line comment names an anchor", anchor + "\t// " + anchor, corpus, ""},
		{"literal names an anchor", anchor + "\t_ = \"ShellInvocations(\"\n", corpus, ""},
		{"line comment names a corpus run", anchor, corpus + "\t// " + corpus, ""},
		{"block comment names a corpus run", anchor, corpus + "\t/* text\n" + corpus + "\t*/\n", ""},
		{"raw literal names a corpus run", anchor, corpus + "\t_ = `text\n" + corpus + "`\n", ""},
		{"call in a one-line function body", "func validate() {" + strings.TrimSuffix(anchor, "\n") + " }\n", corpus, ""},
		{"declaration is not a call", "func ShellInvocations(script, command string) [][]string {\n" + anchor, corpus, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "internal", "example")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			write := func(name, body string) {
				if err := os.WriteFile(filepath.Join(dir, name), []byte("package example\n\n"+body), 0o644); err != nil {
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
