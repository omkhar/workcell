// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package adapters

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/omkhar/workcell/internal/providerid"
)

const repoRoot = "../.."

func loadRepoManifests(t *testing.T) []Manifest {
	t.Helper()
	manifests, err := LoadManifests(filepath.Join(repoRoot, "adapters"))
	if err != nil {
		t.Fatal(err)
	}
	return manifests
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func manifestIDs(manifests []Manifest, keep func(Manifest) bool) []string {
	var out []string
	for _, m := range manifests {
		if keep(m) {
			out = append(out, m.ID)
		}
	}
	return out
}

func sorted(values []string) []string {
	out := slices.Clone(values)
	slices.Sort(out)
	return out
}

func TestManifestsMatchProviderIDLists(t *testing.T) {
	manifests := loadRepoManifests(t)

	certified := manifestIDs(manifests, func(m Manifest) bool { return m.Tier == "certified" })
	if !slices.Equal(certified, providerid.AllProviders) {
		t.Errorf("certified manifests = %v, want providerid.AllProviders %v", certified, providerid.AllProviders)
	}
	withCredentials := manifestIDs(manifests, func(m Manifest) bool { return len(m.Credentials) > 0 })
	if !slices.Equal(withCredentials, providerid.CredentialMetadataProviders) {
		t.Errorf("manifests with credentials = %v, want providerid.CredentialMetadataProviders %v", withCredentials, providerid.CredentialMetadataProviders)
	}
	planned := manifestIDs(manifests, func(m Manifest) bool { return m.Tier == "planned" })
	if !slices.Equal(planned, []string{providerid.Antigravity}) {
		t.Errorf("planned manifests = %v, want [%s]", planned, providerid.Antigravity)
	}
	documents := append([]string{providerid.CommonDocument}, manifestIDs(manifests, func(m Manifest) bool { return m.ManagedDocument })...)
	if !slices.Equal(sorted(documents), sorted(providerid.DocumentKeys)) {
		t.Errorf("managed document keys = %v, want providerid.DocumentKeys %v", documents, providerid.DocumentKeys)
	}
}

func TestManifestsMatchProviderRegistry(t *testing.T) {
	var got []providerDefinition
	for _, m := range loadRepoManifests(t) {
		if m.Tier == "planned" {
			continue
		}
		def := providerDefinition{
			id:                       m.ID,
			sharedCredentialsEnabled: m.SharedCredentials,
			tables: providerTables{
				credentialContainerPaths: map[string]string{},
				reservedTargets:          m.ReservedTargets,
			},
		}
		for _, c := range m.Credentials {
			def.tables.credentialKeys = append(def.tables.credentialKeys, c.Key)
			def.tables.credentialContainerPaths[c.Key] = c.ContainerPath
		}
		got = append(got, def)
	}
	if !reflect.DeepEqual(got, providers) {
		t.Fatalf("manifests differ from data.go providers:\n got  %+v\n want %+v", got, providers)
	}
}

func TestManifestsMatchLauncherProviderEndpoints(t *testing.T) {
	// endpoints runs provider_endpoints under an xtrace that prefixes each
	// executed command with its line number, so the trace names the case arm.
	endpoints := func(id string) (out, trace string, code int) {
		cmd := exec.Command("bash", "--noprofile", "--norc", "-c",
			`source scripts/lib/launcher/egress-endpoints.sh && PS4='+$LINENO ' && set -x && provider_endpoints "$1"`, "bash", id)
		cmd.Dir = repoRoot
		cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
		var stderr strings.Builder
		cmd.Stderr = &stderr
		stdout, err := cmd.Output()
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatalf("provider_endpoints %s: %v", id, err)
		}
		return string(stdout), stderr.String(), cmd.ProcessState.ExitCode()
	}
	const unknown = "no-such-provider"
	unknownOut, unknownTrace, unknownCode := endpoints(unknown)
	if unknownCode != 1 || unknownOut != "" || !strings.Contains(unknownTrace, "return 1") {
		t.Fatalf("provider_endpoints probe cannot detect the default arm (exit %d, out %q): %s", unknownCode, unknownOut, unknownTrace)
	}
	for _, m := range loadRepoManifests(t) {
		out, trace, code := endpoints(m.ID)
		if m.Tier == "planned" {
			// An absent row runs the default arm, as an unknown id does. An
			// explicit arm, even one that only returns 1, traces other lines.
			if code != 1 || out != "" || strings.ReplaceAll(trace, m.ID, unknown) != unknownTrace || len(m.EgressEndpoints) != 0 {
				t.Errorf("%s: planned adapter must have no provider_endpoints row and no egress endpoints (exit %d, out %q):\n%s", m.ID, code, out, trace)
			}
			continue
		}
		if code != 0 {
			t.Fatalf("%s: provider_endpoints exit %d:\n%s", m.ID, code, trace)
		}
		if got := strings.Fields(out); !slices.Equal(got, m.EgressEndpoints) {
			t.Errorf("%s: provider_endpoints = %v, manifest egress.endpoints = %v", m.ID, got, m.EgressEndpoints)
		}
	}
}

