// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"cmp"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/omkhar/workcell/internal/rootio"
)

// The toolchain scope guard checks the tree that a toolchain candidate patch
// gives, not the patch text. It applies the patch to HEAD in a private index,
// so git itself decides what the patch changes, and then requires:
//   - each changed path is a toolchain file below, a regular file before and
//     after, with the same mode, and neither added, deleted nor renamed;
//   - the file keeps its line count, and each changed line is a pin line of
//     that file before and after, of the same key, with a new value in the
//     reviewed format;
//   - each changed key appears once in the file, and no version moves back;
//   - a checksum or a Debian package path changes only with the version line
//     or the snapshot that it belongs to.
//
// The guard checks the shape of each pin, not that a digest or a checksum is
// the real upstream one. The re-derive job (upstream-refresh-rederive.sh)
// checks that: the publisher needs both verdicts for the same base and tree.
//
// The verdict names the base commit and the tree it checked. The publisher
// refuses to publish any other tree on it.

const toolchainScopeMaxFileBytes = 1 << 20

// toolchainPinOrder says how a new pin value must relate to the old one.
type toolchainPinOrder int

const (
	// toolchainAnyValue allows any value in the format, such as a digest.
	toolchainAnyValue toolchainPinOrder = iota
	// toolchainNoDowngrade compares the numbers in the value, up to an
	// image digest, and refuses a lower version.
	toolchainNoDowngrade
	// toolchainSameTrack keeps the image repository and tag and moves only
	// the digest.
	toolchainSameTrack
)

// toolchainPinRule is one pin line: prefix, a value in format, and suffix.
// When requires is set, the line may change only when the line with that
// prefix in the same file changes too.
type toolchainPinRule struct {
	prefix   string
	format   *regexp.Regexp
	suffix   string
	order    toolchainPinOrder
	requires string
}

// requiring returns the rule with requires set to the prefix of rule.
func (r toolchainPinRule) requiring(rule toolchainPinRule) toolchainPinRule {
	r.requires = rule.prefix
	return r
}

const (
	toolchainSemver = `(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)`
	toolchainDigest = `@sha256:[0-9a-f]{64}`
)

