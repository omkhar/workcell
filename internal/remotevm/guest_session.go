// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// The session wire format is one header line, then one ack line:
//
//	host:  workcell-session-start-v1 <session-id> <image-ref> <argv-size> <argv-sha256>\n<argv words, each NUL-terminated>
//	guest: ok <session-id> <container-name>\n  or  error <quoted reason>\n
//	host:  workcell-session-stop-v1 <session-id> <container-name>\n
//	guest: ok <session-id> removed|absent\n  or  error <quoted reason>\n
const (
	sessionStartMagic = "workcell-session-start-v1"
	sessionStopMagic  = "workcell-session-stop-v1"
	// maxRunArgvBytes bounds the framed run argv.
	maxRunArgvBytes = 1 << 20
	// containerStopSeconds is the docker stop grace before docker sends SIGKILL.
	containerStopSeconds = "10"
)

// wireTokenPattern is the Docker container name rule. Session ids and
// container names must match it, so each is one header field and one argv
// word.
var wireTokenPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

func checkWireToken(label, value string) error {
	if !wireTokenPattern.MatchString(value) {
		return fmt.Errorf("%s must match %s: %q", label, wireTokenPattern, value)
	}
	return nil
}

// checkRunArgv accepts only a detached `run -d` argv for the pinned image with
// a valid --name before the image, and returns that name. The guest docker
// runs it without a shell.
func checkRunArgv(argv []string, imageRef string) (string, error) {
	if len(argv) < 2 || argv[0] != "run" || argv[1] != "-d" {
		return "", fmt.Errorf("run argv must start with run -d")
	}
	if slices.ContainsFunc(argv, func(word string) bool { return strings.ContainsRune(word, 0) }) {
		return "", fmt.Errorf("run argv must not contain a NUL byte")
	}
	image := slices.Index(argv, imageRef)
	if image < 0 {
		return "", fmt.Errorf("run argv must run the bootstrapped image %s", imageRef)
	}
	name := ""
	for i := 2; i+1 < image; i++ {
		if argv[i] == "--name" {
			name = argv[i+1]
		}
	}
	if err := checkWireToken("container name", name); err != nil {
		return "", err
	}
	return name, nil
}

// sendSessionStart sends the framed run argv and accepts only an ack that
// names the same session and container.
func sendSessionStart(conn io.ReadWriter, sessionID, imageRef, name string, argv []string) error {
	var frame bytes.Buffer
	for _, word := range argv {
		frame.WriteString(word)
		frame.WriteByte(0)
	}
	sum := sha256.Sum256(frame.Bytes())
	if _, err := fmt.Fprintf(conn, "%s %s %s %d %s\n", sessionStartMagic, sessionID, imageRef, frame.Len(), hex.EncodeToString(sum[:])); err != nil {
		return err
	}
	if _, err := conn.Write(frame.Bytes()); err != nil {
		return err
	}
	return readSessionAck(conn, "ok "+sessionID+" "+name)
}

// sendSessionStop asks the guest to remove the session container. It returns
// "removed", or "absent" when an earlier teardown removed it.
func sendSessionStop(conn io.ReadWriter, sessionID, name string) (string, error) {
	if _, err := fmt.Fprintf(conn, "%s %s %s\n", sessionStopMagic, sessionID, name); err != nil {
		return "", err
	}
	line, err := readArchiveLine(bufio.NewReaderSize(conn, 4096))
	if err != nil {
		return "", fmt.Errorf("read session ack: %w", err)
	}
	for _, state := range []string{"removed", "absent"} {
		if line == "ok "+sessionID+" "+state {
			return state, nil
		}
	}
	return "", fmt.Errorf("guest refused the session stop: %q", line)
}

func readSessionAck(conn io.Reader, want string) error {
	line, err := readArchiveLine(bufio.NewReaderSize(conn, 4096))
	if err != nil {
		return fmt.Errorf("read session ack: %w", err)
	}
	if line != want {
		return fmt.Errorf("guest refused the session start: %q", line)
	}
	return nil
}

// receiveSessionStart verifies the frame before it runs docker, and acks.
func receiveSessionStart(ctx context.Context, conn io.ReadWriter, tools guestTools) error {
	sessionID, name, err := startGuestSession(ctx, bufio.NewReaderSize(conn, 4096), tools)
	return ackGuest(conn, err, sessionID+" "+name)
}

// receiveSessionStop removes the session container, and acks.
func receiveSessionStop(ctx context.Context, conn io.ReadWriter, tools guestTools) error {
	sessionID, state, err := stopGuestSession(ctx, bufio.NewReaderSize(conn, 4096), tools)
	return ackGuest(conn, err, sessionID+" "+state)
}

