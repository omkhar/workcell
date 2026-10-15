// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"slices"
	"strings"
)

// The names the egress-deny-audited order rests on. A protected name may be
// a command word only: as an argument it could write, alias, hash or eval a
// definition that replaces the reviewed one.
const (
	sealHelper     = "stop_egress_proxy_then_finalize"
	fallbackHelper = "finish_stopped_session_fallback"
	sealCommand    = "finalize_session_audit"
	proxyStop      = "stop_egress_proxy"
	sessionStop    = "stop_egress_proxy_for_session"
	fallbackSeal   = "sign_session_audit_head_explicit"
)

var sealProtectedNames = []string{sealHelper, fallbackHelper, sealCommand, proxyStop, sessionStop}

// sealCalls is the reviewed number of calls of each helper in each definition
// ("" is the code outside every function): the foreground launch and the
// session monitor seal through the seal helper, and the two dead-monitor
// branches of session stop through the fallback helper.
var sealCalls = map[string]map[string]int{
	"":                     {sealHelper: 1},
	"session_monitor_main": {sealHelper: 1},
	"session_stop_main":    {fallbackHelper: 2},
}

// ValidateEgressDenySealOrder requires that every session seal in
// scripts/workcell runs after the egress proxy stop that appends the deny
// records (invariant egress-deny-audited). It splits the script into its
// function definitions with the shared reader and reads each one with
// EveryShellCommand, which keeps commands in groups, subshells, case arms,
// guarded lists and nested helpers. Only the seal helper may call
// finalize_session_audit, after stop_egress_proxy. Only the fallback helper may
// stop a session's proxy with audit, before it signs. Every other
// stop_egress_proxy_for_session passes no-audit.
func ValidateEgressDenySealOrder(script string) error {
	fail := errors.New("Expected every egress-proxy session seal to follow the proxy stop that appends the deny records")
	commands := map[string][][]string{}
	definitions := map[string]int{}
	var collect func(text, name string)
	collect = func(text, name string) {
		for _, part := range functionBodies(flattenSubstitutions(heredocsAsHereStrings(text))) {
			if part.name == "" {
				commands[name] = append(commands[name], EveryShellCommand(part.text)...)
				continue
			}
			definitions[part.name]++
			collect(part.text, part.name)
		}
	}
	collect(script, "")
	auditingStop := func(words []string) bool {
		return words[0] == sessionStop && (len(words) != 4 || words[3] != "no-audit")
	}
	named := func(name string) func([]string) bool {
		return func(words []string) bool { return len(words) > 0 && words[0] == name }
	}
	for _, name := range append(sealProtectedNames, "session_monitor_main", "session_stop_main") {
		if definitions[name] > 1 {
			return fail
		}
	}
	for name, list := range commands {
		if slices.ContainsFunc(list, namesProtectedArgument) {
			return fail
		}
		seals, stops := countFunc(list, named(sealCommand)), countFunc(list, auditingStop)
		switch name {
		case sealHelper:
			if seals != 1 || !stopBeforeSeal(list, named(proxyStop), sealCommand) {
				return fail
			}
		case fallbackHelper:
			if stops != 1 || !stopBeforeSeal(list, auditingStop, fallbackSeal) {
				return fail
			}
		default:
			if seals != 0 || stops != 0 {
				return fail
			}
		}
	}
	for name, want := range sealCalls {
		for helper, calls := range want {
			if countFunc(commands[name], named(helper)) != calls {
				return fail
			}
		}
	}
	return nil
}

// namesProtectedArgument reports whether an argument of the command holds a
// protected name as a whole identifier.
func namesProtectedArgument(words []string) bool {
	return slices.ContainsFunc(words[1:], func(arg string) bool {
		identifiers := strings.FieldsFunc(arg, func(r rune) bool {
			return r != '_' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
		})
		return slices.ContainsFunc(identifiers, func(identifier string) bool { return slices.Contains(sealProtectedNames, identifier) })
	})
}

func countFunc(list [][]string, match func([]string) bool) int {
	count := 0
	for _, words := range list {
		if len(words) > 0 && match(words) {
			count++
		}
	}
	return count
}

// stopBeforeSeal reports whether a stop command comes before the first seal command.
func stopBeforeSeal(list [][]string, stop func([]string) bool, seal string) bool {
	at := slices.IndexFunc(list, func(words []string) bool { return words[0] == seal })
	return at > 0 && slices.ContainsFunc(list[:at], stop)
}
