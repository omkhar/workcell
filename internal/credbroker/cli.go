// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package credbroker

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/rootio"
	"golang.org/x/sys/unix"
)

const usageText = "usage: workcell-hostutil credential-broker-cli serve --socket PATH --token-fd N " +
	"--ssh-config FILE --ssh-host HOST --guest-socket PATH --credential KEY=keychain:SERVICE[/ACCOUNT]... --log FILE"

// securityPath is fixed so a PATH entry cannot stand in for the Keychain tool.
var securityPath = "/usr/bin/security"

const keychainTimeout = 2 * time.Minute

// Main runs `credential-broker-cli serve`.
func Main(args []string) error {
	usage := &cliexit.ExitCodeError{Code: 2, Message: usageText}
	if len(args) == 0 || args[0] != "serve" {
		return usage
	}
	flags := flag.NewFlagSet("credential-broker-cli", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	socket := flags.String("socket", "", "")
	tokenFD := flags.Int("token-fd", -1, "")
	sshConfig := flags.String("ssh-config", "", "")
	sshHost := flags.String("ssh-host", "", "")
	guestSocket := flags.String("guest-socket", "", "")
	logPath := flags.String("log", "", "")
	sources := map[string]string{}
	flags.Func("credential", "", func(value string) error {
		key, source, ok := strings.Cut(value, "=")
		if _, dup := sources[key]; !ok || dup {
			return errors.New("bad --credential")
		}
		sources[key] = source
		return nil
	})
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || len(sources) == 0 ||
		*tokenFD < 0 || *sshConfig == "" || *logPath == "" ||
		!filepath.IsAbs(*socket) || !filepath.IsAbs(*guestSocket) ||
		strings.Contains(*socket, ":") || strings.Contains(*guestSocket, ":") ||
		*sshHost == "" || strings.HasPrefix(*sshHost, "-") {
		return usage
	}
	token, err := readToken(*tokenFD)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	credentials := make(map[string]string, len(sources))
	for key, source := range sources {
		if _, ok := grants[key]; !ok {
			return fmt.Errorf("credential %s has no broker grant", key)
		}
		if credentials[key], err = resolveSource(ctx, source); err != nil {
			return fmt.Errorf("credential %s: %w", key, err)
		}
	}
	log, err := openLog(*logPath)
	if err != nil {
		return err
	}
	defer log.Close()
	return Serve(ctx, Config{
		SocketPath:  *socket,
		Token:       token,
		Credentials: credentials,
		Log:         log,
		SSHArgs:     []string{"-F", *sshConfig, "-N", "-o", "ExitOnForwardFailure=yes", "-R", *guestSocket + ":" + *socket, *sshHost},
	})
}

// openLog appends to path without following a symlink anywhere in it.
func openLog(path string) (*os.File, error) {
	parent, cleaned, err := rootio.OpenParentDirectoryNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	fd, err := unix.Openat(int(parent.Fd()), filepath.Base(cleaned), unix.O_WRONLY|unix.O_APPEND|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open request log: %w", err)
	}
	return os.NewFile(uintptr(fd), cleaned), nil
}

// readToken reads the 32-byte session token, hex encoded, from fd.
func readToken(fd int) ([]byte, error) {
	file := os.NewFile(uintptr(fd), "token")
	if file == nil {
		return nil, errors.New("bad --token-fd")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 130))
	if err != nil {
		return nil, err
	}
	token := strings.TrimSuffix(string(data), "\n")
	if decoded, err := hex.DecodeString(token); err != nil || len(decoded) != 32 {
		return nil, errors.New("session token must be 64 hex characters")
	}
	return []byte(token), nil
}

// resolveSource reads keychain:SERVICE[/ACCOUNT] from the login Keychain. The
// value stays in broker memory; it is never written to disk or sent to the VM.
//
// The first read of an item makes macOS ask whether /usr/bin/security may use
// it. Choose "Always Allow" once to add security to the item's access list;
// later sessions then read it without a prompt. "Allow" works for one session
// only, and "Deny" makes the broker exit before it serves anything.
func resolveSource(ctx context.Context, source string) (string, error) {
	item, ok := strings.CutPrefix(source, "keychain:")
	service, account, hasAccount := strings.Cut(item, "/")
	if !ok || service == "" || (hasAccount && account == "") {
		return "", fmt.Errorf("source must be keychain:SERVICE[/ACCOUNT], got %q", source)
	}
	args := []string{"find-generic-password", "-s", service}
	if hasAccount {
		args = append(args, "-a", account)
	}
	// The bound leaves time to answer the Keychain prompt.
	ctx, cancel := context.WithTimeout(ctx, keychainTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, securityPath, append(args, "-w")...)
	cmd.Env = []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "LC_ALL=C"}
	if home, ok := os.LookupEnv("HOME"); ok {
		cmd.Env = append(cmd.Env, "HOME="+home)
	}
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read Keychain item %q: %w", service, err)
	}
	value := strings.TrimSuffix(string(output), "\n")
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("keychain item %q is empty or spans lines", service)
	}
	return value, nil
}
