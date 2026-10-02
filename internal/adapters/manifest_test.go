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

	supported := manifestIDs(manifests, func(m Manifest) bool { return m.Tier != "planned" })
	if !slices.Equal(supported, providerid.AllProviders) {
		t.Errorf("non-planned manifests = %v, want providerid.AllProviders %v", supported, providerid.AllProviders)
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
				credentialKeys:           []string{},
				credentialContainerPaths: map[string]string{},
				reservedTargets:          append([]string{}, m.ReservedTargets...),
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

// providerEndpoints runs provider_endpoints from script, rewritten from declare -f
// so that the lone "*)" arm prints "default-arm-N" to stderr and any other line
// ending in ")" (another arm pattern) prints "line-N": arm lists each arm entered.
// The probe is parsed before the source runs, and POSIX-mode unset (a special
// builtin) drops any function the script defines over a builtin it uses.
func providerEndpoints(t *testing.T, script, id string) (out, arm string, code int) {
	t.Helper()
	const probe = `{ source "$1" || exit 3
POSIXLY_CORRECT=y; unset -f builtin declare read echo eval exit set unset trap; trap - DEBUG RETURN; unset POSIXLY_CORRECT
n=0 body=""
while IFS= read -r line; do
  n=$((n + 1)) body+="$line"$'\n'
  [[ "$line" =~ ^[[:space:]]*\*\)$ ]] && body+="echo default-arm-$n >&2"$'\n' && continue
  [[ "$line" =~ \)$ ]] && body+="echo line-$n >&2"$'\n'
done < <(declare -f provider_endpoints)
eval "$body" || exit 3
provider_endpoints "$2"; }`
	cmd := exec.Command("bash", "--noprofile", "--norc", "-c", probe, "bash", script, id)
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

// endpointRowAbsent reports whether script has no provider_endpoints row for
// id: id and an unknown id must enter only the same lone "*)" arm, with exit 1
// and no output. Any other arm entered first (a pattern list, a glob, a
// fall-through, a recursive call) adds its own marker, so it fails.
func endpointRowAbsent(t *testing.T, script, id string) bool {
	t.Helper()
	unknownOut, unknownArm, unknownCode := providerEndpoints(t, script, "no-such-provider")
	out, arm, code := providerEndpoints(t, script, id)
	return unknownCode == 1 && unknownOut == "" && regexp.MustCompile(`^default-arm-[0-9]+\n$`).MatchString(unknownArm) &&
		code == 1 && out == "" && arm == unknownArm
}

func TestEndpointRowAbsentRejectsExplicitArms(t *testing.T) {
	const script = "provider_endpoints() {\n  case \"$1\" in\n    codex)\n      echo api.openai.com:443\n      ;;\n%s    *)\n      return 1\n      ;;\n  esac\n}\n"
	cases := map[string]struct {
		arm  string
		want bool
	}{
		"no row":                    {"", true},
		"other row":                 {"    gemini)\n      return 1\n      ;;\n", true},
		"return 1 arm":              {"    antigravity)\n      return 1\n      ;;\n", false},
		"arm in a pattern":          {"    gemini | antigravity)\n      return 1\n      ;;\n", false},
		"wildcard pattern list":     {"    antigravity | *)\n      return 1\n      ;;\n", false},
		"glob matching both probes": {"    anti* | no-*)\n      return 1\n      ;;\n", false},
		"nested wildcard arm":       {"    antigravity)\n      case x in\n        *)\n          return 1\n          ;;\n      esac\n      ;;\n", false},
		"fall-through arm":          {"    antigravity)\n      ;&\n", false},
		"test-next arm":             {"    antigravity)\n      true\n      ;;&\n", false},
		"arm that recurses":         {"    antigravity)\n      provider_endpoints no-such-provider\n      ;;\n", false},
		"arm with a row":            {"    antigravity)\n      echo x:443\n      ;;\n", false},
	}
	absent := func(src string) bool {
		path := filepath.Join(t.TempDir(), "endpoints.sh")
		if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		return endpointRowAbsent(t, path, providerid.Antigravity)
	}
	for name, c := range cases {
		if got := absent(fmt.Sprintf(script, c.arm)); got != c.want {
			t.Errorf("%s: row absent = %v, want %v", name, got, c.want)
		}
	}
	// A script function over the declare builtin must not forge a default-only body.
	forge := `declare() { printf 'provider_endpoints () {\ncase "$1" in\n*)\nreturn 1\n;;\nesac\n}\n'; }`
	if absent(fmt.Sprintf(script, cases["return 1 arm"].arm) + forge) {
		t.Error("a declare override forged an absent row")
	}
}

func TestManifestsMatchLauncherProviderEndpoints(t *testing.T) {
	const script = "scripts/lib/launcher/egress-endpoints.sh"
	for _, m := range loadRepoManifests(t) {
		if m.Tier == "planned" {
			if !endpointRowAbsent(t, script, m.ID) || len(m.EgressEndpoints) != 0 {
				t.Errorf("%s: planned adapter must have no provider_endpoints row and no egress endpoints", m.ID)
			}
			continue
		}
		out, stderr, code := providerEndpoints(t, script, m.ID)
		if code != 0 {
			t.Fatalf("%s: provider_endpoints exit %d:\n%s", m.ID, code, stderr)
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
		case "planned":
			if code != 2 || !strings.Contains(out, planned) {
				t.Errorf("%s: planned manifest but the launcher does not report a planned adapter (exit %d): %s", m.ID, code, out)
			}
		default:
			if code != 0 || strings.Contains(out, unsupported) || strings.Contains(out, planned) {
				t.Errorf("%s: %s manifest but the launcher rejects --agent (exit %d): %s", m.ID, m.Tier, code, out)
			}
		}
	}
}

var (
	// rustLaunchTable is the whole table: LaunchTarget entries with plain string
	// fields and one or more invocations, and nothing else.
	rustLaunchTable   = regexp.MustCompile(`^\[ (?:LaunchTarget \{ name : "[^"]*" , script_path : "[^"]*" , approved_invocations : & \[ "[^"]*" (?:, "[^"]*" )*(?:, )?\] (?:, )?\} (?:, )?)*\]$`)
	rustLaunchBlock   = regexp.MustCompile(`(?:^| )LaunchTarget \{ name : "([a-z-]+)" , script_path : "/usr/local/libexec/workcell/provider-wrapper\.sh" , approved_invocations : & \[ "([^"]+)" , "([^"]+)" (?:, )?\] (?:, )?\}`)
	dockerDirective   = regexp.MustCompile(`^#[ \t]*([a-zA-Z][a-zA-Z0-9]*)[ \t]*=[ \t]*(.+?)[ \t]*$`)
	dockerHeredocWord = regexp.MustCompile(`^[0-9]*<<(-?)([^<]*)$`)
)

// rustTokens splits Rust source into tokens and drops comments (nested too). A
// plain "..." string without escapes keeps its text; every other literal (raw,
// byte, C, char, or escaped) is one opaque "<lit>" token, so literal text never
// looks like code. An unterminated comment or literal is an error.
func rustTokens(src string) ([]string, error) {
	var tokens []string
	isWord := func(c byte) bool {
		return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
	}
	// quoted returns the index past the literal whose opening quote is at i.
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
			for i < len(src) && src[i] != '\n' {
				i++
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
// table, or has a table entry of another shape.
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
	// The declaration reads "const LAUNCH_TARGETS: &[LaunchTarget] = &[...];".
	// The table runs to the next ";" token; the whole-table match below
	// rejects anything else in that span.
	head := []string{":", "&", "[", "LaunchTarget", "]", "=", "&"}
	rest := tokens[start[0]+1:]
	end := slices.Index(rest, ";")
	if end < len(head) || !slices.Equal(rest[:len(head)], head) {
		return nil
	}
	// Any other shape (an attribute, a macro, a const or escaped field, a
	// provider row with other than two invocations) could hide a row, so
	// reject it instead of skipping it.
	table := strings.Join(rest[len(head):end], " ")
	if !rustLaunchTable.MatchString(table) {
		return nil
	}
	targets := rustLaunchBlock.FindAllStringSubmatch(table, -1)
	if strings.Count(table, `script_path : "/usr/local/libexec/workcell/provider-wrapper.sh"`) != len(targets) {
		return nil
	}
	return targets
}

// dockerfilePinnedArgs returns the ARG names a Dockerfile pins with a non-empty
// value, splitting instructions as BuildKit does: an escape directive, line
// continuations, comment and blank lines dropped even inside a continuation,
// and RUN/COPY/ADD heredocs ("<<NAME" ends at a line equal to NAME; "<<-NAME"
// first strips leading tabs), queued in order. A decoy in a comment, a
// continuation, or a heredoc body does not count. A malformed file returns nil.
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
		words := dockerWords(instruction.String(), escape, true)
		unquoted := dockerWords(instruction.String(), escape, false)
		instruction.Reset()
		switch strings.ToUpper(words[0]) {
		case "ARG":
			for _, word := range unquoted[1:] {
				if name, value, ok := strings.Cut(word, "="); ok && value != "" {
					args[name] = true
				}
			}
		case "RUN", "COPY", "ADD":
			for _, word := range words[1:] {
				if m := dockerHeredocWord.FindStringSubmatch(word); m != nil {
					if name := dockerWords(m[2], escape, false)[0]; name != "" {
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

// dockerWords splits text at blanks outside quotes. With raw, quotes and
// escapes stay in each word, as BuildKit's heredoc scan keeps them, so a
// quoted "<<EOF" is not a heredoc; without raw, they are removed.
func dockerWords(text string, escape byte, raw bool) []string {
	var words []string
	var word strings.Builder
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote == 0 && (c == ' ' || c == '\t'):
			if word.Len() > 0 {
				words = append(words, word.String())
				word.Reset()
			}
			continue
		case c == escape && quote != '\'' && i+1 < len(text):
			if raw {
				word.WriteByte(c)
			}
			i++
			c = text[i]
		case quote == 0 && (c == '"' || c == '\''), c == quote:
			quote ^= c // opens or closes the quote
			if !raw {
				continue
			}
		}
		word.WriteByte(c)
	}
	return append(words, word.String())
}

const rustTableFixture = "const LAUNCH_TARGETS: &[LaunchTarget] = &[\n%s\n];\n"

const rustDemoTarget = `LaunchTarget {
    name: "demo",
    script_path: "/usr/local/libexec/workcell/provider-wrapper.sh",
    approved_invocations: &["/usr/local/bin/demo", "/usr/local/libexec/workcell/core/demo",],
},`

func TestRustLaunchTargetsIgnoreDecoys(t *testing.T) {
	table := func(entries string) string { return fmt.Sprintf(rustTableFixture, entries) }
	// other is a valid row, so a check that skips a bad row still finds one.
	other := strings.ReplaceAll(rustDemoTarget, "demo", "other") + "\n"
	cases := map[string]struct {
		source string
		want   int
	}{
		"live":                         {table(rustDemoTarget), 1},
		"live after lifetime":          {"struct S { name: &'static str }\n" + table(rustDemoTarget), 1},
		"live after char quote":        {"const Q: char = '\"';\n" + table(rustDemoTarget), 1},
		"live after byte char":         {"const Q: u8 = b'\\'';\n" + table(rustDemoTarget), 1},
		"live without trailing comma":  {table(strings.TrimSuffix(rustDemoTarget, ",")), 1},
		"line comment":                 {table("// " + strings.ReplaceAll(rustDemoTarget, "\n", "\n// ")), 0},
		"block comment":                {table("/*\n" + rustDemoTarget + "\n*/"), 0},
		"nested block comment":         {table("/* outer /* inner */\n" + rustDemoTarget + "\n*/"), 0},
		"raw string":                   {table(`const D: &str = r#"` + rustDemoTarget + `"#;`), 0},
		"raw string with hashes":       {table(`r##"x"# ` + rustDemoTarget + ` "##`), 0},
		"byte raw string":              {table(`br"` + rustDemoTarget + `"`), 0},
		"escaped string":               {table(`"\"` + strings.ReplaceAll(rustDemoTarget, `"`, `\"`) + `"`), 0},
		"outside the table":            {"const OTHER: &[LaunchTarget] = &[\n" + rustDemoTarget + "\n];\n" + table(""), 0},
		"cfg attribute on entry":       {table("#[cfg(any())]\n" + rustDemoTarget), 0},
		"macro in table":               {table("demo!(" + rustDemoTarget + ")"), 0},
		"table in a function":          {"fn f() {\n" + table(rustDemoTarget) + "}\n", 0},
		"table in a cfg module":        {"#[cfg(any())]\nmod m {\n" + table(rustDemoTarget) + "}\n", 0},
		"cfg attribute on table":       {"#[cfg(any())]\n" + table(rustDemoTarget), 0},
		"inactive copy beside live":    {"#[cfg(any())]\n" + table("") + table(rustDemoTarget), 1},
		"provider row, one invocation": {table(other + strings.Replace(rustDemoTarget, `"/usr/local/bin/demo", `, "", 1)), 0},
		"escaped script path":          {table(other + strings.Replace(rustDemoTarget, `wrapper.sh`, `wrapper\x2esh`, 1)), 0},
		"const script path":            {table(other + strings.Replace(rustDemoTarget, `"/usr/local/libexec/workcell/provider-wrapper.sh"`, "WRAPPER", 1)), 0},
		"other row beside provider":    {table(`LaunchTarget { name: "git", script_path: "/g.sh", approved_invocations: &["/a", "/b", "/c"] },` + "\n" + rustDemoTarget), 1},
		"two tables":                   {table(rustDemoTarget) + table(rustDemoTarget), 0},
		"unterminated comment":         {table(rustDemoTarget) + "/* /* */", 0},
		"unterminated raw string":      {table(rustDemoTarget) + `r#"x"`, 0},
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
	var names []string
	for _, target := range rustLaunchTargets(readRepoFile(t, "runtime/container/rust/src/bin/workcell-launcher.rs")) {
		name := target[1]
		names = append(names, name)
		if target[2] != "/usr/local/bin/"+name || target[3] != "/usr/local/libexec/workcell/core/"+name {
			t.Errorf("LaunchTarget %s invocations = %v", name, target[2:])
		}
	}
	var binaries []string
	for _, m := range loadRepoManifests(t) {
		if m.Tier != "planned" {
			if m.Binary != m.ID {
				t.Errorf("%s: binary %q must equal the adapter id to match its Rust LaunchTarget", m.ID, m.Binary)
			}
			binaries = append(binaries, m.Binary)
		}
	}
	if !slices.Equal(sorted(names), sorted(binaries)) {
		t.Errorf("Rust provider LaunchTargets = %v, non-planned manifest binaries = %v", names, binaries)
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
		// Each pin and provenance script is named for its own adapter (no swaps).
		if want := strings.ToUpper(m.ID) + "_VERSION"; m.Install.Method == "binary" && m.Install.VersionArg != want {
			t.Errorf("%s: version_arg = %q, want %q", m.ID, m.Install.VersionArg, want)
		}
		if want := "scripts/verify-upstream-" + m.ID + "-release.sh"; m.Install.Provenance != want {
			t.Errorf("%s: provenance = %q, want %q", m.ID, m.Install.Provenance, want)
		}
		if m.Install.VersionArg != "" && !pinned[m.Install.VersionArg] {
			t.Errorf("%s: Dockerfile has no pinned ARG %s", m.ID, m.Install.VersionArg)
		}
		// The runtime runs this exact package (Dockerfile and provider-wrapper.sh).
		if want := map[string]string{providerid.Gemini: "@google/gemini-cli"}[m.ID]; m.Install.Package != want || want != "" && pkg.Dependencies[want] == "" {
			t.Errorf("%s: package = %q, want %q as a providers/package.json dependency", m.ID, m.Install.Package, want)
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
		"planned with fields":     strings.Replace(valid, `"certified"`, `"planned"`, 1),
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
		"id does not match directory": func(root string) {
			os.Mkdir(filepath.Join(root, "other"), 0o700)
			os.WriteFile(filepath.Join(root, "other", "adapter.toml"), []byte(planned), 0o600)
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
		"regular file with an adapter name": func(root string) {
			os.WriteFile(filepath.Join(root, "demo"), []byte(planned), 0o600)
		},
		"manifest is a directory": func(root string) {
			os.MkdirAll(filepath.Join(root, "demo", "adapter.toml"), 0o700)
		},
		"adapter entry is a FIFO": func(root string) {
			if err := unix.Mkfifo(filepath.Join(root, "demo"), 0o600); err != nil {
				t.Fatal(err)
			}
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
	// Positive control: a regular file named like README.md is skipped.
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "README.md"), []byte("notes\n"), 0o600)
	os.Mkdir(filepath.Join(root, "demo"), 0o700)
	os.WriteFile(filepath.Join(root, "demo", "adapter.toml"), []byte(planned), 0o600)
	if manifests, err := LoadManifests(root); err != nil || len(manifests) != 1 {
		t.Errorf("LoadManifests = %v, %v; want the demo manifest only", manifests, err)
	}
}