// TestManifestsMatchLauncherAgentDispatch runs the launcher instead of reading
// its source, so a decoy in a comment or heredoc cannot satisfy it.
func TestManifestsMatchLauncherAgentDispatch(t *testing.T) {
	probe := func(agent string) (string, int) {
		cmd := exec.Command("bash", "--noprofile", "--norc", "scripts/workcell", "--auth-status", "--agent", agent, "--workspace", ".")
		cmd.Dir = repoRoot
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir()}
		out, err := cmd.CombinedOutput()
		var exitErr *exec.ExitError
		if err != nil && !errors.As(err, &exitErr) {
			t.Fatalf("launcher probe for %s: %v", agent, err)
		}
		return string(out), cmd.ProcessState.ExitCode()
	}
	const unsupported, planned = "Unsupported agent: ", "is a planned Workcell provider adapter"
	if out, code := probe("no-such-agent"); code != 2 || !strings.Contains(out, unsupported+"no-such-agent") {
		t.Fatalf("launcher probe cannot detect an unsupported agent (exit %d): %s", code, out)
	}
	for _, m := range loadRepoManifests(t) {
		out, code := probe(m.ID)
		switch m.Tier {
		case "certified":
			if code != 0 || strings.Contains(out, unsupported) || strings.Contains(out, planned) {
				t.Errorf("%s: certified manifest but the launcher rejects --agent (exit %d): %s", m.ID, code, out)
			}
		case "planned":
			if code != 2 || !strings.Contains(out, planned) {
				t.Errorf("%s: planned manifest but the launcher does not report a planned adapter (exit %d): %s", m.ID, code, out)
			}
		}
	}
}

var (
	rustLaunchBlock   = regexp.MustCompile(`(?:^| )LaunchTarget \{ name : "([a-z-]+)" , script_path : "/usr/local/libexec/workcell/provider-wrapper\.sh" , approved_invocations : & \[ "([^"]+)" , "([^"]+)" (?:, )?\] (?:, )?\}`)
	dockerDirective   = regexp.MustCompile(`^#[ \t]*([a-zA-Z][a-zA-Z0-9]*)[ \t]*=[ \t]*(.+?)[ \t]*$`)
	dockerHeredocWord = regexp.MustCompile(`^[0-9]*<<(-?)([^<]*)$`)
)

