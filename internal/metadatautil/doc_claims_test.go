// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package metadatautil_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/metadatautil"
)

func TestDocClaimHits(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{
		"scripts/validate-repo.sh": "scripts/gate.sh\n",
		"scripts/ci/job-x.sh":      "true\n",
		".github/workflows/x.yml":  "name: x\n",
		"scripts/gate.sh":          "true\n",
		"scripts/orphan.sh":        "true\n",
		"outside/x.sh":             "true\n",
	} {
		path := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"scripts/link.sh": "../outside/x.sh", "scripts/dir": "../outside"} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	cited := "d\tscripts/gate.sh\nd\tscripts/orphan.sh\nd\tscripts/nope.sh\nd\tscripts/gate.sh/x\n" +
		"d\tscripts/link.sh\nd\tscripts/dir/x.sh\nd\tscripts/../outside/x.sh\nd\tinternal/nope\n"
	var hits strings.Builder
	if err := metadatautil.DocClaimHits(root, strings.NewReader(cited), &hits); err != nil {
		t.Fatal(err)
	}
	want := "d\tunwired-script\tscripts/orphan.sh\nd\tmissing-path\tscripts/nope.sh\nd\tmissing-path\tscripts/gate.sh/x\n" +
		"d\tescaping-path\tscripts/link.sh\nd\tescaping-path\tscripts/dir/x.sh\nd\tescaping-path\tscripts/../outside/x.sh\n" +
		"d\tmissing-path\tinternal/nope\n"
	if hits.String() != want {
		t.Fatalf("DocClaimHits() =\n%s\nwant\n%s", hits.String(), want)
	}
	if err := metadatautil.DocClaimHits(root, strings.NewReader("no tab\n"), &hits); err == nil {
		t.Fatal("DocClaimHits() on a line with no tab: want an error")
	}
}
