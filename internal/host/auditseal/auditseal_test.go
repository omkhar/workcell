// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package auditseal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omkhar/workcell/internal/host/hoststate"
	"github.com/omkhar/workcell/internal/host/keystore"
	"github.com/omkhar/workcell/internal/ocsf"
	"golang.org/x/sys/unix"
)

// record is a synthetic audit line for tests, with simple-ASCII values so bash
// `printf %q` is the identity encoding (the on-disk line is space-joined key=value).
type record struct {
	session string
	args    []string
}

// buildLog renders records into a single global hash chain in the shape of
// append_audit_record_to_path; each record's prev_digest links to the previous
// digest and carries session_id=<session>.
func buildLog(t *testing.T, records []record) []string {
	t.Helper()
	lines := make([]string, 0, len(records))
	prev := ""
	for i, r := range records {
		ts := "2026-07-08T00:00:0" + string(rune('0'+i%10)) + "Z"
		args := append([]string{"session_id=" + r.session}, r.args...)
		digest := hoststate.AuditRecordDigest(prev, ts, args)
		var b strings.Builder
		b.WriteString("timestamp=" + ts)
		for _, a := range args {
			b.WriteString(" " + a)
		}
		if prev != "" {
			b.WriteString(" prev_digest=" + prev)
		}
		b.WriteString(" record_digest=" + digest)
		lines = append(lines, b.String())
		prev = digest
	}
	return lines
}

func writeLog(t *testing.T, dir string, lines []string) string {
	t.Helper()
	path := filepath.Join(dir, "workcell.audit.log")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("write log: %v", err)
	}
	return path
}

func genuineLog(t *testing.T) []record {
	t.Helper()
	return []record{
		{session: "sess-A", args: []string{"event=launch"}},
		{session: "sess-B", args: []string{"event=launch"}}, // interleaved other session
		{session: "sess-A", args: []string{"event=assurance_change", "final=lower"}},
		{session: "sess-A", args: []string{"event=exit", "exit_status=0"}},
	}
}

func signGenuine(t *testing.T, records []record, session string) (signingDir, logPath string, seal Seal) {
	t.Helper()
	tmp := t.TempDir()
	signingDir = filepath.Join(tmp, "signing")
	logPath = writeLog(t, tmp, buildLog(t, records))
	seal, err := SignSessionHead(signingDir, logPath, "colima", session, "2026-07-08T00:00:09Z")
	if err != nil {
		t.Fatalf("SignSessionHead: %v", err)
	}
	return signingDir, logPath, seal
}

// signedGenuine builds and signs the standard genuine sess-A log, returning the
// tmp dir, signing dir, log path, lines, and seal for a tamper test to rewrite.
func signedGenuine(t *testing.T) (tmp, signingDir, logPath string, lines []string, seal Seal) {
	t.Helper()
	tmp = t.TempDir()
	signingDir = filepath.Join(tmp, "signing")
	lines = buildLog(t, genuineLog(t))
	logPath = writeLog(t, tmp, lines)
	s, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tmp, signingDir, logPath, lines, s
}

// verifyA verifies sess-A against the seal and returns the error (nil = pass).
func verifyA(signingDir, logPath string, seal Seal) error {
	_, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal)
	return err
}

// legacyLine renders a pre-record_digest audit line (no prev_digest, no
// record_digest) — how an upgraded profile's leading entries look.
func legacyLine(ts, session, event string) string {
	return "timestamp=" + ts + " session_id=" + session + " event=" + event
}

// TestVerifyToleratesLegacyPrefix (L185): a log whose leading lines predate
// record_digest (legacy prefix) followed by a valid chain for the session
// verifies OK and returns the real head — the contiguous leading no-digest run is
// skipped, and strict verification starts at the chain root.
func TestVerifyToleratesLegacyPrefix(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := []string{
		legacyLine("2026-07-07T00:00:00Z", "sess-A", "launch"),
		legacyLine("2026-07-07T00:00:01Z", "sess-B", "launch"),
	}
	lines = append(lines, buildLog(t, genuineLog(t))...) // chain root has prev_digest=""
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign over legacy-prefix log: %v", err)
	}
	head, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal)
	if err != nil {
		t.Fatalf("legacy-prefix log must verify, got %v", err)
	}
	if head == "" || head != seal.HeadDigest {
		t.Fatalf("verify returned wrong head %q (seal %q)", head, seal.HeadDigest)
	}
}

