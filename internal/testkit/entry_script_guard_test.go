// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package testkit

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
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

// TestEntryScriptGuardFindsHelpersFromThePhysicalRoot starts the script
// through a symlink to its root and through a symlink to an ancestor of the
// root. Right after the guard passes, the script itself points that symlink at
// another tree, as a racing user who can replace the symlink would. The
// helper still comes from the tree the guard checked, because the script
// finds it from ${workcell_entry_root}.
func TestEntryScriptGuardFindsHelpersFromThePhysicalRoot(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	for _, side := range []string{"good", "evil"} {
		tree := filepath.Join(base, side, "tree")
		mkdirs(t, filepath.Join(tree, "scripts", "lib"))
		if err := os.WriteFile(filepath.Join(tree, "scripts", "lib", "helper.sh"), []byte("echo "+side+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		body := "#!/bin/bash\n" + entryScriptGuard("scripts/entry.sh") +
			`read -r link target <"${workcell_entry_root}/../swap" && ln -sfn -- "${target}" "${link}"` + "\n" +
			`source "${workcell_entry_root}/scripts/lib/helper.sh"` + "\n"
		if err := os.WriteFile(filepath.Join(tree, "scripts", "entry.sh"), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		name, link, good, evil, entry string
	}{
		{"a symlinked root", "root-link", filepath.Join(base, "good", "tree"), filepath.Join(base, "evil", "tree"), filepath.Join("root-link", "scripts", "entry.sh")},
		{"a symlinked ancestor", "ancestor-link", filepath.Join(base, "good"), filepath.Join(base, "evil"), filepath.Join("ancestor-link", "tree", "scripts", "entry.sh")},
	} {
		link := filepath.Join(base, row.link)
		if err := os.Symlink(row.good, link); err != nil {
			t.Fatal(err)
		}
		swap := link + " " + row.evil + "\n"
		if err := os.WriteFile(filepath.Join(base, "good", "swap"), []byte(swap), 0o644); err != nil {
			t.Fatal(err)
		}
		if code, out := runEntryScript(t, base, filepath.Join(base, row.entry)); code != 0 || out != "good\n" {
			t.Errorf("%s swapped after the guard: exit %d, want the checked tree's helper:\n%s", row.name, code, out)
		}
		if target, err := os.Readlink(link); err != nil || target != row.evil {
			t.Errorf("%s: the script did not swap the link: %q, %v", row.name, target, err)
		}
	}
}

// TestEntryScriptGuardReadsGroupsAndACLs covers the group and access control
// list rules where they differ from the mode bits alone: an ancestor above the
// root's parent, as /opt/homebrew/Cellar is above Cellar/workcell/X/libexec;
// an access control list that grants only read; and a group that is private by
// name and members but is another user's primary group.
func TestEntryScriptGuardReadsGroupsAndACLs(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	tree := filepath.Join(base, "parent", "tree")
	mkdirs(t, filepath.Join(tree, "scripts", "lib"))
	script := filepath.Join(tree, "scripts", "entry.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\n"+entryScriptGuard("scripts/entry.sh")+"echo ran\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(tree, "scripts", "lib", "helper.sh")
	if err := os.WriteFile(helper, []byte("true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check := func(name string, ok bool, out string, err error) {
		t.Helper()
		switch {
		case ok && (err != nil || out != "ran\n"):
			t.Errorf("%s: %v:\n%s", name, err, out)
		case !ok && (err == nil || !strings.Contains(out, entryScriptRefusal)):
			t.Errorf("%s: %v, want the refusal:\n%s", name, err, out)
		}
	}
	run := func(name string, ok bool) {
		t.Helper()
		code, out := runEntryScript(t, base, script)
		var err error
		if code != 0 {
			err = fmt.Errorf("exit %d", code)
		}
		check(name, ok, out, err)
	}
	baseMode := os.FileMode(0o700)
	run("a private tree", true)

	// A shared group may write an ancestor only where the guard trusts it:
	// the user's private group, and on macOS the admin group, whose members
	// can already run sudo.
	if privateGroup(t) {
		chmod(t, base, 0o775)
		run("a 775 ancestor in the user's private group", true)
		chmod(t, base, baseMode)
	}
	if gid, ok := sharedGroup(t); ok {
		chgrp(t, base, gid)
		chmod(t, base, 0o775)
		run("a 775 ancestor in a shared group", false)
		chmod(t, base, baseMode)
		chgrp(t, base, os.Getgid())
	}
	if runtime.GOOS == "darwin" {
		if err := os.Lchown(base, -1, 80); err != nil {
			t.Logf("cannot give the ancestor to the admin group: %v", err)
		} else {
			chmod(t, base, 0o775)
			run("a 775 ancestor in the admin group", true)
			chmod(t, base, baseMode)
			chgrp(t, base, os.Getgid())
		}
	}

	// An access control list counts only when it lets another user write.
	if runtime.GOOS == "linux" && privateGroup(t) {
		for _, row := range []struct {
			name string
			path string
			perm uint32
			ok   bool
		}{
			{"an ancestor whose access control list lets another user read", base, 5, true},
			{"an ancestor whose access control list lets another user write", base, 7, false},
			{"a helper whose access control list lets another user read", helper, 4, true},
		} {
			info, err := os.Stat(row.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := setNamedUserACL(row.path, 65534, row.perm); err != nil {
				t.Logf("no access control list on this filesystem: %v", err)
				break
			}
			run(row.name, row.ok)
			if err := unix.Removexattr(row.path, "system.posix_acl_access"); err != nil {
				t.Fatal(err)
			}
			chmod(t, row.path, info.Mode().Perm())
		}
	}
	if runtime.GOOS == "darwin" {
		for _, row := range []struct {
			name, entry string
			ok          bool
		}{
			{"a helper whose access control list lets everyone read", "everyone allow read", true},
			{"a helper whose access control list lets everyone write", "everyone allow write", false},
		} {
			if out, err := execRetryETXTBSY(func() *exec.Cmd { return exec.Command("/bin/chmod", "+a", row.entry, helper) }); err != nil {
				t.Fatalf("chmod +a: %v\n%s", err, out)
			}
			run(row.name, row.ok)
			if out, err := execRetryETXTBSY(func() *exec.Cmd { return exec.Command("/bin/chmod", "-N", helper) }); err != nil {
				t.Fatalf("chmod -N: %v\n%s", err, out)
			}
		}
	}

	// getent group lists no user whose primary group it is, so the guard also
	// reads getent passwd: a fake /etc/group and /etc/passwd in a private mount
	// namespace, which only root can set up.
	if os.Geteuid() == 0 && os.Getgid() == 0 {
		chmod(t, filepath.Join(tree, "scripts"), 0o775)
		const rootUser = "root:x:0:0:root:/root:/bin/bash\n"
		for _, row := range []struct {
			name, group, passwd string
			gid                 int
			ok                  bool
		}{
			{"group write for a group that no other user has as primary group", "root:x:0:\n", rootUser, 0, true},
			{"group write for a group that another user has as primary group", "root:x:0:\n", rootUser + "svc:x:4242:0::/:/bin/false\n", 0, false},
			// Run with gid 1000, the prefix of another user's primary group 10000.
			{"group write for a group whose number begins another user's primary group", "root:x:1000:\n",
				"root:x:0:1000:root:/root:/bin/bash\nsvc:x:4242:10000::/:/bin/false\n", 1000, true},
		} {
			chgrp(t, filepath.Join(tree, "scripts"), row.gid)
			dir := t.TempDir()
			group, passwd := filepath.Join(dir, "group"), filepath.Join(dir, "passwd")
			if err := os.WriteFile(group, []byte(row.group), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(passwd, []byte(row.passwd), 0o644); err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			out, err := execRetryETXTBSY(func() *exec.Cmd {
				cmd := exec.Command("unshare", "-m", "sh", "-c", `mount --bind "$1" /etc/group && mount --bind "$2" /etc/passwd && exec setpriv --regid="$4" --clear-groups bash "$3"`, "sh", group, passwd, script, fmt.Sprint(row.gid))
				cmd.Dir = base
				cmd.Env = []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}
				return cmd
			})
			if strings.Contains(string(out), "mount:") || strings.Contains(string(out), "unshare:") || strings.Contains(string(out), "setpriv:") {
				t.Logf("no private mount namespace: %s", out)
				break
			}
			check(row.name, row.ok, string(out), err)
		}
		chgrp(t, filepath.Join(tree, "scripts"), 0)
		chmod(t, filepath.Join(tree, "scripts"), 0o755)
	}
}

// The launcher starts its session monitor from the physical root, which an
// upgrade can move, as a Homebrew upgrade moves the Cellar version directory.
// A newer launcher from another root must still find the monitor live for its
// own state file, also where COLUMNS would narrow ps, and not for another
// session's state file or a command that only mentions this one.
func TestSessionMonitorLivenessSurvivesAMovedRoot(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	oldScript := filepath.Join(dir, "old", "scripts", "workcell")
	mkdirs(t, filepath.Dir(oldScript))
	if err := os.WriteFile(oldScript, []byte("while :; do sleep 1; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	audit := filepath.Join(dir, "audit")
	mkdirs(t, audit)
	state := filepath.Join(audit, "session-monitor.env")
	launcher := ShellQuote(filepath.Join(repoRoot(t), "scripts", "workcell"))
	var b strings.Builder
	for _, name := range []string{"session_monitor_state_path", "session_monitor_pid_is_live"} {
		b.WriteString(`eval "$(sed -n '/^` + name + `() {$/,/^}$/p' ` + launcher + `)"` + "\n")
	}
	// The probe starts the rest of its arguments as the monitor and checks it
	// against the audit directory in its first argument.
	b.WriteString(`workcell_entry_root=` + ShellQuote(filepath.Join(dir, "new")) + `
"${@:2}" </dev/null >/dev/null 2>&1 &
SESSION_META_MONITOR_PID=$!
trap 'kill "${SESSION_META_MONITOR_PID}"' EXIT
for _ in $(seq 100); do
  [[ "$(ps -ww -o command= -p "${SESSION_META_MONITOR_PID}")" != *"--state-file "* ]] || break
  sleep 0.05
done
SESSION_META_SESSION_AUDIT_DIR="$1"
session_monitor_pid_is_live
`)
	probe := filepath.Join(dir, "probe.sh")
	if err := os.WriteFile(probe, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	monitor := func(stateFile string) []string {
		return []string{"/bin/bash", oldScript, "session", "monitor", "--state-file", stateFile}
	}
	for _, row := range []struct {
		name, audit string
		argv        []string
		columns     bool
		live        bool
	}{
		{"its own state file", audit, monitor(state), false, true},
		{"its own state file with COLUMNS=80", audit, monitor(state), true, true},
		{"another session's state file", filepath.Join(dir, "other"), monitor(state), false, false},
		{"a state file with a suffix", audit, monitor(state + ".bak"), false, false},
		{"a state file with a prefix", audit, monitor("/zz" + state), false, false},
		{"a comment that names the state file", audit, []string{"/bin/sh", "-c", "while :; do sleep 1; done # session monitor --state-file " + state}, false, false},
	} {
		out, err := execRetryETXTBSY(func() *exec.Cmd {
			cmd := exec.Command("/bin/bash", append([]string{probe, row.audit}, row.argv...)...)
			if row.columns {
				cmd.Env = append(os.Environ(), "COLUMNS=80")
			}
			return cmd
		})
		if (err == nil) != row.live {
			t.Errorf("a monitor from another root, %s: live=%v, want %v:\n%s", row.name, err == nil, row.live, out)
		}
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

// TestEntryScriptsCarryTheGuard is the one rule for repository entry scripts:
// each tracked executable Bash script in the entry directories that reads
// BASH_SOURCE to find its helpers runs entryScriptGuard, with its own path in
// the tree, before any other command. scripts/workcell follows its installed
// symlink first, so it runs the same guard on the file that resolve_self_path
// finds before it sets ROOT_DIR and sources a library.
func TestEntryScriptsCarryTheGuard(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	listing, err := exec.Command("git", "-C", root, "ls-files", "-s", "-z", "--", "scripts", "tests", ".githooks", "verify").Output()
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, record := range strings.Split(string(listing), "\x00") {
		meta, path, ok := strings.Cut(record, "\t")
		if !ok || !strings.HasPrefix(meta, "100755 ") || path == "scripts/workcell" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(string(data), "\n")
		if !strings.HasPrefix(lines[0], "#!") || !strings.Contains(lines[0], "bash") || !strings.Contains(string(data), "BASH_SOURCE") {
			continue
		}
		checked++
		first := 1
		for first < len(lines) && (strings.TrimSpace(lines[first]) == "" || strings.HasPrefix(strings.TrimSpace(lines[first]), "#")) {
			first++
		}
		want := entryScriptGuard(path)
		if got := strings.Join(lines[first:min(first+strings.Count(want, "\n"), len(lines))], "\n") + "\n"; got != want {
			t.Errorf("%s: the first command is not the entry guard for its path:\n%s", path, got)
			continue
		}
		rest := lines[min(first+strings.Count(want, "\n"), len(lines)):]
		checkPhysicalSelf(t, path, rest)
		if strings.Contains(strings.Join(rest, "\n"), "/scripts/lib/trusted-entrypoint.sh\"") && filepath.Dir(path) != "scripts" {
			t.Errorf("%s: scripts/lib/trusted-entrypoint.sh re-runs its caller as scripts/ plus the caller's name, so only a script in scripts/ may source it", path)
		}
	}
	if checked < 100 {
		t.Fatalf("checked %d entry scripts; the listing looks incomplete", checked)
	}

	launcher, err := os.ReadFile(filepath.Join(root, "scripts", "workcell"))
	if err != nil {
		t.Fatal(err)
	}
	want := "WORKCELL_SELF_PATH=\"$(resolve_self_path)\"\n" +
		strings.ReplaceAll(entryScriptGuard("scripts/workcell"), "${BASH_SOURCE[0]}", "${WORKCELL_SELF_PATH}") +
		`ROOT_DIR="${workcell_entry_root}"` + "\n"
	at := strings.Index(string(launcher), want)
	if strings.Count(string(launcher), want) != 1 || at > strings.Index(string(launcher), "\nsource ") {
		t.Error("scripts/workcell does not run the entry guard on its resolved file before it sets ROOT_DIR from the checked root and sources a library")
	} else {
		checkPhysicalSelf(t, "scripts/workcell", strings.Split(string(launcher[at+len(want):]), "\n"))
	}

	// A library runs in its caller's shell, where $0 is the caller's path as
	// it was started, so no shell library may pass that path on as a word.
	// Files in another language, such as workcell-cache-lock.pl, are skipped.
	shellLibraries := 0
	for _, pattern := range []string{"scripts/lib/*", "scripts/lib/launcher/*", "scripts/ci/lib/*"} {
		libraries, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			t.Fatal(err)
		}
		for _, library := range libraries {
			if info, err := os.Lstat(library); err != nil || !info.Mode().IsRegular() {
				continue
			}
			data, err := os.ReadFile(library)
			if err != nil {
				t.Fatal(err)
			}
			first, _, _ := strings.Cut(string(data), "\n")
			if !strings.HasSuffix(library, ".sh") && !(strings.HasPrefix(first, "#!") && strings.Contains(first, "sh")) {
				continue
			}
			shellLibraries++
			for number, line := range strings.Split(string(data), "\n") {
				if trimmed := strings.TrimSpace(line); !strings.HasPrefix(trimmed, "#") && libraryDollarZero.MatchString(line) {
					t.Errorf("%s:%d: a library passes on $0, the caller's unchecked path: %s", library[len(root)+1:], number+1, strings.TrimSpace(line))
				}
			}
		}
	}
	if shellLibraries < 20 {
		t.Fatalf("checked %d shell libraries; the listing looks incomplete", shellLibraries)
	}
}

// selfDollarZero matches $0 run as a command or read as a file, and
// selfSourcePath matches a path built from BASH_SOURCE: either one follows the
// path the script was started from, whose symlinks can move after the guard.
// libraryDollarZero matches $0 as a word of its own anywhere in a library, as
// an argument to a command that may run it.
var (
	selfDollarZero    = regexp.MustCompile(`(^\s*|exec |bash -p |\| |head -n 1 )"\$0"(\s|$)`)
	libraryDollarZero = regexp.MustCompile(`(^|[\s;&|(])"?\$(0|\{0\})"?([\s;&|)]|$)`)
	selfSourcePath    = regexp.MustCompile(`dirname "\$\{BASH_SOURCE|\$\{BASH_SOURCE\[0\]%/|(^\s*|bash |exec |--launcher=)"?\$\{BASH_SOURCE\[0\]\}|\*"\$\{BASH_SOURCE\[0\]\}"\*`)
)

// checkPhysicalSelf reports each line after the guard of path that finds a
// file from the path the script was started from rather than from
// ${workcell_entry_root}, the physical root that the guard checked.
func checkPhysicalSelf(t *testing.T, path string, rest []string) {
	t.Helper()
	for number, line := range rest {
		if selfSourcePath.MatchString(line) || selfDollarZero.MatchString(line) {
			t.Errorf("%s: after the guard, line %d finds a path from BASH_SOURCE or $0 instead of ${workcell_entry_root}: %s", path, number+1, strings.TrimSpace(line))
		}
	}
}
