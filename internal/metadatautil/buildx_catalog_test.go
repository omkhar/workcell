// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func TestSelectInstallableBuildxVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		catalog   string
		current   string
		candidate string
		want      string
		wantErr   string
	}{
		{
			name:      "candidate available",
			catalog:   `{"v0.36.0":{"tag_name":"v0.36.0"},"v0.36.1":{"tag_name":"v0.36.1"}}`,
			current:   "v0.36.0",
			candidate: "v0.36.1",
			want:      "v0.36.1",
		},
		{
			name:      "candidate not propagated",
			catalog:   `{"v0.36.0":{"tag_name":"v0.36.0"}}`,
			current:   "v0.36.0",
			candidate: "v0.36.1",
			want:      "v0.36.0",
		},
		{
			name:      "current absent",
			catalog:   `{"v0.36.1":{"tag_name":"v0.36.1"}}`,
			current:   "v0.36.0",
			candidate: "v0.36.1",
			wantErr:   "current Buildx pin v0.36.0 is absent",
		},
		{
			name:      "candidate tag mismatch",
			catalog:   `{"v0.36.0":{"tag_name":"v0.36.0"},"v0.36.1":{"tag_name":"v0.36.0"}}`,
			current:   "v0.36.0",
			candidate: "v0.36.1",
			wantErr:   `entry v0.36.1 has tag_name "v0.36.0"`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			response := fmt.Sprintf(`{"encoding":"base64","content":"%s"}`, base64.StdEncoding.EncodeToString([]byte(tt.catalog)))
			got, err := SelectInstallableBuildxVersion(strings.NewReader(response), tt.current, tt.candidate)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("SelectInstallableBuildxVersion error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("SelectInstallableBuildxVersion = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSelectInstallableBuildxVersionRejectsMalformedEnvelope(t *testing.T) {
	t.Parallel()

	for _, response := range []string{
		`{"encoding":"none","content":"e30="}`,
		`{"encoding":"base64","content":"not-base64"}`,
		`{"encoding":"base64","content":"e30="} {}`,
	} {
		if _, err := SelectInstallableBuildxVersion(strings.NewReader(response), "v0.36.0", "v0.36.1"); err == nil {
			t.Fatalf("SelectInstallableBuildxVersion accepted malformed response %q", response)
		}
	}
}
