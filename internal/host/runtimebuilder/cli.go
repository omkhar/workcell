// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package runtimebuilder

import (
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
)

// Main runs the internal launcher-facing runtime builder lifecycle CLI.
func Main(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return usageError()
	}
	flags := flag.NewFlagSet("runtime-builder-cli", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var cfg Config
	flags.StringVar(&cfg.Profile, "profile", "", "")
	flags.StringVar(&cfg.Backend, "backend", "", "")
	flags.StringVar(&cfg.TargetKind, "target-kind", "", "")
	flags.StringVar(&cfg.TargetStateRoot, "target-state-root", "", "")
	flags.StringVar(&cfg.ColimaStateRoot, "colima-state-root", "", "")
	flags.StringVar(&cfg.Workspace, "workspace", "", "")
	flags.StringVar(&cfg.DockerHost, "docker-host", "", "")
	flags.StringVar(&cfg.DockerContext, "docker-context", "", "")
	flags.StringVar(&cfg.DockerEndpoint, "docker-endpoint", "", "")
	flags.StringVar(&cfg.DockerBin, "docker-bin", "", "")
	flags.StringVar(&cfg.DockerConfig, "docker-config", "", "")
	flags.StringVar(&cfg.DockerHome, "docker-home", "", "")
	flags.StringVar(&cfg.RealHome, "real-home", "", "")
	flags.StringVar(&cfg.DockerCWD, "docker-cwd", "", "")
	flags.StringVar(&cfg.BuildxBin, "buildx-bin", "", "")
	flags.StringVar(&cfg.BuildkitdConfig, "buildkitd-config", "", "")
	flags.StringVar(&cfg.ToolPath, "tool-path", "", "")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
		return usageError()
	}
	if args[0] != "claim" && args[0] != "create" && args[0] != "cleanup" {
		return usageError()
	}
	target, err := prepare(cfg)
	if err != nil {
		return err
	}
	docker := commandRunner{cfg: cfg, bin: cfg.DockerBin}
	buildx := commandRunner{cfg: cfg, bin: cfg.BuildxBin}
	switch args[0] {
	case "claim":
		builder, err := claim(target, docker, rand.Reader, time.Sleep)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "builder_name=%s\n", builder)
		return err
	case "create":
		return create(target, docker, buildx)
	case "cleanup":
		return cleanup(target, docker, time.Sleep)
	}
	panic("validated runtime builder action was not dispatched")
}

func usageError() error {
	return &cliexit.ExitCodeError{Code: 2, Message: "usage: workcell-hostutil runtime-builder-cli <claim|create|cleanup> [flags]"}
}