func ackGuest(conn io.Writer, err error, ok string) error {
	if err != nil {
		_, ackErr := fmt.Fprintf(conn, "error %q\n", err.Error())
		return errors.Join(err, ackErr)
	}
	_, err = fmt.Fprintf(conn, "ok %s\n", ok)
	return err
}

func startGuestSession(ctx context.Context, reader *bufio.Reader, tools guestTools) (string, string, error) {
	line, err := readArchiveLine(reader)
	if err != nil {
		return "", "", fmt.Errorf("read session header: %w", err)
	}
	fields := strings.Split(line, " ")
	if len(fields) != 5 || fields[0] != sessionStartMagic {
		return "", "", fmt.Errorf("session start header is malformed")
	}
	sessionID, imageRef := fields[1], fields[2]
	if err := checkWireToken("session id", sessionID); err != nil {
		return "", "", err
	}
	if err := checkGuestImageRef(imageRef); err != nil {
		return "", "", err
	}
	size, err := strconv.Atoi(fields[3])
	if err != nil || size < 1 || size > maxRunArgvBytes {
		return "", "", fmt.Errorf("run argv must be 1 to %d bytes", maxRunArgvBytes)
	}
	frame := make([]byte, size)
	if _, err := io.ReadFull(reader, frame); err != nil {
		return "", "", fmt.Errorf("run argv is truncated: %w", err)
	}
	if sum := sha256.Sum256(frame); hex.EncodeToString(sum[:]) != fields[4] {
		return "", "", fmt.Errorf("run argv digest mismatch")
	}
	if frame[len(frame)-1] != 0 {
		return "", "", fmt.Errorf("run argv must end with a NUL byte")
	}
	argv := strings.Split(string(frame[:len(frame)-1]), "\x00")
	name, err := checkRunArgv(argv, imageRef)
	if err != nil {
		return "", "", err
	}
	if _, err := runGuestTool(ctx, "docker run", slices.Concat(tools.Docker, argv), nil); err != nil {
		// A failed run can leave a created container under the name.
		_, cleanupErr := removeGuestContainer(ctx, tools, name)
		return "", "", errors.Join(err, cleanupErr)
	}
	return sessionID, name, nil
}

func stopGuestSession(ctx context.Context, reader *bufio.Reader, tools guestTools) (string, string, error) {
	line, err := readArchiveLine(reader)
	if err != nil {
		return "", "", fmt.Errorf("read session header: %w", err)
	}
	fields := strings.Split(line, " ")
	if len(fields) != 3 || fields[0] != sessionStopMagic {
		return "", "", fmt.Errorf("session stop header is malformed")
	}
	if err := checkWireToken("session id", fields[1]); err != nil {
		return "", "", err
	}
	if err := checkWireToken("container name", fields[2]); err != nil {
		return "", "", err
	}
	state, err := removeGuestContainer(ctx, tools, fields[2])
	return fields[1], state, err
}

// removeGuestContainer stops and removes the named container, and succeeds
// only when docker no longer lists it. A container that is already gone is
// "absent", so a repeated teardown succeeds.
func removeGuestContainer(ctx context.Context, tools guestTools, name string) (string, error) {
	listed, err := listGuestContainer(ctx, tools, name)
	if err != nil {
		return "", err
	}
	if !listed {
		return "absent", nil
	}
	_, stopErr := runGuestTool(ctx, "docker stop", slices.Concat(tools.Docker, []string{"container", "stop", "--time", containerStopSeconds, name}), nil)
	_, removeErr := runGuestTool(ctx, "docker rm", slices.Concat(tools.Docker, []string{"container", "rm", "--force", name}), nil)
	listed, err = listGuestContainer(ctx, tools, name)
	if err != nil {
		return "", errors.Join(stopErr, removeErr, err)
	}
	if listed {
		return "", errors.Join(fmt.Errorf("container %s is still present after its removal", name), stopErr, removeErr)
	}
	return "removed", nil
}

// listGuestContainer reports whether docker lists a container with exactly
// this name. The daemon may match the name with its leading slash.
func listGuestContainer(ctx context.Context, tools guestTools, name string) (bool, error) {
	out, err := runGuestTool(ctx, "docker container ls", slices.Concat(tools.Docker, []string{"container", "ls", "--all", "--quiet", "--filter", "name=^/?" + regexp.QuoteMeta(name) + "$"}), nil)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}
