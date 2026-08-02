// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package startupbench

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const captureLimit = 4096

var safeSessionID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type config struct {
	iterations, warmup, runs           int
	stabilityPct                       int
	outputPath, runtime                string
	modes, target                      []string
	sampleGroups                       [][]int64
	prep                               map[string]string
	warmVerify, teardown, cleanupCheck string
	teardownTimeout, verifyTimeout     time.Duration
}

type sample struct {
	mode, index, sessionID string
	run                    int
	duration               int64
}
type statistics struct{ n, mean, median, p90, stddev, min, max int64 }

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "certify" {
		fmt.Fprintln(stderr, "run-startup-bench: certification is not a startup-bench command")
		return 2
	}
	cfg, skip, err := loadConfig(args, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "run-startup-bench:", err)
		return 2
	}
	if skip {
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGHUP, syscall.SIGTERM)
	defer stop()
	report, stable, err := execute(ctx, cfg, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "run-startup-bench:", err)
		return 1
	}
	fmt.Fprint(stdout, report)
	if cfg.outputPath != "" {
		if err := os.WriteFile(cfg.outputPath, []byte(report), 0o644); err != nil {
			fmt.Fprintln(stderr, "run-startup-bench: write report:", err)
			return 1
		}
		fmt.Fprintf(stderr, "run-startup-bench: report written to %s\n", cfg.outputPath)
	}
	if !stable {
		fmt.Fprintln(stderr, "run-startup-bench: cross-run stability gate FAILED")
		return 1
	}
	return 0
}
func loadConfig(args []string, stderr io.Writer) (config, bool, error) {
	cfg := config{
		iterations: 5, warmup: 1, runs: 2,
		modes: []string{"cold", "cache-hit"}, prep: map[string]string{},
		outputPath: os.Getenv("WORKCELL_STARTUP_OUTPUT"), teardownTimeout: 30 * time.Second, verifyTimeout: 30 * time.Second,
	}
	var err error
	for _, control := range []struct {
		name      string
		dst       *int
		base, min int
	}{{"WORKCELL_STARTUP_ITERATIONS", &cfg.iterations, 5, 1}, {"WORKCELL_STARTUP_WARMUP", &cfg.warmup, 1, 0}, {"WORKCELL_STARTUP_RUNS", &cfg.runs, 2, 1}, {"WORKCELL_STARTUP_STABILITY_PCT", &cfg.stabilityPct, 15, 0}} {
		if *control.dst, err = envInt(control.name, control.base, control.min); err != nil {
			return cfg, false, err
		}
	}
	rawSamples := os.Getenv("WORKCELL_STARTUP_SAMPLES_NS")
	if rawSamples != "" {
		if len(args) != 0 {
			return cfg, false, fmt.Errorf("canned-sample mode does not accept arguments")
		}
		cfg.runtime = "dry-run (canned samples)"
		cfg.modes = []string{"cold", "cache-hit", "warm"}
		cfg.sampleGroups, err = parseSampleGroups(rawSamples)
		if err != nil {
			return cfg, false, err
		}
		if _, set := os.LookupEnv("WORKCELL_STARTUP_ITERATIONS"); set && cfg.iterations != len(cfg.sampleGroups[0]) {
			return cfg, false, fmt.Errorf("WORKCELL_STARTUP_ITERATIONS=%d does not match canned group size %d", cfg.iterations, len(cfg.sampleGroups[0]))
		}
		cfg.iterations = len(cfg.sampleGroups[0])
		if len(cfg.sampleGroups) > 1 {
			cfg.runs = len(cfg.sampleGroups)
		}
	} else {
		if len(args) > 0 && (args[0] != "--" || len(args) < 2) {
			return cfg, false, fmt.Errorf("measured argv must follow -- and include a target")
		}
		cfg.runtime = os.Getenv("WORKCELL_STARTUP_RUNTIME")
		if cfg.runtime == "" {
			cfg.runtime = detectRuntime()
		}
		if cfg.runtime == "" || cfg.runtime == "none" {
			fmt.Fprintln(stderr, "run-startup-bench: no container runtime (Colima / Apple container) is available on this host; session-start latency needs a live runtime.")
			fmt.Fprintln(stderr, "run-startup-bench: skipping (clean exit). Set WORKCELL_STARTUP_SAMPLES_NS for a canned dry run, or run on a host with a runtime. See docs/session-startup-benchmarks.md.")
			return cfg, true, nil
		}
		if cfg.runs < 2 {
			return cfg, false, fmt.Errorf("a live run requires WORKCELL_STARTUP_RUNS >= 2 for cross-run stability evidence")
		}
		if len(args) < 2 {
			return cfg, false, fmt.Errorf("measured argv must follow -- and include a target")
		}
	}
	if rawModes := os.Getenv("WORKCELL_STARTUP_MODES"); rawModes != "" {
		cfg.modes, err = parseModes(rawModes)
		if err != nil {
			return cfg, false, err
		}
	}
	if len(cfg.sampleGroups) != 0 {
		return cfg, false, nil
	}
	cfg.target = append([]string(nil), args[1:]...)
	cfg.prep["cold"] = os.Getenv("WORKCELL_STARTUP_COLD_PREP")
	cfg.prep["cache-hit"] = os.Getenv("WORKCELL_STARTUP_CACHE_HIT_PREP")
	cfg.prep["warm"] = os.Getenv("WORKCELL_STARTUP_WARM_PREP")
	cfg.warmVerify = os.Getenv("WORKCELL_STARTUP_WARM_VERIFY")
	cfg.teardown = os.Getenv("WORKCELL_STARTUP_TEARDOWN")
	cfg.cleanupCheck = os.Getenv("WORKCELL_STARTUP_TEARDOWN_VERIFY")
	for _, mode := range cfg.modes {
		if err := requireExecutable(prepEnv(mode), cfg.prep[mode]); err != nil {
			return cfg, false, fmt.Errorf("mode %q: %w", mode, err)
		}
	}
	if slices.Contains(cfg.modes, "warm") {
		if err := requireExecutable("WORKCELL_STARTUP_WARM_VERIFY", cfg.warmVerify); err != nil {
			return cfg, false, err
		}
	}
	if err := requireExecutable("WORKCELL_STARTUP_TEARDOWN", cfg.teardown); err != nil {
		return cfg, false, err
	}
	if err := requireExecutable("WORKCELL_STARTUP_TEARDOWN_VERIFY", cfg.cleanupCheck); err != nil {
		return cfg, false, err
	}
	return cfg, false, nil
}
func execute(ctx context.Context, cfg config, stderr io.Writer) (string, bool, error) {
	var report strings.Builder
	fmt.Fprintln(&report, "# session-start latency benchmark results")
	fmt.Fprintln(&report)
	fmt.Fprintln(&report, "- classification: benchmark-only; caller-provided hooks are not C2 certification evidence")
	fmt.Fprintf(&report, "- date (UTC): %s\n", time.Now().UTC().Format(time.RFC3339))
	host := runtime.GOOS + " " + runtime.GOARCH
	if release, err := exec.Command("uname", "-r").Output(); err == nil {
		host = runtime.GOOS + " " + strings.TrimSpace(string(release)) + " " + runtime.GOARCH
	}
	fmt.Fprintf(&report, "- host: %s\n", host)
	fmt.Fprintf(&report, "- online CPUs: %d\n", runtime.NumCPU())
	fmt.Fprintf(&report, "- runtime: %s\n", cfg.runtime)
	fmt.Fprintf(&report, "- modes: %s\n", strings.Join(cfg.modes, " "))
	fmt.Fprintf(&report, "- iterations: %d (warmup %d for warm; caller-supplied teardown and absence-verification hooks for live launches) x %d run(s)\n", cfg.iterations, cfg.warmup, cfg.runs)
	fmt.Fprintf(&report, "- stability threshold: %d%% cross-run median spread\n\n", cfg.stabilityPct)
	medians := make(map[string][]int64, len(cfg.modes))
	var raw []sample
	for runIndex := 1; runIndex <= cfg.runs; runIndex++ {
		fmt.Fprintf(&report, "## Run %d\n\n", runIndex)
		fmt.Fprint(&report, "| Mode | Median (ns) | p90 (ns) | Mean (ns) | Stddev (ns) | Min (ns) | Max (ns) | n |\n|---|---|---|---|---|---|---|---|\n")
		for _, mode := range cfg.modes {
			samples, err := measureMode(ctx, cfg, mode, runIndex, stderr)
			if err != nil {
				return "", false, err
			}
			raw = append(raw, samples...)
			values := make([]int64, len(samples))
			for i := range samples {
				values[i] = samples[i].duration
			}
			stats := calculate(values)
			medians[mode] = append(medians[mode], stats.median)
			fmt.Fprintf(&report, "| %s | %d | %d | %d | %d | %d | %d | %d |\n",
				mode, stats.median, stats.p90, stats.mean, stats.stddev, stats.min, stats.max, stats.n)
		}
		fmt.Fprintln(&report)
	}
	fmt.Fprint(&report, "## Raw samples\n\n```text\n")
	for _, item := range raw {
		fmt.Fprintf(&report, "mode=%s run=%d index=%s duration_ns=%d session_id=%s\n",
			item.mode, item.run, item.index, item.duration, item.sessionID)
	}
	fmt.Fprintln(&report, "```")
	fmt.Fprintln(&report)
	stable := true
	if cfg.runs >= 2 {
		fmt.Fprintln(&report, "## Cross-run stability (median)")
		fmt.Fprintln(&report)
		fmt.Fprint(&report, "| Mode | Min median (ns) | Max median (ns) | Spread (ns) | Spread (%) | Verdict |\n|---|---|---|---|---|---|\n")
		var worst float64
		degenerate := false
		for _, mode := range cfg.modes {
			values := medians[mode]
			minimum, maximum := slices.Min(values), slices.Max(values)
			spread := maximum - minimum
			if minimum <= 0 {
				degenerate, stable = true, false
				fmt.Fprintf(&report, "| %s | %d | %d | %d | n/a | UNSTABLE |\n", mode, minimum, maximum, spread)
				continue
			}
			percent := float64(spread) * 100 / float64(minimum)
			if percent > worst {
				worst = percent
			}
			verdict := "STABLE"
			if percent > float64(cfg.stabilityPct) {
				verdict, stable = "UNSTABLE", false
			}
			fmt.Fprintf(&report, "| %s | %d | %d | %d | %.1f | %s |\n", mode, minimum, maximum, spread, percent, verdict)
		}
		fmt.Fprintln(&report)
		switch {
		case stable:
			fmt.Fprintf(&report, "Stability gate: STABLE (max cross-run median spread %.1f%% <= %d%%).\n\n", worst, cfg.stabilityPct)
		case degenerate:
			fmt.Fprintln(&report, "Stability gate: UNSTABLE (a mode reported a zero median across runs -- degenerate measurement, not a fast start).")
			fmt.Fprintln(&report)
		default:
			fmt.Fprintf(&report, "Stability gate: UNSTABLE (max cross-run median spread %.1f%% > %d%%).\n\n", worst, cfg.stabilityPct)
		}
	}
	return report.String(), stable, nil
}
func measureMode(ctx context.Context, cfg config, mode string, runIndex int, stderr io.Writer) ([]sample, error) {
	if len(cfg.sampleGroups) != 0 {
		group := cfg.sampleGroups[min(runIndex-1, len(cfg.sampleGroups)-1)]
		result := make([]sample, len(group))
		for i, duration := range group {
			result[i] = sample{mode: mode, run: runIndex, index: strconv.Itoa(i + 1), duration: duration, sessionID: "not-reported"}
		}
		return result, nil
	}
	warmups := 0
	if mode == "warm" {
		if err := runOperation(ctx, cfg.prep[mode], sampleEnv(mode, runIndex, "0", "", ""), stderr); err != nil {
			return nil, fmt.Errorf("%s prep: %w", mode, err)
		}
		warmups = cfg.warmup
	}
	result := make([]sample, 0, cfg.iterations)
	for i := 1; i <= warmups+cfg.iterations; i++ {
		measured := i > warmups
		index := strconv.Itoa(i - warmups)
		if !measured {
			index = fmt.Sprintf("warmup-%d", i)
		}
		if mode == "warm" {
			if err := runOperation(ctx, cfg.warmVerify, sampleEnv(mode, runIndex, index, "", ""), stderr); err != nil {
				return nil, fmt.Errorf("warm verify: %w", err)
			}
		} else if err := runOperation(ctx, cfg.prep[mode], sampleEnv(mode, runIndex, index, "", ""), stderr); err != nil {
			return nil, fmt.Errorf("%s prep: %w", mode, err)
		}
		item, err := measureOne(ctx, cfg, mode, runIndex, index, stderr)
		if err != nil {
			return nil, err
		}
		if measured {
			result = append(result, item)
		}
	}
	return result, nil
}
func measureOne(ctx context.Context, cfg config, mode string, runIndex int, index string, stderr io.Writer) (sample, error) {
	token, err := randomToken()
	if err != nil {
		return sample{}, err
	}
	env := sampleEnv(mode, runIndex, index, "", token)
	var capture limitedBuffer
	start := time.Now()
	launchErr := runCommand(ctx, cfg.target, env, &capture, io.Discard)
	duration := time.Since(start).Nanoseconds()
	sessionID, parseErr := parseCapture(capture.Bytes(), token)
	teardownCtx, cancelTeardown := context.WithTimeout(context.Background(), cfg.teardownTimeout)
	teardownErr := runOperation(teardownCtx, cfg.teardown, env, stderr)
	cancelTeardown()
	verifyCtx, cancelVerify := context.WithTimeout(context.Background(), cfg.verifyTimeout)
	verifyErr := verifyCleanup(verifyCtx, cfg.cleanupCheck, sampleEnv(mode, runIndex, index, sessionID, token), sessionID, token)
	cancelVerify()
	var failures []error
	for _, item := range []struct {
		label string
		err   error
	}{{"teardown", teardownErr}, {"cleanup verification", verifyErr}, {mode + " launch", launchErr}, {"target output", parseErr}} {
		if item.err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", item.label, item.err))
		}
	}
	if len(failures) != 0 {
		return sample{}, errors.Join(failures...)
	}
	return sample{mode: mode, run: runIndex, index: index, duration: duration, sessionID: sessionID}, nil
}
func runOperation(ctx context.Context, path string, env []string, output io.Writer) error {
	return runCommand(ctx, []string{path}, env, output, output)
}
func runCommand(ctx context.Context, argv, env []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = commandEnv(env)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
		if err == syscall.ESRCH {
			return nil
		}
		return err
	}
	cmd.WaitDelay = 5 * time.Second
	err := cmd.Run()
	if ctx.Err() != nil && cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return err
}
func commandEnv(overrides []string) []string {
	env := slices.DeleteFunc(os.Environ(), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return strings.HasPrefix(key, "WORKCELL_STARTUP_SAMPLE_") || key == "WORKCELL_STARTUP_SESSION_ID"
	})
	return append(env, overrides...)
}
func verifyCleanup(ctx context.Context, path string, env []string, sessionID, token string) error {
	var output, diagnostics limitedBuffer
	if err := runCommand(ctx, []string{path}, env, &output, &diagnostics); err != nil {
		return fmt.Errorf("%w (%s)", err, strings.TrimSpace(diagnostics.String()))
	}
	if output.String() != fmt.Sprintf("absent session_id=%s sample_token=%s\n", sessionID, token) {
		return fmt.Errorf("unexpected verifier output")
	}
	return nil
}
func parseCapture(data []byte, token string) (string, error) {
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "session_id=") || !strings.HasPrefix(lines[1], "sample_token=") {
		return "", fmt.Errorf("measured target must report exactly session_id and sample_token")
	}
	sessionID := strings.TrimPrefix(lines[0], "session_id=")
	if !safeSessionID.MatchString(sessionID) {
		return "", fmt.Errorf("measured target reported an unsafe session id")
	}
	if strings.TrimPrefix(lines[1], "sample_token=") != token {
		return "", fmt.Errorf("measured target reported the wrong sample token")
	}
	return sessionID, nil
}
func calculate(values []int64) statistics {
	sorted := append([]int64(nil), values...)
	slices.Sort(sorted)
	var sum float64
	for _, value := range sorted {
		sum += float64(value)
	}
	mean := sum / float64(len(sorted))
	var variance float64
	for _, value := range sorted {
		delta := float64(value) - mean
		variance += delta * delta
	}
	p90 := len(sorted) * 9 / 10
	return statistics{
		n: int64(len(sorted)), mean: int64(math.RoundToEven(mean)), median: sorted[len(sorted)/2],
		p90: sorted[p90], stddev: int64(math.RoundToEven(math.Sqrt(variance / float64(len(sorted))))),
		min: sorted[0], max: sorted[len(sorted)-1],
	}
}
func parseSampleGroups(raw string) ([][]int64, error) {
	rawGroups := strings.Split(raw, ";")
	groups := make([][]int64, len(rawGroups))
	for i, rawGroup := range rawGroups {
		for _, field := range strings.Fields(rawGroup) {
			value, err := strconv.ParseInt(field, 10, 64)
			if err != nil || value < 0 {
				return nil, fmt.Errorf("WORKCELL_STARTUP_SAMPLES_NS holds invalid sample %q", field)
			}
			groups[i] = append(groups[i], value)
		}
		if len(groups[i]) == 0 {
			return nil, fmt.Errorf("WORKCELL_STARTUP_SAMPLES_NS contains an empty group")
		}
		if i > 0 && len(groups[i]) != len(groups[0]) {
			return nil, fmt.Errorf("canned sample groups have different sizes")
		}
	}
	return groups, nil
}
func parseModes(raw string) ([]string, error) {
	modes, seen := strings.Fields(raw), map[string]bool{}
	if len(modes) == 0 {
		return nil, fmt.Errorf("WORKCELL_STARTUP_MODES must select at least one mode")
	}
	for _, mode := range modes {
		if mode != "cold" && mode != "cache-hit" && mode != "warm" {
			return nil, fmt.Errorf("WORKCELL_STARTUP_MODES contains unsupported mode %q", mode)
		}
		if seen[mode] {
			return nil, fmt.Errorf("WORKCELL_STARTUP_MODES contains duplicate mode %q", mode)
		}
		seen[mode] = true
	}
	return modes, nil
}
func detectRuntime() string {
	for _, candidate := range []struct {
		name string
		args []string
	}{{"colima", []string{"status"}}, {"container", []string{"system", "status"}}, {"docker", []string{"info"}}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := exec.CommandContext(ctx, candidate.name, candidate.args...).Run()
		cancel()
		if err == nil {
			return candidate.name
		}
	}
	return ""
}
func envInt(name string, fallback, floor int) (int, error) {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < floor {
		return 0, fmt.Errorf("%s must be an integer >= %d, got %q", name, floor, raw)
	}
	return value, nil
}
func requireExecutable(name, path string) error {
	if path == "" {
		return fmt.Errorf("live run requires %s to name an executable path", name)
	}
	if _, err := exec.LookPath(path); err != nil {
		return fmt.Errorf("%s must name an executable path", name)
	}
	if strings.ContainsAny(path, " \t\n") {
		return fmt.Errorf("%s must name one executable path, not a shell fragment", name)
	}
	return nil
}
func prepEnv(mode string) string {
	return map[string]string{"cold": "WORKCELL_STARTUP_COLD_PREP", "cache-hit": "WORKCELL_STARTUP_CACHE_HIT_PREP", "warm": "WORKCELL_STARTUP_WARM_PREP"}[mode]
}
func sampleEnv(mode string, run int, index, sessionID, token string) []string {
	return []string{"WORKCELL_STARTUP_SAMPLE_MODE=" + mode, "WORKCELL_STARTUP_SAMPLE_RUN=" + strconv.Itoa(run), "WORKCELL_STARTUP_SAMPLE_INDEX=" + index, "WORKCELL_STARTUP_SESSION_ID=" + sessionID, "WORKCELL_STARTUP_SAMPLE_TOKEN=" + token}
}
func randomToken() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", fmt.Errorf("generate sample token: %w", err)
	}
	return hex.EncodeToString(value), nil
}

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > captureLimit {
		return 0, fmt.Errorf("captured output exceeds %d bytes", captureLimit)
	}
	return b.Buffer.Write(p)
}