// TestVerifyFailsOnMidChainStrippedDigest (L185): only the contiguous LEADING
// no-digest run is skippable — a record_digest stripped from a MID-chain line
// (after the chain root) is still stripped-digest tamper.
func TestVerifyFailsOnMidChainStrippedDigest(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := buildLog(t, genuineLog(t)) // line 0 is the chain root
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := append([]string{}, lines...)
	// Strip the whole record_digest token from a mid-chain line (index 2, after root).
	tampered[2] = strings.Split(tampered[2], " record_digest=")[0]
	writeLog(t, tmp, tampered)
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err == nil ||
		!strings.Contains(err.Error(), "missing record_digest") {
		t.Fatalf("mid-chain stripped digest must fail closed, got %v", err)
	}
}

// TestPureLegacyLogIsUnsupported (L185): a session whose records are ALL legacy
// (no record_digest at all) has no chain to seal — ErrUnsupportedAuditChain,
// unchanged.
func TestPureLegacyLogIsUnsupported(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	logPath := writeLog(t, tmp, []string{
		legacyLine("2026-07-07T00:00:00Z", "sess-A", "launch"),
		legacyLine("2026-07-07T00:00:01Z", "sess-A", "exit"),
	})
	if _, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t"); !errors.Is(err, ErrUnsupportedAuditChain) {
		t.Fatalf("pure-legacy log must be ErrUnsupportedAuditChain, got %v", err)
	}
}

// TestStrippedHeadDigestIsTamperNotUnsupported (L179): on a chained provider,
// stripping record_digest from the session's HEAD record must be reported as a
// broken chain (missing record_digest) — NOT masked as ErrUnsupportedAuditChain
// — and HasSignableChain must stay true, since the region still has digests.
func TestStrippedHeadDigestIsTamperNotUnsupported(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := buildLog(t, genuineLog(t))
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := append([]string{}, lines...)
	// The head record for sess-A is the last line (index 3); strip its digest.
	tampered[3] = strings.Split(tampered[3], " record_digest=")[0]
	writeLog(t, tmp, tampered)
	_, err = VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal)
	if errors.Is(err, ErrUnsupportedAuditChain) {
		t.Fatalf("stripped head digest must be tamper, not unsupported: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "missing record_digest") {
		t.Fatalf("stripped head digest must fail chain-broken, got %v", err)
	}
	if !HasSignableChain(logPath, "colima", "sess-A") {
		t.Fatal("HasSignableChain must stay true when a stripped head is tamper")
	}
}

// TestAppendedNoDigestHeadIsTamperNotUnsupported (L179): appending a no-digest
// record that claims the session AFTER a valid chain (making it the new head)
// must be reported as broken-chain tamper, not ErrUnsupportedAuditChain, and
// HasSignableChain must stay true.
func TestAppendedNoDigestHeadIsTamperNotUnsupported(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := buildLog(t, genuineLog(t))
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Append a no-digest record claiming sess-A; it becomes the new head.
	tampered := append(append([]string{}, lines...),
		legacyLine("2026-07-08T00:00:00Z", "sess-A", "injected"))
	writeLog(t, tmp, tampered)
	_, err = VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal)
	if errors.Is(err, ErrUnsupportedAuditChain) {
		t.Fatalf("appended no-digest head must be tamper, not unsupported: %v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "missing record_digest") {
		t.Fatalf("appended no-digest head must fail chain-broken, got %v", err)
	}
	if !HasSignableChain(logPath, "colima", "sess-A") {
		t.Fatal("HasSignableChain must stay true when an appended no-digest head is tamper")
	}
}

