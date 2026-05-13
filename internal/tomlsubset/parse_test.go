// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package tomlsubset

import (
	"strings"
	"testing"
)

func TestParseDocumentEmpty(t *testing.T) {
	doc, err := ParseDocument("", "empty.toml")
	if err != nil {
		t.Fatalf("ParseDocument empty: %v", err)
	}
	if len(doc.Tables) != 0 {
		t.Fatalf("expected 0 tables, got %d", len(doc.Tables))
	}
	if len(doc.TopLevel.Pairs) != 0 {
		t.Fatalf("expected 0 top-level pairs, got %d", len(doc.TopLevel.Pairs))
	}
}

func TestParseDocumentTopLevelPairs(t *testing.T) {
	src := strings.Join([]string{
		"# leading comment",
		"name = \"workcell\"",
		"count = 42",
		"enabled = true",
		"tags = [\"alpha\", \"beta\"]",
		"",
	}, "\n")
	doc, err := ParseDocument(src, "top.toml")
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(doc.Tables) != 0 {
		t.Fatalf("unexpected named tables: %#v", doc.Tables)
	}
	if got := len(doc.TopLevel.Pairs); got != 4 {
		t.Fatalf("want 4 pairs, got %d", got)
	}
	wantKeys := []string{"name", "count", "enabled", "tags"}
	for i, want := range wantKeys {
		if doc.TopLevel.Pairs[i].Key != want {
			t.Fatalf("pair %d: want key %q, got %q", i, want, doc.TopLevel.Pairs[i].Key)
		}
	}
	if v, ok := doc.TopLevel.Pairs[0].Value.(string); !ok || v != "workcell" {
		t.Fatalf("name value = %#v", doc.TopLevel.Pairs[0].Value)
	}
	if v, ok := doc.TopLevel.Pairs[1].Value.(int); !ok || v != 42 {
		t.Fatalf("count value = %#v", doc.TopLevel.Pairs[1].Value)
	}
	if v, ok := doc.TopLevel.Pairs[2].Value.(bool); !ok || v != true {
		t.Fatalf("enabled value = %#v", doc.TopLevel.Pairs[2].Value)
	}
	tags, ok := doc.TopLevel.Pairs[3].Value.([]any)
	if !ok {
		t.Fatalf("tags value = %#v", doc.TopLevel.Pairs[3].Value)
	}
	if len(tags) != 2 || tags[0] != "alpha" || tags[1] != "beta" {
		t.Fatalf("tags contents = %#v", tags)
	}
}

func TestParseDocumentMultipleTablesPreserveOrder(t *testing.T) {
	src := strings.Join([]string{
		"[alpha]",
		"x = 1",
		"y = 2",
		"",
		"[beta]",
		"z = 3",
		"",
		"[gamma]",
		"w = \"hi\"",
	}, "\n")
	doc, err := ParseDocument(src, "multi.toml")
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if len(doc.Tables) != 3 {
		t.Fatalf("want 3 tables, got %d", len(doc.Tables))
	}
	wantNames := []string{"alpha", "beta", "gamma"}
	for i, want := range wantNames {
		if doc.Tables[i].Name != want {
			t.Fatalf("table %d: want %q got %q", i, want, doc.Tables[i].Name)
		}
	}
	if got := doc.Tables[0].Line; got != 1 {
		t.Fatalf("alpha line want 1 got %d", got)
	}
	if got := doc.Tables[1].Line; got != 5 {
		t.Fatalf("beta line want 5 got %d", got)
	}
	if pair := doc.Tables[0].Lookup("y"); pair == nil || pair.Value.(int) != 2 {
		t.Fatalf("alpha.y lookup wrong: %#v", pair)
	}
}

func TestParseDocumentCommentsAndBlankLines(t *testing.T) {
	src := strings.Join([]string{
		"# a header comment",
		"",
		"[server]",
		"# inside-table comment",
		"host = \"example.com\" # trailing comment",
		"port = 8080",
		"",
		"# trailing blank+comment",
	}, "\n")
	doc, err := ParseDocument(src, "comments.toml")
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	server := doc.LookupTable("server")
	if server == nil {
		t.Fatalf("server table missing")
	}
	if got := server.Lookup("host").Value.(string); got != "example.com" {
		t.Fatalf("host = %q", got)
	}
	if got := server.Lookup("port").Value.(int); got != 8080 {
		t.Fatalf("port = %d", got)
	}
}

