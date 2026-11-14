// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// runGitHubReleaseQuotaScript sources scripts/lib/github-release-quota.sh with
// a stub curl that answers every request with the status, headers and body in
// dir, and runs body. HOME is dir, so the release cache stays in the test.
func runGitHubReleaseQuotaScript(t *testing.T, dir, body string) (string, error) {
	t.Helper()
	script := `set -euo pipefail
source "$1"
stub="$2"
curl() {
  local out="" headers="" format=""
  while (($#)); do
    case "$1" in
      -o) out="$2"; shift ;;
      -D) headers="$2"; shift ;;
      -w) format="$2"; shift ;;
      -f*) echo "stub curl: unexpected fail flag $1" >&2; return 2 ;;
    esac
    shift
  done
  printf 'request\n' >>"${stub}/requests"
  cp "${stub}/headers" "${headers}"
  cp "${stub}/body" "${out}"
  [[ "${format}" == '%{http_code}' ]] && cat "${stub}/status"
  return 0
}
` + body
	cmd := exec.Command("bash", "-c", script, "bash",
		filepath.Join(repoRoot(t), "scripts/lib/github-release-quota.sh"), dir)
	tmp := filepath.Join(dir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(withoutEnv(os.Environ(), "WORKCELL_GITHUB_ACTIONS", "WORKCELL_UPSTREAM_RELEASE_REQUIRE_FULL_CHECK", "GITHUB_ACTIONS"), "HOME="+dir, "TMPDIR="+tmp)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func writeGitHubStub(t *testing.T, dir, status, headers, body string) {
	t.Helper()
	for name, content := range map[string]string{"status": status, "headers": headers, "body": body} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestGitHubUnauthenticatedGetNamesTheSpentQuota proves that a 403 or 429 with
// no requests left is reported as the rate limit with its reset time, at once
// and not after curl retries, and that other refusals are not.
func TestGitHubUnauthenticatedGetNamesTheSpentQuota(t *testing.T) {
	cases := map[string]struct {
		status, headers string
		wantRC          string
		wantText        string
	}{
		"403 with no quota left": {
			status:   "403",
			headers:  "HTTP/2 403\r\nx-ratelimit-remaining: 0\r\nX-RateLimit-Reset: 1792300740\r\n\r\n",
			wantRC:   "rc=3",
			wantText: "GitHub API rate limit reached (status 403); the quota resets at 2026-10-18T",
		},
		"429 with no quota left": {
			status:   "429",
			headers:  "HTTP/2 429\r\nX-RateLimit-Remaining: 0\r\n\r\n",
			wantRC:   "rc=3",
			wantText: "GitHub API rate limit reached (status 429); the quota resets at an unknown time",
		},
		"403 with quota left": {
			status:   "403",
			headers:  "HTTP/2 403\r\nX-RateLimit-Remaining: 12\r\n\r\n",
			wantRC:   "rc=1",
			wantText: "Unexpected GitHub API response 403",
		},
		"not found": {
			status:   "404",
			headers:  "HTTP/2 404\r\nX-RateLimit-Remaining: 0\r\n\r\n",
			wantRC:   "rc=1",
			wantText: "Unexpected GitHub API response 404",
		},
		"ok": {
			status:   "200",
			headers:  "HTTP/2 200\r\nX-RateLimit-Remaining: 0\r\n\r\n",
			wantRC:   "rc=0",
			wantText: `{"tag_name":"v1.2.3"}`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeGitHubStub(t, dir, tc.status, tc.headers, `{"tag_name":"v1.2.3"}`)
			out, err := runGitHubReleaseQuotaScript(t, dir, `rc=0
github_unauthenticated_get https://api.github.com/repos/o/r/releases/tags/v1.2.3 "${stub}/out" application/vnd.github+json || rc=$?
echo "rc=${rc}"
[[ "${rc}" != 0 ]] || cat "${stub}/out"
`)
			if err != nil {
				t.Fatalf("script: %v\n%s", err, out)
			}
			if !strings.Contains(out, tc.wantRC) || !strings.Contains(out, tc.wantText) {
				t.Fatalf("output = %q; want %q and %q", out, tc.wantRC, tc.wantText)
			}
			requests, err := os.ReadFile(filepath.Join(dir, "requests"))
			if err != nil || strings.Count(string(requests), "request") != 1 {
				t.Fatalf("requests = %q, %v; want one request", requests, err)
			}
		})
	}
}

// TestGitHubReleaseCacheKeepsOnlyOwnedRegularFiles proves that the release
// cache gives back what it stored, and refuses a missing entry, a symlinked
// entry, a symlinked cache directory and a name that is not a plain file name.
func TestGitHubReleaseCacheKeepsOnlyOwnedRegularFiles(t *testing.T) {
	dir := t.TempDir()
	out, err := runGitHubReleaseQuotaScript(t, dir, `printf '{"pin":1}' >"${stub}/in"
github_release_cache_store copilot-v1.2.3.json "${stub}/in"
github_release_cache_load copilot-v1.2.3.json "${stub}/loaded"
cat "${stub}/loaded"; echo
github_release_cache_load copilot-v9.9.9.json "${stub}/missing" && echo "loaded a missing entry"
for name in ../escape.json .hidden.json "a/b.json" ""; do
  github_release_cache_store "${name}" "${stub}/in" 2>/dev/null && echo "stored ${name}"
  github_release_cache_load "${name}" "${stub}/x" 2>/dev/null && echo "loaded ${name}"
done
cache="${HOME}/.cache/workcell/upstream-releases"
[[ "$(stat -c %a "${cache}" 2>/dev/null || stat -f %Lp "${cache}")" == 700 ]] || echo "cache mode is not 700"
printf '{"forged":1}' >"${stub}/forged"
ln -s "${stub}/forged" "${cache}/copilot-v2.0.0.json"
github_release_cache_load copilot-v2.0.0.json "${stub}/y" && echo "loaded a symlinked entry"
mv "${cache}" "${stub}/real-cache"
ln -s "${stub}/real-cache" "${cache}"
github_release_cache_load copilot-v1.2.3.json "${stub}/z" && echo "loaded through a symlinked cache directory"
github_release_cache_store copilot-v1.2.4.json "${stub}/in" 2>/dev/null && echo "stored through a symlinked cache directory"
echo done
`)
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	if out != "{\"pin\":1}\ndone\n" {
		t.Fatalf("output = %q; want only the stored entry and done", out)
	}
}

func withoutEnv(env []string, names ...string) []string {
	kept := env[:0:0]
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if !slices.Contains(names, name) {
			kept = append(kept, entry)
		}
	}
	return kept
}

// TestGitHubReleaseLocalRunFailsClosed proves that only a run with no token,
// outside GitHub Actions and not asked for a full check may read the cache or
// skip the check on a spent quota.
func TestGitHubReleaseLocalRunFailsClosed(t *testing.T) {
	dir := t.TempDir()
	out, err := runGitHubReleaseQuotaScript(t, dir, `check() { if github_release_local_run "$1"; then echo "$2=local"; else echo "$2=full"; fi; }
check 0 plain
check 1 token
WORKCELL_GITHUB_ACTIONS=1 check 0 actions
WORKCELL_UPSTREAM_RELEASE_REQUIRE_FULL_CHECK=1 check 0 fresh-pins
check "" empty-token-state
`)
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	if want := "plain=local\ntoken=full\nactions=full\nfresh-pins=full\nempty-token-state=full\n"; out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}

// TestTrustedEntrypointKeepsAGitHubActionsRunKnown starts a sanitized script
// in GitHub Actions that starts another one, as update-provider-pins.sh starts
// the release checks. The inner script must still know the run is in GitHub
// Actions, even when the caller set WORKCELL_GITHUB_ACTIONS=0.
func TestTrustedEntrypointKeepsAGitHubActionsRunKnown(t *testing.T) {
	dir := t.TempDir()
	entrypoint := filepath.Join(repoRoot(t), "scripts", "lib", "trusted-entrypoint.sh")
	inner := filepath.Join(dir, "inner.sh")
	outer := filepath.Join(dir, "outer.sh")
	for path, body := range map[string]string{
		inner: "#!/bin/bash -p\nsource \"" + entrypoint + "\"\necho \"actions=${WORKCELL_GITHUB_ACTIONS:-unset} raw=${GITHUB_ACTIONS:-unset}\"\n",
		outer: "#!/bin/bash -p\nsource \"" + entrypoint + "\"\nexec /bin/bash -p \"" + inner + "\"\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, tc := range map[string]struct {
		env  []string
		want string
	}{
		"in GitHub Actions":                 {env: []string{"GITHUB_ACTIONS=true", "WORKCELL_GITHUB_ACTIONS=0"}, want: "actions=1 raw=unset\n"},
		"outside GitHub Actions":            {env: nil, want: "actions=unset raw=unset\n"},
		"a GITHUB_ACTIONS that is not true": {env: []string{"GITHUB_ACTIONS=false"}, want: "actions=unset raw=unset\n"},
	} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command("/bin/bash", "-p", outer)
			cmd.Env = append(withoutEnv(os.Environ(), "WORKCELL_GITHUB_ACTIONS", "GITHUB_ACTIONS", "WORKCELL_SANITIZED_ENTRYPOINT"), tc.env...)
			out, err := cmd.CombinedOutput()
			if err != nil || string(out) != tc.want {
				t.Fatalf("output = %q, %v; want %q", out, err, tc.want)
			}
		})
	}
}

