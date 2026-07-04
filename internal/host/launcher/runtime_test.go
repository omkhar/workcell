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

// TestSubtractEndpointListRemovesDeniedEndpoints pins the A1 operator-tightening
// contract: SubtractEndpointList removes exactly the denied endpoints from the
// allow list (deny wins), preserves the surviving order, and never adds an
// endpoint — so it can only ever tighten the allowlist, never weaken it.
func TestSubtractEndpointListRemovesDeniedEndpoints(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		allow string
		deny  string
		want  string
	}{
		{
			name:  "deny wins over a provider-required endpoint",
			allow: "github.com:443 chatgpt.com:443 api.github.com:443",
			deny:  "chatgpt.com:443",
			want:  "github.com:443 api.github.com:443",
		},
		{
			name:  "deny matches provider endpoint case-insensitively",
			allow: "github.com:443 chatgpt.com:443",
			deny:  "CHATGPT.COM:443",
			want:  "github.com:443",
		},
		{
			name:  "mixed-case allow removed by lower-case deny",
			allow: "API.GitHub.com:443 github.com:443",
			deny:  "api.github.com:443",
			want:  "github.com:443",
		},
		{
			name:  "empty deny leaves allow untouched",
			allow: "github.com:443 api.github.com:443",
			deny:  "",
			want:  "github.com:443 api.github.com:443",
		},
		{
			name:  "deny entry not present is a no-op",
			allow: "github.com:443",
			deny:  "not-in-allow.example:443",
			want:  "github.com:443",
		},
		{
			name:  "multiple denies",
			allow: "a.example:443 b.example:443 c.example:443",
			deny:  "a.example:443 c.example:443",
			want:  "b.example:443",
		},
		{
			name:  "deny cannot add endpoints",
			allow: "",
			deny:  "attacker.example:443",
			want:  "",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SubtractEndpointList(tc.allow, tc.deny); got != tc.want {
				t.Fatalf("SubtractEndpointList(%q, %q) = %q, want %q", tc.allow, tc.deny, got, tc.want)
			}
		})
	}
}