func TestParseDocumentStringEscapes(t *testing.T) {
	src := strings.Join([]string{
		"newline = \"a\\nb\"",
		"quote = \"q\\\"q\"",
		"tab = \"x\\ty\"",
	}, "\n")
	doc, err := ParseDocument(src, "esc.toml")
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	cases := []struct {
		key  string
		want string
	}{
		{"newline", "a\nb"},
		{"quote", "q\"q"},
		{"tab", "x\ty"},
	}
	for _, c := range cases {
		got, ok := doc.TopLevel.Lookup(c.key).Value.(string)
		if !ok || got != c.want {
			t.Fatalf("%s: want %q got %q (ok=%v)", c.key, c.want, got, ok)
		}
	}
}

func TestParseDocumentMultilineArray(t *testing.T) {
	src := strings.Join([]string{
		"[required]",
		"contexts = [",
		"  \"Validate repository\",",
		"  \"Reproducible build\",",
		"]",
	}, "\n")
	doc, err := ParseDocument(src, "ml.toml")
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	pair := doc.LookupTable("required").Lookup("contexts")
	if pair == nil {
		t.Fatalf("contexts pair missing")
	}
	arr, ok := pair.Value.([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("contexts = %#v", pair.Value)
	}
	if arr[0] != "Validate repository" || arr[1] != "Reproducible build" {
		t.Fatalf("contexts contents = %#v", arr)
	}
}

func TestParseDocumentLookupHelpers(t *testing.T) {
	doc, err := ParseDocument("[t]\nk = 1\n", "h.toml")
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	if got := doc.LookupTable("missing"); got != nil {
		t.Fatalf("missing table should be nil, got %#v", got)
	}
	if got := (*Table)(nil).Lookup("any"); got != nil {
		t.Fatalf("nil table Lookup should be nil")
	}
	if got := (*Document)(nil).LookupTable("any"); got != nil {
		t.Fatalf("nil doc LookupTable should be nil")
	}
	tbl := doc.LookupTable("t")
	if pair := tbl.Lookup("absent"); pair != nil {
		t.Fatalf("absent key should be nil, got %#v", pair)
	}
}

func TestParseDocumentRejects(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "array of tables",
			src:  "[[copies]]\nx = 1\n",
			want: "array-of-table",
		},
		{
			name: "dotted key",
			src:  "a.b = 1\n",
			want: "dotted TOML keys",
		},
		{
			name: "duplicate key in same table",
			src:  "[t]\nk = 1\nk = 2\n",
			want: "duplicate key",
		},
		{
			name: "duplicate table",
			src:  "[t]\nk = 1\n[t]\nm = 2\n",
			want: "duplicate table",
		},
		{
			name: "multi-line string triple-double",
			src:  "[t]\nk = \"\"\"hello\"\"\"\n",
			want: "multi-line strings",
		},
		{
			name: "multi-line string triple-single",
			src:  "[t]\nk = '''hello'''\n",
			want: "multi-line strings",
		},
		{
			name: "inline table",
			src:  "[t]\nk = { a = 1 }\n",
			want: "inline tables",
		},
		{
			name: "datetime",
			src:  "[t]\nk = 1979-05-27T07:32:00Z\n",
			want: "datetimes",
		},
		{
			name: "local time",
			src:  "[t]\nk = 07:32:00\n",
			want: "datetimes",
		},
		{
			name: "date only",
			src:  "[t]\nk = 1979-05-27\n",
			want: "datetimes",
		},
		{
			name: "empty key",
			src:  "[t]\n = 1\n",
			want: "empty key",
		},
		{
			name: "empty table name",
			src:  "[]\nk = 1\n",
			want: "empty table name",
		},
		{
			name: "missing =",
			src:  "[t]\nbare_line\n",
			want: "expected key = value",
		},
		{
			name: "unterminated array",
			src:  "[t]\nk = [\n  \"a\",\n",
			want: "unterminated TOML array",
		},
		{
			name: "unsupported value",
			src:  "[t]\nk = banana\n",
			want: "unsupported TOML value",
		},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			_, err := ParseDocument(c.src, "reject.toml")
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", c.want)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestParseDocumentDottedTableHeaderAllowed(t *testing.T) {
	// Dotted *table* headers (like [credentials.api]) are accepted
	// because Workcell uses them; only dotted keys are rejected.
	doc, err := ParseDocument("[credentials.api]\ntoken = \"xyz\"\n", "dotted.toml")
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	tbl := doc.LookupTable("credentials.api")
	if tbl == nil {
		t.Fatalf("credentials.api table missing")
	}
	if got := tbl.Lookup("token").Value.(string); got != "xyz" {
		t.Fatalf("token = %q", got)
	}
}

