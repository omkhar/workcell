// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package hostutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateSupportMatrixMatchesReviewedRow(t *testing.T) {
	t.Parallel()

	path := writeSupportMatrixFixture(t)
	result, err := EvaluateSupportMatrix(path, SupportMatrixQuery{
		HostOS:               "macos",
		HostArch:             "arm64",
		TargetKind:           "local_vm",
		TargetProvider:       "colima",
		TargetAssuranceClass: "strict",
	})
	if err != nil {
		t.Fatalf("EvaluateSupportMatrix() error = %v", err)
	}
	if result.Status != "supported" {
		t.Fatalf("status = %q, want supported", result.Status)
	}
	if result.Launch != "allowed" {
		t.Fatalf("launch = %q, want allowed", result.Launch)
	}
	if result.Evidence != "certification-only" {
		t.Fatalf("evidence = %q, want certification-only", result.Evidence)
	}
	if result.ValidationLane != "none" {
		t.Fatalf("validation_lane = %q, want none", result.ValidationLane)
	}
}

func TestEvaluateSupportMatrixReturnsValidationHostLane(t *testing.T) {
	t.Parallel()

	path := writeSupportMatrixFixture(t)
	result, err := EvaluateSupportMatrix(path, SupportMatrixQuery{
		HostOS:               "linux",
		HostArch:             "amd64",
		TargetKind:           "local_vm",
		TargetProvider:       "colima",
		TargetAssuranceClass: "strict",
	})
	if err != nil {
		t.Fatalf("EvaluateSupportMatrix() error = %v", err)
	}
	if result.Status != "validation-host-only" {
		t.Fatalf("status = %q, want validation-host-only", result.Status)
	}
	if result.Launch != "blocked" {
		t.Fatalf("launch = %q, want blocked", result.Launch)
	}
	if result.Evidence != "repo-required" {
		t.Fatalf("evidence = %q, want repo-required", result.Evidence)
	}
	if result.ValidationLane != "trusted-linux-amd64-validator" {
		t.Fatalf("validation_lane = %q, want trusted-linux-amd64-validator", result.ValidationLane)
	}
}

func TestEvaluateSupportMatrixReturnsPreviewOnlyRow(t *testing.T) {
	t.Parallel()

	path := writeSupportMatrixFixture(t)
	result, err := EvaluateSupportMatrix(path, SupportMatrixQuery{
		HostOS:               "macos",
		HostArch:             "arm64",
		TargetKind:           "remote_vm",
		TargetProvider:       "aws-ec2-ssm",
		TargetAssuranceClass: "compat",
	})
	if err != nil {
		t.Fatalf("EvaluateSupportMatrix() error = %v", err)
	}
	if result.Status != "preview-only" {
		t.Fatalf("status = %q, want preview-only", result.Status)
	}
	if result.Launch != "blocked" {
		t.Fatalf("launch = %q, want blocked", result.Launch)
	}
	if result.Evidence != "certification-only" {
		t.Fatalf("evidence = %q, want certification-only", result.Evidence)
	}
	if result.ValidationLane != "none" {
		t.Fatalf("validation_lane = %q, want none", result.ValidationLane)
	}
}

func TestEvaluateSupportMatrixDefaultsToUnsupported(t *testing.T) {
	t.Parallel()

	path := writeSupportMatrixFixture(t)
	result, err := EvaluateSupportMatrix(path, SupportMatrixQuery{
		HostOS:               "windows",
		HostArch:             "amd64",
		TargetKind:           "local_vm",
		TargetProvider:       "colima",
		TargetAssuranceClass: "strict",
	})
	if err != nil {
		t.Fatalf("EvaluateSupportMatrix() error = %v", err)
	}
	if result.Status != "unsupported" {
		t.Fatalf("status = %q, want unsupported", result.Status)
	}
	if result.Launch != "blocked" {
		t.Fatalf("launch = %q, want blocked", result.Launch)
	}
	if result.Evidence != "none" {
		t.Fatalf("evidence = %q, want none", result.Evidence)
	}
	if result.ValidationLane != "none" {
		t.Fatalf("validation_lane = %q, want none", result.ValidationLane)
	}
	if result.Reason != "not-in-reviewed-support-matrix" {
		t.Fatalf("reason = %q, want not-in-reviewed-support-matrix", result.Reason)
	}
}

func TestEvaluateSupportMatrixRejectsInvalidRows(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "host-support-matrix.tsv")
	if err := os.WriteFile(path, []byte(strings.Join([]string{
		"host_os\thost_arch\ttarget_kind\ttarget_provider\ttarget_assurance_class\tstatus\tlaunch\tevidence\tvalidation_lane\treason",
		"linux\tamd64\tlocal_vm\tcolima\tstrict\tvalidation-host-only\tblocked\trepo-required\tnone\tbad-row",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := EvaluateSupportMatrix(path, SupportMatrixQuery{
		HostOS:               "linux",
		HostArch:             "amd64",
		TargetKind:           "local_vm",
		TargetProvider:       "colima",
		TargetAssuranceClass: "strict",
	})
	if err == nil || !strings.Contains(err.Error(), "validation-host-only rows must name a validation_lane") {
		t.Fatalf("EvaluateSupportMatrix() error = %v, want validation-lane rejection", err)
	}
}

func writeSupportMatrixFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "host-support-matrix.tsv")
	content := strings.Join([]string{
		"# reviewed host support matrix fixture",
		"host_os\thost_arch\ttarget_kind\ttarget_provider\ttarget_assurance_class\tstatus\tlaunch\tevidence\tvalidation_lane\treason",
		"macos\tarm64\tlocal_vm\tcolima\tstrict\tsupported\tallowed\tcertification-only\tnone\tapple-silicon-macos-reviewed-launch-host",
		"macos\tarm64\tremote_vm\taws-ec2-ssm\tcompat\tpreview-only\tblocked\tcertification-only\tnone\tapple-silicon-macos-aws-ec2-ssm-preview-certification-only",
		"linux\tamd64\tlocal_vm\tcolima\tstrict\tvalidation-host-only\tblocked\trepo-required\ttrusted-linux-amd64-validator\ttrusted-linux-amd64-validation-host-only",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
