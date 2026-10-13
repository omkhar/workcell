// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package credbroker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/omkhar/workcell/internal/cliexit"
	"github.com/omkhar/workcell/internal/rootio"
)

func fakeSecurity(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	original := securityPath
	securityPath = writeScript(t, dir, "security", body)
	t.Cleanup(func() { securityPath = original })
	return dir
}

func TestResolveSourceRunsFixedSecurityWithScrubbedEnv(t *testing.T) {
	t.Setenv("WORKCELL_LEAK_CANARY", "leak")
	t.Setenv("PATH", t.TempDir())
	dir := fakeSecurity(t, `printf '%s\n' "$@" > "$(dirname "$0")/args"; env > "$(dirname "$0")/env"; printf 'sk-from-keychain\n'`)
	value, err := resolveSource(context.Background(), "keychain:workcell-claude/me@example")
	if err != nil || value != "sk-from-keychain" {
		t.Fatalf("resolveSource = %q, %v", value, err)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if got := strings.Fields(string(args)); strings.Join(got, " ") != "find-generic-password -s workcell-claude -a me@example -w" {
		t.Fatalf("security args = %q", got)
	}
	env, _ := os.ReadFile(filepath.Join(dir, "env"))
	if strings.Contains(string(env), "WORKCELL_LEAK_CANARY") || !strings.Contains(string(env), "PATH=/usr/bin:/bin") {
		t.Fatalf("security env not scrubbed: %s", env)
	}
	if _, err := resolveSource(context.Background(), "keychain:svc"); err != nil {
		t.Fatalf("service-only source: %v", err)
	}
	args, _ = os.ReadFile(filepath.Join(dir, "args"))
	if strings.Contains(string(args), "-a") {
		t.Fatalf("service-only source passed an account: %q", args)
	}
}

func TestResolveSourceFailsClosed(t *testing.T) {
	fakeSecurity(t, "echo 'security: item not found' >&2; exit 44\n")
	if _, err := resolveSource(context.Background(), "keychain:svc"); err == nil || !strings.Contains(err.Error(), "exit status 44") {
		t.Fatalf("missing item = %v", err)
	}
	fakeSecurity(t, "printf 'a\\nb\\n'\n")
	if _, err := resolveSource(context.Background(), "keychain:svc"); err == nil {
		t.Fatal("multi-line value accepted")
	}
	fakeSecurity(t, "printf '\\n'\n")
	if _, err := resolveSource(context.Background(), "keychain:svc"); err == nil {
		t.Fatal("empty value accepted")
	}
	for _, source := range []string{"file:/x", "keychain:", "keychain:/acct", "keychain:svc/", "svc"} {
		if _, err := resolveSource(context.Background(), source); err == nil {
			t.Fatalf("resolveSource accepted %q", source)
		}
	}
}

func TestMainServesKeychainValuesThroughTheForward(t *testing.T) {
	fakeSecurity(t, "printf 'sk-main\\n'\n")
	dir := fakeSSH(t, `printf '%s\n' "$@" > "$(dirname "$0")/args.tmp"; mv "$(dirname "$0")/args.tmp" "$(dirname "$0")/args"
while [ ! -f "$(dirname "$0")/stop" ]; do sleep 0.05; done
`)
	sockets := socketDir(t)
	socket := filepath.Join(sockets, "b.sock")
	logPath := filepath.Join(sockets, "log.jsonl")
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteString(testToken + "\n"); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	fd, err := syscall.Dup(int(reader.Fd()))
	_ = reader.Close()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- Main([]string{"serve", "--socket", socket, "--token-fd", strconv.Itoa(fd), "--ssh-config", "/cfg",
			"--ssh-host", "colima-wcl", "--guest-socket", "/tmp/workcell-broker-s1.sock",
			"--credential", "claude_api_key=keychain:svc", "--log", logPath})
	}()
	argsFile := filepath.Join(dir, "args")
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if _, err := os.Stat(argsFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("fake ssh never started: %v", <-done)
		}
	}
	args, _ := os.ReadFile(argsFile)
	want := "-F /cfg -N -o ExitOnForwardFailure=yes -R /tmp/workcell-broker-s1.sock:" + socket + " colima-wcl"
	if got := strings.Join(strings.Fields(string(args)), " "); got != want {
		t.Fatalf("ssh args = %q, want %q", got, want)
	}
	if v, err := Lookup(context.Background(), socket, testToken, "api.anthropic.com", "x-api-key"); err != nil || v != "sk-main" {
		t.Fatalf("lookup = %q, %v", v, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stop"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil || !strings.Contains(err.Error(), "ssh forward exited") {
		t.Fatalf("Main = %v, want ssh forward exit", err)
	}
	logData, _ := os.ReadFile(logPath)
	if !strings.Contains(string(logData), `"result":"granted"`) || strings.Contains(string(logData), "sk-main") {
		t.Fatalf("log = %s", logData)
	}
}

