// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"encoding/binary"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// entryScriptGuardTemplate is the first command of each repository entry
// script that finds its helpers from its own path, with ENTRY_PATH in place of
// the script's path in the Workcell tree. The guard prints the root of the
// tree that holds the script at that path, or nothing, and the script stops
// unless a root came back. The root is the physical directory that the
// script's directory, with the path's directories stripped, resolves to. No
// symlink may sit on the way from that root to the script. A symlink to the
// root or to one of its ancestors may lead there, so the script finds its
// helpers from ${workcell_entry_root}, the physical root, never again from
// BASH_SOURCE or $0. The guard checks the script, each directory from it up
// to the root, and scripts, scripts/ci, scripts/ci/lib, scripts/lib,
// scripts/lib/launcher and runtime/container with their direct entries where
// they exist: each must be a directory or a regular file owned by the user or
// root, that no other user can write and that no group can write unless it is
// the user's private group. That group has the user's own name and no
// members in getent, as USERGROUPS_ENAB makes it. Each ancestor of the root
// must be a directory owned by the user or root that others cannot write
// unless it is sticky, as /tmp is. No checked path may carry an access
// control list: ls -ld marks one with + on Linux, and on macOS ls -lde may
// list only deny entries, as a home directory carries. Deeper helper files
// are not checked. A copy or a link in another place, or in a tree that
// another user can write, loads no helper beside it, and a hard link
// elsewhere does not stop the real script.
const entryScriptGuardTemplate = `workcell_entry_root="$(shopt -s nullglob && f="${BASH_SOURCE[0]}" r=ENTRY_PATH && y=/usr/bin/find && { [[ -x "${y}" ]] || y=/bin/find; } && i=/usr/bin/id && { [[ -x "${i}" ]] || i=/bin/id; } && l=/bin/ls && { [[ -x "${l}" ]] || l=/usr/bin/ls; } && e=/usr/bin/getent && { [[ -x "${e}" ]] || e=/bin/getent; } && { [[ "${f}" == /* ]] || f="${PWD}/${f}"; } && cd -P -- "${f%/*}/" && d="${PWD}" && p="${d%/}/${f##*/}" && root="${p%/"${r}"}" && [[ -n "${root}" && "${root}/${r}" == "${p}" ]] && set -- "${p}" && f="${f%/*}" && while ((${#d} >= ${#root})); do cd -L -- "${f}/" && cd -P . && [[ "${PWD}" == "${d}" ]] && set -- "$@" "${d}" && d="${d%/*}" f="${f}/.." || exit 1; done && for h in scripts scripts/ci scripts/ci/lib scripts/lib scripts/lib/launcher runtime/container; do [[ ! -e "${root}/${h}" && ! -L "${root}/${h}" ]] || set -- "$@" "${root}/${h}" "${root}/${h}"/*; done && q="${root%/*}" && a=(/) && while [[ -n "${q}" ]]; do a+=("${q}") && q="${q%/*}"; done && { u="$(LC_ALL=C "${i}")" || u=""; } && g=(-perm -020) && { [[ "${u}" =~ ^uid=[0-9]+\(([^\)]+)\)\ gid=([0-9]+)\( ]] && n="${BASH_REMATCH[1]}" k="${BASH_REMATCH[2]}" && w="$(LC_ALL=C "${e}" group "${k}")" && [[ "${w}" == "${n}:"*":${k}:" ]] && j=$'\n' && o="${j}$(LC_ALL=C "${e}" passwd)" && o="${o//"${j}${n}:"/${j}#:#:#:#:}" && x=$'\n[^:\n]*:[^:\n]*:[^:\n]*:'"${k}:" && [[ ! "${o}" =~ ${x} ]] && g+=(! -gid "${k}") || :; } && { [[ "${OSTYPE}" != darwin* ]] || g+=(! -gid 80); } && bad="$("${y}" "$@" -maxdepth 0 \( ! -type d ! -type f -o ! -uid "${EUID}" ! -uid 0 -o "${g[@]}" -o -perm -002 \) -print)" && [[ -z "${bad}" ]] && bad="$("${y}" "${a[@]}" -maxdepth 0 \( ! -type d -o ! -uid "${EUID}" ! -uid 0 -o \( "${g[@]}" -o -perm -002 \) ! -perm -1000 \) -print)" && [[ -z "${bad}" ]] && if [[ "${OSTYPE}" == darwin* ]]; then z="$(LC_ALL=C "${l}" -lde -- "$@" "${a[@]}")" && x=$' allow ([^\n]*,)?(write|append|add_file|add_subdirectory|delete|delete_child|writeattr|writeextattr|writesecurity|chown)(,|\n|$)' && [[ ! "${z}" =~ ${x} ]]; else z="$(LC_ALL=C "${l}" -ld -- "$@" "${a[@]}")" && [[ $'\n'"${z}" != *$'\n'?????w????+* ]]; fi && printf '%s\n' "${root}")" || workcell_entry_root="" # fail-closed: an empty root stops the script below
[[ -n "${workcell_entry_root}" ]] || {
  echo "Run ${BASH_SOURCE[0]} from its own place in a Workcell tree that no other user can write, not through a symlink or a copy: it loads helpers from that tree. A filesystem without Unix permissions, such as WSL /mnt/c or exFAT, cannot hold such a tree." >&2
  exit 2
}
`

