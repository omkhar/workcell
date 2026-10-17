// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authresolve

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/omkhar/workcell/internal/credbroker"
	"github.com/omkhar/workcell/internal/rootio"
	"github.com/omkhar/workcell/internal/secretfile"
)

const testClaudeExportEnv = "WORKCELL_TEST_CLAUDE_KEYCHAIN_EXPORT_FILE"

// BrokerResolver names the resolver for an API key that the host credential
// broker serves from the macOS Keychain under the egress proxy.
const BrokerResolver = "keychain-broker"

func resolveCredential(key, resolverName string, outputRoot *os.Root, relativeDestination, resolutionMode, placeholder string) (string, error) {
	if resolverName == BrokerResolver {
		return resolveBrokerPlaceholder(key, outputRoot, relativeDestination, resolutionMode, placeholder)
	}
	if key == "codex_auth" && resolverName == "codex-home-auth-file" {
		return resolveCodexHomeAuthFile(outputRoot, relativeDestination, resolutionMode)
	}
	if key == "claude_auth" && resolverName == "claude-macos-keychain" {
		return resolveClaudeMacosKeychain(outputRoot, relativeDestination, resolutionMode)
	}
	return "", fmt.Errorf("unsupported credential resolver: %s", resolverName)
}

// ResolverSupported reports whether a credential key supports the named built-in resolver.
func ResolverSupported(key, resolverName string) bool {
	if resolverName == BrokerResolver {
		return credbroker.Brokers(key)
	}
	resolverSet, ok := allowedResolvers[key]
	if !ok {
		return false
	}
	_, ok = resolverSet[resolverName]
	return ok
}

// ProbeResolverReadiness reports whether a configured resolver is launch-ready on the host.
func ProbeResolverReadiness(key, resolverName string) (string, error) {
	switch {
	case key == "codex_auth" && resolverName == "codex-home-auth-file":
		return probeCodexHomeAuthFile()
	case key == "claude_auth" && resolverName == "claude-macos-keychain":
		return "configured-only", nil
	case resolverName == BrokerResolver && ResolverSupported(key, resolverName):
		// The broker reads the Keychain at launch; probing here could raise
		// a Keychain prompt during a status command.
		return "host-source", nil
	default:
		return "", fmt.Errorf("unsupported credential resolver: %s", resolverName)
	}
}

func resolveCodexHomeAuthFile(outputRoot *os.Root, relativeDestination, resolutionMode string) (string, error) {
	source, found, err := findCodexHomeAuthFile()
	if err != nil {
		return "", err
	}
	if found {
		if resolutionMode == "metadata" {
			if err := writePlaceholderUnderRoot(outputRoot, relativeDestination, "codex-home-auth-file"); err != nil {
				return "", err
			}
			return "host-source", nil
		}
		if err := materializeFileUnderRoot(source, "credentials.codex_auth", outputRoot, relativeDestination); err != nil {
			return "", err
		}
		return "resolved", nil
	}
	if resolutionMode == "metadata" {
		if err := writePlaceholderUnderRoot(outputRoot, relativeDestination, "codex-home-auth-file"); err != nil {
			return "", err
		}
		return "configured-only", nil
	}
	return "", fmt.Errorf(
		"codex host auth reuse is configured but no supported auth file is available at %s; stage codex_auth directly or remove credentials.codex_auth",
		codexHomeAuthFilePath(),
	)
}

func probeCodexHomeAuthFile() (string, error) {
	_, found, err := findCodexHomeAuthFile()
	if err != nil {
		return "", err
	}
	if found {
		return "host-source", nil
	}
	return "configured-only", nil
}

func findCodexHomeAuthFile() (string, bool, error) {
	source := codexHomeAuthFilePath()
	info, err := os.Stat(source)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.IsDir() {
		return "", false, fmt.Errorf("credentials.codex_auth resolver path must be a file: %s", source)
	}
	if err := requireNoSymlinkInPathChain(source, "credentials.codex_auth"); err != nil {
		return "", false, err
	}
	validated, err := requireSecretFile(source, "credentials.codex_auth")
	if err != nil {
		return "", false, err
	}
	return validated, true, nil
}

func codexHomeAuthFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".codex", "auth.json")
	}
	return filepath.Join(home, ".codex", "auth.json")
}

func resolveClaudeMacosKeychain(outputRoot *os.Root, relativeDestination, resolutionMode string) (string, error) {
	if exportPath := os.Getenv(testClaudeExportEnv); exportPath != "" {
		source, err := validateSourcePath(exportPath, testClaudeExportEnv, cwd())
		if err != nil {
			return "", err
		}
		source, err = requireSecretFile(source, testClaudeExportEnv)
		if err != nil {
			return "", err
		}
		if err := materializeFileUnderRoot(source, testClaudeExportEnv, outputRoot, relativeDestination); err != nil {
			return "", err
		}
		return "resolved", nil
	}
	if resolutionMode == "metadata" {
		if err := writePlaceholderUnderRoot(outputRoot, relativeDestination, "claude-macos-keychain"); err != nil {
			return "", err
		}
		return "configured-only", nil
	}
	return "", errors.New("claude macOS login reuse is configured but no supported export path is available; use claude_api_key or remove credentials.claude_auth")
}

// resolveBrokerPlaceholder stages the placeholder that the agent gets in
// place of a brokered API key, at the key's usual credential path. The real
// key never reaches the bundle: the broker reads it from the Keychain and
// the egress proxy swaps the placeholder for it. Without a placeholder the
// session brokers nothing, so a launch fails closed; a metadata run stages
// an all-zero placeholder.
func resolveBrokerPlaceholder(key string, outputRoot *os.Root, relativeDestination, resolutionMode, placeholder string) (string, error) {
	if placeholder == "" {
		if resolutionMode != "metadata" {
			return "", fmt.Errorf("credentials.%s resolver %s needs the egress proxy of a strict Colima session without --egress-proxy=off: the credential broker serves the key and the agent gets only a placeholder", key, BrokerResolver)
		}
		placeholder = "wcph-" + strings.Repeat("0", 32)
	}
	content := placeholder + "\n"
	switch key {
	case "gemini_env":
		content = "GEMINI_API_KEY=" + content
	case "codex_auth":
		// The Codex auth.json of API-key mode.
		content = `{"OPENAI_API_KEY":"` + placeholder + `"}` + "\n"
	}
	if err := rootio.WriteFileAtomic(outputRoot, relativeDestination, []byte(content), 0o600, ".workcell-write-"); err != nil {
		return "", err
	}
	return "resolved", nil
}

func materializeFileUnderRoot(source, label string, outputRoot *os.Root, relativeDestination string) error {
	sourceHandle, err := secretfile.Open(source, label, os.Getuid())
	if err != nil {
		return err
	}
	defer sourceHandle.Close()
	return rootio.WriteFileAtomicFromReader(outputRoot, relativeDestination, sourceHandle, 0o600, ".workcell-resolve-")
}

func writePlaceholderUnderRoot(outputRoot *os.Root, relativeDestination, resolver string) error {
	content := fmt.Sprintf(`{"resolver": %q, "workcell": %q}`+"\n", resolver, "metadata-only")
	return rootio.WriteFileAtomic(outputRoot, relativeDestination, []byte(content), 0o600, ".workcell-write-")
}

func writeAtomicText(outputRoot *os.Root, destination, content string) error {
	return rootio.WriteFileAtomic(outputRoot, destination, []byte(content), 0o600, ".workcell-write-")
}
