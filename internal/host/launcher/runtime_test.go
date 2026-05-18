// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"strings"
	"testing"
)

func TestValidateSecurityOptionsRequiresSeccomp(t *testing.T) {
	t.Parallel()

	err := ValidateSecurityOptions(`["name=apparmor","name=cgroupns"]`)
	if err == nil {
		t.Fatal("ValidateSecurityOptions accepted daemon options missing seccomp")
	}
	if !strings.Contains(err.Error(), "seccomp") {
		t.Fatalf("ValidateSecurityOptions error = %v, want seccomp rejection", err)
	}
}

func TestValidateSecurityOptionsRequiresMAC(t *testing.T) {
	t.Parallel()

	err := ValidateSecurityOptions(`["name=seccomp,profile=builtin","name=cgroupns"]`)
	if err == nil {
		t.Fatal("ValidateSecurityOptions accepted daemon options missing AppArmor/SELinux")
	}
	if !strings.Contains(err.Error(), "AppArmor or SELinux") {
		t.Fatalf("ValidateSecurityOptions error = %v, want AppArmor/SELinux rejection", err)
	}
}

func TestValidateSecurityOptionsAcceptsAppArmorSeccomp(t *testing.T) {
	t.Parallel()

	if err := ValidateSecurityOptions(`["name=apparmor","name=seccomp,profile=builtin","name=cgroupns"]`); err != nil {
		t.Fatalf("ValidateSecurityOptions error = %v, want nil for AppArmor+seccomp daemon", err)
	}
}

func TestValidateSecurityOptionsAcceptsSELinuxSeccomp(t *testing.T) {
	t.Parallel()

	if err := ValidateSecurityOptions(`["name=seccomp,profile=builtin","name=selinux","name=cgroupns"]`); err != nil {
		t.Fatalf("ValidateSecurityOptions error = %v, want nil for SELinux+seccomp daemon", err)
	}
}

func TestValidateCompatSecurityOptionsAcceptsSeccompOnly(t *testing.T) {
	t.Parallel()

	if err := ValidateCompatSecurityOptions(`["name=seccomp,profile=builtin","name=cgroupns"]`); err != nil {
		t.Fatalf("ValidateCompatSecurityOptions error = %v, want nil for Docker Desktop compat daemon", err)
	}
}

func TestValidateCompatSecurityOptionsRequiresSeccomp(t *testing.T) {
	t.Parallel()

	err := ValidateCompatSecurityOptions(`["name=cgroupns"]`)
	if err == nil {
		t.Fatal("ValidateCompatSecurityOptions accepted daemon options missing seccomp")
	}
	if !strings.Contains(err.Error(), "seccomp") {
		t.Fatalf("ValidateCompatSecurityOptions error = %v, want seccomp rejection", err)
	}
}

func TestValidateContainerSecurityOptionsRequiresNoNewPrivileges(t *testing.T) {
	t.Parallel()

	err := ValidateContainerSecurityOptions(`[]`)
	if err == nil {
		t.Fatal("ValidateContainerSecurityOptions accepted empty HostConfig.SecurityOpt")
	}
	if !strings.Contains(err.Error(), "no-new-privileges") {
		t.Fatalf("ValidateContainerSecurityOptions error = %v, want no-new-privileges rejection", err)
	}
}

func TestValidateContainerSecurityOptionsRejectsNullSecurityOpt(t *testing.T) {
	t.Parallel()

	err := ValidateContainerSecurityOptions("null")
	if err == nil {
		t.Fatal("ValidateContainerSecurityOptions accepted null HostConfig.SecurityOpt")
	}
}

func TestValidateContainerSecurityOptionsRejectsUnconfinedSeccomp(t *testing.T) {
	t.Parallel()

	err := ValidateContainerSecurityOptions(`["no-new-privileges:true","seccomp=unconfined"]`)
	if err == nil {
		t.Fatal("ValidateContainerSecurityOptions accepted seccomp=unconfined")
	}
	if !strings.Contains(err.Error(), "seccomp=unconfined") {
		t.Fatalf("ValidateContainerSecurityOptions error = %v, want seccomp=unconfined rejection", err)
	}
}

func TestValidateContainerSecurityOptionsRejectsUnconfinedAppArmor(t *testing.T) {
	t.Parallel()

	err := ValidateContainerSecurityOptions(`["no-new-privileges:true","apparmor=unconfined"]`)
	if err == nil {
		t.Fatal("ValidateContainerSecurityOptions accepted apparmor=unconfined")
	}
}

func TestValidateContainerSecurityOptionsRejectsDisabledNoNewPrivileges(t *testing.T) {
	t.Parallel()

	err := ValidateContainerSecurityOptions(`["no-new-privileges:false"]`)
	if err == nil {
		t.Fatal("ValidateContainerSecurityOptions accepted no-new-privileges:false")
	}
}

func TestValidateContainerSecurityOptionsRejectsSELinuxDisable(t *testing.T) {
	t.Parallel()

	err := ValidateContainerSecurityOptions(`["no-new-privileges:true","label=disable"]`)
	if err == nil {
		t.Fatal("ValidateContainerSecurityOptions accepted label=disable")
	}
}

func TestValidateContainerSecurityOptionsAcceptsCanonical(t *testing.T) {
	t.Parallel()

	if err := ValidateContainerSecurityOptions(`["no-new-privileges:true"]`); err != nil {
		t.Fatalf("ValidateContainerSecurityOptions error = %v, want nil for canonical HostConfig.SecurityOpt", err)
	}
}
