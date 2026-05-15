// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestDockerDesktopContextNameConstant(t *testing.T) {
	t.Parallel()

	if DockerDesktopContextName != "desktop-linux" {
		t.Fatalf("DockerDesktopContextName = %q, want desktop-linux", DockerDesktopContextName)
	}
}

func TestRouteProfileDockerCommand(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		provider    string
		socketPath  string
		contextName string
		want        DockerCommandRoute
		wantErr     string
	}{
		{
			name:       "colima emits DOCKER_HOST env",
			provider:   "colima",
			socketPath: "/state/wcl-profile/docker.sock",
			want: DockerCommandRoute{
				Provider:  "colima",
				EnvPrefix: []string{"env", "DOCKER_HOST=unix:///state/wcl-profile/docker.sock"},
			},
		},
		{
			name:        "docker-desktop emits DOCKER_CONTEXT env",
			provider:    "docker-desktop",
			contextName: "desktop-linux",
			want: DockerCommandRoute{
				Provider:  "docker-desktop",
				EnvPrefix: []string{"env", "DOCKER_CONTEXT=desktop-linux"},
			},
		},
		{
			name:     "colima without socket path errors",
			provider: "colima",
			wantErr:  "socket path is required",
		},
		{
			name:     "docker-desktop without context name errors",
			provider: "docker-desktop",
			wantErr:  "context name is required",
		},
		{
			name:     "unsupported provider matches bash diagnostic byte-for-byte",
			provider: "aws-ec2-ssm",
			wantErr:  "Unsupported target provider for Docker command routing: aws-ec2-ssm",
		},
		{
			name:     "empty provider also unsupported",
			provider: "",
			wantErr:  "Unsupported target provider for Docker command routing: ",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := RouteProfileDockerCommand(tc.provider, tc.socketPath, tc.contextName)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not contain %q", err.Error(), tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("RouteProfileDockerCommand = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPrepareCurrentDockerClientPlan(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name           string
		backend        string
		contextName    string
		contextExists  bool
		contextHealthy bool
		wantMode       PrepareDockerClientMode
		wantContext    string
		wantErr        string
		wantPlanErr    bool
	}{
		{name: "colima -> noop", backend: "colima", wantMode: PrepareDockerClientModeNoop},
		{name: "aws-ec2-ssm -> noop", backend: "aws-ec2-ssm", wantMode: PrepareDockerClientModeNoop},
		{name: "gcp-vm -> noop", backend: "gcp-vm", wantMode: PrepareDockerClientModeNoop},
		{
			name:           "docker-desktop healthy context yields directive",
			backend:        "docker-desktop",
			contextName:    "desktop-linux",
			contextExists:  true,
			contextHealthy: true,
			wantMode:       PrepareDockerClientModeDockerDesktop,
			wantContext:    "desktop-linux",
		},
		{
			name:        "docker-desktop missing context errors with bash diagnostic",
			backend:     "docker-desktop",
			contextName: "desktop-linux",
			wantPlanErr: true,
		},
		{
			name:           "docker-desktop unhealthy context errors",
			backend:        "docker-desktop",
			contextName:    "desktop-linux",
			contextExists:  true,
			contextHealthy: false,
			wantPlanErr:    true,
		},
		{
			name:        "docker-desktop without context name programmer error",
			backend:     "docker-desktop",
			contextName: "",
			wantErr:     "context name is required",
		},
		{
			name:    "unknown backend programmer error",
			backend: "kubernetes",
			wantErr: "unsupported target backend",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			plan, err := PrepareCurrentDockerClientPlan(tc.backend, tc.contextName, tc.contextExists, tc.contextHealthy)
			if tc.wantPlanErr {
				var planErr *PrepareDockerClientPlanError
				if !errors.As(err, &planErr) {
					t.Fatalf("expected PrepareDockerClientPlanError, got %v", err)
				}
				if planErr.ContextName != tc.contextName {
					t.Fatalf("plan error context = %q, want %q", planErr.ContextName, tc.contextName)
				}
				if !strings.Contains(planErr.Error(), "Docker Desktop target requires a healthy docker context named "+tc.contextName) {
					t.Fatalf("plan error message %q lacks bash diagnostic prefix", planErr.Error())
				}
				if !strings.Contains(planErr.Error(), "Enable Docker Desktop") {
					t.Fatalf("plan error message %q lacks bash followup line", planErr.Error())
				}
				return
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if plan.Mode != tc.wantMode {
				t.Fatalf("plan.Mode = %q, want %q", plan.Mode, tc.wantMode)
			}
			if plan.ContextName != tc.wantContext {
				t.Fatalf("plan.ContextName = %q, want %q", plan.ContextName, tc.wantContext)
			}
		})
	}
}