func TestMainRejectsBadArguments(t *testing.T) {
	valid := []string{"serve", "--socket", "/tmp/b.sock", "--token-fd", "99", "--ssh-config", "/cfg", "--ssh-host", "colima-x",
		"--guest-socket", "/tmp/g.sock", "--credential", "claude_api_key=keychain:svc", "--log", "/tmp/l"}
	replace := func(flag, value string) []string {
		out := append([]string(nil), valid...)
		for i := range out {
			if out[i] == flag {
				out[i+1] = value
			}
		}
		return out
	}
	for name, args := range map[string][]string{
		"no subcommand":      nil,
		"wrong subcommand":   {"run"},
		"relative socket":    replace("--socket", "b.sock"),
		"colon in guest":     replace("--guest-socket", "/tmp/a:b"),
		"option as ssh host": replace("--ssh-host", "-oProxyCommand=x"),
		"credential no =":    replace("--credential", "claude_api_key"),
		"duplicate key":      append(append([]string(nil), valid...), "--credential", "claude_api_key=keychain:other"),
		"extra argument":     append(append([]string(nil), valid...), "stray"),
	} {
		var exit *cliexit.ExitCodeError
		if err := Main(args); !errors.As(err, &exit) || exit.Code != 2 {
			t.Fatalf("%s: Main = %v, want usage exit 2", name, err)
		}
	}
}

func TestOpenLogRefusesSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "leaf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(dir, filepath.Join(dir, "parent")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(dir, "leaf"), filepath.Join(dir, "parent", "log")} {
		if file, err := openLog(path); err == nil {
			_ = file.Close()
			t.Fatalf("openLog(%s) followed a symlink", path)
		}
	}
	file, err := openLog(filepath.Join(dir, "log"))
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if info, err := os.Lstat(filepath.Join(dir, "log")); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %v, %v", info, err)
	}
}

func TestReadTokenRequires32HexBytes(t *testing.T) {
	for token, ok := range map[string]bool{testToken + "\n": true, testToken: true, testToken[:62]: false, strings.Repeat("z", 64): false, testToken + testToken: false} {
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		_, _ = writer.WriteString(token)
		_ = writer.Close()
		fd, _ := syscall.Dup(int(reader.Fd()))
		_ = reader.Close()
		if _, err := readToken(fd); (err == nil) != ok {
			t.Fatalf("readToken(%q) err = %v, want ok=%v", token, err, ok)
		}
	}
}

func TestOpenLogRequiresAnOwnerOnlySingleLinkedRegularFile(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "shared")
	if err := os.WriteFile(shared, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(dir, "linked")
	if err := os.WriteFile(linked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(linked, filepath.Join(dir, "twin")); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{shared, linked, fifo} {
		if file, err := openLog(path); err == nil {
			_ = file.Close()
			t.Fatalf("openLog(%s) accepted a log that is not an owner-only single-linked regular file", path)
		}
	}
	own := filepath.Join(dir, "own")
	if err := os.WriteFile(own, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openLog(own)
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
}

func TestOpenLogRefusesAnExtendedACL(t *testing.T) {
	// Mode 0600 and the broker uid do not show an ACL that grants another uid
	// write access, so the open descriptor is checked for one.
	addACL := []string{"/bin/chmod", "+a", "everyone allow write"}
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("setfacl"); err != nil {
			t.Skip("setfacl is not installed")
		}
		addACL = []string{"setfacl", "-n", "-m", "u:" + strconv.Itoa(os.Getuid()+1) + ":rw-"}
	}
	path := filepath.Join(t.TempDir(), "log")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(addACL[0], append(addACL[1:], path)...).CombinedOutput(); err != nil {
		t.Fatalf("add ACL: %v: %s", err, out)
	}
	file, err := openLog(path)
	if err == nil {
		_ = file.Close()
	}
	if !errors.Is(err, rootio.ErrExtendedACL) {
		t.Fatalf("openLog with an extended ACL = %v, want ErrExtendedACL", err)
	}
}
