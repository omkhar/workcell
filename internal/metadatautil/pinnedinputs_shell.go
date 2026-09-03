// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

const (
	canonicalHostedAPIInvocation = `gh api --hostname github.com -H "X-GitHub-Api-Version: ${GITHUB_API_VERSION}" "$@"`
	canonicalHostedShellSHA256   = "6c4a55e4bc53a854fa4100f2683d787a949898c7f83d79a5063da4cd989a006f"
	canonicalHostedToolFunction  = `require_tool() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "Missing required tool: $1" >&2
    exit 1
  }
}`
	canonicalHostedAPIFunction = `github_api() {
  gh api --hostname github.com -H "X-GitHub-Api-Version: ${GITHUB_API_VERSION}" "$@"
}`
	canonicalHostedCleanupFunction = `cleanup() {
  rm -rf "${TMP_DIR}"
  if [[ -n "${CITOOLS_BIN}" && -e "${CITOOLS_BIN}" ]]; then
    rm -f "${CITOOLS_BIN}"
  fi
}`
)

var (
	errHostedShellRouting   = errors.New("scripts/verify-github-hosted-controls.sh must use the exact reviewed command graph and versioned github_api wrapper")
	allowedHostedShellCalls = map[string]int{
		`set -euo pipefail`: 1,
		`ROOT_DIR="$(CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/.." && pwd -P)"`: 1,
		`CDPATH='' cd -- "${BASH_SOURCE[0]%/*}/.."`:                         1,
		`pwd -P`: 1,
		`source "${ROOT_DIR}/scripts/lib/canonical-build-env.sh"`:    1,
		`workcell_require_modern_privileged_bash "$@"`:               1,
		`workcell_require_canonical_build_environment`:               1,
		`echo "Hosted controls reject ambient GH_HOST and GH_REPO."`: 1,
		`exit 2`: 1,
		`POLICY_PATH="${WORKCELL_GITHUB_HOSTED_CONTROLS_POLICY_PATH:-${ROOT_DIR}/policy/github-hosted-controls.toml}"`: 1,
		`source "${ROOT_DIR}/scripts/lib/go-run-env.sh"`:                                                               1,
		`require_tool gh`: 1,
		`require_tool jq`: 1,
		`REPO="${1:-}"`:   1,
		`REPO="$(gh repo view --json nameWithOwner --jq .nameWithOwner)"`:      1,
		`gh repo view --json nameWithOwner --jq .nameWithOwner`:                1,
		`TMP_DIR="$(mktemp -d "${TMPDIR:-/tmp}/workcell-gh-controls.XXXXXX")"`: 1,
		`mktemp -d "${TMPDIR:-/tmp}/workcell-gh-controls.XXXXXX"`:              1,
		`CITOOLS_BIN=""`:    1,
		`trap cleanup EXIT`: 1,
		`CITOOLS_BIN="$(mktemp "${TMPDIR:-/tmp}/workcell-citools.XXXXXX")"`:           1,
		`mktemp "${TMPDIR:-/tmp}/workcell-citools.XXXXXX"`:                            1,
		`build_go_tool_in_repo "${ROOT_DIR}" "${CITOOLS_BIN}" ./cmd/workcell-citools`: 1,
		`github_api "repos/${REPO}"`:                                                  1,
		`github_api "repos/${REPO}/actions/permissions"`:                              1,
		`github_api "repos/${REPO}/actions/permissions/selected-actions"`:             1,
		`:`: 3,
		`status="$(jq -r '.status // empty' "${TMP_DIR}/actions-selected-actions.json" 2>/dev/null || true)"`: 1,
		`jq -r '.status // empty' "${TMP_DIR}/actions-selected-actions.json"`:                                 1,
		`true`: 1,
		`cat "${TMP_DIR}/actions-selected-actions.err"`:  1,
		`cat "${TMP_DIR}/actions-selected-actions.json"`: 1,
		`exit 1`: 3,
		`github_api "repos/${REPO}/actions/permissions/workflow"`:                                                                1,
		`github_api "repos/${REPO}/immutable-releases"`:                                                                          1,
		`github_api --paginate "repos/${REPO}/actions/variables?per_page=100"`:                                                   1,
		`"${CITOOLS_BIN}" merge-hosted-control-object-pages variables`:                                                           2,
		`github_api "repos/${REPO}/collaborators?affiliation=direct&per_page=100"`:                                               1,
		`github_api --paginate "repos/${REPO}/rulesets?per_page=100"`:                                                            1,
		`"${CITOOLS_BIN}" merge-hosted-control-array-pages`:                                                                      1,
		`"${CITOOLS_BIN}" fetch-rulesets "${TMP_DIR}" "${REPO}"`:                                                                 1,
		`github_api --paginate "repos/${REPO}/environments?per_page=100"`:                                                        1,
		`"${CITOOLS_BIN}" merge-hosted-control-object-pages environments`:                                                        1,
		`github_api "repos/${REPO}/environments/release"`:                                                                        1,
		`echo "Missing required release environment on ${REPO}"`:                                                                 1,
		`IFS= read -r environment_name`:                                                                                          1,
		`continue`:                                                                                                               1,
		`encoded_environment_name="$(jq -rn --arg value "${environment_name}" '$value | @uri')"`:                                 1,
		`jq -rn --arg value "${environment_name}" '$value | @uri'`:                                                               1,
		`safe_environment_name="${encoded_environment_name}"`:                                                                    1,
		`github_api "repos/${REPO}/environments/${encoded_environment_name}"`:                                                    1,
		`echo "Missing required ${environment_name} environment on ${REPO}"`:                                                     1,
		`github_api --paginate "repos/${REPO}/environments/${encoded_environment_name}/deployment-branch-policies?per_page=100"`: 1,
		`"${CITOOLS_BIN}" merge-hosted-control-object-pages branch_policies`:                                                     1,
		`github_api --paginate "repos/${REPO}/environments/${encoded_environment_name}/variables?per_page=100"`:                  1,
		`github_api --paginate "repos/${REPO}/environments/${encoded_environment_name}/secrets?per_page=100"`:                    1,
		`"${CITOOLS_BIN}" merge-hosted-control-object-pages secrets`:                                                             1,
		`"${CITOOLS_BIN}" list-hosted-control-environments "${POLICY_PATH}"`:                                                     1,
		`"${CITOOLS_BIN}" verify-github-hosted-controls "${TMP_DIR}" "${REPO}" "${POLICY_PATH}"`:                                 1,
	}
)

