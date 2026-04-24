// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/omkhar/workcell/internal/hostutil"
)

func TestRunConformanceWithFakeTarget(t *testing.T) {
	t.Parallel()

	target, err := NewFakeTarget(DefaultContract())
	if err != nil {
		t.Fatal(err)
	}
	caseSpec := DefaultConformanceCase(t.TempDir(), filepath.Join("testdata", "source-workspace"))
	result, err := RunConformance(context.Background(), target, DefaultContract(), caseSpec)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Exported.Session.TargetKind, "remote_vm"; got != want {
		t.Fatalf("exported session target_kind = %q, want %q", got, want)
	}
	if got, want := result.Exported.Session.WorkspaceTransport, "remote-materialization"; got != want {
		t.Fatalf("exported session workspace_transport = %q, want %q", got, want)
	}
	if got, want := hostutil.SessionTargetSummary(result.Exported.Session), "remote_vm/fake-remote/"+caseSpec.TargetID; got != want {
		t.Fatalf("target summary = %q, want %q", got, want)
	}
}

func TestRunConformanceWithAWSEC2SSMTarget(t *testing.T) {
	t.Parallel()

	target, err := NewAWSEC2SSMTarget()
	if err != nil {
		t.Fatal(err)
	}
	caseSpec := DefaultConformanceCase(t.TempDir(), filepath.Join("testdata", "source-workspace"))
	caseSpec.TargetID = "i-1234567890abcdef0"
	result, err := RunConformance(context.Background(), target, DefaultAWSEC2SSMContract(), caseSpec)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Exported.Session.TargetProvider, AWSEC2SSMProvider; got != want {
		t.Fatalf("exported session target_provider = %q, want %q", got, want)
	}
	if got, want := hostutil.SessionTargetSummary(result.Exported.Session), "remote_vm/aws-ec2-ssm/"+caseSpec.TargetID; got != want {
		t.Fatalf("target summary = %q, want %q", got, want)
	}
	if got, want := filepath.Base(filepath.Dir(result.Materialization.TargetRoot)), AWSEC2SSMProvider; got != want {
		t.Fatalf("materialization provider root = %q, want %q", got, want)
	}
}

func TestDefaultAWSEC2SSMBrokeredAccessPlan(t *testing.T) {
	t.Parallel()

	plan := DefaultAWSEC2SSMBrokeredAccessPlan("i-1234567890abcdef0")
	if plan.TargetProvider != AWSEC2SSMProvider {
		t.Fatalf("target_provider = %q, want %q", plan.TargetProvider, AWSEC2SSMProvider)
	}
	if plan.Broker != AWSSessionBroker {
		t.Fatalf("broker = %q, want %q", plan.Broker, AWSSessionBroker)
	}
	if plan.InboundPublicSSH {
		t.Fatal("InboundPublicSSH = true, want false")
	}
	if plan.LiveSmokeValidation != AWSCertificationLane {
		t.Fatalf("live_smoke_validation = %q, want %q", plan.LiveSmokeValidation, AWSCertificationLane)
	}
}
