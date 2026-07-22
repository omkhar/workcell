// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/omkhar/workcell/internal/tomlsubset"
)

func validateNodeMarkdownlintPinnedInputs(
	cfg PinnedInputsConfig,
	validatorDockerfile string,
	installDevToolsScript string,
	markdownlintPackageJSON markdownlintPackageJSON,
	markdownlintPackageJSONPath string,
	markdownlintPackageLock markdownlintPackageLock,
	markdownlintPackageLockPath string,
	installDevToolsScriptPath string,
) error {
	validatorMarkdownlintVersion, err := requireArg(validatorDockerfile, "MARKDOWNLINT_VERSION", cfg.ValidatorDockerfilePath)
	if err != nil {
		return err
	}
	if !regexp.MustCompile(`^0\.\d+\.\d+$`).MatchString(validatorMarkdownlintVersion) {
		return fmt.Errorf("MARKDOWNLINT_VERSION must be an exact pinned release, found %q", validatorMarkdownlintVersion)
	}
	markdownlintDependency, ok := markdownlintPackageJSON.Dependencies["markdownlint-cli"]
	if !ok {
		return fmt.Errorf("%s must depend on markdownlint-cli", markdownlintPackageJSONPath)
	}
	if markdownlintDependency != validatorMarkdownlintVersion {
		return fmt.Errorf("markdownlint-cli version must match between %s and %s; found %q and %q", markdownlintPackageJSONPath, cfg.ValidatorDockerfilePath, markdownlintDependency, validatorMarkdownlintVersion)
	}
	markdownlintLockRoot, ok := markdownlintPackageLock.Packages[""]
	if !ok {
		return fmt.Errorf("%s must include the root package lock entry", markdownlintPackageLockPath)
	}
	if markdownlintLockRoot.Dependencies["markdownlint-cli"] != validatorMarkdownlintVersion {
		return fmt.Errorf("markdownlint-cli version must match between %s and %s; found %q and %q", markdownlintPackageLockPath, cfg.ValidatorDockerfilePath, markdownlintLockRoot.Dependencies["markdownlint-cli"], validatorMarkdownlintVersion)
	}
	markdownlintLockPackage, ok := markdownlintPackageLock.Packages["node_modules/markdownlint-cli"]
	if !ok {
		return fmt.Errorf("%s must lock node_modules/markdownlint-cli", markdownlintPackageLockPath)
	}
	if markdownlintLockPackage.Version != validatorMarkdownlintVersion {
		return fmt.Errorf("locked markdownlint-cli package version must match %s; found %q and %q", cfg.ValidatorDockerfilePath, markdownlintLockPackage.Version, validatorMarkdownlintVersion)
	}
	_, installMarkdownlintVersionMatch, err := requireRegex(installDevToolsScript, `(?m)^readonly MARKDOWNLINT_VERSION="([0-9]+\.[0-9]+\.[0-9]+)"$`, "install-dev-tools markdownlint version", installDevToolsScriptPath)
	if err != nil {
		return err
	}
	if installMarkdownlintVersionMatch[1] != validatorMarkdownlintVersion {
		return fmt.Errorf("MARKDOWNLINT_VERSION must match between %s and %s; found %q and %q", installDevToolsScriptPath, cfg.ValidatorDockerfilePath, installMarkdownlintVersionMatch[1], validatorMarkdownlintVersion)
	}
	iniLockPackage, ok := markdownlintPackageLock.Packages["node_modules/ini"]
	if !ok || iniLockPackage.Engines["node"] == "" {
		return fmt.Errorf("%s must lock the markdownlint runtime Node.js requirement", markdownlintPackageLockPath)
	}
	markdownlintNodeMinimums := make([]string, 0, 3)
	for _, name := range []string{"MARKDOWNLINT_NODE_22_MINIMUM", "MARKDOWNLINT_NODE_24_MINIMUM", "MARKDOWNLINT_NODE_OPEN_MINIMUM"} {
		_, match, matchErr := requireRegex(installDevToolsScript, `(?m)^readonly `+name+`="([0-9]+\.[0-9]+\.[0-9]+)"$`, "install-dev-tools "+name, installDevToolsScriptPath)
		if matchErr != nil {
			return matchErr
		}
		markdownlintNodeMinimums = append(markdownlintNodeMinimums, match[1])
	}
	lockedNodeRequirement := fmt.Sprintf("^%s || ^%s || >=%s", markdownlintNodeMinimums[0], markdownlintNodeMinimums[1], markdownlintNodeMinimums[2])
	if lockedNodeRequirement != iniLockPackage.Engines["node"] {
		return fmt.Errorf("markdownlint Node.js minimums in %s produce %q, which must match the locked runtime requirement %q for markdownlint-cli@%s", installDevToolsScriptPath, lockedNodeRequirement, iniLockPackage.Engines["node"], validatorMarkdownlintVersion)
	}
	if !strings.Contains(installDevToolsScript, `readonly MARKDOWNLINT_NODE_VERSION_REQUIREMENT="^${MARKDOWNLINT_NODE_22_MINIMUM} || ^${MARKDOWNLINT_NODE_24_MINIMUM} || >=${MARKDOWNLINT_NODE_OPEN_MINIMUM}"`) {
		return fmt.Errorf("MARKDOWNLINT_NODE_VERSION_REQUIREMENT in %s must be composed from the enforced Node.js minimums", installDevToolsScriptPath)
	}
	if err := rejectInstallScriptAptPackages(installDevToolsScript, installDevToolsScriptPath, "nodejs", "npm"); err != nil {
		return err
	}
	markdownlintNodeCompatibleBody, err := requireDelimitedText(
		installDevToolsScript,
		"markdownlint_node_compatible() {\n",
		"\n}\n\nmarkdownlint_node_install_hint()",
		"markdownlint Node.js range check",
		installDevToolsScriptPath,
	)
	if err != nil {
		return err
	}
	for _, needle := range []string{
		`version_at_least "${version}" "${MARKDOWNLINT_NODE_22_MINIMUM}"`,
		`version_at_least "${version}" "${MARKDOWNLINT_NODE_24_MINIMUM}"`,
		`version_at_least "${version}" "${MARKDOWNLINT_NODE_OPEN_MINIMUM}"`,
	} {
		if !strings.Contains(markdownlintNodeCompatibleBody, needle) {
			return fmt.Errorf("%s must enforce the displayed Node.js range using %q", installDevToolsScriptPath, needle)
		}
	}
	if !strings.Contains(installDevToolsScript, "if [[ \"${host_os}\" == \"Linux\" ]] && markdownlint_needs_install; then\n  require_markdownlint_node\n  require_markdownlint_npm\nfi\n\nif [[ ${#missing[@]} -gt 0 ]]; then") {
		return fmt.Errorf("%s must validate Linux Node.js/npm compatibility before apt installs when markdownlint-cli needs installation", installDevToolsScriptPath)
	}
	markdownlintInstallBody, err := requireDelimitedText(
		installDevToolsScript,
		"if markdownlint_needs_install; then\n",
		"\nfi\n\necho \"Done.\"",
		"markdownlint install block",
		installDevToolsScriptPath,
	)
	if err != nil {
		return err
	}
	if err := requireOrderedText(markdownlintInstallBody, "markdownlint install block", installDevToolsScriptPath, []string{
		"require_markdownlint_node",
		"require_markdownlint_npm",
		"npm install -g \"markdownlint-cli@${MARKDOWNLINT_VERSION}\"",
	}); err != nil {
		return fmt.Errorf("%s must validate the Node.js floor and npm immediately before installing markdownlint-cli: %w", installDevToolsScriptPath, err)
	}
	markdownlintNodeHintBody, err := requireDelimitedText(
		installDevToolsScript,
		"markdownlint_node_install_hint() {\n",
		"\n}\n\nrequire_markdownlint_node()",
		"markdownlint Node.js upgrade hint",
		installDevToolsScriptPath,
	)
	if err != nil {
		return err
	}
	for _, needle := range []string{
		"Install a Node.js version matching ${MARKDOWNLINT_NODE_VERSION_REQUIREMENT} before installing markdownlint-cli@${MARKDOWNLINT_VERSION}.",
		"Homebrew's node package",
		"NodeSource",
		"nvm",
		"asdf",
		"Ubuntu 24.04's nodejs/npm apt packages are too old for this markdownlint release.",
		"Then rerun scripts/install-dev-tools.sh.",
	} {
		if !strings.Contains(markdownlintNodeHintBody, needle) {
			return fmt.Errorf("%s must print manual Node.js upgrade instructions before failing markdownlint-cli installation", installDevToolsScriptPath)
		}
	}
	requireMarkdownlintNodeBody, err := requireDelimitedText(
		installDevToolsScript,
		"require_markdownlint_node() {\n",
		"\n}\n\nrequire_markdownlint_npm()",
		"markdownlint Node.js floor check",
		installDevToolsScriptPath,
	)
	if err != nil {
		return err
	}
	for _, needle := range []string{
		"no usable node binary was found.\" >&2\n    markdownlint_node_install_hint\n    exit 1",
		"found ${version}.\" >&2\n    markdownlint_node_install_hint\n    exit 1",
	} {
		if !strings.Contains(requireMarkdownlintNodeBody, needle) {
			return fmt.Errorf("%s must print Node.js upgrade instructions before exiting every markdownlint-cli Node.js floor failure path", installDevToolsScriptPath)
		}
	}
	requireMarkdownlintNPMBody, err := requireDelimitedText(
		installDevToolsScript,
		"require_markdownlint_npm() {\n",
		"\n}\n\nmarkdownlint_needs_install()",
		"markdownlint npm check",
		installDevToolsScriptPath,
	)
	if err != nil {
		return err
	}
	for _, needle := range []string{
		"command -v npm &>/dev/null",
		"requires npm from a Node.js ${MARKDOWNLINT_NODE_VERSION_REQUIREMENT} installation.\" >&2\n  markdownlint_node_install_hint\n  exit 1",
	} {
		if !strings.Contains(requireMarkdownlintNPMBody, needle) {
			return fmt.Errorf("%s must print Node.js upgrade instructions before exiting the markdownlint-cli npm failure path", installDevToolsScriptPath)
		}
	}
	for _, needle := range []string{
		`GOBIN=/usr/local/bin go install "golang.org/x/tools/cmd/deadcode@${DEADCODE_VERSION}"`,
		`COPY tools/markdownlint/package.json tools/markdownlint/package-lock.json /usr/local/lib/workcell-markdownlint/`,
		`deadcode -h >/dev/null`,
		`npm ci --prefix /usr/local/lib/workcell-markdownlint --ignore-scripts --omit=dev`,
		`ln -sf /usr/local/lib/workcell-markdownlint/node_modules/.bin/markdownlint /usr/local/bin/markdownlint`,
		`markdownlint --version | grep -F "${MARKDOWNLINT_VERSION}" >/dev/null`,
	} {
		if !strings.Contains(validatorDockerfile, needle) {
			return fmt.Errorf("%s must contain %q", cfg.ValidatorDockerfilePath, needle)
		}
	}
	return nil
}