// rustTokens splits Rust source into tokens. Comments (nested block comments
// included) are dropped. A plain "..." string without escapes keeps its text;
// every other literal (raw, byte, C, and char strings, and strings with
// escapes) becomes one opaque "<lit>" token, so text inside a literal can
// never look like code. An unterminated comment or literal is an error.
func rustTokens(src string) ([]string, error) {
	var tokens []string
	isWord := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	// quoted returns the index after the closing quote of a string or char
	// literal whose opening quote is at i.
	quoted := func(i int, quote byte) (int, error) {
		for j := i + 1; j < len(src); j++ {
			switch src[j] {
			case '\\':
				j++
			case quote:
				return j + 1, nil
			}
		}
		return 0, errors.New("unterminated literal")
	}
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case strings.HasPrefix(src[i:], "//"):
			if end := strings.IndexByte(src[i:], '\n'); end >= 0 {
				i += end
			} else {
				i = len(src)
			}
		case strings.HasPrefix(src[i:], "/*"):
			depth := 0
			for {
				switch {
				case i >= len(src):
					return nil, errors.New("unterminated block comment")
				case strings.HasPrefix(src[i:], "/*"):
					depth++
					i += 2
				case strings.HasPrefix(src[i:], "*/"):
					depth--
					i += 2
				default:
					i++
				}
				if depth == 0 {
					break
				}
			}
		case c == '"':
			end, err := quoted(i, '"')
			if err != nil {
				return nil, err
			}
			if text := src[i:end]; strings.Contains(text, `\`) {
				tokens = append(tokens, "<lit>")
			} else {
				tokens = append(tokens, text)
			}
			i = end
		case c == '\'':
			// A char literal is '\...' or one character then a quote;
			// anything else is a lifetime such as 'static.
			_, size := utf8.DecodeRuneInString(src[i+1:])
			if i+1 < len(src) && src[i+1] == '\\' || i+1+size < len(src) && src[i+1+size] == '\'' {
				end, err := quoted(i, '\'')
				if err != nil {
					return nil, err
				}
				tokens = append(tokens, "<lit>")
				i = end
			} else {
				tokens = append(tokens, "'")
				i++
			}
		case isWord(c):
			start := i
			for i < len(src) && isWord(src[i]) {
				i++
			}
			word := src[start:i]
			rest := src[i:]
			switch {
			case (word == "b" || word == "c") && (strings.HasPrefix(rest, `"`) || word == "b" && strings.HasPrefix(rest, "'")):
				end, err := quoted(i, rest[0])
				if err != nil {
					return nil, err
				}
				tokens = append(tokens, "<lit>")
				i = end
			case (word == "r" || word == "br" || word == "cr") && strings.HasPrefix(strings.TrimLeft(rest, "#"), `"`):
				hashes := len(rest) - len(strings.TrimLeft(rest, "#"))
				body := i + hashes + 1
				end := strings.Index(src[body:], `"`+strings.Repeat("#", hashes))
				if end < 0 {
					return nil, errors.New("unterminated raw string")
				}
				tokens = append(tokens, "<lit>")
				i = body + end + 1 + hashes
			default:
				tokens = append(tokens, word)
			}
		default:
			tokens = append(tokens, string(c))
			i++
		}
	}
	return tokens, nil
}

// rustLaunchTargets returns each provider LaunchTarget entry of the one
// LAUNCH_TARGETS table in the Rust source, as [match, name, invocation,
// invocation]. It matches tokens, not text, so a copy in a comment, in any
// literal, or outside the table does not count. It returns nil when the
// source does not tokenize, has no single active top-level LAUNCH_TARGETS
// table, or has an attribute or macro in the table.
func rustLaunchTargets(source string) [][]string {
	tokens, err := rustTokens(source)
	if err != nil {
		return nil
	}
	// Count only a top-level declaration with no attribute: nesting depth 0,
	// and "const" starts the item (after ";", "}", or the file start). A copy
	// in a function, a module, a macro, or under #[cfg(...)] does not count.
	var start []int
	depth := 0
	for i, token := range tokens {
		switch token {
		case "{", "(", "[":
			depth++
		case "}", ")", "]":
			depth--
		case "LAUNCH_TARGETS":
			if depth == 0 && i > 0 && tokens[i-1] == "const" && (i == 1 || tokens[i-2] == ";" || tokens[i-2] == "}") {
				start = append(start, i)
			}
		}
	}
	if len(start) != 1 {
		return nil
	}
	// The table is the first "[" after "=" through its matching "]".
	i := slices.Index(tokens[start[0]:], "=")
	if i < 0 {
		return nil
	}
	i += start[0]
	for i < len(tokens) && tokens[i] != "[" {
		i++
	}
	for depth, j := 0, i; j < len(tokens); j++ {
		switch tokens[j] {
		case "[":
			depth++
		case "]":
			depth--
		}
		if depth == 0 {
			// An attribute (#[cfg(...)]) or a macro (!) can remove or rewrite
			// an entry at compile time; the table has neither, so reject both.
			table := tokens[i : j+1]
			if slices.Contains(table, "#") || slices.Contains(table, "!") {
				return nil
			}
			return rustLaunchBlock.FindAllStringSubmatch(strings.Join(table, " "), -1)
		}
	}
	return nil
}

