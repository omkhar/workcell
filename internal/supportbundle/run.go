// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package supportbundle

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
)

// UsageText is the `workcell support-bundle --help` body. It names the
// canonical `workcell support-bundle` syntax so the operator-contract
// discoverability check can find it.
func UsageText() string {
	return `Usage: workcell support-bundle [options]

Collect a redacted host-side diagnostics bundle (install, policy, target,
provider, session, and audit-pointer evidence) as a single JSON document.

The bundle never contains credential material or workspace/agent content:
credential files are recorded by path and presence only, log bodies by
pointer only, and every string is passed through a secret redactor. See
SUPPORT.md for the full redaction guarantees before sharing a bundle.

Options:
  --output PATH   Write the bundle to PATH (default: stdout)
  -h, --help      Show this help text

The following options are supplied by the launcher and are not usually set by
hand; each falls back to the matching environment/default when omitted:
  --repo-root DIR            Workcell install/checkout root
  --launcher PATH            Path to the scripts/workcell launcher
  --real-home DIR            Operator home directory (redaction root)
  --workcell-state-root DIR  WORKCELL_STATE_ROOT
  --colima-state-root DIR    COLIMA_STATE_ROOT
  --host-os NAME             Host OS label
  --host-arch NAME           Host architecture label
`
}

// Run is the `workcell support-bundle` entry point. It parses flags, collects a
// redacted bundle from the host context, and writes deterministic JSON to
// stdout or --output. Usage/validation failures return a Code-2
// cliexit.ExitCodeError to match the launcher's exit-code contract.
func Run(args []string, stdout, stderr io.Writer) error {
	cfg := Config{Now: time.Now()}
	output := ""

	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			fmt.Fprint(stdout, UsageText())
			return nil
		case arg == "--output":
			value, next, ok := takeValue(args, i)
			if !ok {
				return usageError("--output requires a value")
			}
			output, i = value, next
		case strings.HasPrefix(arg, "--output="):
			output = strings.TrimPrefix(arg, "--output=")
		default:
			key, value, hasEq := strings.Cut(arg, "=")
			if !hasEq {
				var next int
				var ok bool
				value, next, ok = takeValue(args, i)
				if !ok {
					return usageError(fmt.Sprintf("%s requires a value", key))
				}
				i = next
			}
			if err := applyContextFlag(&cfg, key, value); err != nil {
				return err
			}
		}
	}

	if output != "" && strings.TrimSpace(output) == "" {
		return usageError("--output path may not be blank")
	}

	rendered, err := Collect(cfg).JSON()
	if err != nil {
		return err
	}

	if output == "" {
		_, err := stdout.Write(rendered)
		return err
	}
	if err := os.WriteFile(output, rendered, 0o600); err != nil {
		return err
	}
	// os.WriteFile keeps an existing file's mode, so enforce 0600 explicitly: an
	// already-present group/world-readable target must not leave the diagnostics
	// bundle readable to other local users.
	if err := os.Chmod(output, 0o600); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "wrote support bundle to %s\n", output)
	return nil
}

func applyContextFlag(cfg *Config, key, value string) error {
	switch key {
	case "--repo-root":
		cfg.RepoRoot = value
	case "--launcher":
		cfg.LauncherPath = value
	case "--real-home":
		cfg.RealHome = value
	case "--workcell-state-root":
		cfg.WorkcellStateRoot = value
	case "--colima-state-root":
		cfg.ColimaStateRoot = value
	case "--host-os":
		cfg.HostOS = value
	case "--host-arch":
		cfg.HostArch = value
	default:
		return usageError(fmt.Sprintf("unknown option: %s", key))
	}
	return nil
}

// takeValue returns the value following args[i] as a separate token, the new
// index, and whether a value was available.
func takeValue(args []string, i int) (string, int, bool) {
	if i+1 >= len(args) {
		return "", i, false
	}
	return args[i+1], i + 1, true
}

func usageError(msg string) error {
	return &cliexit.ExitCodeError{Code: 2, Message: msg}
}