var (
	toolchainBlobIDRE     = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)
	toolchainSemverRE     = regexp.MustCompile(`^` + toolchainSemver + `$`)
	toolchainTagRE        = regexp.MustCompile(`^v` + toolchainSemver + `$`)
	toolchainGoLanguageRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.0$`)
	toolchainMinorRE      = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)
	toolchainSHA256RE     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	toolchainNodeImageRE  = toolPinImagePattern("node")
	toolchainRustImageRE  = regexp.MustCompile(`^rust:` + toolchainSemver + `-slim-trixie` + toolchainDigest + `$`)
	toolchainQEMUImageRE  = regexp.MustCompile(`^tonistiigi/binfmt:qemu-v` + toolchainSemver + `(-(0|[1-9][0-9]*))?` + toolchainDigest + `$`)
	toolchainSnapshotRE   = regexp.MustCompile(`^[0-9]{8}T000000Z$`)
	toolchainNumberRE     = regexp.MustCompile(`[0-9]+`)
)

func toolchainRule(prefix string, format *regexp.Regexp, suffix string, order toolchainPinOrder) toolchainPinRule {
	return toolchainPinRule{prefix: prefix, format: format, suffix: suffix, order: order}
}

func toolchainArg(name string, format *regexp.Regexp, order toolchainPinOrder) toolchainPinRule {
	return toolchainRule("ARG "+name+"=", format, "", order)
}

func toolchainQuoted(key string, format *regexp.Regexp, order toolchainPinOrder) toolchainPinRule {
	return toolchainRule(key+` = "`, format, `"`, order)
}

// toolchainChecksum is a SHA-256 line that changes only with its version line.
func toolchainChecksum(rule toolchainPinRule, version toolchainPinRule) toolchainPinRule {
	return rule.requiring(version)
}

// toolchainGoRustArgs are the Go and Rust pins that both Dockerfiles hold.
func toolchainGoRustArgs() []toolchainPinRule {
	goVersion := toolchainArg("GO_VERSION", toolchainSemverRE, toolchainNoDowngrade)
	return []toolchainPinRule{
		goVersion,
		toolchainChecksum(toolchainArg("GO_LINUX_X86_64_SHA256", toolchainSHA256RE, toolchainAnyValue), goVersion),
		toolchainChecksum(toolchainArg("GO_LINUX_ARM64_SHA256", toolchainSHA256RE, toolchainAnyValue), goVersion),
		toolchainArg("RUST_VERSION", toolchainSemverRE, toolchainNoDowngrade),
	}
}

// toolchainScopeRules lists every file a toolchain candidate may change and
// the pin lines of each, exactly as scripts/update-upstream-pins.sh and
// apply-debian-bootstrap write them. Any other path or line fails the guard.
func toolchainScopeRules() map[string][]toolchainPinRule {
	actionlint := toolchainQuoted("actionlint_version", toolchainSemverRE, toolchainNoDowngrade)
	zizmor := toolchainQuoted("zizmor_version", toolchainSemverRE, toolchainNoDowngrade)
	hadolint := toolchainArg("HADOLINT_VERSION", toolchainTagRE, toolchainNoDowngrade)
	rustup := toolchainArg("RUSTUP_VERSION", toolchainSemverRE, toolchainNoDowngrade)
	snapshot := toolchainRule("DEBIAN_SNAPSHOT=", toolchainSnapshotRE, "", toolchainNoDowngrade)
	// A Debian package path changes only with the snapshot, and its checksum
	// only with its path.
	opensslAMD64 := toolchainRule("DEBIAN_OPENSSL_AMD64_PATH=", debianOpenSSLAMD64Pattern, "", toolchainAnyValue).requiring(snapshot)
	opensslARM64 := toolchainRule("DEBIAN_OPENSSL_ARM64_PATH=", debianOpenSSLARM64Pattern, "", toolchainAnyValue).requiring(snapshot)
	caCertificates := toolchainRule("DEBIAN_CA_CERTIFICATES_PATH=", debianCACertificatesPattern, "", toolchainAnyValue).requiring(snapshot)
	return map[string][]toolchainPinRule{
		"go.mod": {
			toolchainRule("go ", toolchainGoLanguageRE, "", toolchainNoDowngrade),
			toolchainRule("toolchain go", toolchainSemverRE, "", toolchainNoDowngrade),
		},
		"policy/tool-pins.toml": {
			toolchainQuoted("cosign", toolchainTagRE, toolchainNoDowngrade),
			toolchainQuoted("buildx", toolchainTagRE, toolchainNoDowngrade),
			toolchainQuoted("buildkit", toolPinBuildkitPattern, toolchainSameTrack),
			toolchainQuoted("qemu", toolchainQEMUImageRE, toolchainNoDowngrade),
			toolchainQuoted("syft", toolchainTagRE, toolchainNoDowngrade),
			actionlint,
			toolchainChecksum(toolchainQuoted("actionlint_sha256", toolchainSHA256RE, toolchainAnyValue), actionlint),
			zizmor,
			toolchainChecksum(toolchainQuoted("zizmor_sha256", toolchainSHA256RE, toolchainAnyValue), zizmor),
		},
		"runtime/container/Dockerfile": append([]toolchainPinRule{
			toolchainArg("NODE_BASE_IMAGE", toolchainNodeImageRE, toolchainSameTrack),
			toolchainArg("RUST_TOOLCHAIN_IMAGE", toolchainRustImageRE, toolchainNoDowngrade),
		}, toolchainGoRustArgs()...),
		"tools/validator/Dockerfile": append([]toolchainPinRule{
			toolchainArg("VALIDATOR_BASE_IMAGE", toolchainNodeImageRE, toolchainSameTrack),
			hadolint,
			toolchainChecksum(toolchainArg("HADOLINT_LINUX_X86_64_SHA256", toolchainSHA256RE, toolchainAnyValue), hadolint),
			toolchainChecksum(toolchainArg("HADOLINT_LINUX_ARM64_SHA256", toolchainSHA256RE, toolchainAnyValue), hadolint),
			toolchainArg("DEADCODE_VERSION", toolchainTagRE, toolchainNoDowngrade),
			rustup,
			toolchainChecksum(toolchainArg("RUSTUP_INIT_LINUX_X86_64_SHA256", toolchainSHA256RE, toolchainAnyValue), rustup),
			toolchainChecksum(toolchainArg("RUSTUP_INIT_LINUX_ARM64_SHA256", toolchainSHA256RE, toolchainAnyValue), rustup),
		}, toolchainGoRustArgs()...),
		"runtime/container/debian-bootstrap.env": {
			snapshot,
			opensslAMD64,
			toolchainChecksum(toolchainRule("DEBIAN_OPENSSL_AMD64_SHA256=", toolchainSHA256RE, "", toolchainAnyValue), opensslAMD64),
			opensslARM64,
			toolchainChecksum(toolchainRule("DEBIAN_OPENSSL_ARM64_SHA256=", toolchainSHA256RE, "", toolchainAnyValue), opensslARM64),
			caCertificates,
			toolchainChecksum(toolchainRule("DEBIAN_CA_CERTIFICATES_SHA256=", toolchainSHA256RE, "", toolchainAnyValue), caCertificates),
		},
		"runtime/container/rust/Cargo.toml": {
			toolchainQuoted("rust-version", toolchainMinorRE, toolchainNoDowngrade),
		},
		"runtime/container/rust/rust-toolchain.toml": {
			toolchainQuoted("channel", toolchainSemverRE, toolchainNoDowngrade),
		},
		"scripts/ci/build-validator-image.sh": {
			toolchainRule(`BUILDKIT_IMAGE="${WORKCELL_BUILDKIT_IMAGE:-`, toolPinBuildkitPattern, `}"`, toolchainSameTrack),
		},
		"scripts/check-dead-code.sh": {
			toolchainRule(`readonly DEADCODE_VERSION="`, toolchainTagRE, `"`, toolchainNoDowngrade),
		},
	}
}

// ToolchainScopeVerdict is what a passed toolchain scope guard checked.
type ToolchainScopeVerdict struct {
	Base, Tree string
}

// String renders the verdict that the publish script requires.
func (v ToolchainScopeVerdict) String() string { return "passed:" + v.Base + ":" + v.Tree }

// CheckUpstreamRefreshToolchainScope applies the patch to HEAD of repoRoot in
// a private index and checks the tree it gives against toolchainScopeRules.
// The work tree and the real index are not touched.
func CheckUpstreamRefreshToolchainScope(repoRoot, patchPath string) (ToolchainScopeVerdict, error) {
	var zero ToolchainScopeVerdict
	// Read the patch once and hand git those bytes, so a file swapped after
	// the read cannot reach the apply.
	patch, err := rootio.ReadFileNoFollow(patchPath, "toolchain candidate patch", scopeGuardMaxPatchBytes)
	if err != nil {
		return zero, err
	}
	scratch, err := os.MkdirTemp("", "workcell-toolchain-scope-")
	if err != nil {
		return zero, err
	}
	scratchParent, cleaned, err := rootio.OpenParentDirectoryNoFollow(scratch)
	if err != nil {
		return zero, err
	}
	defer scratchParent.Close()
	defer rootio.RemoveAllAtNoFollow(scratchParent, filepath.Base(cleaned)) //nolint:errcheck // private scratch directory
	git := toolchainScopeGit{root: repoRoot, index: filepath.Join(cleaned, "index")}
	base, err := git.run(nil, "rev-parse", "--verify", "--end-of-options", "HEAD^{commit}")
	if err != nil {
		return zero, err
	}
	base = strings.TrimSpace(base)
	if _, err := git.run(nil, "read-tree", base); err != nil {
		return zero, err
	}
	if _, err := git.run(patch, "apply", "--cached", "--binary", "-"); err != nil {
		return zero, fmt.Errorf("the toolchain patch does not apply to %s: %w", base, err)
	}
	tree, err := git.run(nil, "write-tree")
	if err != nil {
		return zero, err
	}
	tree = strings.TrimSpace(tree)
	raw, err := git.run(nil, "diff-tree", "-r", "-z", "--no-renames", "--raw", base, tree)
	if err != nil {
		return zero, err
	}
	problems, err := checkToolchainScopeTree(git, raw)
	if err != nil {
		return zero, err
	}
	if len(problems) > 0 {
		return zero, errors.New(strings.Join(problems, "\n"))
	}
	return ToolchainScopeVerdict{Base: base, Tree: tree}, nil
}

// checkToolchainScopeTree checks each entry of a diff-tree -r -z --raw listing.
func checkToolchainScopeTree(git toolchainScopeGit, raw string) ([]string, error) {
	rules := toolchainScopeRules()
	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, "out of toolchain scope: "+fmt.Sprintf(format, args...))
	}
	fields := strings.Split(raw, "\x00")
	if len(fields) > 0 && fields[len(fields)-1] == "" {
		fields = fields[:len(fields)-1]
	}
	if len(fields) == 0 {
		fail("the patch changes nothing")
	}
	if len(fields)%2 != 0 {
		return nil, fmt.Errorf("unexpected git diff-tree listing")
	}
	for i := 0; i < len(fields); i += 2 {
		meta, path := strings.Fields(strings.TrimPrefix(fields[i], ":")), fields[i+1]
		if len(meta) != 5 {
			return nil, fmt.Errorf("unexpected git diff-tree entry %q", fields[i])
		}
		oldMode, newMode, oldBlob, newBlob, status := meta[0], meta[1], meta[2], meta[3], meta[4]
		fileRules, known := rules[path]
		switch {
		case !known:
			fail("path %s", path)
			continue
		case status != "M":
			fail("%s: change status %s (only an edit in place is allowed)", path, status)
			continue
		case oldMode != newMode || (newMode != "100644" && newMode != "100755"):
			fail("%s: mode %s to %s (a regular file must keep its mode)", path, oldMode, newMode)
			continue
		}
		before, err := git.blob(oldBlob)
		if err != nil {
			return nil, err
		}
		after, err := git.blob(newBlob)
		if err != nil {
			return nil, err
		}
		for _, problem := range checkToolchainScopeFile(fileRules, before, after) {
			fail("%s: %s", path, problem)
		}
	}
	return problems, nil
}

// checkToolchainScopeFile compares two versions of one toolchain file line by
// line and returns each change that is not a pin bump.
func checkToolchainScopeFile(rules []toolchainPinRule, before, after []byte) []string {
	if len(before) > toolchainScopeMaxFileBytes || len(after) > toolchainScopeMaxFileBytes {
		return []string{"file is larger than the toolchain scope limit"}
	}
	oldLines, newLines := strings.Split(string(before), "\n"), strings.Split(string(after), "\n")
	if len(oldLines) != len(newLines) {
		return []string{fmt.Sprintf("line count %d to %d (pin bumps replace lines in place)", len(oldLines), len(newLines))}
	}
	var problems []string
	changed := map[string]bool{}
	type requirement struct {
		line           int
		prefix, needed string
	}
	var requirements []requirement
	for i := range newLines {
		if oldLines[i] == newLines[i] {
			continue
		}
		oldRule, oldValue := matchToolchainPin(rules, oldLines[i])
		newRule, newValue := matchToolchainPin(rules, newLines[i])
		switch {
		case newRule == nil || oldRule == nil || newRule.prefix != oldRule.prefix:
			problems = append(problems, fmt.Sprintf("line %d %q to %q is not a pin bump", i+1, oldLines[i], newLines[i]))
		case countToolchainPrefix(oldLines, newRule.prefix) != 1 || countToolchainPrefix(newLines, newRule.prefix) != 1:
			// Docker and the shell read the last assignment, the updater the first.
			problems = append(problems, fmt.Sprintf("line %d: %s must appear once in the file", i+1, strings.TrimSpace(newRule.prefix)))
		default:
			changed[newRule.prefix] = true
			if newRule.requires != "" {
				requirements = append(requirements, requirement{i + 1, newRule.prefix, newRule.requires})
			}
			if problem := checkToolchainPinOrder(*newRule, oldValue, newValue); problem != "" {
				problems = append(problems, fmt.Sprintf("line %d %s: %s", i+1, strings.TrimSpace(newRule.prefix), problem))
			}
		}
	}
	for _, r := range requirements {
		if !changed[r.needed] {
			problems = append(problems, fmt.Sprintf("line %d: %s changes without %s", r.line, strings.TrimSpace(r.prefix), strings.TrimSpace(r.needed)))
		}
	}
	return problems
}

func countToolchainPrefix(lines []string, prefix string) int {
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			count++
		}
	}
	return count
}

// matchToolchainPin returns the rule whose prefix, format and suffix the whole
// line matches, and the value.
func matchToolchainPin(rules []toolchainPinRule, line string) (*toolchainPinRule, string) {
	for i := range rules {
		rule := &rules[i]
		rest, ok := strings.CutPrefix(line, rule.prefix)
		if !ok {
			continue
		}
		value, ok := strings.CutSuffix(rest, rule.suffix)
		if ok && rule.format.MatchString(value) {
			return rule, value
		}
	}
	return nil, ""
}

// checkToolchainPinOrder returns "" when newValue may replace oldValue.
func checkToolchainPinOrder(rule toolchainPinRule, oldValue, newValue string) string {
	oldTrack, _, _ := strings.Cut(oldValue, "@")
	newTrack, _, _ := strings.Cut(newValue, "@")
	switch rule.order {
	case toolchainSameTrack:
		if oldTrack != newTrack {
			return fmt.Sprintf("image %s moved to %s (only the digest may change)", oldTrack, newTrack)
		}
	case toolchainNoDowngrade:
		order, ok := compareToolchainVersions(newTrack, oldTrack)
		switch {
		case !ok:
			return fmt.Sprintf("%s or %s holds a number too large to compare", newTrack, oldTrack)
		case order < 0:
			return fmt.Sprintf("%s moves back from %s", newTrack, oldTrack)
		}
	}
	return ""
}

// compareToolchainVersions compares the runs of digits in a and b in order;
// when one is a prefix of the other, the longer one is higher, so a QEMU
// release with a build suffix sorts above the bare version. ok is false when a
// run does not fit in a uint64.
func compareToolchainVersions(a, b string) (order int, ok bool) {
	left, right := toolchainNumberRE.FindAllString(a, -1), toolchainNumberRE.FindAllString(b, -1)
	for i := 0; i < len(left) && i < len(right); i++ {
		l, lerr := strconv.ParseUint(left[i], 10, 64)
		r, rerr := strconv.ParseUint(right[i], 10, 64)
		if lerr != nil || rerr != nil {
			return 0, false
		}
		if l != r {
			return cmp.Compare(l, r), true
		}
	}
	for _, run := range append(left[min(len(left), len(right)):], right[min(len(left), len(right)):]...) {
		if _, err := strconv.ParseUint(run, 10, 64); err != nil {
			return 0, false
		}
	}
	return cmp.Compare(len(left), len(right)), true
}

// toolchainScopeGit runs git in repoRoot against a private index.
type toolchainScopeGit struct {
	root, index string
}

func (g toolchainScopeGit) run(stdin []byte, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", g.root}, args...)...)
	// No inherited GIT_* variable may move the repository, the object store
	// or the index the guard reads.
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GIT_") })
	cmd.Env = append(env, "GIT_INDEX_FILE="+g.index, "GIT_OPTIONAL_LOCKS=0")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (g toolchainScopeGit) blob(oid string) ([]byte, error) {
	if !toolchainBlobIDRE.MatchString(oid) {
		return nil, fmt.Errorf("unexpected blob id %q", oid)
	}
	size, err := g.run(nil, "cat-file", "-s", oid)
	if err != nil {
		return nil, err
	}
	if n, err := strconv.Atoi(strings.TrimSpace(size)); err != nil || n > toolchainScopeMaxFileBytes {
		return nil, fmt.Errorf("blob %s is larger than the toolchain scope limit", oid)
	}
	content, err := g.run(nil, "cat-file", "blob", oid)
	return []byte(content), err
}
