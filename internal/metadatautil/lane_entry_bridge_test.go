package metadatautil

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The publish bridge's rows read a tree with LaneScripts, whose lane at
// scripts/validate-repo.sh, and library at scripts/lib/l.sh, a row may pin
// for this test binary. "wired" wants scripts/gate.sh listed, "refused" wants
// it unlisted or an error, and "unreadable" wants an error.
const (
	bridgeLane      = "scripts/validate-repo.sh"
	bridgeGuard     = laneBridgeGuard + `:)" || workcell_entry_root=""` + "\n[[ -n \"${workcell_entry_root}\" ]] || {\n  echo \"Run ${BASH_SOURCE[0]} from its own place\" >&2\n  exit 2\n}\n"
	bridgeEntry     = bridgeGuard + "set -eu\n" + laneBridgeRoot + "\n"
	bridgeGate      = "\"${ROOT_DIR}/scripts/gate.sh\"\n"
	bridgeSource    = "source \"${ROOT_DIR}/scripts/lib/l.sh\"\n"
	bridgeCanonical = "ROOT_DIR=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")/..\" && pwd)\"\nreadonly ROOT_DIR\n"
)

var bridgeRows = map[string]struct{ lane, library, pin, want string }{
	"guarded lane at pinned bytes":                           {bridgeEntry + bridgeGate, "", "lane", "wired"},
	"guarded lane at other bytes":                            {bridgeEntry + bridgeGate + "#\n", "", "", "unreadable"},
	"pinned lane that reads the guard's root":                {bridgeEntry + "\"${workcell_entry_root}/scripts/gate.sh\"\n", "", "lane", "wired"},
	"pinned lane without the root line":                      {strings.Replace(bridgeEntry, laneBridgeRoot, "", 1) + bridgeGate, "", "lane", "refused"},
	"pinned library with its own guard":                      {bridgeEntry + bridgeSource + bridgeGate, bridgeGuard + "f() { :; }\n", "both", "wired"},
	"pinned library that moves the guard's root":             {bridgeEntry + bridgeSource + bridgeGate, "workcell_entry_root=/tmp\n", "both", "refused"},
	"library at other bytes":                                 {bridgeEntry + bridgeSource + bridgeGate, "f() { :; }\n", "lane", "unreadable"},
	"unguarded lane":                                         {bridgeCanonical + bridgeSource + bridgeGate, "f() { :; }\n", "", "wired"},
	"unguarded lane whose library reads workcell_entry_root": {bridgeCanonical + bridgeSource, "\"${workcell_entry_root}/scripts/gate.sh\"\n", "", "refused"},
	"unguarded lane that holds the mark":                     {bridgeCanonical + laneBridgeMark + "\n" + bridgeSource + bridgeGate, "f() { :; }\n", "", "unreadable"},
}

// bridgeWrites are the red team's writes of a variable by a name that is not
// a plain literal, with v holding the name's first part and @N its rest, and
// @A the name with its first _ spelled \x5f. Each is unreadable past the root
// line of a guarded lane, and in a library such a lane sources, for ROOT_DIR
// and for workcell_entry_root, as the reader of lint-ratchets-3i finds it.
var bridgeWrites = []string{
	`printf -v "${v}@N" %s /tmp`,
	`read -r "${v}@N" <<</tmp`,
	`declare "${v}@N=/tmp"`,
	`typeset "${v}@N=/tmp"`,
	`export "${v}@N=/tmp"`,
	`declare -n r="${v}@N"; r=/tmp`,
	`mapfile -t "${v}@N" <<</tmp`,
	`read -ra "${v}@N" <<</tmp`,
	`getopts a "${v}@N" -a`,
	`true & wait -p "${v}@N"`,
	`builtin printf -v "${v}@N" %s /tmp`,
	`command printf -v "${v}@N" %s /tmp`,
	`printf -v $'@A' %s /tmp`,
	`v=${v}@N; : "${!v:=/tmp}"`,
	`trap 'printf -v "${v}@N" %s /tmp' DEBUG`,
	`(( ${v}@N = 1 ))`,
	`let "${v}@N=1"`,
	`unset "${v}@N"`,
	`declare -i x="${v}@N=5"`,
	`f() { local "${v}@N=/tmp"; @G; }; f`,
}

