// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

const (
	AWSEC2SSMProvider               = "aws-ec2-ssm"
	AWSSessionBroker                = "aws-ssm-session-manager"
	AWSSSMManagedInstanceCorePolicy = "AmazonSSMManagedInstanceCore"
	AWSCertificationLane            = "shared/aws-ec2-ssm-launch-smoke"
)

type BrokeredAccessPlan struct {
	Version                       int      `json:"version"`
	TargetKind                    string   `json:"target_kind"`
	TargetProvider                string   `json:"target_provider"`
	TargetID                      string   `json:"target_id"`
	AccessModel                   string   `json:"access_model"`
	Broker                        string   `json:"broker"`
	InboundPublicSSH              bool     `json:"inbound_public_ssh"`
	RequiredIAMActions            []string `json:"required_iam_actions"`
	RequiredInstanceProfilePolicy string   `json:"required_instance_profile_policy"`
	LiveSmokeValidation           string   `json:"live_smoke_validation"`
}

func DefaultAWSEC2SSMContract() Contract {
	return DefaultContractForProvider(AWSEC2SSMProvider)
}

func NewAWSEC2SSMTarget() (FakeTarget, error) {
	return NewFakeTarget(DefaultAWSEC2SSMContract())
}

func DefaultAWSEC2SSMBrokeredAccessPlan(targetID string) BrokeredAccessPlan {
	return BrokeredAccessPlan{
		Version:          1,
		TargetKind:       TargetKind,
		TargetProvider:   AWSEC2SSMProvider,
		TargetID:         targetID,
		AccessModel:      AccessModel,
		Broker:           AWSSessionBroker,
		InboundPublicSSH: false,
		RequiredIAMActions: []string{
			"ec2:DescribeInstances",
			"ssm:DescribeInstanceInformation",
			"ssm:ResumeSession",
			"ssm:StartSession",
			"ssm:TerminateSession",
		},
		RequiredInstanceProfilePolicy: AWSSSMManagedInstanceCorePolicy,
		LiveSmokeValidation:           AWSCertificationLane,
	}
}
