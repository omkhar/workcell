// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Omkhar Arasaratnam

package auditseal

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/omkhar/workcell/internal/host/hoststate"
)

// record is a synthetic audit line for tests. Values are kept simple ASCII so
// bash `printf %q` is the identity encoding and the on-disk line is exactly the
// space-joined key=value form scripts/workcell's append_audit_record_to_path
// writes.
type record struct {
	session string
	args    []string
}

// buildLog renders records into a single global hash chain identical in shape to
// append_audit_record_to_path: `timestamp=.. <args..> [prev_digest=..]
// record_digest=..`, with each record's prev_digest linking to the previous
// record's digest. Every record carries session_id=<session>.
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

func TestSignVerifyGenuineSessionPasses(t *testing.T) {
	signingDir, logPath, seal := signGenuine(t, genuineLog(t), "sess-A")
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err != nil {
		t.Fatalf("genuine session must verify, got %v", err)
	}
}

func TestVerifyFailsOnFlippedByte(t *testing.T) {
	signingDir, tmp := "", t.TempDir()
	signingDir = filepath.Join(tmp, "signing")
	lines := buildLog(t, genuineLog(t))
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Flip a byte in the first session-A record: "event=launch" -> "event=xaunch".
	tampered := make([]string, len(lines))
	copy(tampered, lines)
	tampered[0] = strings.Replace(tampered[0], "event=launch", "event=xaunch", 1)
	writeLog(t, tmp, tampered)
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err == nil {
		t.Fatal("flipped byte must fail verification")
	}
}

func TestVerifyFailsOnReorder(t *testing.T) {
	signingDir, tmp := "", t.TempDir()
	signingDir = filepath.Join(tmp, "signing")
	lines := buildLog(t, genuineLog(t))
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := []string{lines[1], lines[0], lines[2], lines[3]} // swap first two
	writeLog(t, tmp, tampered)
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err == nil {
		t.Fatal("reorder must fail verification")
	}
}

func TestVerifyFailsOnDroppedMiddleRecord(t *testing.T) {
	signingDir, tmp := "", t.TempDir()
	signingDir = filepath.Join(tmp, "signing")
	lines := buildLog(t, genuineLog(t))
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := []string{lines[0], lines[1], lines[3]} // drop record 2 (assurance_change)
	writeLog(t, tmp, tampered)
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err == nil {
		t.Fatal("dropped middle record must fail verification")
	}
}

func TestVerifyFailsOnDroppedHeadRecord(t *testing.T) {
	signingDir, tmp := "", t.TempDir()
	signingDir = filepath.Join(tmp, "signing")
	lines := buildLog(t, genuineLog(t))
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := []string{lines[0], lines[1], lines[2]} // drop the exit head record
	writeLog(t, tmp, tampered)
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err == nil {
		t.Fatal("dropped head record must fail verification")
	}
}

func TestVerifyFailsOnDuplicateKey(t *testing.T) {
	signingDir, tmp := "", t.TempDir()
	signingDir = filepath.Join(tmp, "signing")
	lines := buildLog(t, genuineLog(t))
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	tampered := make([]string, len(lines))
	copy(tampered, lines)
	// Forge a second session_id on the head record.
	tampered[3] = strings.Replace(tampered[3], "record_digest=", "session_id=sess-A record_digest=", 1)
	writeLog(t, tmp, tampered)
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err == nil {
		t.Fatal("duplicate key must fail verification")
	}
}

