// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const pinnedTestImage = "ghcr.io/omkhar/workcell@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// TestFakeGuestToolProcess is the fake docker and bash of the guest. A guest
// test runs the test binary with "-- <tool> <mode> <log> <tool args...>"; a
// normal run has no arguments and skips. Each run appends its tool and
// arguments to the log, and bash also keeps its stdin in <log>.stdin.
func TestFakeGuestToolProcess(t *testing.T) {
	args := flag.Args()
	if len(args) < 3 {
		t.Skip("fake guest tool helper process")
	}
	tool, mode, log := args[0], args[1], args[2]
	toolArgs := args[3:]
	logFile, err := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(5)
	}
	fmt.Fprintln(logFile, tool, strings.Join(toolArgs, " "))
	logFile.Close()
	switch {
	case tool == "bash":
		stdin, _ := io.ReadAll(os.Stdin)
		if err := os.WriteFile(log+".stdin", stdin, 0o600); err != nil {
			os.Exit(5)
		}
		if mode == "fail" {
			os.Stderr.WriteString("iptables: No chain/target/match by that name.\n")
			os.Exit(4)
		}
	case mode == "hang":
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Minute)
	case toolArgs[0] == "run" || toolArgs[0] == "container":
		if err := fakeDocker(log+".containers", mode, toolArgs); err != nil {
			os.Stderr.WriteString(err.Error() + "\n")
			os.Exit(1)
		}
	case toolArgs[0] == "pull" && mode == "pullfail":
		os.Stderr.WriteString("Error response from daemon: manifest unknown\n")
		os.Exit(9)
	case toolArgs[0] == "image" && mode == "wrongdigest":
		fmt.Println(`["ghcr.io/omkhar/workcell@sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"]`)
	case toolArgs[0] == "image":
		ref := toolArgs[len(toolArgs)-1]
		fmt.Printf("[%q]\n", ref)
	}
	os.Exit(0)
}

func fakeGuestTools(dockerMode, bashMode, log string) guestTools {
	tool := func(name, mode string) []string {
		return []string{os.Args[0], "-test.run=^TestFakeGuestToolProcess$", "--", name, mode, log}
	}
	return guestTools{Docker: tool("docker", dockerMode), Bash: tool("bash", bashMode)}
}

func guestBootstrapTarget(dockerMode, bashMode, log string, started *bool) BrokeredTarget {
	return BrokeredTarget{Contract: DefaultGCPVMContract(), Command: func(string) []string {
		*started = true
		return []string{os.Args[0], "-test.run=^TestFakeBrokerProcess$", "--", "guest", filepath.Dir(log), dockerMode, bashMode, log}
	}}
}

func bootstrapRequest(t *testing.T, script []byte) BootstrapRequest {
	return BootstrapRequest{StateRoot: t.TempDir(), TargetID: "workcell-l4-test", BootstrapID: "b1", ImageRef: pinnedTestImage, GuestFloorScript: script}
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(content)
}

// guestFloorScript renders the floor through the one rule owner.
func guestFloorScript(t *testing.T, endpoints string) []byte {
	t.Helper()
	script, err := exec.Command(filepath.Join("..", "..", "scripts", "colima-egress-allowlist.sh"), "guest-script", endpoints).Output()
	if err != nil {
		t.Fatalf("guest-script: %v", err)
	}
	return script
}

func TestBrokeredTargetBootstrapsTheGuestWithTheSharedFloor(t *testing.T) {
	t.Parallel()

	script := guestFloorScript(t, "1.2.3.4:443 [2001:db8::1]:443 api.openai.com:443")
	if !bytes.HasPrefix(script, []byte("set -euo pipefail\n")) || !bytes.Contains(script, []byte("iptables-restore --noflush")) {
		t.Fatalf("guest-script did not render the apply plan:\n%s", script)
	}
	log := filepath.Join(t.TempDir(), "log")
	var started bool
	req := bootstrapRequest(t, script)
	result, err := guestBootstrapTarget("ok", "ok", log, &started).BootstrapTarget(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(script)
	if want := hex.EncodeToString(sum[:]); result.Manifest.EgressFloorSHA256 != want || result.Manifest.ImageRef != pinnedTestImage {
		t.Fatalf("manifest = %+v, want floor %s and the pinned image", result.Manifest, want)
	}
	written := readLog(t, result.ManifestPath)
	if !strings.Contains(written, result.Manifest.EgressFloorSHA256) || !strings.Contains(written, pinnedTestImage) {
		t.Fatalf("bootstrap.json does not record the guest floor and image: %s", written)
	}
	if got := readLog(t, log+".stdin"); got != string(script) {
		t.Fatalf("the guest shell read a different floor:\n%s", got)
	}
	want := "bash -s --\ndocker pull --quiet " + pinnedTestImage + "\ndocker image inspect --format {{json .RepoDigests}} " + pinnedTestImage + "\n"
	if got := readLog(t, log); got != want {
		t.Fatalf("guest steps =\n%s\nwant\n%s", got, want)
	}
}

func TestBrokeredTargetBootstrapFailsClosed(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, dockerMode, bashMode string
		mutate                     func(*BootstrapRequest)
		wantErr, wantSteps         string
		wantBroker                 bool
	}{
		{name: "tag reference", mutate: func(r *BootstrapRequest) { r.ImageRef = "ghcr.io/omkhar/workcell:latest" }, wantErr: "pinned by digest"},
		{name: "option reference", mutate: func(r *BootstrapRequest) { r.ImageRef = "-q@sha256:" + strings.Repeat("a", 64) }, wantErr: "pinned by digest"},
		{name: "spaced reference", mutate: func(r *BootstrapRequest) { r.ImageRef = "a b@sha256:" + strings.Repeat("a", 64) }, wantErr: "pinned by digest"},
		{name: "option target", mutate: func(r *BootstrapRequest) { r.TargetID = "--project=other" }, wantErr: "must not start with '-'"},
		{name: "no floor", mutate: func(r *BootstrapRequest) { r.GuestFloorScript = nil }, wantErr: "guest floor script"},
		{name: "floor failure", dockerMode: "ok", bashMode: "fail", wantBroker: true, wantErr: "egress floor failed (exit status 4)", wantSteps: "bash -s --\n"},
		{name: "pull failure", dockerMode: "pullfail", bashMode: "ok", wantBroker: true, wantErr: "docker pull failed (exit status 9)", wantSteps: "bash -s --\ndocker pull --quiet " + pinnedTestImage + "\n"},
		{name: "wrong digest", dockerMode: "wrongdigest", bashMode: "ok", wantBroker: true, wantErr: "do not include"},
	} {
		log := filepath.Join(t.TempDir(), "log")
		var started bool
		req := bootstrapRequest(t, []byte("true\n"))
		if tc.mutate != nil {
			tc.mutate(&req)
		}
		_, err := guestBootstrapTarget(tc.dockerMode, tc.bashMode, log, &started).BootstrapTarget(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
			t.Fatalf("%s: BootstrapTarget() error = %v, want %q", tc.name, err, tc.wantErr)
		}
		if started != tc.wantBroker {
			t.Fatalf("%s: broker started = %v, want %v", tc.name, started, tc.wantBroker)
		}
		if tc.wantSteps != "" && readLog(t, log) != tc.wantSteps {
			t.Fatalf("%s: guest steps =\n%s\nwant\n%s", tc.name, readLog(t, log), tc.wantSteps)
		}
		manifest := filepath.Join(req.StateRoot, "targets", TargetKind, GCPVMProvider, req.TargetID, "bootstrap", BootstrapFile)
		if _, err := os.Lstat(manifest); !os.IsNotExist(err) {
			t.Fatalf("%s: a failed bootstrap left bootstrap.json: %v", tc.name, err)
		}
	}
}