// dockerfilePinnedArgs returns the ARG names that a Dockerfile pins with a
// non-empty value. It splits instructions as BuildKit does: parser directives
// at the top set the escape character; an escape at the end of a line (before
// optional blanks) continues the instruction; comment and blank lines are
// dropped, also inside a continuation; and a RUN, COPY, or ADD instruction
// queues one heredoc per "<<NAME" or "<<-NAME" word, whose bodies follow in
// order. A body ends at a line equal to NAME ("<<-" first strips leading
// tabs, not spaces). Text in a comment, a continuation, or a heredoc body is
// not an instruction, so an ARG there does not count. A malformed file (bad
// escape directive, unterminated heredoc) returns nil.
func dockerfilePinnedArgs(dockerfile string) map[string]bool {
	args := map[string]bool{}
	escape := byte('\\')
	directives := true
	var instruction strings.Builder
	type heredoc struct {
		name  string
		chomp bool
	}
	var heredocs []heredoc
	for line := range strings.Lines(dockerfile) {
		line = strings.TrimRight(line, "\r\n")
		if len(heredocs) > 0 {
			terminator := line
			if heredocs[0].chomp {
				terminator = strings.TrimLeft(line, "\t")
			}
			if terminator == heredocs[0].name {
				heredocs = heredocs[1:]
			}
			continue
		}
		if directives {
			if m := dockerDirective.FindStringSubmatch(line); m != nil {
				if strings.EqualFold(m[1], "escape") {
					if m[2] != `\` && m[2] != "`" {
						return nil
					}
					escape = m[2][0]
				}
				continue
			}
			directives = false
		}
		if trimmed := strings.TrimLeft(line, " \t"); trimmed == "" || trimmed[0] == '#' {
			continue
		}
		if body := strings.TrimRight(line, " \t"); body[len(body)-1] == escape {
			instruction.WriteString(body[:len(body)-1])
			continue
		}
		instruction.WriteString(line)
		words := dockerWords(instruction.String(), escape)
		instruction.Reset()
		switch strings.ToUpper(words[0]) {
		case "ARG":
			for _, word := range words[1:] {
				if name, value, ok := strings.Cut(dockerUnquote(word, escape), "="); ok && value != "" {
					args[name] = true
				}
			}
		case "RUN", "COPY", "ADD":
			for _, word := range words[1:] {
				if m := dockerHeredocWord.FindStringSubmatch(word); m != nil {
					if name := dockerUnquote(m[2], escape); name != "" {
						heredocs = append(heredocs, heredoc{name, m[1] == "-"})
					}
				}
			}
		}
	}
	if len(heredocs) > 0 {
		return nil
	}
	return args
}

// dockerWords splits an instruction at blanks outside quotes. Quotes and
// escapes stay in each word, as BuildKit's heredoc scan keeps them, so a
// quoted "<<EOF" is not a heredoc.
func dockerWords(instruction string, escape byte) []string {
	var words []string
	var word strings.Builder
	var quote byte
	for i := 0; i < len(instruction); i++ {
		c := instruction[i]
		switch {
		case quote == 0 && (c == ' ' || c == '\t'):
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
			continue
		case c == escape && quote != '\'' && i+1 < len(instruction):
			word.WriteByte(c)
			i++
			c = instruction[i]
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case c == quote:
			quote = 0
		}
		word.WriteByte(c)
	}
	return append(words, word.String())
}

// dockerUnquote removes quotes and escapes from one word.
func dockerUnquote(word string, escape byte) string {
	var out strings.Builder
	var quote byte
	for i := 0; i < len(word); i++ {
		c := word[i]
		switch {
		case c == escape && quote != '\'' && i+1 < len(word):
			i++
			out.WriteByte(word[i])
		case quote == 0 && (c == '"' || c == '\''):
			quote = c
		case c == quote:
			quote = 0
		default:
			out.WriteByte(c)
		}
	}
	return out.String()
}

const rustTableFixture = "const LAUNCH_TARGETS: &[LaunchTarget] = &[\n%s\n];\n"

const rustDemoTarget = `LaunchTarget {
    name: "demo",
    script_path: "/usr/local/libexec/workcell/provider-wrapper.sh",
    approved_invocations: &["/usr/local/bin/demo", "/usr/local/libexec/workcell/core/demo",],
},`