func init() {
	for _, row := range bridgeRows {
		files := bridgeFiles(row.lane, row.library)
		laneBridgePins[bridgeLane+" "+laneBridgeDigest(files[bridgeLane])] = row.pin != ""
		laneBridgePins["scripts/lib/l.sh "+laneBridgeDigest(files["scripts/lib/l.sh"])] = row.pin == "both"
	}
}

// bridgeFiles returns a tree with lane, past its first line, and library.
func bridgeFiles(lane, library string) map[string]string {
	return map[string]string{bridgeLane: "#!/bin/bash\n" + lane, "scripts/lib/l.sh": library, "scripts/ci/job-x.sh": "true\n", ".github/workflows/w.yml": "name: w\n"}
}

// bridgeRead writes files under a new root and returns whether LaneScripts
// lists scripts/gate.sh, and its error.
func bridgeRead(t *testing.T, files map[string]string) (bool, error) {
	t.Helper()
	root := t.TempDir()
	for rel, body := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LaneScripts(root)
	return err == nil && slices.Contains(got, "scripts/gate.sh"), err
}

func TestLaneEntryBridgeReadsPinnedGuardedLanes(t *testing.T) {
	t.Parallel()
	for name, row := range bridgeRows {
		wired, err := bridgeRead(t, bridgeFiles(row.lane, row.library))
		if row.want == "wired" && (!wired || err != nil) || row.want == "refused" && wired || row.want == "unreadable" && err == nil {
			t.Errorf("%s: LaneScripts() lists scripts/gate.sh: %v (%v), want %s", name, wired, err, row.want)
		}
	}
	if got, err := LaneScripts("../.."); err != nil || !slices.Contains(got, "scripts/check-hardened-fs.sh") {
		t.Errorf("LaneScripts() of this tree = %v, %v, want its guarded lanes read at their pinned bytes", got, err)
	}
}

func TestLaneEntryBridgeRefusesWritesByName(t *testing.T) {
	t.Parallel()
	for _, name := range [][2]string{{"ROOT_", "DIR"}, {"workcell_entry_", "root"}} {
		spell := strings.NewReplacer("@N", name[1], "@A", strings.Replace(name[0]+name[1], "_", `\x5f`, 1), "@G", `"${`+name[0]+name[1]+`}/scripts/gate.sh"`)
		for _, write := range bridgeWrites {
			write, gate := spell.Replace("v="+name[0]+"\n"+write+"\n"), spell.Replace("@G\n")
			for _, files := range []map[string]string{bridgeFiles(bridgeEntry+write+gate, ""), bridgeFiles(bridgeEntry+bridgeSource+gate, write)} {
				if wired, err := bridgeRead(t, files); err == nil {
					t.Errorf("LaneScripts() reads %q, listing scripts/gate.sh: %v, want it unreadable", write, wired)
				}
			}
		}
	}
}

// main's claude-mention.yml leaves its pinned step unread; one changed byte,
// or the bytes at another path, are read as any workflow is, whose case on
// the event name this reader reads as an exit 0.
func TestLaneEntryBridgePinsClaudeMention(t *testing.T) {
	t.Parallel()
	text, err := os.ReadFile("../../.github/workflows/claude-mention.yml")
	if err != nil || laneBridgeDigest(string(text)) != laneClaudeMentionPin || laneStepPinned(".github/workflows/claude-mention.yml", text, "Run Claude") {
		t.Fatalf(".github/workflows/claude-mention.yml is not at the pinned bytes, or another step is pinned (%v)", err)
	}
	for path, workflow := range map[string]string{"claude-mention.yml": string(text), "mention.yml": string(text), "claude-mention.yml#": strings.Replace(string(text), "# The repository owner", "# Tha repository owner", 1)} {
		files := bridgeFiles(bridgeCanonical+bridgeGate, "")
		files[".github/workflows/"+strings.TrimSuffix(path, "#")] = workflow
		wired, err := bridgeRead(t, files)
		if want := path == "claude-mention.yml"; want != (wired && err == nil) || !want && (err == nil || !strings.Contains(err.Error(), "exit 0") || workflow == string(text) && path != "mention.yml") {
			t.Errorf("%s: LaneScripts() lists scripts/gate.sh: %v, %v, want it listed: %v, else an error on the exit 0", path, wired, err, want)
		}
	}
}