// TestNoChainProviderReportsUnsupported (L179): a session whose records ALL lack
// record_digest (genuine no-chain provider) stays ErrUnsupportedAuditChain and
// HasSignableChain=false — the guard must not over-report tamper.
func TestNoChainProviderReportsUnsupported(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	logPath := writeLog(t, tmp, []string{
		legacyLine("2026-07-07T00:00:00Z", "sess-A", "launch"),
		legacyLine("2026-07-07T00:00:01Z", "sess-B", "launch"),
		legacyLine("2026-07-07T00:00:02Z", "sess-A", "exit"),
	})
	if _, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t"); !errors.Is(err, ErrUnsupportedAuditChain) {
		t.Fatalf("no-chain provider must be ErrUnsupportedAuditChain, got %v", err)
	}
	if HasSignableChain(logPath, "colima", "sess-A") {
		t.Fatal("HasSignableChain must be false for a no-chain provider")
	}
}

func TestSignVerifyGenuineSessionPasses(t *testing.T) {
	signingDir, logPath, seal := signGenuine(t, genuineLog(t), "sess-A")
	if err := verifyA(signingDir, logPath, seal); err != nil {
		t.Fatalf("genuine session must verify, got %v", err)
	}
}

func TestVerifyFailsOnFlippedByte(t *testing.T) {
	tmp, signingDir, logPath, lines, seal := signedGenuine(t)
	tampered := append([]string{}, lines...)
	tampered[0] = strings.Replace(tampered[0], "event=launch", "event=xaunch", 1)
	writeLog(t, tmp, tampered)
	if verifyA(signingDir, logPath, seal) == nil {
		t.Fatal("flipped byte must fail verification")
	}
}

func TestVerifyFailsOnReorder(t *testing.T) {
	tmp, signingDir, logPath, lines, seal := signedGenuine(t)
	writeLog(t, tmp, []string{lines[1], lines[0], lines[2], lines[3]}) // swap first two
	if verifyA(signingDir, logPath, seal) == nil {
		t.Fatal("reorder must fail verification")
	}
}

func TestVerifyFailsOnDroppedMiddleRecord(t *testing.T) {
	tmp, signingDir, logPath, lines, seal := signedGenuine(t)
	writeLog(t, tmp, []string{lines[0], lines[1], lines[3]}) // drop the assurance_change record
	if verifyA(signingDir, logPath, seal) == nil {
		t.Fatal("dropped middle record must fail verification")
	}
}

func TestVerifyFailsOnDroppedHeadRecord(t *testing.T) {
	tmp, signingDir, logPath, lines, seal := signedGenuine(t)
	writeLog(t, tmp, []string{lines[0], lines[1], lines[2]}) // drop the exit head record
	if verifyA(signingDir, logPath, seal) == nil {
		t.Fatal("dropped head record must fail verification")
	}
}

func TestVerifyFailsOnDuplicateKey(t *testing.T) {
	tmp, signingDir, logPath, lines, seal := signedGenuine(t)
	tampered := append([]string{}, lines...)
	tampered[3] = strings.Replace(tampered[3], "record_digest=", "session_id=sess-A record_digest=", 1)
	writeLog(t, tmp, tampered)
	if verifyA(signingDir, logPath, seal) == nil {
		t.Fatal("duplicate key must fail verification")
	}
}

func TestVerifyFailsWithWrongKey(t *testing.T) {
	signingDir, logPath, seal := signGenuine(t, genuineLog(t), "sess-A")
	scratch := filepath.Join(t.TempDir(), "other")
	if _, _, err := keystore.LoadOrCreateSigningKey(scratch); err != nil {
		t.Fatalf("scratch key: %v", err)
	}
	otherPub := findPub(t, scratch)
	dst := filepath.Join(signingDir, seal.KeyID+".pub")
	data, err := os.ReadFile(otherPub)
	if err != nil {
		t.Fatalf("read other pub: %v", err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("overwrite pub: %v", err)
	}
	if err := verifyA(signingDir, logPath, seal); err == nil {
		t.Fatal("wrong public key must fail verification")
	}
}

func TestVerifyFailsWhenSealSessionMismatch(t *testing.T) {
	signingDir, logPath, seal := signGenuine(t, genuineLog(t), "sess-A")
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-B", seal); err == nil {
		t.Fatal("seal for a different session must fail verification")
	}
}