// TestGitHubReleaseCacheEntryBindsTheVerifiedDigests proves that a cache entry
// matches only its own version and the exact pins that its full check
// verified, and only when its release metadata names each asset once with
// that digest.
func TestGitHubReleaseCacheEntryBindsTheVerifiedDigests(t *testing.T) {
	dir := t.TempDir()
	a, b, c := strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64)
	out, err := runGitHubReleaseQuotaScript(t, dir, `a=`+a+` b=`+b+` c=`+c+`
release() { jq -n --arg tag "$1" --arg da "$2" --arg db "$3" '{tag_name: $tag, prerelease: false, assets: [{name: "x.tgz", digest: ("sha256:" + $da)}, {name: "y.tgz", digest: ("sha256:" + $db)}]}' >"${stub}/release.json"; }
check() { if github_release_cache_entry_matches "${stub}/entry.json" "$@"; then echo hit; else echo miss; fi; }
release v1.0.0 "$a" "$b"
github_release_cache_entry_write "${stub}/entry.json" 1.0.0 "${stub}/release.json" "x.tgz=$a" "y.tgz=$b"
check 1.0.0 "x.tgz=$a" "y.tgz=$b"
check 1.0.0 "y.tgz=$b" "x.tgz=$a"
check 1.0.1 "x.tgz=$a" "y.tgz=$b"
check 1.0.0 "x.tgz=$a" "y.tgz=$c"
check 1.0.0 "x.tgz=$a"
check 1.0.0 "x.tgz=$a" "y.tgz=$b" "z.tgz=$c"
check 1.0.0 "x.tgz=$a" "y.tgz=nothex"
jq '.verified_assets = {}' "${stub}/entry.json" >"${stub}/e" && mv "${stub}/e" "${stub}/entry.json"
check 1.0.0 "x.tgz=$a" "y.tgz=$b"
release v1.0.0 "$a" "$c"
github_release_cache_entry_write "${stub}/entry.json" 1.0.0 "${stub}/release.json" "x.tgz=$a" "y.tgz=$b"
check 1.0.0 "x.tgz=$a" "y.tgz=$b"
jq '.assets += [{name: "x.tgz", digest: ("sha256:" + "'"$a"'")}]' "${stub}/release.json" >"${stub}/r" && mv "${stub}/r" "${stub}/release.json"
github_release_cache_entry_write "${stub}/entry.json" 1.0.0 "${stub}/release.json" "x.tgz=$a"
check 1.0.0 "x.tgz=$a"
`)
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	if want := "hit\nhit\nmiss\nmiss\nmiss\nmiss\nmiss\nmiss\nmiss\nmiss\n"; out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}

