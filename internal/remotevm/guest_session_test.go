// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeDocker keeps the guest containers as one name per line in state. The
// mode makes one step fail: runfail leaves a created container and exits
// nonzero, stopfail fails docker stop, stuck keeps the container after rm,
// lsfail fails docker container ls, and rmlsfail fails it after a rm.
func fakeDocker(state, mode string, args []string) error {
	content, err := os.ReadFile(state)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	names := strings.Fields(string(content))
	save := func() error { return os.WriteFile(state, []byte(strings.Join(names, "\n")+"\n"), 0o600) }
	switch {
	case args[0] == "run":
		names = append(names, args[slices.Index(args, "--name")+1])
		if err := save(); err != nil {
			return err
		}
		if mode == "runfail" {
			return fmt.Errorf("docker: Error response from daemon: failed to set up container networking")
		}
		fmt.Println(strings.Repeat("c", 64))
	case args[1] == "ls":
		if _, err := os.Stat(state + ".removed"); mode == "lsfail" || (mode == "rmlsfail" && err == nil) {
			return fmt.Errorf("Cannot connect to the Docker daemon")
		}
		filter := args[len(args)-1]
		name := strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(filter, "name=^/?"), "$"), `\.`, ".")
		if slices.Contains(names, name) {
			fmt.Println("c0ffee")
		}
	case args[1] == "stop" && mode == "stopfail":
		return fmt.Errorf("Error response from daemon: cannot stop container")
	case args[1] == "rm" && mode != "stuck":
		names = slices.DeleteFunc(names, func(name string) bool { return name == args[len(args)-1] })
		if err := os.WriteFile(state+".removed", nil, 0o600); err != nil {
			return err
		}
		return save()
	}
	return nil
}

const sessionContainer = "workcell-codex-strict-l4"

func sessionRunArgv() []string {
	return []string{"run", "-d", "-i", "-t", "--name", sessionContainer, "--cap-drop", "ALL", pinnedTestImage, "--", "codex"}
}