type hostedShellRouteAudit struct {
	script           string
	toolFunctions    int
	apiFunctions     int
	cleanupFunctions int
	callCounts       map[string]int
	err              error
}

func validateCanonicalHostedControlsScript(script string) error {
	if !strings.HasPrefix(script, "#!/bin/bash -p\n") {
		return errors.New("scripts/verify-github-hosted-controls.sh must use the exact privileged Bash shebang")
	}
	for _, needle := range []string{
		`readonly GITHUB_API_VERSION="2026-03-10"`,
		"  " + canonicalHostedAPIInvocation,
		"github_api --paginate \"repos/${REPO}/actions/variables?per_page=100\"",
		"repos/${REPO}/actions/permissions/selected-actions",
		"repos/${REPO}/actions/permissions/workflow",
		"repos/${REPO}/immutable-releases",
		`"${CITOOLS_BIN}" merge-hosted-control-object-pages variables >"${TMP_DIR}/actions-variables.json"`,
		"github_api --paginate \"repos/${REPO}/rulesets?per_page=100\"",
		`"${CITOOLS_BIN}" merge-hosted-control-array-pages >"${TMP_DIR}/rulesets-summary.json"`,
		"github_api --paginate \"repos/${REPO}/environments?per_page=100\"",
		`"${CITOOLS_BIN}" merge-hosted-control-object-pages environments >"${TMP_DIR}/environments.json"`,
		`list-hosted-control-environments "${POLICY_PATH}"`,
		"safe_environment_name=\"${encoded_environment_name}\"",
		"environment-${safe_environment_name}.json",
		"repos/${REPO}/environments/${encoded_environment_name}/variables?per_page=100",
		"repos/${REPO}/environments/${encoded_environment_name}/secrets?per_page=100",
		`"${CITOOLS_BIN}" merge-hosted-control-object-pages branch_policies >"${TMP_DIR}/environment-${safe_environment_name}-deployment-branch-policies.json"`,
		`"${CITOOLS_BIN}" merge-hosted-control-object-pages variables >"${TMP_DIR}/environment-${safe_environment_name}-variables.json"`,
		`"${CITOOLS_BIN}" merge-hosted-control-object-pages secrets >"${TMP_DIR}/environment-${safe_environment_name}-secrets.json"`,
		`verify-github-hosted-controls "${TMP_DIR}" "${REPO}" "${POLICY_PATH}"`,
	} {
		if !strings.Contains(script, needle) {
			return fmt.Errorf("scripts/verify-github-hosted-controls.sh must contain %q", needle)
		}
	}
	return validateHostedControlsAPIRouting(script)
}