func TestRustLaunchTargetsIgnoreDecoys(t *testing.T) {
	table := func(entries string) string { return fmt.Sprintf(rustTableFixture, entries) }
	cases := map[string]struct {
		source string
		want   int
	}{
		"live":                        {table(rustDemoTarget), 1},
		"live after lifetime":         {"struct S { name: &'static str }\n" + table(rustDemoTarget), 1},
		"live after char quote":       {"const Q: char = '\"';\n" + table(rustDemoTarget), 1},
		"live after byte char":        {"const Q: u8 = b'\\'';\n" + table(rustDemoTarget), 1},
		"live without trailing comma": {table(strings.TrimSuffix(rustDemoTarget, ",")), 1},
		"line comment":                {table("// " + strings.ReplaceAll(rustDemoTarget, "\n", "\n// ")), 0},
		"block comment":               {table("/*\n" + rustDemoTarget + "\n*/"), 0},
		"nested block comment":        {table("/* outer /* inner */\n" + rustDemoTarget + "\n*/"), 0},
		"raw string":                  {table(`const D: &str = r#"` + rustDemoTarget + `"#;`), 0},
		"raw string with hashes":      {table(`r##"x"# ` + rustDemoTarget + ` "##`), 0},
		"byte raw string":             {table(`br"` + rustDemoTarget + `"`), 0},
		"escaped string":              {table(`"\"` + strings.ReplaceAll(rustDemoTarget, `"`, `\"`) + `"`), 0},
		"outside the table":           {"const OTHER: &[LaunchTarget] = &[\n" + rustDemoTarget + "\n];\n" + table(""), 0},
		"cfg attribute on entry":      {table("#[cfg(any())]\n" + rustDemoTarget), 0},
		"macro in table":              {table("demo!(" + rustDemoTarget + ")"), 0},
		"table in a function":         {"fn f() {\n" + table(rustDemoTarget) + "}\n", 0},
		"table in a cfg module":       {"#[cfg(any())]\nmod m {\n" + table(rustDemoTarget) + "}\n", 0},
		"cfg attribute on table":      {"#[cfg(any())]\n" + table(rustDemoTarget), 0},
		"inactive copy beside live":   {"#[cfg(any())]\n" + table("") + table(rustDemoTarget), 1},
		"two tables":                  {table(rustDemoTarget) + table(rustDemoTarget), 0},
		"unterminated comment":        {table(rustDemoTarget) + "/* /* */", 0},
		"unterminated raw string":     {table(rustDemoTarget) + `r#"x"`, 0},
	}
	for name, c := range cases {
		if got := rustLaunchTargets(c.source); len(got) != c.want {
			t.Errorf("%s: found %d targets, want %d: %v", name, len(got), c.want, got)
		}
	}
}

func TestDockerfilePinnedArgsIgnoreDecoys(t *testing.T) {
	cases := map[string]struct {
		text string
		want bool
	}{
		"live":                          {"ARG DEMO_VERSION=1.2.3\n", true},
		"lower-case keyword":            {"arg DEMO_VERSION=1.2.3\n", true},
		"second name on the line":       {"ARG OTHER=1 DEMO_VERSION=1.2.3\n", true}, // BuildKit defines each name=value operand.
		"comment":                       {"# ARG DEMO_VERSION=1.2.3\n", false},
		"unpinned":                      {"ARG DEMO_VERSION\n", false},
		"empty value":                   {"ARG DEMO_VERSION=\n", false},
		"COPY heredoc":                  {"COPY <<EOF /x\nARG DEMO_VERSION=1.2.3\nEOF\n", false},
		"RUN quoted heredoc":            {"RUN <<'EOF'\nARG DEMO_VERSION=1.2.3\nEOF\n", false},
		"partly quoted delimiter":       {"RUN <<E\"O\"F\nARG DEMO_VERSION=1.2.3\nEOF\n", false},
		"fd-prefixed heredoc":           {"RUN cat 3<<EOF\nARG DEMO_VERSION=1.2.3\nEOF\n", false},
		"continuation":                  {"RUN echo \\\nARG DEMO_VERSION=1.2.3\n", false},
		"continuation with blanks":      {"RUN echo \\  \nARG DEMO_VERSION=1.2.3\n", false},
		"continuation over comment":     {"RUN echo \\\n# note\nARG DEMO_VERSION=1.2.3\n", false},
		"continuation over blank line":  {"RUN echo \\\n\nARG DEMO_VERSION=1.2.3\n", false},
		"heredoc on continuation line":  {"RUN cat \\\n  <<EOF\nARG DEMO_VERSION=1.2.3\nEOF\n", false},
		"second heredoc body":           {"RUN <<A <<B\ntrue\nA\nARG DEMO_VERSION=1.2.3\nB\n", false},
		"indented plain terminator":     {"RUN <<EOF\n  EOF\nARG DEMO_VERSION=1.2.3\nEOF\n", false},
		"tab-indented plain terminator": {"RUN <<EOF\n\tEOF\nARG DEMO_VERSION=1.2.3\nEOF\n", false},
		"space-indented chomp end":      {"RUN <<-EOF\n  EOF\nARG DEMO_VERSION=1.2.3\nEOF\n", false},
		"unterminated heredoc":          {"RUN <<EOF\nARG DEMO_VERSION=1.2.3\n", false},
		"backtick escape directive":     {"# escape=`\nRUN echo `\nARG DEMO_VERSION=1.2.3\n", false},
		"bad escape directive":          {"# escape=x\nARG DEMO_VERSION=1.2.3\n", false},
		"tab-indented chomp end":        {"RUN <<-EOF\n\tEOF\nARG DEMO_VERSION=1.2.3\n", true},
		"live after heredoc":            {"RUN <<EOF\ntrue\nEOF\nARG DEMO_VERSION=1.2.3\n", true},
		"live after continuation":       {"RUN echo \\\n  ok\nARG DEMO_VERSION=1.2.3\n", true},
		"quoted heredoc-like word":      {"RUN echo \"<<EOF\"\nARG DEMO_VERSION=1.2.3\n", true},
		"heredoc outside RUN/COPY/ADD":  {"ENV X <<EOF\nARG DEMO_VERSION=1.2.3\n", true},
		"escape directive after ARG":    {"ARG A=1\n# escape=`\nRUN echo \\\nARG DEMO_VERSION=1.2.3\n", false},
	}
	for name, c := range cases {
		if got := dockerfilePinnedArgs(c.text)["DEMO_VERSION"]; got != c.want {
			t.Errorf("%s: pinned = %v, want %v", name, got, c.want)
		}
	}
}