func TestVerifyFailsWithWrongKey(t *testing.T) {
	signingDir, logPath, seal := signGenuine(t, genuineLog(t), "sess-A")
	// Overwrite the pinned .pub with an unrelated key's public half: verification
	// must fail because the signature was made by the original private key.
	scratch := filepath.Join(t.TempDir(), "other")
	if _, _, err := loadOrCreateSigningKey(scratch); err != nil {
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
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err == nil {
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
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err == nil {
		t.Fatal("missing pinned public key must fail verification")
	}
}

func TestVerifyIgnoresTamperAfterHead(t *testing.T) {
	// A record that appears AFTER this session's head belongs to a later session
	// and is not covered by this seal; tampering it must not fail this session.
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
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err != nil {
		t.Fatalf("tamper after session head must not fail this session, got %v", err)
	}
}

func TestSigningKeyIsStableAndRotates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	_, id1, err := loadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	_, id2, err := loadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if id1 != id2 {
		t.Fatalf("key id must be stable across loads: %s != %s", id1, id2)
	}
	// Rotation: remove the private key; a new key with a new id is generated and
	// the old public key is retained so historical seals still verify.
	if err := os.Remove(filepath.Join(dir, signingKeyBasename)); err != nil {
		t.Fatalf("remove key: %v", err)
	}
	_, id3, err := loadOrCreateSigningKey(dir)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if id3 == id1 {
		t.Fatal("rotated key must have a new id")
	}
	if _, err := os.Stat(filepath.Join(dir, id1+".pub")); err != nil {
		t.Fatalf("old public key must be retained after rotation: %v", err)
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

// TestVerifyFailsOnAppendedBareToken is the P1 tamper-hole regression: strict
// decoding must reject a bare (non key=value) token the tolerant tokenizer drops.
func TestVerifyFailsOnAppendedBareToken(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := buildLog(t, genuineLog(t))
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Append a bare token to the session-A head record (the exit line, index 3).
	tampered := make([]string, len(lines))
	copy(tampered, lines)
	tampered[3] = tampered[3] + " FORGED"
	writeLog(t, tmp, tampered)
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err == nil {
		t.Fatal("appended bare token must fail verification")
	}
}

// TestVerifyIgnoresUnrelatedLaterTornRecord proves a torn/legacy line and a
// later unrelated session's record appended AFTER this session's head do not
// fail this session (they are outside its verified range).
func TestVerifyIgnoresUnrelatedLaterTornRecord(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	// Session A only, sealed at its head.
	lines := buildLog(t, []record{
		{session: "sess-A", args: []string{"event=launch"}},
		{session: "sess-A", args: []string{"event=exit", "exit_status=0"}},
	})
	logPath := writeLog(t, tmp, lines)
	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	// A later session appends a torn/legacy line (no record_digest) and a valid
	// session-B record. Neither belongs to A, and both are after A's head.
	appended := append([]string{}, lines...)
	appended = append(appended,
		"timestamp=2026-07-08T00:00:05Z session_id=sess-B event=legacy_no_digest",
		"timestamp=2026-07-08T00:00:06Z session_id=sess-B event=exit record_digest=deadbeef",
	)
	writeLog(t, tmp, appended)
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err != nil {
		t.Fatalf("unrelated later records must not fail session A, got %v", err)
	}
}

// TestConcurrentKeyGenerationIsAtomic proves concurrent first-time signers all
// converge on one complete key (never a partial PEM) with no temp file left.
func TestConcurrentKeyGenerationIsAtomic(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "signing")
	const n = 8
	ids := make([]string, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			_, id, err := loadOrCreateSigningKey(dir)
			ids[i], errs[i] = id, err
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("goroutines disagree on key id: %s != %s", ids[i], ids[0])
		}
	}
	// The published key parses (never a partial PEM) and no temp files remain.
	data, err := os.ReadFile(filepath.Join(dir, signingKeyBasename))
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	if _, err := parsePrivateKey(data); err != nil {
		t.Fatalf("published key must parse: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
}

// TestVerifyRepairsCorruptPublicKey proves a truncated <keyID>.pub is repaired
// on the next sign, so seals verify again.
func TestVerifyRepairsCorruptPublicKey(t *testing.T) {
	signingDir, logPath, seal := signGenuine(t, genuineLog(t), "sess-A")
	pubPath := filepath.Join(signingDir, seal.KeyID+".pub")
	if err := os.WriteFile(pubPath, []byte("-----BEGIN PUBLIC KEY-----\ntruncat"), 0o644); err != nil {
		t.Fatalf("corrupt pub: %v", err)
	}
	// Corrupt .pub must not verify yet.
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err == nil {
		t.Fatal("corrupt public key must fail verification before repair")
	}
	// Next sign repairs the public key from the private key.
	if _, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t2"); err != nil {
		t.Fatalf("re-sign: %v", err)
	}
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err != nil {
		t.Fatalf("verification must pass after public key repair, got %v", err)
	}
}

// TestSignUnsupportedForNoChainProvider proves a no-chain session (apple-container
// writes lifecycle lines without record_digest) is scoped out of signing with a
// typed ErrUnsupportedAuditChain, not a chain-broken error or an unverifiable seal.
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

	// A genuine chain provider is still signable and verifies.
	signingDir2, logPath2, seal := signGenuine(t, genuineLog(t), "sess-A")
	if !HasSignableChain(logPath2, "colima", "sess-A") {
		t.Fatal("chain provider must report a signable chain")
	}
	if _, err := VerifySessionSeal(signingDir2, logPath2, "colima", "sess-A", seal); err != nil {
		t.Fatalf("chain provider must still verify, got %v", err)
	}
}

// TestVerifyIgnoresSpoofedSessionIDInLaterArgv proves membership uses the
// DECODED session_id: a later session-B `session send` whose bash-%q argv
// contains `session_id=sess-A` must not hijack session A's head. Load-bearing —
// a raw strings.Fields scan would split the escaped argv and match the spoof.
func TestVerifyIgnoresSpoofedSessionIDInLaterArgv(t *testing.T) {
	tmp := t.TempDir()
	signingDir := filepath.Join(tmp, "signing")
	lines := buildLog(t, []record{
		{session: "sess-A", args: []string{"event=launch"}},
		{session: "sess-A", args: []string{"event=exit", "exit_status=0"}},
	})
	// A genuine session-B control record whose argv bash-%q encodes to a `\ `
	// space, so a raw field scan sees a fake `session_id=sess-A` token.
	spoof := `timestamp=2026-07-08T00:00:05Z session_id=sess-B event=command ` +
		`argv=run\ session_id=sess-A\ now record_digest=ffffffff`
	lines = append(lines, spoof)
	logPath := writeLog(t, tmp, lines)

	// The raw hazard exists (guards the regression), but decoded membership excludes it.
	if !strings.Contains(spoof, "session_id=sess-A") {
		t.Fatal("spoof must contain a raw session_id=sess-A token")
	}
	if lineHasSessionID(spoof, "colima", "sess-A") {
		t.Fatal("decoded membership must not match a spoofed argv session_id")
	}
	if !lineHasSessionID(spoof, "colima", "sess-B") {
		t.Fatal("decoded membership must match the record's true session_id")
	}

	seal, err := SignSessionHead(signingDir, logPath, "colima", "sess-A", "t")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := VerifySessionSeal(signingDir, logPath, "colima", "sess-A", seal); err != nil {
		t.Fatalf("a later spoofed argv session_id must not hijack session A's head: %v", err)
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
