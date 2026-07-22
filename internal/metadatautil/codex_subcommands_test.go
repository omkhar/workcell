// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const codexSubcommandSourceFixture = `#[derive(Debug, Parser)]
#[clap(
    author,
    version
)]
struct UnrelatedRoot {}

#[derive(Debug, clap::Subcommand)]
enum Subcommand {
    /// Run Codex non-interactively.
    #[clap(visible_alias = "e")]
    Exec(ExecCli),

    /// Launch the Desktop app on supported hosts.
    #[cfg(any(target_os = "macos", target_os = "windows"))]
    App(app_cmd::AppCommand),

    /// Update Codex.
    Update,

    /// Browse cloud tasks.
    #[clap(name = "cloud", alias = "cloud-tasks")]
    Cloud(CloudTasksCli),

    /// Internal relay.
    #[clap(hide = true, name = "stdio-to-uds")]
    StdioToUds(StdioToUdsCommand),
}
`

func TestParseCodexSubcommandsIncludesAliasesHiddenAndUnitVariants(t *testing.T) {
	want := []string{"exec", "e", "app", "update", "cloud", "cloud-tasks", "stdio-to-uds", "help"}
	got, err := parseCodexSubcommands([]byte(codexSubcommandSourceFixture))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseCodexSubcommands() = %q, want %q", got, want)
	}
}

func TestParseCodexSubcommandsFailsClosedOnUnsupportedShapes(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		want   string
	}{
		{name: "missing-enum", source: "enum Other { Exec, }\n", want: "one complete enum"},
		{name: "multiline-attribute", source: codexTestEnum("    #[clap(\n    Exec(ExecCli),\n"), want: "multiline Subcommand attribute"},
		{name: "unsupported-alias", source: codexTestEnum("    #[clap(alias(\"e\"))]\n    Exec(ExecCli),\n"), want: "unsupported dispatch-affecting Clap item"},
		{name: "dynamic-alias", source: codexTestEnum("    #[clap(alias = make_alias())]\n    Exec(ExecCli),\n"), want: "unsupported dispatch-affecting Clap item"},
		{name: "multiple-names", source: codexTestEnum("    #[clap(name = \"first\", name = \"second\")]\n    Exec(ExecCli),\n"), want: "multiple explicit command names"},
		{name: "malformed-alias-list", source: codexTestEnum("    #[clap(aliases = [\"e\", make_alias()])]\n    Exec(ExecCli),\n"), want: "empty or malformed alias list"},
		{name: "duplicate", source: codexTestEnum("    Exec(ExecCli),\n    #[clap(name = \"exec\")]\n    Other(OtherCli),\n"), want: "duplicate Codex subcommand"},
		{name: "long-flag", source: codexTestEnum("    #[command(long_flag = \"danger\")]\n    Exec(ExecCli),\n"), want: "unsupported dispatch-affecting Clap item"},
		{name: "short-flag", source: codexTestEnum("    #[command(short_flag = 'd')]\n    Exec(ExecCli),\n"), want: "unsupported dispatch-affecting Clap item"},
		{name: "external-subcommand", source: codexTestEnum("    #[command(external_subcommand)]\n    Exec(ExecCli),\n"), want: "unsupported dispatch-affecting Clap item"},
		{name: "flatten", source: codexTestEnum("    #[command(flatten)]\n    Exec(ExecCli),\n"), want: "unsupported dispatch-affecting Clap item"},
		{name: "skip", source: codexTestEnum("    #[command(skip)]\n    Exec(ExecCli),\n"), want: "unsupported dispatch-affecting Clap item"},
		{name: "nested-subcommand", source: codexTestEnum("    #[command(subcommand)]\n    Exec(ExecCli),\n"), want: "unsupported dispatch-affecting Clap item"},
		{name: "indirect-command", source: codexTestEnum("    #[cfg_attr(unix, command(long_flag = \"danger\"))]\n    Exec(ExecCli),\n"), want: "unsupported Subcommand attribute"},
		{name: "cfg-then-command-same-line", source: codexTestEnum("    #[cfg(unix)] #[command(long_flag = \"danger\")]\n    Exec(ExecCli),\n"), want: "unsupported Subcommand attribute"},
		{name: "spaced-command", source: codexTestEnum("    # [command(long_flag = \"danger\")]\n    Exec(ExecCli),\n"), want: "unrecognized Subcommand variant"},
		{name: "enum-rename-all", source: strings.Replace(codexTestEnum("    Exec(ExecCli),\n"), "enum Subcommand", "#[command(rename_all = \"snake_case\")]\nenum Subcommand", 1), want: "unsupported enum-level attribute"},
		{name: "enum-multiline-attribute", source: strings.Replace(codexTestEnum("    Exec(ExecCli),\n"), "enum Subcommand", "#[command(\n    rename_all = \"snake_case\"\n)]\nenum Subcommand", 1), want: "unsupported enum-level attribute"},
		{name: "multiple-variants-one-line", source: codexTestEnum("    Exec(ExecCli), Danger(DangerCli),\n"), want: "unrecognized Subcommand variant"},
		{name: "multiple-tuple-fields", source: codexTestEnum("    Exec(ExecCli, DangerCli),\n"), want: "unrecognized Subcommand variant"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := parseCodexSubcommands([]byte(test.source))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseCodexSubcommands() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestPrepareCodexSubcommandFixtureAdvancesUnchangedNamespace(t *testing.T) {
	server := codexSourceServer(t, codexSubcommandSourceFixture)
	fixturePath := filepath.Join(t.TempDir(), "codex-subcommands.txt")
	outputPath := filepath.Join(t.TempDir(), "prepared.txt")
	fixture := codexFixtureText("0.144.1", []string{"update", "exec", "e", "app", "cloud", "cloud-tasks", "stdio-to-uds", "help"})
	if err := os.WriteFile(fixturePath, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := prepareCodexSubcommandFixture("0.145.0", fixturePath, outputPath, server.URL, server.Client()); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	if !strings.Contains(text, "# codex-version: 0.145.0\n") || !strings.Contains(text, "openai/codex tag rust-v0.145.0") {
		t.Fatalf("prepared fixture did not advance both version bindings:\n%s", text)
	}
	wantSuffix := "exec\ne\napp\nupdate\ncloud\ncloud-tasks\nstdio-to-uds\nhelp\n"
	if !strings.HasSuffix(text, wantSuffix) {
		t.Fatalf("prepared fixture token order mismatch:\n%s", text)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("prepared fixture mode = %04o, want 0644", info.Mode().Perm())
	}
}

func TestPrepareCodexSubcommandFixtureRejectsNamespaceDriftWithoutMutation(t *testing.T) {
	server := codexSourceServer(t, strings.Replace(codexSubcommandSourceFixture, "    Update,\n", "    Update,\n    Danger(DangerCli),\n", 1))
	root := t.TempDir()
	fixturePath := filepath.Join(root, "codex-subcommands.txt")
	outputPath := filepath.Join(root, "prepared.txt")
	fixture := codexFixtureText("0.144.1", []string{"exec", "e", "app", "update", "cloud", "cloud-tasks", "stdio-to-uds", "help"})
	if err := os.WriteFile(fixturePath, []byte(fixture), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outputPath, []byte("sentinel\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := prepareCodexSubcommandFixture("0.145.0", fixturePath, outputPath, server.URL, server.Client())
	if err == nil || !strings.Contains(err.Error(), "added=[danger]") {
		t.Fatalf("namespace drift error = %v", err)
	}
	content, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "sentinel\n" {
		t.Fatalf("failed preparation mutated output: %q", content)
	}
}

func TestPrepareCodexSubcommandFixtureRejectsAliasedSourceAndDuplicateFixture(t *testing.T) {
	server := codexSourceServer(t, codexSubcommandSourceFixture)
	t.Run("hard-linked-output", func(t *testing.T) {
		root := t.TempDir()
		fixturePath := filepath.Join(root, "fixture")
		fixture := codexFixtureText("0.144.1", []string{"exec", "e", "app", "update", "cloud", "cloud-tasks", "stdio-to-uds", "help"})
		if err := os.WriteFile(fixturePath, []byte(fixture), 0o644); err != nil {
			t.Fatal(err)
		}
		outputPath := filepath.Join(root, "output")
		if err := os.Link(fixturePath, outputPath); err != nil {
			t.Fatal(err)
		}
		err := prepareCodexSubcommandFixture("0.145.0", fixturePath, outputPath, server.URL, server.Client())
		if err == nil || !strings.Contains(err.Error(), "must not alias") {
			t.Fatalf("aliased output error = %v", err)
		}
	})
	t.Run("duplicate-token", func(t *testing.T) {
		root := t.TempDir()
		fixturePath := filepath.Join(root, "fixture")
		commands := []string{"exec", "exec", "e", "app", "update", "cloud", "cloud-tasks", "stdio-to-uds", "help"}
		if err := os.WriteFile(fixturePath, []byte(codexFixtureText("0.144.1", commands)), 0o644); err != nil {
			t.Fatal(err)
		}
		err := prepareCodexSubcommandFixture("0.145.0", fixturePath, filepath.Join(root, "output"), server.URL, server.Client())
		if err == nil || !strings.Contains(err.Error(), `duplicate token "exec"`) {
			t.Fatalf("duplicate fixture error = %v", err)
		}
	})
}

func TestPrepareCodexSubcommandFixtureRejectsMalformedContentMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(githubContentsFile{Type: "symlink", Encoding: "base64", Content: "eA==", SHA: strings.Repeat("a", 40)})
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	fixturePath := filepath.Join(root, "fixture")
	if err := os.WriteFile(fixturePath, []byte(codexFixtureText("0.144.1", []string{"exec", "help"})), 0o644); err != nil {
		t.Fatal(err)
	}
	err := prepareCodexSubcommandFixture("0.145.0", fixturePath, filepath.Join(root, "output"), server.URL, server.Client())
	if err == nil || !strings.Contains(err.Error(), "malformed GitHub content metadata") {
		t.Fatalf("malformed metadata error = %v", err)
	}
}

func TestPrepareCodexSubcommandFixtureRejectsGitBlobSHADisagreement(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(githubContentsFile{
			Type:     "file",
			Encoding: "base64",
			Content:  base64.StdEncoding.EncodeToString([]byte(codexSubcommandSourceFixture)),
			SHA:      strings.Repeat("a", 40),
		})
	}))
	t.Cleanup(server.Close)
	root := t.TempDir()
	fixturePath := filepath.Join(root, "fixture")
	if err := os.WriteFile(fixturePath, []byte(codexFixtureText("0.144.1", []string{"exec", "help"})), 0o644); err != nil {
		t.Fatal(err)
	}
	err := prepareCodexSubcommandFixture("0.145.0", fixturePath, filepath.Join(root, "output"), server.URL, server.Client())
	if err == nil || !strings.Contains(err.Error(), "disagrees with its Git blob SHA") {
		t.Fatalf("Git blob SHA disagreement error = %v", err)
	}
}

func codexSourceServer(t *testing.T, source string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		response := githubContentsFile{
			Type:     "file",
			Encoding: "base64",
			Content:  base64.StdEncoding.EncodeToString([]byte(source)),
			SHA:      codexGitBlobObjectID([]byte(source)),
		}
		if err := json.NewEncoder(w).Encode(response); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func codexFixtureText(version string, commands []string) string {
	return "# Complete fixture.\n#\n# codex-version: " + version + "\n#\n# derived from the AUTHORITATIVE source (openai/codex tag rust-v" + version + ", codex-rs/cli/src/main.rs).\n# One name per line.\n" + strings.Join(commands, "\n") + "\n"
}

func codexTestEnum(body string) string {
	return "#[derive(Debug, clap::Subcommand)]\nenum Subcommand {\n" + body + "}\n"
}
