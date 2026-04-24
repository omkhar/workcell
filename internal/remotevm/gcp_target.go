// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

const (
	GCPVMProvider        = "gcp-vm"
	GCPIAPBroker         = "gcp-iap-ssh"
	GCPCertificationLane = "shared/gcp-vm-launch-smoke"
)

type GCPBrokeredAccessPlan struct {
	Version             int      `json:"version"`
	TargetKind          string   `json:"target_kind"`
	TargetProvider      string   `json:"target_provider"`
	TargetID            string   `json:"target_id"`
	AccessModel         string   `json:"access_model"`
	Broker              string   `json:"broker"`
	InboundPublicSSH    bool     `json:"inbound_public_ssh"`
	RequiredPermissions []string `json:"required_permissions"`
	LiveSmokeValidation string   `json:"live_smoke_validation"`
}

func DefaultGCPVMContract() Contract {
	return DefaultContractForProvider(GCPVMProvider)
}

func NewGCPVMTarget() (FakeTarget, error) {
	return NewFakeTarget(DefaultGCPVMContract())
}

func DefaultGCPBrokeredAccessPlan(targetID string) GCPBrokeredAccessPlan {
	return GCPBrokeredAccessPlan{
		Version:          1,
		TargetKind:       TargetKind,
		TargetProvider:   GCPVMProvider,
		TargetID:         targetID,
		AccessModel:      AccessModel,
		Broker:           GCPIAPBroker,
		InboundPublicSSH: false,
		RequiredPermissions: []string{
			"compute.instances.get",
			"compute.projects.get",
			"iap.tunnelInstances.accessViaIAP",
			"oslogin.users.getLoginProfile",
		},
		LiveSmokeValidation: GCPCertificationLane,
	}
}