func TestManifestsMatchRustLaunchTargets(t *testing.T) {
	source := readRepoFile(t, "runtime/container/rust/src/bin/workcell-launcher.rs")
	targets := rustLaunchTargets(source)
	var names []string
	for _, target := range targets {
		name := target[1]
		names = append(names, name)
		if target[2] != "/usr/local/bin/"+name || target[3] != "/usr/local/libexec/workcell/core/"+name {
			t.Errorf("LaunchTarget %s invocations = %v", name, target[2:])
		}
	}
	var binaries []string
	for _, m := range loadRepoManifests(t) {
		if m.Tier == "certified" {
			if m.Binary != m.ID {
				t.Errorf("%s: binary %q must equal the adapter id to match its Rust LaunchTarget", m.ID, m.Binary)
			}
			binaries = append(binaries, m.Binary)
		}
	}
	if !slices.Equal(sorted(names), sorted(binaries)) {
		t.Errorf("Rust provider LaunchTargets = %v, certified manifest binaries = %v", names, binaries)
	}
}

func TestManifestInstallPinsExist(t *testing.T) {
	pinned := dockerfilePinnedArgs(readRepoFile(t, "runtime/container/Dockerfile"))
	var pkg struct {
		Dependencies map[string]string `json:"dependencies"`
	}
	if err := json.Unmarshal([]byte(readRepoFile(t, "runtime/container/providers/package.json")), &pkg); err != nil {
		t.Fatal(err)
	}
	for _, m := range loadRepoManifests(t) {
		if m.Tier == "planned" {
			continue
		}
		// Each pin and provenance script is named for its own adapter, so two
		// manifests cannot swap them and still pass.
		if want := strings.ToUpper(m.ID) + "_VERSION"; m.Install.Method == "binary" && m.Install.VersionArg != want {
			t.Errorf("%s: version_arg = %q, want %q", m.ID, m.Install.VersionArg, want)
		}
		if want := "scripts/verify-upstream-" + m.ID + "-release.sh"; m.Install.Provenance != want {
			t.Errorf("%s: provenance = %q, want %q", m.ID, m.Install.Provenance, want)
		}
		if m.Install.VersionArg != "" && !pinned[m.Install.VersionArg] {
			t.Errorf("%s: Dockerfile has no pinned ARG %s", m.ID, m.Install.VersionArg)
		}
		if m.Install.Package != "" && pkg.Dependencies[m.Install.Package] == "" {
			t.Errorf("%s: providers/package.json has no dependency %s", m.ID, m.Install.Package)
		}
		if _, err := os.Stat(filepath.Join(repoRoot, m.Install.Provenance)); err != nil {
			t.Errorf("%s: provenance script: %v", m.ID, err)
		}
	}
}

