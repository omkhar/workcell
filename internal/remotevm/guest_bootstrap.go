// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package remotevm

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/imageref"
)

// The bootstrap wire format is one header line and the floor script, then one
// ack line:
//
//	host:  workcell-bootstrap-v1 <image-ref> <floor-sha256> <floor-size>\n<floor script>
//	guest: ok <image-ref> <floor-sha256>\n  or  error <quoted reason>\n
const bootstrapMagic = "workcell-bootstrap-v1"

// maxFloorScriptBytes bounds the floor script. The plan for a few hundred
// endpoints is tens of KiB.
const maxFloorScriptBytes = 1 << 20

// guestTools holds the pinned argv prefix of each guest tool. The guest runs
// them without a shell; only the floor script itself is shell.
type guestTools struct {
	Docker []string
	Bash   []string
}

// checkGuestImageRef accepts only a digest-pinned reference that is one argv
// word and one header field.
func checkGuestImageRef(ref string) error {
	if !imageref.Pinned(ref) || strings.HasPrefix(ref, "-") || strings.ContainsAny(ref, " \t\r\n") {
		return fmt.Errorf("image ref must be one word pinned by digest (name@sha256:<64 hex>): %q", ref)
	}
	return nil
}

// sendBootstrap sends the framed request and accepts only an ack that names
// the same image and floor digest.
func sendBootstrap(conn io.ReadWriter, ref string, script []byte, floorDigest string) error {
	if _, err := fmt.Fprintf(conn, "%s %s %s %d\n", bootstrapMagic, ref, floorDigest, len(script)); err != nil {
		return err
	}
	if _, err := conn.Write(script); err != nil {
		return err
	}
	line, err := readArchiveLine(bufio.NewReaderSize(conn, 4096))
	if err != nil {
		return fmt.Errorf("read bootstrap ack: %w", err)
	}
	if line != "ok "+ref+" "+floorDigest {
		return fmt.Errorf("guest refused the bootstrap: %q", line)
	}
	return nil
}

// serveGuest answers one brokered request on conn: a bootstrap, a session
// start or stop, or else a workspace archive.
func serveGuest(ctx context.Context, conn io.ReadWriter, root string, limits workspaceLimits, tools guestTools) error {
	reader := bufio.NewReaderSize(conn, 4096)
	stream := struct {
		io.Reader
		io.Writer
	}{reader, conn}
	for magic, receive := range map[string]func(context.Context, io.ReadWriter, guestTools) error{
		bootstrapMagic:    receiveBootstrap,
		sessionStartMagic: receiveSessionStart,
		sessionStopMagic:  receiveSessionStop,
	} {
		if peek, _ := reader.Peek(len(magic) + 1); string(peek) == magic+" " {
			return receive(ctx, stream, tools)
		}
	}
	return receiveWorkspace(stream, root, limits)
}

// receiveBootstrap runs one framed bootstrap and acks the result.
func receiveBootstrap(ctx context.Context, conn io.ReadWriter, tools guestTools) error {
	ref, floorDigest, err := bootstrapGuest(ctx, bufio.NewReaderSize(conn, 4096), tools)
	return ackGuest(conn, err, ref+" "+floorDigest)
}

// bootstrapGuest verifies the frame before it runs anything. It applies the
// floor first, so the floor is in force before an image is pulled or started.
// Then it pulls the image by digest and checks that the local image carries
// that digest.
func bootstrapGuest(ctx context.Context, reader *bufio.Reader, tools guestTools) (string, string, error) {
	line, err := readArchiveLine(reader)
	if err != nil {
		return "", "", fmt.Errorf("read bootstrap header: %w", err)
	}
	fields := strings.Split(line, " ")
	if len(fields) != 4 || fields[0] != bootstrapMagic {
		return "", "", fmt.Errorf("bootstrap header is malformed")
	}
	ref, floorDigest := fields[1], fields[2]
	if err := checkGuestImageRef(ref); err != nil {
		return "", "", err
	}
	size, err := strconv.Atoi(fields[3])
	if err != nil || size < 1 || size > maxFloorScriptBytes {
		return "", "", fmt.Errorf("guest floor script must be 1 to %d bytes", maxFloorScriptBytes)
	}
	script := make([]byte, size)
	if _, err := io.ReadFull(reader, script); err != nil {
		return "", "", fmt.Errorf("guest floor script is truncated: %w", err)
	}
	if sum := sha256.Sum256(script); hex.EncodeToString(sum[:]) != floorDigest {
		return "", "", fmt.Errorf("guest floor script digest mismatch")
	}
	if _, err := runGuestTool(ctx, "egress floor", slices.Concat(tools.Bash, []string{"-s", "--"}), script); err != nil {
		return "", "", err
	}
	if _, err := runGuestTool(ctx, "docker pull", slices.Concat(tools.Docker, []string{"pull", "--quiet", ref}), nil); err != nil {
		return "", "", err
	}
	out, err := runGuestTool(ctx, "docker image inspect", slices.Concat(tools.Docker, []string{"image", "inspect", "--format", "{{json .RepoDigests}}", ref}), nil)
	if err != nil {
		return "", "", err
	}
	var repoDigests []string
	if err := json.Unmarshal([]byte(out), &repoDigests); err != nil {
		return "", "", fmt.Errorf("docker image inspect output is not a digest list: %q", out)
	}
	digest := ref[strings.LastIndex(ref, "@"):]
	if !slices.ContainsFunc(repoDigests, func(repoDigest string) bool { return strings.HasSuffix(repoDigest, digest) }) {
		return "", "", fmt.Errorf("pulled image digests %q do not include %s", repoDigests, digest)
	}
	return ref, floorDigest, nil
}

// runGuestTool feeds stdin to argv, waits for it to exit 0, and returns the
// tail of its stdout.
func runGuestTool(ctx context.Context, name string, argv []string, stdin []byte) (string, error) {
	return runPinned(ctx, name, argv, true, func(stdio io.ReadWriteCloser) (string, error) {
		if _, err := stdio.Write(stdin); err != nil {
			return "", err
		}
		if err := stdio.Close(); err != nil {
			return "", err
		}
		stdout := &tailBuffer{max: 64 << 10}
		if _, err := io.Copy(stdout, stdio); err != nil {
			return "", err
		}
		return string(stdout.b), nil
	})
}