func TestVerifyFailsWhenPublicKeyMissing(t *testing.T) {
	signingDir, logPath, seal := signGenuine(t, genuineLog(t), "sess-A")
	if err := os.Remove(filepath.Join(signingDir, seal.KeyID+".pub")); err != nil {
		t.Fatalf("remove pub: %v", err)
	}
	if err := verifyA(signingDir, logPath, seal); err == nil {
		t.Fatal("missing pinned public key must fail verification")
	}
}

func TestVerifyIgnoresTamperAfterHead(t *testing.T) {
	records := append(genuineLog(t), record{session: "sess-C", args: []string{"event=launch"}})
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := buildLog(t, records)
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := make([]string, len(lines))
	copy(tampered, lines)
	tampered[4] = strings.Replace(tampered[4], "event=launch", "event=xaunch", 1)
	writeLog(t, tmp, tampered)
	if err := verifyA(signingDir, logPath, seal); err != nil {
		t.Fatalf("tamper after session head must not fail this session, got %v", err)
	}
}

func TestSealSidecarRoundTrip(t *testing.T) {
	_, _, seal := signGenuine(t, genuineLog(t), "sess-A")
	path := SealPathForRecord(filepath.Join(t.TempDir(), "sess-A.json"))
	if !strings.HasSuffix(path, "sess-A.audit-sig") {
		t.Fatalf("unexpected sidecar path %s", path)
	}
	if err := WriteSeal(path, seal); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadSeal(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != seal {
		t.Fatalf("round trip mismatch: %+v != %+v", got, seal)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("seal must be owner-only, got %o", info.Mode().Perm())
	}
}

// withDirFsyncFailure makes the parent-directory fsync in WriteSeal fail with
// EIO, returning a restore func. It records how many times the fsync ran so a
// test can assert the durability step was actually exercised.
func withDirFsyncFailure() (calls *int, restore func()) {
	n := 0
	orig := fsyncDir
	fsyncDir = func(fd int) error {
		n++
		return unix.EIO
	}
	return &n, func() { fsyncDir = orig }
}

// TestWriteSealFsyncsParentDirOnFirstWriteAndReplace (sealio.go L67): the seal
// directory must be fsynced after the rename so the .audit-sig entry is durable,
// on BOTH the first write and a replace of an existing seal.
func TestWriteSealFsyncsParentDirOnFirstWriteAndReplace(t *testing.T) {
	_, _, seal := signGenuine(t, genuineLog(t), "sess-A")
	path := SealPathForRecord(filepath.Join(t.TempDir(), "sess-A.json"))

	calls, restore := func() (*int, func()) {
		n := 0
		orig := fsyncDir
		fsyncDir = func(fd int) error {
			n++
			return orig(fd)
		}
		return &n, func() { fsyncDir = orig }
	}()
	defer restore()

	if err := WriteSeal(path, seal); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("first write must fsync parent dir once, got %d", *calls)
	}
	if err := WriteSeal(path, seal); err != nil {
		t.Fatalf("replace write: %v", err)
	}
	if *calls != 2 {
		t.Fatalf("replace write must fsync parent dir again, got %d total", *calls)
	}
}

// TestWriteSealFailsClosedOnDirFsyncError (sealio.go L67): a parent-directory
// fsync writeback failure must fail WriteSeal closed so a non-durable seal never
// reports success, on both the first write and a replace.
func TestWriteSealFailsClosedOnDirFsyncError(t *testing.T) {
	_, _, seal := signGenuine(t, genuineLog(t), "sess-A")
	path := SealPathForRecord(filepath.Join(t.TempDir(), "sess-A.json"))

	// First write must fail closed.
	calls, restore := withDirFsyncFailure()
	if err := WriteSeal(path, seal); !errors.Is(err, unix.EIO) {
		restore()
		t.Fatalf("first-write dir fsync failure must fail closed with EIO, got %v", err)
	}
	if *calls != 1 {
		restore()
		t.Fatalf("dir fsync must run once on first write, got %d", *calls)
	}
	restore()

	// Establish an existing durable seal, then a replace whose dir fsync fails
	// must also fail closed.
	if err := WriteSeal(path, seal); err != nil {
		t.Fatalf("seed durable seal: %v", err)
	}
	calls, restore = withDirFsyncFailure()
	defer restore()
	if err := WriteSeal(path, seal); !errors.Is(err, unix.EIO) {
		t.Fatalf("replace dir fsync failure must fail closed with EIO, got %v", err)
	}
	if *calls != 1 {
		t.Fatalf("dir fsync must run once on replace, got %d", *calls)
	}
}