func validateHostedControlsAPIRouting(script string) error {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(script), "scripts/verify-github-hosted-controls.sh")
	if err != nil {
		return fmt.Errorf("parse scripts/verify-github-hosted-controls.sh: %w", err)
	}
	audit := hostedShellRouteAudit{script: script, callCounts: make(map[string]int)}
	syntax.Walk(file, audit.visit)
	if err := audit.result(); err != nil {
		return err
	}
	return requireCanonicalHostedShell(file)
}

func requireCanonicalHostedShell(file *syntax.File) error {
	var canonical bytes.Buffer
	if err := syntax.NewPrinter().Print(&canonical, file); err != nil {
		return fmt.Errorf("print scripts/verify-github-hosted-controls.sh: %w", err)
	}
	digest := sha256.Sum256(canonical.Bytes())
	if fmt.Sprintf("%x", digest) != canonicalHostedShellSHA256 {
		return fmt.Errorf("%w: unexpected shell structure", errHostedShellRouting)
	}
	return nil
}

func (audit *hostedShellRouteAudit) visit(node syntax.Node) bool {
	if audit.err != nil {
		return false
	}
	switch node := node.(type) {
	case *syntax.CallExpr:
		audit.err = audit.validateCall(node)
	case *syntax.FuncDecl:
		audit.err = audit.validateFunction(node)
		return false
	}
	return audit.err == nil
}

func (audit *hostedShellRouteAudit) validateCall(call *syntax.CallExpr) error {
	text := audit.nodeText(call)
	if _, ok := allowedHostedShellCalls[text]; !ok {
		return fmt.Errorf("%w: unexpected command %q", errHostedShellRouting, text)
	}
	audit.callCounts[text]++
	return nil
}

func (audit *hostedShellRouteAudit) validateFunction(function *syntax.FuncDecl) error {
	switch function.Name.Value {
	case "require_tool":
		audit.toolFunctions++
		return requireHostedShellFunction(audit.nodeText(function), canonicalHostedToolFunction)
	case "github_api":
		audit.apiFunctions++
		return requireHostedShellFunction(audit.nodeText(function), canonicalHostedAPIFunction)
	case "cleanup":
		audit.cleanupFunctions++
		return requireHostedShellFunction(audit.nodeText(function), canonicalHostedCleanupFunction)
	default:
		return errHostedShellRouting
	}
}

func requireHostedShellFunction(actual, expected string) error {
	if actual != expected {
		return errHostedShellRouting
	}
	return nil
}

func (audit hostedShellRouteAudit) result() error {
	if audit.err != nil {
		return audit.err
	}
	if !audit.hasCanonicalCounts() {
		return errHostedShellRouting
	}
	return audit.requireCanonicalCallCounts()
}

func (audit hostedShellRouteAudit) hasCanonicalCounts() bool {
	return allHostedShellCountsOne(
		audit.toolFunctions,
		audit.apiFunctions,
		audit.cleanupFunctions,
	)
}

func (audit hostedShellRouteAudit) requireCanonicalCallCounts() error {
	for call, expected := range allowedHostedShellCalls {
		actual := audit.callCounts[call]
		if actual != expected {
			return fmt.Errorf("%w: command %q appears %d times; expected %d", errHostedShellRouting, call, actual, expected)
		}
	}
	return nil
}

func allHostedShellCountsOne(counts ...int) bool {
	for _, count := range counts {
		if count != 1 {
			return false
		}
	}
	return true
}

func (audit *hostedShellRouteAudit) nodeText(node syntax.Node) string {
	return audit.script[int(node.Pos().Offset()):int(node.End().Offset())]
}