func entryScriptGuard(path string) string {
	return strings.Replace(entryScriptGuardTemplate, "r=ENTRY_PATH ", "r="+path+" ", 1)
}

const entryScriptRefusal = "not through a symlink or a copy"

// runEntryScript runs path with bash from dir and returns its exit status and
// output.
func runEntryScript(t *testing.T, dir, path string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{path}, args...)...)
	cmd.Dir = dir
	cmd.Env = []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH")}
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatal(err)
	}
	return 0, string(out)
}

// mkdirs makes each directory with mode 0755 whatever the umask, so a test
// tree is not group-writable by accident.
func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// TestEntryScriptGuardAnchorsTheTree runs the guard as scripts/entry.sh in
// each form it can be started from, and from each place a copy or a link of
// it can sit.
func TestEntryScriptGuardAnchorsTheTree(t *testing.T) {
	t.Parallel()

	base := filepath.Join(t.TempDir(), "dir with space $x `y`")
	tree := filepath.Join(base, "tree")
	mkdirs(t, filepath.Join(tree, "scripts"))
	script := filepath.Join(tree, "scripts", "entry.sh")
	body := []byte("#!/bin/bash\n" + entryScriptGuard("scripts/entry.sh") + "echo ran\n")
	if err := os.WriteFile(script, body, 0o755); err != nil {
		t.Fatal(err)
	}
	runs := func(name, dir, path string) {
		t.Helper()
		if code, out := runEntryScript(t, dir, path); code != 0 || out != "ran\n" {
			t.Errorf("%s: exit %d:\n%s", name, code, out)
		}
	}
	refused := func(name, dir, path string) {
		t.Helper()
		if code, out := runEntryScript(t, dir, path); code != 2 || !strings.Contains(out, entryScriptRefusal) || strings.Contains(out, "ran") {
			t.Errorf("%s: exit %d, want the refusal:\n%s", name, code, out)
		}
	}
	runs("absolute path", base, script)
	runs("relative path", base, filepath.Join("tree", "scripts", "entry.sh"))
	runs("bare name in its directory", filepath.Join(tree, "scripts"), "entry.sh")
	runs("path from the root", tree, filepath.Join("scripts", "entry.sh"))
	runs("path back in from its directory", filepath.Join(tree, "scripts"), filepath.Join("..", "scripts", "entry.sh"))

	// A hard link elsewhere does not stop the real script, as a store that
	// deduplicates files makes one.
	elsewhere := filepath.Join(base, "elsewhere")
	mkdirs(t, elsewhere)
	if err := os.Link(script, filepath.Join(elsewhere, "entry.sh")); err != nil {
		t.Fatal(err)
	}
	runs("the real script with a hard link elsewhere", base, script)
	refused("the hard link at another path", base, filepath.Join(elsewhere, "entry.sh"))

	// A plain copy beside other helpers, the case issue 927 names.
	copied := filepath.Join(base, "evil")
	mkdirs(t, copied)
	if err := os.WriteFile(filepath.Join(copied, "entry.sh"), body, 0o755); err != nil {
		t.Fatal(err)
	}
	refused("a copy at another path", base, filepath.Join(copied, "entry.sh"))
	if err := os.Symlink(script, filepath.Join(base, "linked.sh")); err != nil {
		t.Fatal(err)
	}
	refused("a symlink to the script", base, filepath.Join(base, "linked.sh"))
	// A symlinked scripts directory in another tree: the logical root would be
	// that other tree.
	fake := filepath.Join(base, "fake")
	mkdirs(t, fake)
	if err := os.Symlink(filepath.Join(tree, "scripts"), filepath.Join(fake, "scripts")); err != nil {
		t.Fatal(err)
	}
	refused("a symlinked script directory", base, filepath.Join(fake, "scripts", "entry.sh"))

	// A symlink at the script's own path in another private tree. Every
	// directory is real and private, so on macOS, where a symlink takes the
	// umask, only the type check refuses it; on Linux its 0777 mode does too.
	other := filepath.Join(base, "other")
	mkdirs(t, filepath.Join(other, "scripts"))
	if err := os.Symlink(script, filepath.Join(other, "scripts", "entry.sh")); err != nil {
		t.Fatal(err)
	}
	refused("a symlink at the script's path in another private tree", base, filepath.Join(other, "scripts", "entry.sh"))

	// The same path in a tree that another user can write.
	shared := filepath.Join(base, "shared")
	sharedScript := filepath.Join(shared, "scripts", "entry.sh")
	mkdirs(t, filepath.Join(shared, "scripts"))
	if err := os.Link(script, sharedScript); err != nil {
		t.Fatal(err)
	}
	runs("a hard link at the same path in a private tree", base, sharedScript)
	for _, mode := range []os.FileMode{0o777, 0o757, os.ModeSticky | 0o777} {
		for _, dir := range []string{shared, filepath.Join(shared, "scripts")} {
			chmod(t, dir, mode)
			refused("a tree directory with mode "+mode.String(), base, sharedScript)
			chmod(t, dir, 0o755)
		}
	}

	// Group write is refused for a shared group and allowed for the user's
	// private group, the default on Ubuntu, Debian and Fedora with umask 002.
	if gid, ok := sharedGroup(t); ok {
		for _, path := range []string{shared, filepath.Join(shared, "scripts"), script} {
			chgrp(t, path, gid)
			chmod(t, path, 0o775)
			refused("group write for a shared group on "+filepath.Base(path), base, sharedScript)
			chmod(t, path, 0o755)
			chgrp(t, path, os.Getgid())
		}
	} else {
		t.Log("no shared group to test group write with")
	}
	if privateGroup(t) {
		for _, path := range []string{shared, filepath.Join(shared, "scripts"), script} {
			chmod(t, path, 0o775)
		}
		runs("group write for the user's private group", base, sharedScript)
		for _, path := range []string{shared, filepath.Join(shared, "scripts"), script} {
			chmod(t, path, 0o755)
		}
	} else {
		t.Log("the user has no private group")
	}

	// The helper directories that the scripts source from, and their direct
	// entries, follow the same rule where they exist.
	helper := filepath.Join(tree, "scripts", "lib", "helper.sh")
	ciHelper := filepath.Join(tree, "scripts", "ci", "lib", "helper.sh")
	sibling := filepath.Join(tree, "scripts", "sibling.sh")
	mkdirs(t, filepath.Join(tree, "scripts", "lib", "launcher"), filepath.Join(tree, "scripts", "ci", "lib"), filepath.Join(tree, "runtime", "container"))
	for _, path := range []string{helper, ciHelper, sibling} {
		if err := os.WriteFile(path, []byte("true\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runs("private helper directories", base, script)
	for _, path := range []string{helper, ciHelper, sibling, filepath.Join(tree, "scripts", "lib", "launcher"), filepath.Join(tree, "scripts", "ci", "lib"), filepath.Join(tree, "runtime", "container")} {
		chmod(t, path, 0o757)
		refused("a helper path that others can write: "+path[len(tree):], base, script)
		chmod(t, path, 0o755)
	}
	if err := os.Symlink(helper, filepath.Join(tree, "runtime", "container", "linked.sh")); err != nil {
		t.Fatal(err)
	}
	refused("a symlink among the helpers", base, script)
	if err := os.Remove(filepath.Join(tree, "runtime", "container", "linked.sh")); err != nil {
		t.Fatal(err)
	}
	// A FIFO with a private mode: on Linux, where a symlink's own mode is
	// 0777, only the type check refuses a path that is neither a directory
	// nor a regular file.
	fifo := filepath.Join(tree, "runtime", "container", "fifo")
	if out, err := execRetryETXTBSY(func() *exec.Cmd { return exec.Command("mkfifo", "-m", "0644", fifo) }); err != nil {
		t.Fatalf("mkfifo: %v\n%s", err, out)
	}
	refused("a FIFO among the helpers", base, script)
	if err := os.Remove(fifo); err != nil {
		t.Fatal(err)
	}

	// The root's parent can rename the root, so others must not write it,
	// unless the sticky bit stops them as it does on /tmp.
	chmod(t, base, 0o777)
	refused("a root whose parent others can write", base, script)
	chmod(t, base, os.ModeSticky|0o777)
	runs("a root whose parent is sticky", base, script)
	chmod(t, base, 0o755)
	if gid, ok := sharedGroup(t); ok {
		chgrp(t, base, gid)
		chmod(t, base, 0o775)
		refused("a root whose parent a shared group can write", base, script)
		chmod(t, base, 0o755)
		chgrp(t, base, os.Getgid())
	}
	// Every further ancestor follows the same rule, since it can rename the
	// directory below it.
	ancestor := filepath.Dir(base)
	ancestorInfo, err := os.Stat(ancestor)
	if err != nil {
		t.Fatal(err)
	}
	chmod(t, ancestor, 0o777)
	refused("a root whose grandparent others can write", base, script)
	chmod(t, ancestor, os.ModeSticky|0o777)
	runs("a root whose grandparent is sticky", base, script)
	chmod(t, ancestor, ancestorInfo.Mode().Perm())

	// An access control list can grant another user write while the mode
	// bits show only the group mask, which the private group allowance
	// passes, so a list whose mask grants write is refused.
	if runtime.GOOS == "linux" && privateGroup(t) {
		chmod(t, helper, 0o664)
		runs("a helper that the user's private group can write", base, script)
		if err := setNamedUserACL(helper, 65534, 6); err != nil {
			t.Logf("no access control list on this filesystem: %v", err)
		} else {
			refused("a helper whose access control list lets another user write", base, script)
			if err := unix.Removexattr(helper, "system.posix_acl_access"); err != nil {
				t.Fatal(err)
			}
		}
		chmod(t, helper, 0o644)
	}

	// The private group needs both the user's name and no members, read the
	// way the guard reads them: a fake /etc/group in a private mount
	// namespace, which only root can set up.
	if os.Geteuid() == 0 && os.Getgid() == 0 {
		chmod(t, filepath.Join(shared, "scripts"), 0o775)
		for _, row := range []struct {
			name, group string
			ok          bool
		}{
			{"the user's own group", "root:x:0:\n", true},
			{"a group named after another user", "wheel:x:0:\n", false},
			{"a group named after the user with members", "root:x:0:nobody\n", false},
		} {
			fakeGroup := filepath.Join(t.TempDir(), "group")
			if err := os.WriteFile(fakeGroup, []byte(row.group), 0o644); err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			out, err := execRetryETXTBSY(func() *exec.Cmd {
				cmd := exec.Command("unshare", "-m", "sh", "-c", `mount --bind "$1" /etc/group && exec bash "$2"`, "sh", fakeGroup, sharedScript)
				cmd.Dir = base
				cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
				return cmd
			})
			switch {
			case strings.Contains(string(out), "mount:") || strings.Contains(string(out), "unshare:"):
				t.Logf("no private mount namespace: %s", out)
			case row.ok && (err != nil || string(out) != "ran\n"):
				t.Errorf("group write for %s: %v:\n%s", row.name, err, out)
			case !row.ok && (err == nil || !strings.Contains(string(out), entryScriptRefusal)):
				t.Errorf("group write for %s ran: %v:\n%s", row.name, err, out)
			}
		}
		chmod(t, filepath.Join(shared, "scripts"), 0o755)
	}

	// The owner check needs a file that another user owns, which only root
	// can make; a non-root run has no such file to place.
	if os.Geteuid() == 0 {
		if err := os.Lchown(filepath.Join(shared, "scripts"), 65534, 65534); err != nil {
			t.Fatal(err)
		}
		refused("a tree directory that another user owns", base, sharedScript)
	}
	if code, out := runEntryScript(t, base, filepath.Join(base, "missing.sh")); code == 0 {
		t.Errorf("a missing script ran:\n%s", out)
	}
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func chgrp(t *testing.T, path string, gid int) {
	t.Helper()
	if err := os.Lchown(path, -1, gid); err != nil {
		t.Fatal(err)
	}
}

// privateGroup reports whether the user's group is the private group that
// USERGROUPS_ENAB makes, as the guard reads it: getent lists it with the
// user's own name and no members, and no other user has it as primary group.
func privateGroup(t *testing.T) bool {
	t.Helper()
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	gid := strconv.Itoa(os.Getgid())
	out, err := execRetryETXTBSY(func() *exec.Cmd { return exec.Command("getent", "group", gid) })
	line := strings.TrimSuffix(string(out), "\n")
	if err != nil || !strings.HasPrefix(line, current.Username+":") || !strings.HasSuffix(line, ":"+gid+":") {
		return false
	}
	users, err := execRetryETXTBSY(func() *exec.Cmd { return exec.Command("getent", "passwd") })
	if err != nil {
		return false
	}
	for _, entry := range strings.Split(string(users), "\n") {
		if fields := strings.Split(entry, ":"); len(fields) > 3 && fields[3] == gid && fields[0] != current.Username {
			return false
		}
	}
	return true
}

// setNamedUserACL gives uid the permission bits perm on path through a POSIX
// access control list entry, as setfacl -m u:UID:PERM does. The owner, group
// and other entries keep path's mode, and the mask, which the mode then shows
// as its group bits, is perm and the group's own bits.
func setNamedUserACL(path string, uid, perm uint32) error {
	const undefined = 0xffffffff
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	mode := uint32(info.Mode().Perm())
	group := mode >> 3 & 7
	entries := [][3]uint32{{0x01, mode >> 6 & 7, undefined}, {0x02, perm, uid}, {0x04, group, undefined}, {0x10, perm | group, undefined}, {0x20, mode & 7, undefined}}
	data := binary.LittleEndian.AppendUint32(nil, 2)
	for _, entry := range entries {
		data = binary.LittleEndian.AppendUint16(data, uint16(entry[0]))
		data = binary.LittleEndian.AppendUint16(data, uint16(entry[1]))
		data = binary.LittleEndian.AppendUint32(data, entry[2])
	}
	return unix.Setxattr(path, "system.posix_acl_access", data, 0)
}

// sharedGroup returns a group with another name than the user that this
// process may give its files to: any group for root, else one it is in. On
// macOS it skips the admin group, which the guard trusts.
func sharedGroup(t *testing.T) (int, bool) {
	t.Helper()
	current, err := user.Current()
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		candidates = append(candidates, 1, 2, 3, 65534)
	}
	for _, gid := range candidates {
		if runtime.GOOS == "darwin" && gid == 80 {
			continue
		}
		if group, err := user.LookupGroupId(strconv.Itoa(gid)); err == nil && group.Name != current.Username {
			return gid, true
		}
	}
	return 0, false
}

// TestUninstallRefusesCopyBesideHostileHelper is the case issue 927 names: a
// copy of scripts/uninstall.sh, or a hard link to it, beside a hostile
// lib/launcher/host-detect.sh. The script refuses before it loads that helper.
func TestUninstallRefusesCopyBesideHostileHelper(t *testing.T) {
	t.Parallel()

	data, err := os.ReadFile(filepath.Join(repoRoot(t), "scripts", "uninstall.sh"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	original := filepath.Join(dir, "checkout", "scripts", "uninstall.sh")
	mkdirs(t, filepath.Dir(original))
	if err := os.WriteFile(original, data, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, place := range map[string]string{
		"a copy in another directory":         filepath.Join(dir, "evil"),
		"a hard link in a shared tree":        filepath.Join(dir, "shared", "scripts"),
		"a copy in the checkout's other path": filepath.Join(dir, "checkout", "evil"),
	} {
		marker := filepath.Join(place, "hostile-ran")
		mkdirs(t, filepath.Join(place, "lib", "launcher"))
		if err := os.WriteFile(filepath.Join(place, "lib", "launcher", "host-detect.sh"), []byte("touch "+ShellQuote(marker)+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		entry := filepath.Join(place, "uninstall.sh")
		if strings.Contains(name, "hard link") {
			if err := os.Link(original, entry); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Dir(place), 0o777); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(entry, data, 0o755); err != nil {
			t.Fatal(err)
		}
		if code, out := runEntryScript(t, dir, entry, "--dry-run"); code != 2 || !strings.Contains(out, entryScriptRefusal) {
			t.Errorf("%s: exit %d, want the refusal:\n%s", name, code, out)
		}
		if _, statErr := os.Stat(marker); statErr == nil {
			t.Errorf("%s: uninstall.sh ran the helper beside it", name)
		}
	}
}

// TestWorkcellLauncherAnchorsItsTree covers the launcher, which follows a
// symlink to its checkout file on purpose: the installed symlink runs, a hard
// link in another tree does not stop the checkout launcher, and that link or
// a copy is refused where another user can write before the launcher reads
// anything beside it.
func TestWorkcellLauncherAnchorsItsTree(t *testing.T) {
	t.Parallel()

	changelog := "## v1.2.3\n"
	launcher := writeWorkcellVersionFixture(t, &changelog)
	installed := filepath.Join(t.TempDir(), "workcell")
	if err := os.Symlink(launcher, installed); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(filepath.Dir(filepath.Dir(launcher)), "other")
	mkdirs(t, filepath.Join(other, "scripts"))
	if err := os.WriteFile(filepath.Join(other, "CHANGELOG.md"), []byte("## v6.6.6\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(other, "scripts", "workcell")
	if err := os.Link(launcher, hardlink); err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(other, "workcell")
	data, err := os.ReadFile(launcher)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copied, data, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{launcher, installed} {
		if got := runWorkcellVersion(t, path); got != "workcell v1.2.3\n" {
			t.Errorf("%s --version = %q with a hard link elsewhere", path, got)
		}
	}
	if err := os.Chmod(other, 0o777); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{hardlink, copied} {
		out, err := execRetryETXTBSY(func() *exec.Cmd { return exec.Command(path, "--version") })
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 || !strings.Contains(string(out), entryScriptRefusal) || strings.Contains(string(out), "v6.6.6") {
			t.Errorf("%s --version = %v, want exit 2 and the refusal:\n%s", path, err, out)
		}
	}
}
