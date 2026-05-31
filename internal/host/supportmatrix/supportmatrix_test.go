// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportmatrix

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEvaluateMatchesReviewedRow(t *testing.T) {
	t.Parallel()

	path := writeSupportMatrixFixture(t)
	result, err := Evaluate(path, Query{
		HostOS:               "macos",
		HostArch:             "arm64",
		HostDistro:           "none",
		HostDistroVersion:    "none",
		TargetKind:           "local_vm",
		TargetProvider:       "colima",
		TargetAssuranceClass: "strict",
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
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

func TestEvaluateReturnsValidationHostLane(t *testing.T) {
	t.Parallel()

	path := writeSupportMatrixFixture(t)
	result, err := Evaluate(path, Query{
		HostOS:               "linux",
		HostArch:             "amd64",
		HostDistro:           "debian",
		HostDistroVersion:    "13",
		TargetKind:           "local_vm",
		TargetProvider:       "colima",
		TargetAssuranceClass: "strict",
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
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
	if result.HostDistro != "debian" || result.HostDistroVersion != "13" {
		t.Fatalf("host distro = %q/%q, want debian/13", result.HostDistro, result.HostDistroVersion)
	}
}

func TestEvaluateReturnsPreviewOnlyRow(t *testing.T) {
	t.Parallel()

	path := writeSupportMatrixFixture(t)
	for _, provider := range []string{"aws-ec2-ssm", "gcp-vm"} {
		provider := provider
		t.Run(provider, func(t *testing.T) {
			t.Parallel()

			result, err := Evaluate(path, Query{
				HostOS:               "macos",
				HostArch:             "arm64",
				HostDistro:           "none",
				HostDistroVersion:    "none",
				TargetKind:           "remote_vm",
				TargetProvider:       provider,
				TargetAssuranceClass: "compat",
			})
			if err != nil {
				t.Fatalf("Evaluate() error = %v", err)
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
		})
	}
}

func TestEvaluateDefaultsToUnsupported(t *testing.T) {
	t.Parallel()

	path := writeSupportMatrixFixture(t)
	result, err := Evaluate(path, Query{
		HostOS:               "windows",
		HostArch:             "amd64",
		HostDistro:           "none",
		HostDistroVersion:    "none",
		TargetKind:           "local_vm",
		TargetProvider:       "colima",
		TargetAssuranceClass: "strict",
	})
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
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

func TestEvaluateRejectsInvalidRows(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "host-support-matrix.tsv")
	if err := os.WriteFile(path, []byte(strings.Join([]string{
		"host_os\thost_arch\thost_distro\thost_distro_version\ttarget_kind\ttarget_provider\ttarget_assurance_class\tstatus\tlaunch\tevidence\tvalidation_lane\treason",
		"linux\tamd64\tany\tany\tlocal_vm\tcolima\tstrict\tvalidation-host-only\tblocked\trepo-required\tnone\tbad-row",
	}, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Evaluate(path, Query{
		HostOS:               "linux",
		HostArch:             "amd64",
		HostDistro:           "debian",
		HostDistroVersion:    "13",
		TargetKind:           "local_vm",
		TargetProvider:       "colima",
		TargetAssuranceClass: "strict",
	})
	if err == nil || !strings.Contains(err.Error(), "validation-host-only rows must name a validation_lane") {
		t.Fatalf("Evaluate() error = %v, want validation-lane rejection", err)
	}
}

func writeSupportMatrixFixture(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "host-support-matrix.tsv")
	content := strings.Join([]string{
		"# reviewed host support matrix fixture",
		"host_os\thost_arch\thost_distro\thost_distro_version\ttarget_kind\ttarget_provider\ttarget_assurance_class\tstatus\tlaunch\tevidence\tvalidation_lane\treason",
		"macos\tarm64\tnone\tnone\tlocal_vm\tcolima\tstrict\tsupported\tallowed\tcertification-only\tnone\tapple-silicon-macos-reviewed-launch-host",
		"macos\tarm64\tnone\tnone\tremote_vm\taws-ec2-ssm\tcompat\tpreview-only\tblocked\tcertification-only\tnone\tapple-silicon-macos-aws-ec2-ssm-preview-certification-only",
		"macos\tarm64\tnone\tnone\tremote_vm\tgcp-vm\tcompat\tpreview-only\tblocked\tcertification-only\tnone\tapple-silicon-macos-gcp-vm-preview-certification-only",
		"linux\tamd64\tany\tany\tlocal_vm\tcolima\tstrict\tvalidation-host-only\tblocked\trepo-required\ttrusted-linux-amd64-validator\ttrusted-linux-amd64-validation-host-only",
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
