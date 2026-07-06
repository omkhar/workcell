// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package applecontainer

import (
	"strings"
	"testing"
)

func TestDefaultContractValidates(t *testing.T) {
	t.Parallel()

	contract := DefaultContract()
	if err := contract.Validate(); err != nil {
		t.Fatalf("DefaultContract().Validate() error = %v", err)
	}
	if contract.TargetKind != "local_vm" {
		t.Fatalf("target_kind = %q, want local_vm", contract.TargetKind)
	}
	if contract.TargetProvider != "apple-container" {
		t.Fatalf("target_provider = %q, want apple-container", contract.TargetProvider)
	}
	if contract.AccessModel != "direct" {
		t.Fatalf("access_model = %q, want direct (non-brokered)", contract.AccessModel)
	}
	if contract.WorkspaceTransport != "local-materialization" {
		t.Fatalf("workspace_transport = %q, want local-materialization", contract.WorkspaceTransport)
	}
}

func TestContractValidateRejectsRemoteValues(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		mutate  func(*Contract)
		wantSub string
	}{
		{
			name:    "brokered access is a remote-VM value",
			mutate:  func(c *Contract) { c.AccessModel = "brokered" },
			wantSub: "access_model",
		},
		{
			name:    "remote_vm target kind rejected",
			mutate:  func(c *Contract) { c.TargetKind = "remote_vm" },
			wantSub: "target_kind",
		},
		{
			name:    "remote materialization transport rejected",
			mutate:  func(c *Contract) { c.WorkspaceTransport = "remote-materialization" },
			wantSub: "workspace_transport",
		},
		{
			name:    "wrong version rejected",
			mutate:  func(c *Contract) { c.Version = 2 },
			wantSub: "version",
		},
		{
			name:    "excluded paths must be .git",
			mutate:  func(c *Contract) { c.WorkspaceMaterialization.ExcludedPaths = nil },
			wantSub: "excluded_paths",
		},
		{
			name:    "audit events must match lifecycle",
			mutate:  func(c *Contract) { c.RequiredAuditEvents = []string{"session_started"} },
			wantSub: "required_audit_events",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			contract := DefaultContract()
			tc.mutate(&contract)
			err := contract.Validate()
			if err == nil {
				t.Fatalf("Validate() error = nil, want error containing %q", tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("Validate() error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}
