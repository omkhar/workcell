// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package providerpolicy

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestShellVerdictFixture replays testdata/shell-verdicts.txt, the verdicts
// that runtime/container/provider-policy.sh gave before it was removed, so
// the Go policy keeps the equivalence that the differential test checked.
// The shell ran in the C locale (LC_ALL=C), so the verdicts are C-locale
// verdicts: under a UTF-8 locale bash folds the case of some non-ASCII
// letters.
// Each line holds Go-quoted fields: kind (check or validate), provider,
// WORKCELL_MODE, AGENT_UI, WORKCELL_WRAPPER_CONTEXT, whether the caller is
// PID 1, the exit status, the stderr line, and the arguments. The cases come
// from the generated differential corpus of the four reviewed policies, where
// the two policies agreed: a greedy pick that kills every deny-flip mutant of
// the policy that the corpus could kill, and a few allowed cases per
// provider. The shell is gone, so the file cannot be regenerated; a reviewed
// change of a verdict edits its line.
func TestShellVerdictFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "shell-verdicts.txt"))
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var f []string
		for rest := line; rest != ""; rest = strings.TrimPrefix(rest, " ") {
			quoted, err := strconv.QuotedPrefix(rest)
			if err != nil {
				t.Fatalf("%q: %v", line, err)
			}
			field, _ := strconv.Unquote(quoted)
			f = append(f, field)
			rest = rest[len(quoted):]
		}
		if len(f) < 8 {
			t.Fatalf("%q: %d fields, want at least 8", line, len(f))
		}
		s := Session{Mode: f[2], AgentUI: f[3], WrapperContext: f[4], CallerIsPID1: f[5] == "true"}
		args := f[8:]
		if f[0] == "check" {
			err = Check(f[1], args, s, Adapters)
		} else {
			err = ValidateCommand(f[1], args, s, Adapters)
		}
		code, got := "0", ""
		if err != nil {
			code, got = "2", err.Error()
		}
		if code != f[6] || got != f[7] {
			t.Errorf("%s %s %q (%+v) = %s %q, want %s %q", f[0], f[1], args, s, code, got, f[6], f[7])
		}
		n++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if n < 100 {
		t.Fatalf("testdata/shell-verdicts.txt holds %d cases, want at least 100", n)
	}
}