func TestGuestBootstrapVerifiesTheFrameBeforeItRunsAnything(t *testing.T) {
	t.Parallel()

	script := []byte("true\n")
	sum := sha256.Sum256(script)
	digest := hex.EncodeToString(sum[:])
	for name, frame := range map[string]string{
		"digest mismatch": fmt.Sprintf("%s %s %s %d\n%s", bootstrapMagic, pinnedTestImage, strings.Repeat("0", 64), len(script), script),
		"truncated":       fmt.Sprintf("%s %s %s %d\n%s", bootstrapMagic, pinnedTestImage, digest, len(script)+1, script),
		"oversized":       fmt.Sprintf("%s %s %s %d\n", bootstrapMagic, pinnedTestImage, digest, maxFloorScriptBytes+1),
		"empty":           fmt.Sprintf("%s %s %x 0\n", bootstrapMagic, pinnedTestImage, sha256.Sum256(nil)),
		"tag reference":   fmt.Sprintf("%s ghcr.io/omkhar/workcell:latest %s %d\n%s", bootstrapMagic, digest, len(script), script),
		"extra field":     fmt.Sprintf("%s %s %s %d x\n%s", bootstrapMagic, pinnedTestImage, digest, len(script), script),
	} {
		log := filepath.Join(t.TempDir(), "log")
		var ack bytes.Buffer
		err := receiveBootstrap(context.Background(), struct {
			io.Reader
			io.Writer
		}{strings.NewReader(frame), &ack}, fakeGuestTools("ok", "ok", log))
		if err == nil || !strings.HasPrefix(ack.String(), "error ") {
			t.Fatalf("%s: receiveBootstrap() error = %v, ack = %q", name, err, ack.String())
		}
		if steps := readLog(t, log); steps != "" {
			t.Fatalf("%s: the guest ran %q for a bad frame", name, steps)
		}
	}
}

func TestSendBootstrapRejectsAMismatchedAck(t *testing.T) {
	t.Parallel()

	digest := strings.Repeat("a", 64)
	for name, ack := range map[string]string{
		"other floor": "ok " + pinnedTestImage + " " + strings.Repeat("b", 64) + "\n",
		"other image": "ok ghcr.io/x/y@sha256:" + strings.Repeat("c", 64) + " " + digest + "\n",
		"guest error": "error \"docker pull failed\"\n",
		"no ack":      "",
	} {
		var sent bytes.Buffer
		err := sendBootstrap(struct {
			io.Reader
			io.Writer
		}{bufio.NewReader(strings.NewReader(ack)), &sent}, pinnedTestImage, []byte("true\n"), digest)
		if err == nil {
			t.Fatalf("%s: sendBootstrap() accepted %q", name, ack)
		}
	}
}

func TestGuestToolTimeoutKillsAToolThatIgnoresSIGTERM(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	begin := time.Now()
	_, err := runGuestTool(ctx, "docker pull", fakeGuestTools("hang", "ok", filepath.Join(t.TempDir(), "log")).Docker, nil)
	if err == nil || !strings.Contains(err.Error(), "docker pull transfer timed out") {
		t.Fatalf("runGuestTool() error = %v, want a timeout", err)
	}
	if elapsed := time.Since(begin); elapsed > brokerStopGrace+10*time.Second {
		t.Fatalf("SIGKILL escalation took %v", elapsed)
	}
}

func TestBrokeredBootstrapTimesOutABrokerThatHangs(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	var started bool
	_, err := fakeBrokerTarget(t, "hang", t.TempDir(), &started).BootstrapTarget(ctx, bootstrapRequest(t, []byte("true\n")))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("BootstrapTarget() error = %v, want a timeout", err)
	}
}