// TestGitHubReleaseCacheRefusesASharedOrMissingHome proves that the cache is
// never kept under a shared temporary directory or a HOME that is not an
// absolute directory that the user owns.
func TestGitHubReleaseCacheRefusesASharedOrMissingHome(t *testing.T) {
	dir := t.TempDir()
	out, err := runGitHubReleaseQuotaScript(t, dir, `printf '{}' >"${stub}/in"
for home in /tmp /tmp/ /var/tmp "${TMPDIR}" relative "" "${stub}/missing"; do
  if HOME="${home}" github_release_cache_store copilot-v1.json "${stub}/in" 2>/dev/null; then echo "stored under ${home}"; fi
done
(unset HOME; github_release_cache_store copilot-v1.json "${stub}/in" 2>/dev/null && echo "stored with no HOME") || true
github_release_cache_store copilot-v1.json "${stub}/in" && echo "stored under the user's HOME"
`)
	if err != nil {
		t.Fatalf("script: %v\n%s", err, out)
	}
	if want := "stored under the user's HOME\n"; out != want {
		t.Fatalf("output = %q, want %q", out, want)
	}
}

// TestCopilotReleaseCheckGuardsEachLocalOnlyBranch proves that the Copilot
// release check reaches the spent-quota skip, the cache read and the cache
// write only through github_release_local_run, and that a cache entry must
// match the pins before its metadata is used.
func TestCopilotReleaseCheckGuardsEachLocalOnlyBranch(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "verify-upstream-copilot-release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	for _, want := range []string{
		"    3)\n      github_release_local_run \"${RELEASE_TOKEN_AVAILABLE}\" || exit 1\n      echo \"NOTICE: skipped",
		"  github_release_local_run \"${RELEASE_TOKEN_AVAILABLE}\" &&\n  github_release_cache_load \"${RELEASE_METADATA_CACHE_NAME}\" \"${RELEASE_CACHE_ENTRY_PATH}\" &&\n  github_release_cache_entry_matches \"${RELEASE_CACHE_ENTRY_PATH}\" \"${COPILOT_VERSION}\" \"${RELEASE_PINS[@]}\" &&\n  jq '.release'",
		"if [[ \"${RELEASE_METADATA_FROM_CACHE}\" != \"1\" ]] && github_release_local_run \"${RELEASE_TOKEN_AVAILABLE}\"; then\n  { github_release_cache_entry_write",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("verify-upstream-copilot-release.sh does not guard a local-only branch with:\n%s", want)
		}
	}
	for _, name := range []string{"github_release_cache_load", "github_release_cache_store", "exit 0"} {
		if got := strings.Count(script, name); got != map[string]int{"github_release_cache_load": 1, "github_release_cache_store": 1, "exit 0": 2}[name] {
			t.Errorf("verify-upstream-copilot-release.sh calls %s %d times; review each new call for the local-run guard", name, got)
		}
	}
}
