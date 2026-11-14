// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"os"
	"os/exec"
	"path/filepath"
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
	cmd.Env = append(os.Environ(), "HOME="+dir, "TMPDIR="+dir)
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
