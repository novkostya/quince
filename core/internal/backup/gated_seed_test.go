package backup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStoryGatedSeedOverlap is qn.6b candidate C (stories 3 + 4): when a cold clone is pending, the
// engine launches idevicebackup2 with --gate so the on-device passcode prompt fires immediately,
// captures the fresh Info.plist the tool writes, seeds working/ from latest/ while the tool waits at
// the gate, restores the fresh Info.plist over the clone's stale one, then opens the gate.
//
// Proven end-to-end: the committed version carries the FRESH Info.plist (not latest's) — which can
// only happen if the capture → seed → restore overlap ran — and the passcode narration is logged,
// i.e. the tool reached the prompt during seeding, before the clone finished and the gate opened.
func TestStoryGatedSeedOverlap(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport)

	// Make latest/ non-empty so PrepareWork reports seedPending → the gated path. Give it an
	// Info.plist with a DISTINCT marker so "which Info.plist won" is observable.
	latest := filepath.Join(h.dir, "backups", testUDID, "latest")
	if err := os.MkdirAll(latest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(latest, "Info.plist"),
		[]byte(`<?xml version="1.0"?><plist><dict><key>marker</key><string>LATEST</string></dict></plist>`),
		0o644); err != nil {
		t.Fatal(err)
	}

	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 20*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("gated backup state = %s (error=%v), want succeeded", final.State, final.Error)
	}

	// Story 4: the committed version's Info.plist is the FRESH one (captured before the clone,
	// restored after) — NOT latest's "LATEST", and NOT clobbered by the tool's post-gate writes.
	got, err := os.ReadFile(filepath.Join(latest, "Info.plist"))
	if err != nil {
		t.Fatalf("read committed Info.plist: %v", err)
	}
	if !strings.Contains(string(got), "quince-info-marker") || strings.Contains(string(got), "LATEST") {
		t.Fatalf("committed Info.plist did not preserve the fresh copy across the seed:\n%s", got)
	}

	// Story 3: the passcode prompt fired and was narrated while the gated seed was still running
	// (the tool reaches the prompt in ~1–2 s, before the clone completes and the gate opens).
	log, ok := h.eng.JobLog(job.ID)
	if !ok || !strings.Contains(log, "Waiting for passcode") {
		t.Fatalf("passcode narration missing from the job log:\n%s", log)
	}
}

// TestStoryResumeSkipsTheGate is the counterpart: a dirty working/ (a prior FAILED backup) has no
// clone pending, so the engine takes the non-gated path — no gate file, resume straight into the
// existing tree. Guards that seedPending=false bypasses candidate C entirely.
func TestStoryResumeSkipsTheGate(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport)

	// Leave a resumable dirty working/ (complete seed sentinel) — as a failed backup would.
	wd, err := h.mgr.Seed(testUDID, "prior")
	if err != nil {
		t.Fatal(err)
	}
	writeTree(filepath.Join(wd, testUDID), fakeParams{Tree: "complete", Encrypted: true, Kind: "full"}, false)

	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 20*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("resume backup state = %s (error=%v), want succeeded", final.State, final.Error)
	}
	// No gate file should linger in the device dir (the resume path never creates one).
	if _, err := os.Stat(filepath.Join(h.dir, "backups", testUDID, gateFileName)); !os.IsNotExist(err) {
		t.Fatalf("resume path created a gate file (%v) — it should bypass candidate C", err)
	}
}