func TestGuestSessionVerifiesTheFrameBeforeItRunsDocker(t *testing.T) {
	t.Parallel()

	frame := []byte(strings.Join(sessionRunArgv(), "\x00") + "\x00")
	digest := func(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
	header := func(session, image string, body []byte, sum string) string {
		return fmt.Sprintf("%s %s %s %d %s\n", sessionStartMagic, session, image, len(body), sum)
	}
	noNUL := frame[:len(frame)-1]
	words := func(argv ...string) []byte { return []byte(strings.Join(argv, "\x00") + "\x00") }
	attached := words("run", "-i", "--name", sessionContainer, pinnedTestImage)
	exec := words("exec", "-d", "--name", sessionContainer, pinnedTestImage)
	other := words("run", "-d", "--name", sessionContainer, "ghcr.io/omkhar/workcell:latest")
	late := words("run", "-d", pinnedTestImage, "--name", sessionContainer)
	badName := words("run", "-d", "--name", "-x", pinnedTestImage)
	for name, input := range map[string]string{
		"digest mismatch": header("s1", pinnedTestImage, frame, strings.Repeat("0", 64)) + string(frame),
		"truncated":       header("s1", pinnedTestImage, frame, digest(frame)) + string(frame[:10]),
		"no final NUL":    header("s1", pinnedTestImage, noNUL, digest(noNUL)) + string(noNUL),
		"bad session id":  header("-s1", pinnedTestImage, frame, digest(frame)) + string(frame),
		"unpinned image":  header("s1", "ghcr.io/omkhar/workcell:latest", frame, digest(frame)) + string(frame),
		"attached argv":   header("s1", pinnedTestImage, attached, digest(attached)) + string(attached),
		"exec argv":       header("s1", pinnedTestImage, exec, digest(exec)) + string(exec),
		"other image":     header("s1", pinnedTestImage, other, digest(other)) + string(other),
		"late name":       header("s1", pinnedTestImage, late, digest(late)) + string(late),
		"bad name":        header("s1", pinnedTestImage, badName, digest(badName)) + string(badName),
		"short header":    sessionStartMagic + " s1 " + pinnedTestImage + "\n",
		"long header":     strings.TrimSuffix(header("s1", pinnedTestImage, frame, digest(frame)), "\n") + " x\n" + string(frame),
		"long stop":       sessionStopMagic + " s1 " + sessionContainer + " x\n",
		"bad stop name":   sessionStopMagic + " s1 -x\n",
		"short stop":      sessionStopMagic + " s1\n",
	} {
		log := filepath.Join(t.TempDir(), "log")
		var out bytes.Buffer
		conn := struct {
			io.Reader
			io.Writer
		}{strings.NewReader(input), &out}
		if err := serveGuest(context.Background(), conn, t.TempDir(), defaultWorkspaceLimits, fakeGuestTools("ok", "ok", log)); err == nil {
			t.Fatalf("%s: the guest accepted the frame", name)
		}
		if !strings.HasPrefix(out.String(), "error ") || (name == "other image" && !strings.Contains(out.String(), "bootstrapped image")) {
			t.Fatalf("%s: ack = %q", name, out.String())
		}
		if got := readLog(t, log); got != "" {
			t.Fatalf("%s: the guest ran a tool: %s", name, got)
		}
	}
}

func TestSessionSendersRejectAMismatchedAck(t *testing.T) {
	t.Parallel()

	for _, ack := range []string{"ok s2 " + sessionContainer + "\n", "ok s1 other\n", "error \"no\"\n", ""} {
		conn := struct {
			io.Reader
			io.Writer
		}{strings.NewReader(ack), &bytes.Buffer{}}
		if err := sendSessionStart(conn, "s1", pinnedTestImage, sessionContainer, sessionRunArgv()); err == nil {
			t.Fatalf("sendSessionStart() accepted %q", ack)
		}
	}
	for _, ack := range []string{"ok s2 removed\n", "ok s1 running\n", "error \"no\"\n", ""} {
		conn := struct {
			io.Reader
			io.Writer
		}{strings.NewReader(ack), &bytes.Buffer{}}
		if _, err := sendSessionStop(conn, "s1", sessionContainer); err == nil {
			t.Fatalf("sendSessionStop() accepted %q", ack)
		}
	}
}

// guestExchange sends one session request through the real sender to the
// real guest dispatch with the fake docker in mode, and returns the guest
// error and the sender result.
func guestExchange(t *testing.T, log, mode string, send func(io.ReadWriter) error) (error, error) {
	t.Helper()
	var request bytes.Buffer
	_ = send(struct {
		io.Reader
		io.Writer
	}{strings.NewReader(""), &request})
	var ack bytes.Buffer
	guestErr := serveGuest(context.Background(), struct {
		io.Reader
		io.Writer
	}{&request, &ack}, t.TempDir(), defaultWorkspaceLimits, fakeGuestTools(mode, "ok", log))
	return guestErr, send(struct {
		io.Reader
		io.Writer
	}{&ack, io.Discard})
}

func TestGuestSessionStartsAndRemovesTheContainer(t *testing.T) {
	t.Parallel()

	start := func(conn io.ReadWriter) error {
		return sendSessionStart(conn, "s1", pinnedTestImage, sessionContainer, sessionRunArgv())
	}
	stopState := ""
	stop := func(conn io.ReadWriter) error {
		var err error
		stopState, err = sendSessionStop(conn, "s1", sessionContainer)
		return err
	}
	log := filepath.Join(t.TempDir(), "log")
	containers := func() string { return strings.TrimSpace(readLog(t, log+".containers")) }
	if guestErr, err := guestExchange(t, log, "ok", start); guestErr != nil || err != nil || containers() != sessionContainer {
		t.Fatalf("start = %v, %v, containers %q", guestErr, err, containers())
	}
	if !strings.Contains(readLog(t, log), "docker "+strings.Join(sessionRunArgv(), " ")+"\n") {
		t.Fatalf("docker log lacks the run argv:\n%s", readLog(t, log))
	}
	for _, mode := range []string{"stuck", "lsfail", "rmlsfail"} {
		if guestErr, err := guestExchange(t, log, mode, stop); guestErr == nil || err == nil {
			t.Fatalf("%s: stop succeeded", mode)
		}
		if err := os.Remove(log + ".containers.removed"); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if mode == "rmlsfail" {
			// That docker removed the container before its ls failed.
			if err := os.WriteFile(log+".containers", []byte(sessionContainer+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if guestErr, err := guestExchange(t, log, "stopfail", stop); guestErr != nil || err != nil || stopState != "removed" || containers() != "" {
		t.Fatalf("stop = %v, %v, %q, containers %q", guestErr, err, stopState, containers())
	}
	if !strings.Contains(readLog(t, log), "docker container stop --time 10 "+sessionContainer+"\n") {
		t.Fatalf("docker log lacks the stop:\n%s", readLog(t, log))
	}
	if guestErr, err := guestExchange(t, log, "ok", stop); guestErr != nil || err != nil || stopState != "absent" {
		t.Fatalf("repeated stop = %v, %v, %q", guestErr, err, stopState)
	}
	if guestErr, err := guestExchange(t, log, "runfail", start); guestErr == nil || err == nil || containers() != "" {
		t.Fatalf("failed run = %v, %v, containers %q", guestErr, err, containers())
	}
}