// TestVerifyFailsOnAppendedBareToken is the P1 tamper-hole regression: strict decoding must reject a bare (non key=value) token the tolerant tokenizer drops.
func TestVerifyFailsOnAppendedBareToken(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := buildLog(t, genuineLog(t))
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := make([]string, len(lines))
	copy(tampered, lines)
	tampered[3] = tampered[3] + " FORGED"
	writeLog(t, tmp, tampered)
	if err := verifyA(signingDir, logPath, seal); err == nil {
		t.Fatal("appended bare token must fail verification")
	}
}

// TestVerifyIgnoresUnrelatedLaterTornRecord: unrelated later records after the head do not fail this session.
func TestVerifyIgnoresUnrelatedLaterTornRecord(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := buildLog(t, []record{
		{session: "sess-A", args: []string{"event=launch"}},
		{session: "sess-A", args: []string{"event=exit", "exit_status=0"}},
	})
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	appended := append([]string{}, lines...)
	appended = append(appended,
		"timestamp=2026-07-08T00:00:05Z session_id=sess-B event=legacy_no_digest",
		"timestamp=2026-07-08T00:00:06Z session_id=sess-B event=exit record_digest=deadbeef",
	)
	writeLog(t, tmp, appended)
	if err := verifyA(signingDir, logPath, seal); err != nil {
		t.Fatalf("unrelated later records must not fail session A, got %v", err)
	}
}

// TestSignUnsupportedForNoChainProvider: a no-chain session (apple-container) is scoped out of signing with a typed ErrUnsupportedAuditChain.
func TestSignUnsupportedForNoChainProvider(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	logPath := writeLog(t, tmp, []string{
		"timestamp=2026-07-08T00:00:00Z session_id=sess-X event=session_started v=1",
		"timestamp=2026-07-08T00:00:01Z session_id=sess-X event=session_finished v=1",
	})
	if _, err := SignSessionHead(signingDir, logPath, "apple-container", "sess-X", "t"); !errors.Is(err, ErrUnsupportedAuditChain) {
		t.Fatalf("no-chain provider sign must return ErrUnsupportedAuditChain, got %v", err)
	}
	if HasSignableChain(logPath, "apple-container", "sess-X") {
		t.Fatal("no-chain provider must not report a signable chain")
	}

	signingDir2, logPath2, seal := signGenuine(t, genuineLog(t), "sess-A")
	if !HasSignableChain(logPath2, "colima", "sess-A") {
		t.Fatal("chain provider must report a signable chain")
	}
	if _, err := VerifySessionSeal(signingDir2, logPath2, "colima", "sess-A", seal); err != nil {
		t.Fatalf("chain provider must still verify, got %v", err)
	}
}

// TestVerifyIgnoresSpoofedSessionIDInLaterArgv: a later session-B argv containing `session_id=sess-A` must not hijack session A's head (a raw scan would).
func TestVerifyIgnoresSpoofedSessionIDInLaterArgv(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := buildLog(t, []record{
		{session: "sess-A", args: []string{"event=launch"}},
		{session: "sess-A", args: []string{"event=exit", "exit_status=0"}},
	})
	spoof := `timestamp=2026-07-08T00:00:05Z session_id=sess-B event=command ` +
		`argv=run\ session_id=sess-A\ now record_digest=ffffffff`
	lines = append(lines, spoof)
	logPath := writeLog(t, tmp, lines)

	if !strings.Contains(spoof, "session_id=sess-A") {
		t.Fatal("spoof must contain a raw session_id=sess-A token")
	}
	if ocsf.AuditLineClaimsSession(spoof, "colima", "sess-A") {
		t.Fatal("tokenized claim must not match a spoofed argv session_id")
	}
	if !ocsf.AuditLineClaimsSession(spoof, "colima", "sess-B") {
		t.Fatal("tokenized claim must match the record's true session_id")
	}

	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if err := verifyA(signingDir, logPath, seal); err != nil {
		t.Fatalf("a later spoofed argv session_id must not hijack session A's head: %v", err)
	}
}

