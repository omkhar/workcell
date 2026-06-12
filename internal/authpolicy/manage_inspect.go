// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package authpolicy

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

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
