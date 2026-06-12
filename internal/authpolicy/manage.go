// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/omkhar/workcell/internal/authresolve"
	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/providerid"
)

var (
	canonicalCredentialDestinations = map[string][2]string{
		"codex_auth":      {providerid.Codex, "auth.json"},
		"claude_api_key":  {providerid.Claude, "api-key.txt"},
		"claude_mcp":      {providerid.Claude, "mcp.json"},
		"gemini_env":      {providerid.Gemini, "gemini.env"},
		"gemini_oauth":    {providerid.Gemini, "oauth_creds.json"},
		"gemini_projects": {providerid.Gemini, "projects.json"},
		"gcloud_adc":      {providerid.Gemini, "gcloud-adc.json"},
		"github_hosts":    {"shared", "github-hosts.yml"},
		"github_config":   {"shared", "github-config.yml"},
	}
	statusOrder = map[string][]string{
		providerid.Codex:  {"codex_auth"},
		providerid.Claude: {"claude_api_key", "claude_auth"},
		providerid.Gemini: {"gemini_env", "gemini_oauth"},
	}
	entryAllowedKeys = map[string]struct{}{
		"source":          {},
		"resolver":        {},
		"materialization": {},
		"providers":       {},
		"modes":           {},
	}
)

// Run is the byte-for-byte translation of the bash
// workcell-manage-injection-policy entry point.  It writes diagnostics
// to stderr exactly as the original did and returns a
// *cliexit.ExitCodeError encoding the bash exit-code contract; the
// caller (cmd/workcell-hostutil/main.go) recovers Code via errors.As
// and propagates it to os.Exit.  Returning the typed error rather than
// an int keeps the package on the canonical error-returning idiom
// every other Go CLI translation in this repo uses.
func Run(program string, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage(program))
		return &cliexit.ExitCodeError{Code: 2}
	}
	switch args[0] {
	case "init":
		policyPath, managedRoot, err := parseInitArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandInit(policyPath, managedRoot); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		fmt.Fprintln(stdout, "policy_path="+resolveOutputPath(policyPath))
		fmt.Fprintln(stdout, "managed_root="+resolveOutputPath(managedRoot))
		return nil
	case "set":
		opts, err := parseSetArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandSet(opts); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		printSetOutput(stdout, opts)
		return nil
	case "unset":
		policyPath, managedRoot, credential, err := parseUnsetArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		removed, err := commandUnset(policyPath, managedRoot, credential)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		fmt.Fprintln(stdout, "policy_path="+policyPath)
		fmt.Fprintln(stdout, "credential="+credential)
		fmt.Fprintf(stdout, "removed=%d\n", removed)
		return nil
	case "status":
		opts, err := parseStatusArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandStatus(opts, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	case "show":
		policyPath, err := parsePolicyPathArgs(program, "show", args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandShow(policyPath, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	case "validate":
		policyPath, err := parsePolicyPathArgs(program, "validate", args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandValidate(policyPath, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	case "diff":
		policyPath, err := parsePolicyPathArgs(program, "diff", args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandDiff(policyPath, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	case "why":
		opts, err := parseWhyArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandWhy(opts, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	case "bootstrap-summary":
		opts, err := parseBootstrapSummaryArgs(program, args[1:], stderr)
		if err != nil {
			return &cliexit.ExitCodeError{Code: 2}
		}
		if err := commandBootstrapSummary(opts, stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return &cliexit.ExitCodeError{Code: 1}
		}
		return nil
	default:
		fmt.Fprintln(stderr, usage(program))
		fmt.Fprintf(stderr, "%s: unsupported command: %s\n", program, args[0])
		return &cliexit.ExitCodeError{Code: 2}
	}
}

type setOptions struct {
	policyPath      string
	managedRoot     string
	agent           string
	credential      string
	sourceRaw       string
	sourceBaseRaw   string
	resolver        string
	ackHostResolver bool
}

type statusOptions struct {
	policyPath string
	agent      string
	mode       string
}

type whyOptions struct {
	policyPath string
	agent      string
	mode       string
	credential string
}

type bootstrapSummaryOptions struct {
	agent               string
	inputKinds          string
	resolvers           string
	resolutionStates    string
	providerReadyStates string
}

type credentialSelectionReport struct {
	selected  bool
	reason    string
	readiness string
	inputKind string
	providers []string
	modes     []string
	resolver  string
}

type bootstrapSummary struct {
	state    string
	path     string
	support  string
	handoff  string
	doc      string
	nextStep string
}

func usage(program string) string {
	if program == "" {
		program = "workcell-manage-injection-policy"
	}
	return fmt.Sprintf(
		"Usage: %s {init,set,unset,status,show,validate,diff,why} ...",
		program,
	)
}

// newCmdFlagSet constructs a flag.FlagSet whose error output and usage
// callback are wired identically across every parse* helper below.
// Each helper used to repeat this four-line preamble verbatim.
func commandShow(policyPath string, stdout io.Writer) error {
	policy, _, err := loadPolicyBundle(policyPath)
	if err != nil {
		return err
	}
	rendered, err := renderPolicyTOML(policy)
	if err != nil {
		return err
	}
	_, err = io.WriteString(stdout, rendered)
	return err
}

func commandValidate(policyPath string, stdout io.Writer) error {
	policy, _, err := loadPolicyBundle(policyPath)
	if err != nil {
		return err
	}
	credentials, _ := policy["credentials"].(map[string]any)
	hasResolverBacked := false
	for key, raw := range credentials {
		if rawMap, ok := raw.(map[string]any); ok {
			if err := validateStatusCredentialEntry(key, rawMap); err != nil {
				return err
			}
			if resolver, ok := rawMap["resolver"].(string); ok && resolver != "" {
				hasResolverBacked = true
			}
		}
		if err := validateStatusCredentialSource(key, raw, filepath.Dir(policyPath)); err != nil {
			return err
		}
	}
	if _, err := selectedCredentials(policy, "", "strict"); err != nil {
		return err
	}
	if _, err := renderPolicyTOML(policy); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "policy_valid=1")
	if hasResolverBacked {
		fmt.Fprintln(stdout, "resolver_readiness=deferred-to-launch")
	} else {
		fmt.Fprintln(stdout, "resolver_readiness=not-applicable")
	}
	return nil
}

func commandDiff(policyPath string, stdout io.Writer) error {
	source, err := os.ReadFile(policyPath)
	if err != nil {
		return err
	}
	policy, _, err := loadPolicyBundle(policyPath)
	if err != nil {
		return err
	}
	rendered, err := renderPolicyTOML(policy)
	if err != nil {
		return err
	}
	sourceText := string(source)
	if sourceText == rendered {
		fmt.Fprintln(stdout, "diff_status=clean")
		return nil
	}
	fmt.Fprintln(stdout, "diff_status=changed")
	_, err = io.WriteString(stdout, renderTextDiff("current", "canonical", sourceText, rendered))
	return err
}

func commandWhy(opts whyOptions, stdout io.Writer) error {
	policy, _, err := loadPolicyBundle(opts.policyPath)
	if err != nil {
		return err
	}
	if _, err := selectedCredentials(policy, opts.agent, opts.mode); err != nil {
		return err
	}
	report, err := explainCredentialSelection(policy, filepath.Dir(opts.policyPath), opts.credential, opts.agent, opts.mode)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "policy_path="+opts.policyPath)
	fmt.Fprintln(stdout, "credential="+opts.credential)
	fmt.Fprintln(stdout, "agent="+opts.agent)
	fmt.Fprintln(stdout, "mode="+opts.mode)
	fmt.Fprintf(stdout, "selected=%d\n", boolToInt(report.selected))
	fmt.Fprintln(stdout, "selection_reason="+report.reason)
	fmt.Fprintln(stdout, "credential_readiness="+report.readiness)
	fmt.Fprintln(stdout, "credential_input_kind="+report.inputKind)
	fmt.Fprintln(stdout, "credential_providers="+renderModes(report.providers))
	fmt.Fprintln(stdout, "credential_modes="+renderModes(report.modes))
	if report.resolver != "" {
		fmt.Fprintln(stdout, "credential_resolver="+report.resolver)
	}
	printCredentialBootstrapSummary(stdout, bootstrapSummaryForCredential(opts.agent, opts.credential, report))
	return nil
}

func commandBootstrapSummary(opts bootstrapSummaryOptions, stdout io.Writer) error {
	inputKinds := parseRenderedMap(opts.inputKinds)
	selected := map[string]any{}
	for key := range inputKinds {
		selected[key] = true
	}
	summary := summarizeBootstrap(
		opts.agent,
		selected,
		inputKinds,
		parseRenderedMap(opts.resolvers),
		parseRenderedMap(opts.resolutionStates),
		parseRenderedMap(opts.providerReadyStates),
	)
	printBootstrapSummary(stdout, summary)
	return nil
}

func explainCredentialSelection(policy map[string]any, policyBase string, credential string, agent string, mode string) (credentialSelectionReport, error) {
	credentials, _ := policy["credentials"].(map[string]any)
	raw, ok := credentials[credential]
	if !ok {
		return credentialSelectionReport{
			selected:  false,
			reason:    "not configured in policy",
			readiness: "absent",
			inputKind: "none",
		}, nil
	}
	rawMap, ok := raw.(map[string]any)
	if !ok {
		if _, shared := SharedCredentialKeys[credential]; shared {
			return credentialSelectionReport{}, die(fmt.Sprintf("credentials.%s.providers is required so shared GitHub credentials stay least-privilege", credential))
		}
		if !credentialAllowedForAgent(agent, credential) {
			return credentialSelectionReport{
				selected:  false,
				reason:    "credential is not in scope for agent " + agent,
				readiness: "out-of-scope",
				inputKind: "source",
			}, nil
		}
		if err := validateStatusCredentialSource(credential, raw, policyBase); err != nil {
			return credentialSelectionReport{}, err
		}
		return credentialSelectionReport{
			selected:  true,
			reason:    "scalar credential entry is selected without provider or mode restrictions",
			readiness: "ready",
			inputKind: "source",
		}, nil
	}
	if err := validateStatusCredentialEntry(credential, rawMap); err != nil {
		return credentialSelectionReport{}, err
	}
	report := credentialSelectionReport{
		inputKind: credentialInputKind(rawMap),
	}
	if resolver, ok := rawMap["resolver"].(string); ok {
		report.resolver = resolver
	}
	if providers, ok := rawMap["providers"]; ok {
		values, err := selectorStrings(providers, "credentials."+credential+".providers", SupportedAgents)
		if err != nil {
			return credentialSelectionReport{}, err
		}
		report.providers = values
	}
	if modes, ok := rawMap["modes"]; ok {
		values, err := selectorStrings(modes, "credentials."+credential+".modes", SupportedModes)
		if err != nil {
			return credentialSelectionReport{}, err
		}
		report.modes = values
	}
	if !credentialAllowedForAgent(agent, credential) {
		report.reason = "credential is not in scope for agent " + agent
		report.readiness = "out-of-scope"
		return report, nil
	}
	providerMatch := true
	if len(report.providers) > 0 {
		providerMatch = false
		for _, candidate := range report.providers {
			if candidate == agent {
				providerMatch = true
				break
			}
		}
	}
	modeMatch := true
	if len(report.modes) > 0 {
		modeMatch = false
		for _, candidate := range report.modes {
			if candidate == mode {
				modeMatch = true
				break
			}
		}
	}
	report.selected = providerMatch && modeMatch
	switch {
	case !providerMatch:
		report.readiness = "filtered-provider"
	case !modeMatch:
		report.readiness = "filtered-mode"
	case report.inputKind == "resolver":
		readiness, err := resolverReadinessForStatus(credential, report.resolver)
		if err != nil {
			return credentialSelectionReport{}, err
		}
		report.readiness = readiness
	default:
		if err := validateStatusCredentialSource(credential, rawMap, policyBase); err != nil {
			return credentialSelectionReport{}, err
		}
		report.readiness = "ready"
	}
	reasons := make([]string, 0, 2)
	if providerMatch {
		if len(report.providers) == 0 {
			reasons = append(reasons, "providers not restricted")
		} else {
			reasons = append(reasons, "agent matches providers")
		}
	} else {
		reasons = append(reasons, "agent does not match providers")
	}
	if modeMatch {
		if len(report.modes) == 0 {
			reasons = append(reasons, "modes not restricted")
		} else {
			reasons = append(reasons, "mode matches modes")
		}
	} else {
		reasons = append(reasons, "mode does not match modes")
	}
	report.reason = strings.Join(reasons, "; ")
	return report, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func allowedCredentialsForAgent(agent string) map[string]struct{} {
	allowed := map[string]struct{}{}
	for key := range SharedCredentialKeys {
		allowed[key] = struct{}{}
	}
	for key := range AgentScopedCredentialKeys[agent] {
		allowed[key] = struct{}{}
	}
	return allowed
}

func credentialAllowedForAgent(agent string, credential string) bool {
	_, ok := allowedCredentialsForAgent(agent)[credential]
	return ok
}

func validateSelectorValues(values any, label string, allowedValues map[string]struct{}) error {
	_, err := selectorStrings(values, label, allowedValues)
	return err
}

func validateStatusCredentialEntry(key string, raw any) error {
	rawMap, ok := raw.(map[string]any)
	if !ok {
		if _, shared := SharedCredentialKeys[key]; shared {
			return die(fmt.Sprintf("credentials.%s.providers is required so shared GitHub credentials stay least-privilege", key))
		}
		return nil
	}
	if err := validateAllowedKeys(rawMap, entryAllowedKeys, "credentials."+key); err != nil {
		return err
	}
	sourceRaw := rawMap["source"]
	resolver, _ := rawMap["resolver"].(string)
	providers := rawMap["providers"]
	materialization, hasMaterialization := rawMap["materialization"]
	if _, ok := SharedCredentialKeys[key]; ok && providers == nil {
		return die(fmt.Sprintf("credentials.%s.providers is required so shared GitHub credentials stay least-privilege", key))
	}
	if sourceRaw != nil && resolver != "" {
		return die(fmt.Sprintf("credentials.%s must not declare both source and resolver", key))
	}
	if resolver == "" {
		if hasMaterialization {
			return die(fmt.Sprintf("credentials.%s.materialization is only valid for resolver-backed auth", key))
		}
		if sourceRaw == nil {
			return die(fmt.Sprintf("credentials.%s must declare source or resolver", key))
		}
		return nil
	}
	if !authresolve.ResolverSupported(key, resolver) {
		return die(fmt.Sprintf("credentials.%s.resolver is unsupported: %s", key, resolver))
	}
	if hasMaterialization {
		if mat, ok := materialization.(string); !ok || mat != "ephemeral" {
			return die(fmt.Sprintf("credentials.%s.materialization must stay ephemeral for resolver-backed auth", key))
		}
	}
	return nil
}

func validateStatusCredentialSource(key string, raw any, policyBase string) error {
	sourceRaw := raw
	if rawMap, ok := raw.(map[string]any); ok {
		sourceRaw = rawMap["source"]
	}
	if sourceRaw == nil {
		return nil
	}
	source, err := validateSourcePath(sourceRaw, "credentials."+key, policyBase)
	if err != nil {
		return err
	}
	_, err = requireSecretFile(source, "credentials."+key)
	return err
}

func renderMap(value map[string]string) string {
	if len(value) == 0 {
		return "none"
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+":"+value[key])
	}
	return strings.Join(parts, ",")
}

func parseRenderedMap(raw string) map[string]string {
	parsed := map[string]string{}
	if raw == "" || raw == "none" {
		return parsed
	}
	for _, item := range strings.Split(raw, ",") {
		if item == "" {
			continue
		}
		key, value, ok := strings.Cut(item, ":")
		if !ok || key == "" {
			continue
		}
		parsed[key] = value
	}
	return parsed
}

func renderModes(keys []string) string {
	if len(keys) == 0 {
		return "none"
	}
	return strings.Join(keys, ",")
}

func renderTextDiff(fromName string, toName string, fromText string, toText string) string {
	fromLines := splitDiffLines(fromText)
	toLines := splitDiffLines(toText)
	matrix := make([][]int, len(fromLines)+1)
	for i := range matrix {
		matrix[i] = make([]int, len(toLines)+1)
	}
	for i := len(fromLines) - 1; i >= 0; i-- {
		for j := len(toLines) - 1; j >= 0; j-- {
			if fromLines[i] == toLines[j] {
				matrix[i][j] = matrix[i+1][j+1] + 1
				continue
			}
			if matrix[i+1][j] >= matrix[i][j+1] {
				matrix[i][j] = matrix[i+1][j]
			} else {
				matrix[i][j] = matrix[i][j+1]
			}
		}
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "--- %s\n", fromName)
	fmt.Fprintf(&builder, "+++ %s\n", toName)
	i, j := 0, 0
	for i < len(fromLines) && j < len(toLines) {
		if fromLines[i] == toLines[j] {
			builder.WriteByte(' ')
			builder.WriteString(fromLines[i])
			builder.WriteByte('\n')
			i++
			j++
			continue
		}
		if matrix[i+1][j] >= matrix[i][j+1] {
			builder.WriteByte('-')
			builder.WriteString(fromLines[i])
			builder.WriteByte('\n')
			i++
			continue
		}
		builder.WriteByte('+')
		builder.WriteString(toLines[j])
		builder.WriteByte('\n')
		j++
	}
	for ; i < len(fromLines); i++ {
		builder.WriteByte('-')
		builder.WriteString(fromLines[i])
		builder.WriteByte('\n')
	}
	for ; j < len(toLines); j++ {
		builder.WriteByte('+')
		builder.WriteString(toLines[j])
		builder.WriteByte('\n')
	}
	return builder.String()
}

func splitDiffLines(text string) []string {
	if text == "" {
		return nil
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func selectorStrings(values any, label string, allowedValues map[string]struct{}) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	rawValues, ok := values.([]any)
	if !ok || len(rawValues) == 0 {
		return nil, die(fmt.Sprintf("%s must be a non-empty array when specified", label))
	}
	parsed := make([]string, 0, len(rawValues))
	for _, value := range rawValues {
		s, ok := value.(string)
		if !ok {
			return nil, die(fmt.Sprintf("%s values must be strings", label))
		}
		if _, ok := allowedValues[s]; !ok {
			return nil, die(fmt.Sprintf("%s contains unsupported value: %s", label, s))
		}
		parsed = append(parsed, s)
	}
	return parsed, nil
}

func credentialInputKind(raw any) string {
	if rawMap, ok := raw.(map[string]any); ok {
		if rawMap["resolver"] != nil {
			return "resolver"
		}
	}
	return "source"
}

func selectedCredentials(policy map[string]any, agent string, mode string) (map[string]any, error) {
	credentials, _ := policy["credentials"].(map[string]any)
	if credentials == nil {
		return map[string]any{}, nil
	}
	if agent == "" {
		selected := map[string]any{}
		for key, raw := range credentials {
			if err := validateStatusCredentialEntry(key, raw); err != nil {
				return nil, err
			}
			if rawMap, ok := raw.(map[string]any); ok {
				if err := validateSelectorValues(rawMap["providers"], "credentials."+key+".providers", SupportedAgents); err != nil {
					return nil, err
				}
				ok, err := selectedFor(rawMap["modes"], mode, "credentials."+key+".modes", SupportedModes)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
			}
			selected[key] = raw
		}
		return selected, nil
	}
	relevant := map[string]struct{}{}
	for key := range SharedCredentialKeys {
		relevant[key] = struct{}{}
	}
	for key := range AgentScopedCredentialKeys[agent] {
		relevant[key] = struct{}{}
	}
	selected := map[string]any{}
	keys := make([]string, 0, len(relevant))
	for key := range relevant {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		raw, ok := credentials[key]
		if !ok {
			continue
		}
		if err := validateStatusCredentialEntry(key, raw); err != nil {
			return nil, err
		}
		if rawMap, ok := raw.(map[string]any); ok {
			ok, err := selectedFor(rawMap["providers"], agent, "credentials."+key+".providers", SupportedAgents)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			ok, err = selectedFor(rawMap["modes"], mode, "credentials."+key+".modes", SupportedModes)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		selected[key] = raw
	}
	return selected, nil
}