func TestParseDocumentPreservesRaw(t *testing.T) {
	doc, err := ParseDocument("[t]\nk = [ \"a\", \"b\" ]\n", "raw.toml")
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	pair := doc.LookupTable("t").Lookup("k")
	if pair == nil {
		t.Fatalf("k pair missing")
	}
	// Raw should round-trip (modulo leading space) since the value text
	// after `=` is " [ "a", "b" ]" — leading space is preserved.
	if !strings.Contains(pair.Raw, "[ \"a\", \"b\" ]") {
		t.Fatalf("raw value = %q", pair.Raw)
	}
}

func TestStripCommentPreservesQuotedHash(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"key = \"a # b\"", "key = \"a # b\""},
		{"key = \"a\" # comment", "key = \"a\""},
		{"# just a comment", ""},
		{"plain", "plain"},
		{"'literal # inside'", "'literal # inside'"},
	}
	for _, c := range cases {
		if got := StripComment(c.in); got != c.want {
			t.Fatalf("StripComment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestParseValueAllTypes(t *testing.T) {
	cases := []struct {
		in   string
		want any
	}{
		{"true", true},
		{"false", false},
		{"42", 42},
		{"\"hello\"", "hello"},
		{"'literal'", "literal"},
		{"[\"a\", \"b\"]", []any{"a", "b"}},
		{"[]", []any{}},
	}
	for _, c := range cases {
		got, err := ParseValue(c.in, "ctx")
		if err != nil {
			t.Fatalf("ParseValue(%q): %v", c.in, err)
		}
		switch want := c.want.(type) {
		case []any:
			gotArr, ok := got.([]any)
			if !ok || len(gotArr) != len(want) {
				t.Fatalf("ParseValue(%q) = %#v, want %#v", c.in, got, want)
			}
			for i := range want {
				if gotArr[i] != want[i] {
					t.Fatalf("ParseValue(%q)[%d] = %#v, want %#v", c.in, i, gotArr[i], want[i])
				}
			}
		default:
			if got != c.want {
				t.Fatalf("ParseValue(%q) = %#v, want %#v", c.in, got, c.want)
			}
		}
	}
}

func TestArrayClosed(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"[]", true},
		{"[1, 2]", true},
		{"[1,", false},
		{"[\"]\"]", true},
		{"[\"a\",\n\"b\"]", true},
		{"[", false},
	}
	for _, c := range cases {
		if got := ArrayClosed(c.in); got != c.want {
			t.Fatalf("ArrayClosed(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseBackwardsCompatibleMapAPI(t *testing.T) {
	// Existing Parse() callers must keep working: the 5 caller packages
	// rely on the map[string]any shape until PRs 37 + 38 migrate them.
	src := "[t]\nk = 1\n"
	parsed, err := Parse(src, "bc.toml")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	tbl, ok := parsed["t"].(map[string]any)
	if !ok {
		t.Fatalf("Parse returned %#v", parsed)
	}
	if tbl["k"] != 1 {
		t.Fatalf("t.k = %#v", tbl["k"])
	}
}