// TestVerifyFailsOnMalformedSameSessionRecordAfterHead: a malformed record that CLAIMS this session, appended after the head, fails closed (not dropped).
func TestVerifyFailsOnMalformedSameSessionRecordAfterHead(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := buildLog(t, []record{
		{session: "sess-A", args: []string{"event=launch"}},
		{session: "sess-A", args: []string{"event=exit", "exit_status=0"}},
	})
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	appended := append(append([]string{}, lines...),
		`timestamp=2026-07-08T00:00:05Z session_id=sess-A event=exit FORGED`)
	writeLog(t, tmp, appended)
	if err := verifyA(signingDir, logPath, seal); err == nil {
		t.Fatal("a malformed same-session record appended after the head must fail verification")
	}
}

// TestVerifyFailsOnUntokenizableRecordAfterHead: a same-session record whose malformed event makes it untokenizable (session_id unreadable), appended after the head, fails closed as corruption instead of being dropped (the L90 gap).
func TestVerifyFailsOnUntokenizableRecordAfterHead(t *testing.T) {
	tmp, signingDir, logPath, lines, seal := signedGenuine(t)
	appended := append(append([]string{}, lines...),
		`timestamp=2026-07-08T00:00:05Z event=$'unterminated session_id=sess-A record_digest=x`)
	writeLog(t, tmp, appended)
	if verifyA(signingDir, logPath, seal) == nil {
		t.Fatal("an untokenizable same-session record after the head must fail verification")
	}
}

// TestVerifyIgnoresUnrelatedLaterTokenizableRecord: an unrelated other-session record that still tokenizes cleanly must NOT fail this session (L209).
func TestVerifyIgnoresUnrelatedLaterTokenizableRecord(t *testing.T) {
	tmp, signingDir, logPath, lines, seal := signedGenuine(t)
	appended := append(append([]string{}, lines...),
		`timestamp=2026-07-08T00:00:05Z event=legacy session_id=sess-B`)
	writeLog(t, tmp, appended)
	if err := verifyA(signingDir, logPath, seal); err != nil {
		t.Fatalf("an unrelated tokenizable later record must not fail this session, got %v", err)
	}
}

// TestVerifyFailsOnInsecureKeyStorePerms: group/world-writable perms on the signing dir or .pub fail closed at verify time.
func TestVerifyFailsOnInsecureKeyStorePerms(t *testing.T) {
	signingDir, logPath, seal := signGenuine(t, genuineLog(t), "sess-A")
	if err := verifyA(signingDir, logPath, seal); err != nil {
		t.Fatalf("secure key store must verify: %v", err)
	}
	if err := os.Chmod(signingDir, 0o777); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	if err := verifyA(signingDir, logPath, seal); err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("world-writable signing dir must fail with a perms reason, got %v", err)
	}
	if err := os.Chmod(signingDir, 0o700); err != nil {
		t.Fatalf("restore dir: %v", err)
	}
	if err := os.Chmod(filepath.Join(signingDir, seal.KeyID+".pub"), 0o666); err != nil {
		t.Fatalf("chmod pub: %v", err)
	}
	if err := verifyA(signingDir, logPath, seal); err == nil || !strings.Contains(err.Error(), "insecure permissions") {
		t.Fatalf("world-writable public key must fail with a perms reason, got %v", err)
	}
}

func findPub(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".pub") {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatalf("no .pub in %s", dir)
	return ""
}
