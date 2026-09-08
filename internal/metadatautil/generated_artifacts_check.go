// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// CheckGeneratedArtifacts derives two gates from the generator list itself,
// so a new generator cannot land without either of them.
//
//  1. Freshness. A generator that writes a tracked file declares that file.
//     The check runs the generator into a scratch path and compares the bytes.
//     A stale committed artifact fails.
//  2. Orphan. A generator that writes no tracked file must have a caller. A
//     generator that nothing runs is dead code that still reads as coverage.
//
// Both gates read the declaration that every generator carries in its header:
//
//	# generated-artifact: policy/workflow-lanes.json
//	# generated-artifact: none (reason)
//
// The declaration is the only hand-written input, and it lives beside the
// generator rather than in a central list that a new generator can miss.
func CheckGeneratedArtifacts(rootDir string) error {
	generators, err := filepath.Glob(filepath.Join(rootDir, generatorGlob))
	if err != nil {
		return err
	}
	if len(generators) == 0 {
		return fmt.Errorf("no generator matches %s; refusing a vacuous pass", generatorGlob)
	}
	callers, err := shellSources(rootDir)
	if err != nil {
		return err
	}
	var failures []string
	for _, generator := range generators {
		name := filepath.Base(generator)
		artifact, err := generatedArtifactDeclaration(generator)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		if artifact == "" {
			if err := requireGeneratorCaller(rootDir, name, callers); err != nil {
				failures = append(failures, err.Error())
			}
			continue
		}
		if err := requireFreshArtifact(rootDir, generator, artifact); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) == 0 {
		return nil
	}
	sort.Strings(failures)
	return fmt.Errorf("generated artifact check failed:\n  %s", strings.Join(failures, "\n  "))
}

const (
	generatorGlob        = "scripts/generate-*.sh"
	generatedArtifactTag = "# generated-artifact:"
	// generatorHeaderLines bounds the declaration to the header, so a mention
	// of the tag deeper in the script cannot become the declaration.
	generatorHeaderLines = 20
)

// generatedArtifactDeclaration returns the repository-relative artifact the
// generator writes, or the empty string when the generator declares none.
func generatedArtifactDeclaration(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	name := filepath.Base(path)
	lines := strings.Split(string(content), "\n")
	if len(lines) > generatorHeaderLines {
		lines = lines[:generatorHeaderLines]
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, generatedArtifactTag) {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(trimmed, generatedArtifactTag))
		if value == "" {
			return "", fmt.Errorf("%s: %s carries no value", name, generatedArtifactTag)
		}
		if value == "none" || strings.HasPrefix(value, "none ") {
			return "", nil
		}
		if strings.ContainsAny(value, " \t") {
			return "", fmt.Errorf("%s: %s must name one path or none, found %q", name, generatedArtifactTag, value)
		}
		return value, nil
	}
	return "", fmt.Errorf("%s: add a %s header line naming the tracked file the generator writes, or %s none (reason)",
		name, generatedArtifactTag, generatedArtifactTag)
}

// requireFreshArtifact runs the generator into a scratch path and compares the
// result with the committed file. The generator convention is that the first
// argument is the output path.
func requireFreshArtifact(rootDir, generator, artifact string) error {
	name := filepath.Base(generator)
	committed, err := os.ReadFile(filepath.Join(rootDir, artifact))
	if err != nil {
		return fmt.Errorf("%s: read the declared artifact %s: %v", name, artifact, err)
	}
	scratch, err := os.MkdirTemp("", "workcell-generated-artifact")
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch) //nolint:errcheck // scratch directory

	// exec resolves a relative program name against the child's directory, so
	// the generator path has to be absolute before Dir moves the child.
	program, err := filepath.Abs(generator)
	if err != nil {
		return err
	}
	output := filepath.Join(scratch, filepath.Base(artifact))
	command := exec.Command(program, output)
	command.Dir = rootDir
	if combined, runErr := command.CombinedOutput(); runErr != nil {
		return fmt.Errorf("%s: run the generator: %v\n%s", name, runErr, combined)
	}
	regenerated, err := os.ReadFile(output)
	if err != nil {
		return fmt.Errorf("%s: the generator wrote no output at %s: %v", name, output, err)
	}
	if !bytes.Equal(committed, regenerated) {
		return fmt.Errorf("%s: %s is stale; run the generator and commit the result", name, artifact)
	}
	return nil
}

func requireGeneratorCaller(rootDir, name string, callers []string) error {
	for _, caller := range callers {
		if filepath.Base(caller) == name {
			continue
		}
		content, err := os.ReadFile(filepath.Join(rootDir, caller))
		if err != nil {
			return err
		}
		if ValidateGeneratorCaller(string(content), name) == nil {
			return nil
		}
	}
	return fmt.Errorf("%s: %s", name, errNoGeneratorCaller)
}

var errNoGeneratorCaller = errors.New("no tracked script invokes this generator; wire it into its caller, declare the tracked file it writes, or delete it")

// ValidateGeneratorCaller reports whether script invokes the named generator.
//
// A line that names the generator is a caller unless the line is a comment or
// a list entry. Both exclusions are the shapes the repository really carries:
// scripts/validate-repo.sh holds the generator in its shell_files lint array
// and scripts/verify-invariants.sh holds it in HOST_GATE_SCRIPTS, and neither
// array runs anything. The reviewer found this exact defect: every reference
// to scripts/generate-workflow-lane-manifest.sh was a list entry.
//
// ponytail: line shapes, not a shell parser. The shared shell-invocation
// parser reads a list entry on its own line as a command word, so it reports
// a lint array as a caller and cannot serve this check. Replace this function
// with the parser when the parser learns array context.
func ValidateGeneratorCaller(script, generator string) error {
	for line := range strings.Lines(script) {
		trimmed := strings.TrimSpace(strings.TrimSuffix(line, "\n"))
		if !strings.Contains(trimmed, "scripts/"+generator) {
			continue
		}
		if strings.HasPrefix(trimmed, "#") || isListEntry(trimmed) {
			continue
		}
		return nil
	}
	return errNoGeneratorCaller
}

// isListEntry reports a line that is one quoted word and nothing else, which
// is an array element rather than a command.
func isListEntry(line string) bool {
	for _, quote := range []string{`"`, `'`} {
		if len(line) > 1 && strings.HasPrefix(line, quote) && strings.HasSuffix(line, quote) &&
			!strings.Contains(line[1:len(line)-1], quote) {
			return true
		}
	}
	return false
}

// shellSources lists the tracked shell scripts, which are the only files that
// can invoke a generator directly.
func shellSources(rootDir string) ([]string, error) {
	command := exec.Command("git", "-C", rootDir, "ls-files", "-z", "*.sh")
	listing, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("list tracked shell scripts: %w", err)
	}
	var files []string
	for _, path := range strings.Split(string(listing), "\x00") {
		if path != "" {
			files = append(files, path)
		}
	}
	if len(files) == 0 {
		return nil, errors.New("tracked shell script listing is empty; refusing a vacuous pass")
	}
	return files, nil
}
