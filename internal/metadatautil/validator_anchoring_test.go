// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"fmt"
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
		{"a longer identifier is not the call", anchor + "\tcachedShellInvocations(script, command)\n", corpus, ""},
		{"two calls on one line", strings.TrimSuffix(anchor, "\n") + strings.TrimPrefix(anchor, "\t"), corpus + corpus, ""},
		{"a comment between the callee and its arguments is a call", "\t_ = ShellInvocations /* why */ (script, \"tool run\")\n", corpus, ""},
		{"a Unicode identifier prefix is not the call", anchor, corpus + "\t偽RequireRejectsAllEvasions(t, artifact, anchor, want, validate)\n", ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := anchoringRoot(t, "")
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

// anchoringRoot builds a repository with the four validator packages and the
// given baseline rows.
func anchoringRoot(t *testing.T, baseline string) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"internal/adapters/a.go":                  "package adapters\n",
		"internal/testkit/a.go":                   "package testkit\n",
		"internal/workcellhardening/a.go":         "package workcellhardening\n",
		"internal/metadatautil/a.go":              "package metadatautil\n",
		"policy/validator-anchoring-baseline.tsv": "# rows\n" + baseline,
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCheckValidatorAnchoringTextMatchRatchet(t *testing.T) {
	const validator = "package metadatautil\n\nfunc (c *checker) wrapper(p string) bool {\n\ttext, _ := readText(p)\n\treturn strings.Contains(text, \"tool run\")\n}\n"
	const row = "internal/metadatautil\tchecker.wrapper\treviewed\n"
	many := validator
	for index := range 20 {
		many += strings.Replace(validator[len("package metadatautil\n"):], "wrapper", fmt.Sprintf("wrapper%d", index), 1)
	}
	cases := []struct {
		name, file, source, baseline, want string
	}{
		{"planted violation", "v.go", validator, "", "checker.wrapper matches text"},
		{"listed with a reason", "v.go", validator, row, ""},
		{"stale row", "v.go", "package metadatautil\n", row, "remove its stale baseline row"},
		{"row without a reason", "v.go", validator, "internal/metadatautil\tchecker.wrapper\t \n", "PACKAGE<TAB>FUNCTION<TAB>REASON"},
		{"repeated row", "v.go", validator, row + row, "repeated row"},
		{"report is capped", "v.go", many, "", "and 1 more"},
		{"validator in a test file", "v_test.go", "package metadatautil\n\nfunc TestWorkflowRunsTool(t *testing.T) {\n\tif !strings.Contains(readText(p), \"tool run\") {\n\t\tt.Fatal(p)\n\t}\n}\n", "", "metadatautil.TestWorkflowRunsTool matches text"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			root := anchoringRoot(t, testCase.baseline)
			for name, body := range map[string]string{
				testCase.file: testCase.source,
				"p.go":        "package metadatautil\n\nfunc ok() { ShellInvocations(script, \"tool\") }\n",
				"p_test.go":   "package metadatautil\n\nfunc run() { RequireRejectsAllEvasions(t, a, b, c, d) }\n",
			} {
				if err := os.WriteFile(filepath.Join(root, "internal", "metadatautil", name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
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

func TestTextMatchingFunctions(t *testing.T) {
	cases := map[string]struct {
		body string
		want []string
	}{
		"read then contains":    {"func f() { b, _ := os.ReadFile(p); _ = bytes.Contains(b, x) }", []string{"f"}},
		"read then has prefix":  {"func f() { s := readText(p); _ = strings.HasPrefix(s, x) }", []string{"f"}},
		"read then index":       {"func f() { s := readRepoFile(p); _ = strings.Index(s, x) }", []string{"f"}},
		"read then regexp":      {"func f() { s := readText(p); _ = regexp.MustCompile(x).MatchString(s) }", []string{"f"}},
		"read then compiled re": {"func f() { s := readText(p); _ = re.FindStringSubmatch(s) }", []string{"f"}},
		"exec then contains":    {"func f() { o, _ := exec.Command(x).Output(); _ = bytes.Contains(o, y) }", []string{"f"}},
		"match in a closure":    {"func f() { s := readText(p); g := func() bool { return strings.Contains(s, x) }; _ = g }", []string{"f"}},
		"generic receiver":      {"func (c *box[T]) f() { s := readText(p); _ = strings.Contains(s, x) }", []string{"box.f"}},
		"match before read":     {"func f() { _ = strings.Contains(a, x); _ = readText(p) }", []string{"f"}},
		"read nested in match":  {"func f() bool { return strings.Contains(string(readRepoFile(p)), x) }", []string{"f"}},
		"helper gets content":   {"func f(workflow string) error { if !strings.Contains(workflow, x) { return e }; return nil }", []string{"f"}},
		"package-level matcher": {"var holds = func(text string) bool { return strings.HasPrefix(text, x) }", []string{"holds"}},
		"literal pattern":       {"var re = regexp.MustCompile(`^a$`)\nfunc f() *regexp.Regexp { return regexp.MustCompile(\"b\") }", nil},
		"error message":         {"func f() { _ = strings.Contains(err.Error(), x) }", nil},
		"filepath match":        {"func f() { s := readText(p); _, _ = filepath.Match(s, x) }", nil},
		"split is not a search": {"func f() { s := readText(p); _ = strings.Split(s, x) }", nil},
		"text about the call":   {"func f() { _ = readText(p) // strings.Contains(s, x)\n}", nil},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := metadatautil.TextMatchingFunctions("package p\n\n" + testCase.body + "\n")
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(got, ",") != strings.Join(testCase.want, ",") {
				t.Fatalf("TextMatchingFunctions() = %v, want %v", got, testCase.want)
			}
		})
	}
}