func TestParseManifestRejectsInvalidInput(t *testing.T) {
	const valid = `schema = 1
id = "demo"
tier = "certified"
binary = "demo"

[install]
method = "binary"
version_arg = "DEMO_VERSION"
provenance = "scripts/verify-demo.sh"

[credentials.demo_auth]
container_path = "/opt/demo.json"
`
	cases := map[string]string{
		"unknown key":             strings.Replace(valid, `binary = "demo"`, "binary = \"demo\"\nextra = true", 1),
		"unknown table":           valid + "\n[flags]\nmode = \"allowlist\"\n",
		"unknown install key":     strings.Replace(valid, `method = "binary"`, "method = \"binary\"\nsha = \"x\"", 1),
		"bare credentials table":  valid + "\n[credentials]\ncontainer_path = \"/x\"\n",
		"nested credentials key":  valid + "\n[credentials.a.b]\ncontainer_path = \"/x\"\n",
		"unknown credential key":  valid + "env = \"X\"\n",
		"wrong type":              strings.Replace(valid, `tier = "certified"`, "tier = 1", 1),
		"wrong list type":         valid + "\n[home]\nreserved_targets = [1]\n",
		"wrong schema":            strings.Replace(valid, "schema = 1", "schema = 2", 1),
		"missing schema":          strings.Replace(valid, "schema = 1\n", "", 1),
		"invalid id":              strings.Replace(valid, `id = "demo"`, `id = "Demo"`, 1),
		"invalid tier":            strings.Replace(valid, `"certified"`, `"trusted"`, 1),
		"missing binary":          strings.Replace(valid, "binary = \"demo\"\n", "", 1),
		"invalid method":          strings.Replace(valid, `method = "binary"`, `method = "curl"`, 1),
		"binary without arg":      strings.Replace(valid, "version_arg = \"DEMO_VERSION\"\n", "", 1),
		"npm without package":     strings.Replace(valid, `method = "binary"`, `method = "npm"`, 1),
		"missing provenance":      strings.Replace(valid, "provenance = \"scripts/verify-demo.sh\"\n", "", 1),
		"relative container path": strings.Replace(valid, `"/opt/demo.json"`, `"demo.json"`, 1),
		"subset parser rejection": valid + "\n[[install]]\n",
	}
	if _, err := parseManifest("valid.toml", []byte(valid)); err != nil {
		t.Fatalf("valid fixture rejected: %v", err)
	}
	for name, content := range cases {
		if _, err := parseManifest("case.toml", []byte(content)); err == nil {
			t.Errorf("%s: parseManifest accepted invalid input", name)
		}
	}
}

func TestLoadManifestsRejectsIDDirectoryMismatch(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "other"), 0o700); err != nil {
		t.Fatal(err)
	}
	content := "schema = 1\nid = \"demo\"\ntier = \"planned\"\n"
	if err := os.WriteFile(filepath.Join(root, "other", "adapter.toml"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadManifests(root); err == nil {
		t.Fatal("LoadManifests accepted an id that does not match its directory")
	}
}

func TestLoadManifestsFailsClosed(t *testing.T) {
	const planned = "schema = 1\nid = \"demo\"\ntier = \"planned\"\n"
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "demo"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "demo", "adapter.toml"), []byte(planned), 0o600); err != nil {
		t.Fatal(err)
	}
	setup := map[string]func(root string){
		"symlinked adapter directory": func(root string) {
			os.Symlink(filepath.Join(outside, "demo"), filepath.Join(root, "demo"))
		},
		"symlinked manifest": func(root string) {
			os.Mkdir(filepath.Join(root, "demo"), 0o700)
			os.Symlink(filepath.Join(outside, "demo", "adapter.toml"), filepath.Join(root, "demo", "adapter.toml"))
		},
		"directory without manifest": func(root string) {
			os.Mkdir(filepath.Join(root, "demo"), 0o700)
		},
		"manifest is a FIFO": func(root string) {
			os.Mkdir(filepath.Join(root, "demo"), 0o700)
			if err := unix.Mkfifo(filepath.Join(root, "demo", "adapter.toml"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"manifest is a directory": func(root string) {
			os.MkdirAll(filepath.Join(root, "demo", "adapter.toml"), 0o700)
		},
	}
	for name, prepare := range setup {
		root := t.TempDir()
		prepare(root)
		if _, err := LoadManifests(root); err == nil {
			t.Errorf("%s: LoadManifests accepted the tree", name)
		}
	}
	if _, err := LoadManifests(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("LoadManifests accepted a missing root")
	}
}
