// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package launcher

import (
	"errors"
	"fmt"
)

// DockerDesktopContextName returns the docker context name workcell
// expects when the active target backend is Docker Desktop.  Mirrors
// the bash docker_desktop_context_name helper, which has always been a
// constant of "desktop-linux".
const DockerDesktopContextName = "desktop-linux"

// DockerCommandRoute describes how to invoke a docker subcommand
// targeted at a specific profile's runtime: the env-prefix tokens to
// place before the docker binary plus the resolved provider name.
// Mirrors the case-statement at the heart of run_profile_docker_command
// in scripts/workcell.
type DockerCommandRoute struct {
	// Provider is the resolved target provider, e.g. "colima" or
	// "docker-desktop".
	Provider string
	// EnvPrefix is the sequence of tokens that should precede the
	// docker binary on the command line.  For example
	// {"env", "DOCKER_HOST=unix:///.../docker.sock"}.
	EnvPrefix []string
}

// RouteProfileDockerCommand returns the env prefix that scripts/workcell
// must place before HOST_DOCKER_BIN when dispatching a docker command at
// the given profile.  socketPath is required for the colima provider;
// contextName is required for docker-desktop.  An unrecognised provider
// returns an error whose Error() message is byte-identical to the bash
// "Unsupported target provider for Docker command routing" diagnostic.
func RouteProfileDockerCommand(provider, socketPath, contextName string) (DockerCommandRoute, error) {
	switch provider {
	case "colima":
		if socketPath == "" {
			return DockerCommandRoute{}, errors.New("RouteProfileDockerCommand: socket path is required for colima provider")
		}
		return DockerCommandRoute{
			Provider:  provider,
			EnvPrefix: []string{"env", "DOCKER_HOST=unix://" + socketPath},
		}, nil
	case "docker-desktop":
		if contextName == "" {
			return DockerCommandRoute{}, errors.New("RouteProfileDockerCommand: context name is required for docker-desktop provider")
		}
		return DockerCommandRoute{
			Provider:  provider,
			EnvPrefix: []string{"env", "DOCKER_CONTEXT=" + contextName},
		}, nil
	default:
		return DockerCommandRoute{}, fmt.Errorf("Unsupported target provider for Docker command routing: %s", provider)
	}
}

// PrepareDockerClientMode encodes the behaviour
// prepare_current_target_docker_client must apply for a given target
// backend.  scripts/workcell consumes it as a textual directive on
// stdout (one of "noop", "docker-desktop", or "abort").  When the mode
// is "docker-desktop" the plan also carries the context name that bash
// must export as DOCKER_CONTEXT.
type PrepareDockerClientMode string

const (
	// PrepareDockerClientModeNoop indicates the bash helper should
	// leave the docker client environment untouched after the
	// initial sanitize call.  Matches the colima / aws-ec2-ssm /
	// gcp-vm bash cases.
	PrepareDockerClientModeNoop PrepareDockerClientMode = "noop"

	// PrepareDockerClientModeDockerDesktop indicates the bash
	// helper should export DOCKER_CONTEXT to the plan's context
	// name and unset DOCKER_HOST.  Matches the docker-desktop bash
	// branch.
	PrepareDockerClientModeDockerDesktop PrepareDockerClientMode = "docker-desktop"
)

// PrepareDockerClientPlan describes the env actions
// prepare_current_target_docker_client must perform.
type PrepareDockerClientPlan struct {
	Mode        PrepareDockerClientMode
	ContextName string
}

// PrepareDockerClientPlanError is returned when the docker-desktop
// context check fails.  scripts/workcell expects this case to exit with
// status 2 after printing two stderr lines, matching the original bash
// case-block.
type PrepareDockerClientPlanError struct {
	ContextName string
}

func (e *PrepareDockerClientPlanError) Error() string {
	return fmt.Sprintf(
		"Docker Desktop target requires a healthy docker context named %s.\nEnable Docker Desktop and make sure the %s context is available before retrying.",
		e.ContextName, e.ContextName,
	)
}

// PrepareCurrentDockerClientPlan derives the plan
// prepare_current_target_docker_client should apply.  contextExists
// and contextHealthy are only consulted when backend == "docker-desktop";
// callers may pass false unconditionally for the other backends.
func PrepareCurrentDockerClientPlan(backend, contextName string, contextExists, contextHealthy bool) (PrepareDockerClientPlan, error) {
	switch backend {
	case "colima", "aws-ec2-ssm", "gcp-vm":
		return PrepareDockerClientPlan{Mode: PrepareDockerClientModeNoop}, nil
	case "docker-desktop":
		if contextName == "" {
			return PrepareDockerClientPlan{}, errors.New("PrepareCurrentDockerClientPlan: context name is required for docker-desktop backend")
		}
		if !contextExists || !contextHealthy {
			return PrepareDockerClientPlan{}, &PrepareDockerClientPlanError{ContextName: contextName}
		}
		return PrepareDockerClientPlan{Mode: PrepareDockerClientModeDockerDesktop, ContextName: contextName}, nil
	default:
		return PrepareDockerClientPlan{}, fmt.Errorf("PrepareCurrentDockerClientPlan: unsupported target backend %q", backend)
	}
}