func validateNodeProviderLock(providersPackageJSON, providersPackageLock map[string]any) error {
	rootPackage, _ := providersPackageLock["packages"].(map[string]any)
	rootDependencies, _ := rootPackage[""].(map[string]any)
	expectedDependencies, _ := providersPackageJSON["dependencies"].(map[string]any)
	actualDependencies, _ := rootDependencies["dependencies"].(map[string]any)
	if len(actualDependencies) != len(expectedDependencies) {
		return errors.New("runtime/container/providers/package-lock.json root dependencies do not match package.json")
	}
	for name, expected := range expectedDependencies {
		if actualDependencies[name] != expected {
			return errors.New("runtime/container/providers/package-lock.json root dependencies do not match package.json")
		}
	}
	for packageName, expectedVersionAny := range expectedDependencies {
		expectedVersion, _ := expectedVersionAny.(string)
		pkgEntry, ok := rootPackage["node_modules/"+packageName].(map[string]any)
		if !ok {
			return fmt.Errorf("missing pinned provider package entry for %s", packageName)
		}
		if version, _ := pkgEntry["version"].(string); version != expectedVersion {
			return fmt.Errorf("pinned provider package %s is %s, expected %s", packageName, version, expectedVersion)
		}
		if integrity, _ := pkgEntry["integrity"].(string); integrity == "" {
			return fmt.Errorf("pinned provider package %s is missing an integrity hash", packageName)
		}
		if resolved, _ := pkgEntry["resolved"].(string); !strings.HasPrefix(resolved, "https://registry.npmjs.org/") {
			return fmt.Errorf("pinned provider package %s uses an unexpected source: %q", packageName, resolved)
		}
	}
	for packagePath, rawEntry := range rootPackage {
		if packagePath == "" {
			continue
		}
		entry, _ := rawEntry.(map[string]any)
		if link, _ := entry["link"].(bool); link {
			return fmt.Errorf("linked npm dependencies are not allowed in the provider lockfile: %s", packagePath)
		}
		if integrity, _ := entry["integrity"].(string); integrity == "" {
			return fmt.Errorf("provider lockfile entry is missing integrity data: %s", packagePath)
		}
		if resolved, _ := entry["resolved"].(string); !strings.HasPrefix(resolved, "https://registry.npmjs.org/") {
			return fmt.Errorf("provider lockfile entry uses an unexpected source (%s): %q", packagePath, resolved)
		}
	}
	return nil
}

