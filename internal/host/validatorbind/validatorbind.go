// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

// Package validatorbind proves that a selected Docker daemon sees the exact
// checkout a validator-backed lane intends to mount.
package validatorbind

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	validatorBindProbeTimeout   = 30 * time.Second
	validatorBindCleanupTimeout = 5 * time.Second
)

var errValidatorBindProbeTimeout = errors.New("validator workspace bind probe timed out")

const probeScript = `
set -euo pipefail
test -f /workspace/go.mod
test -x /workspace/scripts/validate-repo.sh
test "$(cat "/workspace/${WORKCELL_VALIDATOR_BIND_CHALLENGE_NAME}")" = "${WORKCELL_VALIDATOR_BIND_CHALLENGE_VALUE}"
`

// Options describes one validator workspace visibility proof.
type Options struct {
	DockerBinary    string
	Image           string
	Workspace       string
	Context         string
	ContextExplicit bool
}

type commandFunc func(context.Context, string, string, []string) error

// Require proves that the selected Docker daemon sees the exact canonical
// workspace and its per-invocation challenge.
func Require(ctx context.Context, options Options) error {
	return require(ctx, options, runCommand)
}

func require(ctx context.Context, options Options, command commandFunc) (result error) {
	return requireWithProbeTimeout(ctx, options, command, validatorBindProbeTimeout)
}

func requireWithProbeTimeout(ctx context.Context, options Options, command commandFunc, timeout time.Duration) (result error) {
	if !filepath.IsAbs(options.DockerBinary) {
		return fmt.Errorf("validator Docker binary must be an absolute path")
	}
	if options.Image == "" {
		return fmt.Errorf("validator image is required")
	}
	workspace, err := canonicalWorkspace(options.Workspace)
	if err != nil {
		return err
	}

	challenge, err := os.CreateTemp(workspace, ".workcell-validator-bind.")
	if err != nil {
		return fmt.Errorf("cannot create the validator workspace bind challenge: %s: %w", workspace, err)
	}
	challengePath := challenge.Name()
	defer func() {
		if err := os.Remove(challengePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, fmt.Errorf("remove validator workspace bind challenge: %w", err))
		}
	}()

	value, err := randomValue()
	if err == nil {
		_, err = fmt.Fprintln(challenge, value)
	}
	if closeErr := challenge.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("write validator workspace bind challenge: %w", err)
	}
	containerName := "workcell-validator-bind-" + value
	// The challenge is a random non-secret freshness token. It must be readable
	// when a rootless or userns-remapped daemon changes bind-mount ownership.
	if err := os.Chmod(challengePath, 0o644); err != nil {
		return fmt.Errorf("make validator workspace bind challenge readable: %w", err)
	}
	mount, err := MountSpec(workspace, true)
	if err != nil {
		return err
	}

	args := make([]string, 0, 24)
	if options.Context != "" {
		args = append(args, "--context", options.Context)
	}
	args = append(args,
		"run", "--rm", "--name", containerName,
		"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--entrypoint", "/bin/bash",
		"--mount", mount,
		"-e", "WORKCELL_VALIDATOR_BIND_CHALLENGE_NAME="+filepath.Base(challengePath),
		"-e", "WORKCELL_VALIDATOR_BIND_CHALLENGE_VALUE="+value,
		options.Image,
		"-c", probeScript,
	)
	probeCtx, cancel := context.WithTimeoutCause(ctx, timeout, errValidatorBindProbeTimeout)
	defer cancel()
	commandErr := command(probeCtx, workspace, options.DockerBinary, args)
	if parentErr := ctx.Err(); parentErr != nil {
		return withProbeCleanup(parentErr, command, workspace, options, containerName)
	}
	if errors.Is(context.Cause(probeCtx), errValidatorBindProbeTimeout) {
		probeErr := fmt.Errorf("validator workspace bind probe after %s: %w", timeout, errValidatorBindProbeTimeout)
		return withProbeCleanup(probeErr, command, workspace, options, containerName)
	}
	if commandErr != nil {
		contextLabel := options.Context
		if contextLabel == "" {
			contextLabel = "default"
		}
		message := fmt.Sprintf(
			"Validator workspace is not visible through Docker context %s: %s",
			contextLabel,
			workspace,
		)
		if options.ContextExplicit {
			message += fmt.Sprintf(
				"\nConfigured WORKCELL_DOCKER_CONTEXT=%s cannot bind this checkout; choose a context that can.",
				options.Context,
			)
		} else {
			message += "\nSelect a Docker context whose daemon can bind this checkout, or set WORKCELL_DOCKER_CONTEXT explicitly."
		}
		return fmt.Errorf("%s", message)
	}
	return nil
}

func withProbeCleanup(primary error, command commandFunc, workspace string, options Options, containerName string) error {
	cleanupCtx, cancel := context.WithTimeout(context.Background(), validatorBindCleanupTimeout)
	defer cancel()
	args := make([]string, 0, 5)
	if options.Context != "" {
		args = append(args, "--context", options.Context)
	}
	args = append(args, "rm", "--force", containerName)
	if err := command(cleanupCtx, workspace, options.DockerBinary, args); err != nil {
		return errors.Join(primary, fmt.Errorf("remove timed-out validator workspace bind probe: %w", err))
	}
	return primary
}

// MountSpec returns a Docker --mount CSV record for the workspace bind.
func MountSpec(workspace string, readOnly bool) (string, error) {
	if !filepath.IsAbs(workspace) {
		return "", fmt.Errorf("validator workspace must be an absolute path")
	}
	fields := []string{"type=bind", "src=" + workspace, "dst=/workspace"}
	if readOnly {
		fields = append(fields, "readonly")
	}
	var buffer bytes.Buffer
	writer := csv.NewWriter(&buffer)
	if err := writer.Write(fields); err != nil {
		return "", fmt.Errorf("encode validator workspace mount: %w", err)
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return "", fmt.Errorf("encode validator workspace mount: %w", err)
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

func canonicalWorkspace(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve validator workspace: %w", err)
	}
	workspace, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", fmt.Errorf("Validator workspace does not exist: %s", path)
	}
	info, err := os.Stat(workspace)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("Validator workspace does not exist: %s", path)
	}
	goMod, goModErr := os.Stat(filepath.Join(workspace, "go.mod"))
	validator, validatorErr := os.Stat(filepath.Join(workspace, "scripts", "validate-repo.sh"))
	if goModErr != nil || !goMod.Mode().IsRegular() ||
		validatorErr != nil || !validator.Mode().IsRegular() || validator.Mode().Perm()&0o111 == 0 {
		return "", fmt.Errorf("Validator workspace is missing required validation inputs: %s", workspace)
	}
	return workspace, nil
}

func randomValue() (string, error) {
	value := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func runCommand(ctx context.Context, dir, binary string, args []string) error {
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = dir
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	return command.Run()
}
