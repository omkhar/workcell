// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"net/http"
	"strings"
	"testing"
)

// TestGitHubRateLimitErrorNamesTheQuota proves that a spent quota is reported
// as a rate limit with its reset time, and that other refusals are not.
func TestGitHubRateLimitErrorNamesTheQuota(t *testing.T) {
	response := func(status int, remaining string) *http.Response {
		header := http.Header{}
		if remaining != "" {
			header.Set("X-RateLimit-Remaining", remaining)
		}
		header.Set("X-RateLimit-Reset", "1792300740")
		return &http.Response{StatusCode: status, Header: header}
	}
	for _, status := range []int{http.StatusForbidden, http.StatusTooManyRequests} {
		err := githubRateLimitError(response(status, "0"))
		if !errors.Is(err, errGitHubRateLimit) || !strings.Contains(err.Error(), "2026-10-18T") || !strings.Contains(err.Error(), "WORKCELL_GITHUB_API_TOKEN_FILE") {
			t.Errorf("status %d with no quota left: error = %v; want the rate limit with its reset time and the token file", status, err)
		}
	}
	for name, resp := range map[string]*http.Response{
		"forbidden with quota left": response(http.StatusForbidden, "12"),
		"forbidden with no header":  response(http.StatusForbidden, ""),
		"not found":                 response(http.StatusNotFound, "0"),
		"ok":                        response(http.StatusOK, "0"),
	} {
		if err := githubRateLimitError(resp); err != nil {
			t.Errorf("%s: error = %v; want nil", name, err)
		}
	}
}
