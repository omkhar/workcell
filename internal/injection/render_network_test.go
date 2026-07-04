// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package injection

import (
	"github.com/omkhar/workcell/internal/injectionpolicy"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestRenderNetworkAcceptsAndSortsValidLists proves the happy path: valid
// allow/deny endpoints (host:port and [ipv6]:port) are accepted, de-duplicated,
// and sorted.
func TestRenderNetworkAcceptsAndSortsValidLists(t *testing.T) {
	t.Parallel()

	allow, deny, err := renderNetwork(map[string]any{
		"network": map[string]any{
			"allow_endpoints": []any{"registry.internal.example:443", "10.0.0.5:8443", "[2001:db8::1]:443", "registry.internal.example:443"},
			"deny_endpoints":  []any{"chatgpt.com:443", "api.openai.com:443"},
		},
	})
	if err != nil {
		t.Fatalf("renderNetwork error: %v", err)
	}
	wantAllow := []string{"10.0.0.5:8443", "[2001:db8::1]:443", "registry.internal.example:443"}
	if !reflect.DeepEqual(allow, wantAllow) {
		t.Fatalf("allow endpoints = %#v, want %#v", allow, wantAllow)
	}
	wantDeny := []string{"api.openai.com:443", "chatgpt.com:443"}
	if !reflect.DeepEqual(deny, wantDeny) {
		t.Fatalf("deny endpoints = %#v, want %#v", deny, wantDeny)
	}
}

// TestRenderNetworkAbsentTableYieldsEmpty confirms a policy with no [network]
// table returns empty (non-nil) slices, so callers never see a nil surprise.
func TestRenderNetworkAbsentTableYieldsEmpty(t *testing.T) {
	t.Parallel()

	allow, deny, err := renderNetwork(map[string]any{})
	if err != nil {
		t.Fatalf("renderNetwork error: %v", err)
	}
	if len(allow) != 0 || len(deny) != 0 {
		t.Fatalf("expected empty lists, got allow=%#v deny=%#v", allow, deny)
	}
}

// TestRenderNetworkFailClosedNegatives is the mandatory negative-control set
// for this security item: every malformed input must abort the render with an
// error that names the offending value.
func TestRenderNetworkFailClosedNegatives(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		network any
		wantSub string
	}{
		{
			name:    "malformed endpoint missing port",
			network: map[string]any{"allow_endpoints": []any{"registry.internal.example"}},
			wantSub: "registry.internal.example",
		},
		{
			name:    "malformed endpoint bad host char",
			network: map[string]any{"allow_endpoints": []any{"bad_host!:443"}},
			wantSub: "bad_host!:443",
		},
		{
			name:    "port out of range",
			network: map[string]any{"deny_endpoints": []any{"host.example:70000"}},
			wantSub: "host.example:70000",
		},
		{
			name:    "port zero",
			network: map[string]any{"allow_endpoints": []any{"host.example:0"}},
			wantSub: "host.example:0",
		},
		{
			name:    "leading dot host",
			network: map[string]any{"allow_endpoints": []any{".example:443"}},
			wantSub: ".example:443",
		},
		{
			name:    "double dot host",
			network: map[string]any{"allow_endpoints": []any{"a..b:443"}},
			wantSub: "a..b:443",
		},
		{
			name:    "empty string endpoint",
			network: map[string]any{"allow_endpoints": []any{""}},
			wantSub: `""`,
		},
		{
			name:    "unknown key under network",
			network: map[string]any{"network_policy": "unrestricted"},
			wantSub: "network_policy",
		},
		{
			name:    "non-array allow value",
			network: map[string]any{"allow_endpoints": "registry.internal.example:443"},
			wantSub: "must be an array",
		},
		{
			name:    "non-string array element",
			network: map[string]any{"deny_endpoints": []any{443}},
			wantSub: "non-string element",
		},
		{
			name:    "network is not a table",
			network: "not-a-table",
			wantSub: "network must be a TOML table",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := renderNetwork(map[string]any{"network": tc.network})
			if err == nil {
				t.Fatalf("renderNetwork accepted invalid input %#v", tc.network)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("renderNetwork error %q does not name offending value %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestRenderNetworkCannotSetPolicyMode is the explicit no-weakening proof:
// [network] keys that look like mode/allowlist switches are rejected as
// unknown keys, and the function's only outputs are two endpoint slices — there
// is no return value or side channel through which [network] can change
// NETWORK_POLICY, disable the allowlist, or switch to unrestricted.
func TestRenderNetworkCannotSetPolicyMode(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"network_policy", "mode", "policy", "allowlist", "unrestricted", "disable"} {
		_, _, err := renderNetwork(map[string]any{"network": map[string]any{key: "anything"}})
		if err == nil {
			t.Fatalf("renderNetwork accepted a policy-mode-shaped key %q under [network]", key)
		}
		if !strings.Contains(err.Error(), "unsupported keys") {
			t.Fatalf("renderNetwork error for key %q = %q, want unsupported-keys rejection", key, err.Error())
		}
	}
}

// TestMergeEndpointListsUnionsAndSorts pins the union+sort contract used to
// fold [network].allow_endpoints into the credential-derived extra endpoints
// without clobbering either source.
func TestMergeEndpointListsUnionsAndSorts(t *testing.T) {
	t.Parallel()

	got := mergeEndpointLists(
		[]string{"api.github.com:443", "github.com:443"},
		[]string{"registry.internal.example:443", "api.github.com:443", ""},
	)
	want := []string{"api.github.com:443", "github.com:443", "registry.internal.example:443"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeEndpointLists = %#v, want %#v", got, want)
	}
}

// TestValidateEgressEndpointMirrorsHelperGrammar exercises the host:port /
// [ipv6]:port grammar so it stays in lockstep with
// scripts/colima-egress-allowlist.sh's validate_endpoint.
func TestValidateEgressEndpointMirrorsHelperGrammar(t *testing.T) {
	t.Parallel()

	valid := []string{
		"github.com:443",
		"api.github.com:443",
		"registry.internal.example:8443",
		"10.0.0.5:443",
		"[2001:db8::1]:443",
		"host-with-dash.example:1",
		"host.example:65535",
	}
	for _, endpoint := range valid {
		if err := injectionpolicy.ValidateEgressEndpoint(endpoint, "network.allow_endpoints"); err != nil {
			t.Fatalf("injectionpolicy.ValidateEgressEndpoint(%q) unexpected error: %v", endpoint, err)
		}
	}
	invalid := []string{
		"",
		"host.example",
		"host.example:0",
		"host.example:65536",
		"host.example:70000",
		".leading:443",
		"double..dot:443",
		"under_score:443",
		"has space:443",
		"host:443:443",
		"[bad::host:443",
		"host.example:",
		"host.example:abc",
	}
	for _, endpoint := range invalid {
		if err := injectionpolicy.ValidateEgressEndpoint(endpoint, "network.allow_endpoints"); err == nil {
			t.Fatalf("injectionpolicy.ValidateEgressEndpoint(%q) accepted an invalid endpoint", endpoint)
		}
	}
}

// TestRenderNetworkMergesAcrossIncludes proves [network] endpoint lists are
// unioned across included fragments (mergeNetworkFragment), then validated as a
// whole by renderNetwork.
func TestRenderNetworkMergesAcrossIncludes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fragments := filepath.Join(root, "fragments")
	writeText(t, filepath.Join(fragments, "extra-net.toml"), strings.Join([]string{
		"[network]",
		`allow_endpoints = ["registry.internal.example:443"]`,
		`deny_endpoints  = ["chatgpt.com:443"]`,
	}, "\n"), 0o600)
	policyPath := filepath.Join(root, "policy.toml")
	writeText(t, policyPath, strings.Join([]string{
		"version = 1",
		`includes = ["fragments/extra-net.toml"]`,
		"[network]",
		`allow_endpoints = ["telemetry.internal.example:443"]`,
		`deny_endpoints  = ["api.openai.com:443"]`,
	}, "\n"), 0o600)

	merged, _, err := loadPolicyBundle(Path(policyPath))
	if err != nil {
		t.Fatalf("loadPolicyBundle error: %v", err)
	}
	allow, deny, err := renderNetwork(merged)
	if err != nil {
		t.Fatalf("renderNetwork error: %v", err)
	}
	wantAllow := []string{"registry.internal.example:443", "telemetry.internal.example:443"}
	if !reflect.DeepEqual(allow, wantAllow) {
		t.Fatalf("merged allow = %#v, want %#v", allow, wantAllow)
	}
	wantDeny := []string{"api.openai.com:443", "chatgpt.com:443"}
	if !reflect.DeepEqual(deny, wantDeny) {
		t.Fatalf("merged deny = %#v, want %#v", deny, wantDeny)
	}
}

// TestRunRenderInjectionBundleMergesNetworkEndpoints is the end-to-end proof
// that [network].allow_endpoints unions with the credential-derived extra
// endpoints in the manifest (not clobbering them) and that deny_endpoints is
// emitted verbatim.  A gemini_env credential in GCA mode contributes the Google
// auth endpoints; the [network] surface adds an operator endpoint and a deny.
func TestRunRenderInjectionBundleMergesNetworkEndpoints(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeText(t, filepath.Join(root, "gemini.env"), "GOOGLE_GENAI_USE_GCA=true\n", 0o600)
	policyPath := filepath.Join(root, "policy.toml")
	writeText(t, policyPath, strings.Join([]string{
		"version = 1",
		"[credentials]",
		`gemini_env = "gemini.env"`,
		"[network]",
		`allow_endpoints = ["registry.internal.example:443"]`,
		`deny_endpoints  = ["chatgpt.com:443"]`,
	}, "\n"), 0o600)

	output := filepath.Join(root, "bundle")
	if err := RunRenderInjectionBundle(policyPath, "gemini", "strict", output, ""); err != nil {
		t.Fatalf("RunRenderInjectionBundle error: %v", err)
	}
	manifest := readManifest(t, filepath.Join(output, "manifest.json"))
	meta := manifest["metadata"].(map[string]any)

	extra := stringsFromAny(meta["extra_endpoints"])
	// Union must keep BOTH the credential-derived Google auth endpoints and
	// the operator [network].allow_endpoints entry.
	for _, want := range []string{"accounts.google.com:443", "oauth2.googleapis.com:443", "sts.googleapis.com:443", "registry.internal.example:443"} {
		if !containsString(extra, want) {
			t.Fatalf("extra_endpoints %#v missing %q (allow must union, not clobber, credential endpoints)", extra, want)
		}
	}

	deny := stringsFromAny(meta["deny_endpoints"])
	if !reflect.DeepEqual(deny, []string{"chatgpt.com:443"}) {
		t.Fatalf("deny_endpoints = %#v, want [chatgpt.com:443]", deny)
	}
}

func stringsFromAny(value any) []string {
	raw, _ := value.([]any)
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
