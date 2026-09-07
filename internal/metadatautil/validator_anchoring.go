// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// CheckValidatorAnchoring requires each validator that anchors on the shared
// shell-invocation parser to run the shared evasion corpus. A validator that
// reads a command out of file content is bypassed by a comment, a heredoc body
// or a longer option unless a negative fixture proves otherwise, so the two
// counts must stay equal.
//
// The check counts call sites with a text scan rather than reading the syntax
// tree, the same choice the other checks in this package record: the call sites
// are few, and a scan avoids go/ast for one caller.
func CheckValidatorAnchoring(rootDir string) error {
	anchors, err := countCallSites(rootDir, "ShellInvocations(", false)
	if err != nil {
		return err
	}
	corpus, err := countCallSites(rootDir, "RequireRejectsAllEvasions(", true)
	if err != nil {
		return err
	}
	if anchors == 0 {
		return errors.New("no validator anchors on the shared shell-invocation parser; the anchoring check has lost its subject")
	}
	if anchors != corpus {
		return fmt.Errorf("validator anchoring parity: %d call(s) of ShellInvocations but %d run(s) of the shared evasion corpus; every anchored validator needs one RequireRejectsAllEvasions run", anchors, corpus)
	}
	return nil
}

// countCallSites returns the number of calls of needle under internal/. It
// counts every occurrence on a line, not one per line, so a second call added
// to an existing line still needs its own corpus run.
// It reads test sources when inTests is set and non-test sources otherwise, it
// removes comments and string literals first so that text about a call is not
// counted as one, and it reads a declaration line from its body onwards so that
// a function is never counted as its own caller while a call written in a
// one-line body still is.
func countCallSites(rootDir, needle string, inTests bool) (int, error) {
	count := 0
	root := filepath.Join(rootDir, "internal")
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") != inTests {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for line := range strings.Lines(dropCommentsAndLiterals(string(content))) {
			count += strings.Count(afterDeclaration(line), needle)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("scan %s for %s: %w", root, needle, err)
	}
	return count, nil
}

// dropCommentsAndLiterals blanks the comments and string literals of Go source
// and keeps every newline, so a line number and the code on it survive. A
// comment or a literal that names a call is text about the call, not the call,
// and counting it would let a mention satisfy the parity gate or break it.
//
// A byte scan is enough for this one caller, the same choice this package
// records for structJSONFields, and it avoids go/ast.
func dropCommentsAndLiterals(source string) string {
	var out strings.Builder
	out.Grow(len(source))
	// state is 0 in code, or the byte that ends the current span.
	var state byte
	var lineComment bool
	for index := 0; index < len(source); index++ {
		character := source[index]
		switch {
		case character == '\n':
			// A newline ends a line comment. A block comment and a raw
			// literal both run on, so their state has to survive it. An
			// interpreted literal cannot span a line at all, so a newline
			// inside one means the scan has lost its place; clear it and read
			// the next line as code rather than blanking the rest of the file.
			lineComment = false
			if state == '"' || state == '\'' {
				state = 0
			}
			out.WriteByte(character)
		case lineComment:
			out.WriteByte(' ')
		case state == '*':
			if character == '*' && index+1 < len(source) && source[index+1] == '/' {
				index++
				state = 0
				out.WriteString("  ")
				continue
			}
			out.WriteByte(' ')
		case state != 0:
			// A backslash escapes the next byte in an interpreted literal. A
			// raw literal has no escapes, so only " and ' take this branch.
			if character == '\\' && state != '`' && index+1 < len(source) {
				index++
				out.WriteString("  ")
				continue
			}
			if character == state {
				state = 0
			}
			out.WriteByte(' ')
		case character == '/' && index+1 < len(source) && source[index+1] == '/':
			lineComment = true
			out.WriteString("  ")
			index++
		case character == '/' && index+1 < len(source) && source[index+1] == '*':
			state = '*'
			out.WriteString("  ")
			index++
		case character == '"' || character == '\'' || character == '`':
			state = character
			out.WriteByte(' ')
		default:
			out.WriteByte(character)
		}
	}
	return out.String()
}

// afterDeclaration returns the part of a line that can hold a call. On a
// declaration line that is the body after the opening brace, so that
// func ShellInvocations(...) is not a call of itself while a one-line body such
// as func validate() { ShellInvocations(...) } still is. Every other line is
// returned whole.
func afterDeclaration(line string) string {
	if !strings.HasPrefix(line, "func ") {
		return line
	}
	_, body, found := strings.Cut(line, "{")
	if !found {
		return ""
	}
	return body
}