func rejectInstallScriptAptPackages(text, path string, disallowedPackages ...string) error {
	scanText := strings.ReplaceAll(text, "\\\n", " ")
	disallowed := map[string]struct{}{}
	for _, pkg := range disallowedPackages {
		disallowed[pkg] = struct{}{}
	}
	tokenREs := []struct {
		label string
		re    *regexp.Regexp
	}{
		{
			label: "append_unique_apt",
			re:    regexp.MustCompile(`(?m)^\s*append_unique_apt\s+([^\n#]+)`),
		},
		{
			label: "apt_missing",
			re:    regexp.MustCompile(`(?m)^\s*apt_missing\+\=\(([^)]*)\)`),
		},
		{
			label: "apt-get install",
			re:    regexp.MustCompile(`(?m)(?:^|&&)\s*(?:sudo\s+)?apt-get\s+install(?:\s+-[A-Za-z0-9-]+)*\s+([^\n#]+)`),
		},
	}
	for _, tokenRE := range tokenREs {
		for _, match := range tokenRE.re.FindAllStringSubmatch(scanText, -1) {
			for _, field := range strings.Fields(match[1]) {
				token := strings.Trim(field, `"'`)
				for _, separator := range []string{"=", ":", "/"} {
					if index := strings.Index(token, separator); index >= 0 {
						token = token[:index]
					}
				}
				if _, ok := disallowed[token]; ok {
					return fmt.Errorf("%s must not add %s to the Linux apt package set through %s", path, token, tokenRE.label)
				}
			}
		}
	}
	return nil
}

func requireNoRegistryBootstrapMCP(text, path string) error {
	disallowedFragments := []string{
		"npx",
		"npm exec",
		"pnpm dlx",
		"yarn dlx",
		"bunx",
		"@upstash/context7-mcp",
		"exa-mcp-server",
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = tomlsubset.StripComment(line)
		lower := strings.ToLower(line)
		for _, fragment := range disallowedFragments {
			if strings.Contains(lower, fragment) {
				return fmt.Errorf("%s must not seed mutable registry-backed MCP commands; found %q", path, line)
			}
		}
	}
	return nil
}
