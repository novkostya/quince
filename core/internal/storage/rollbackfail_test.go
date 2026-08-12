package storage

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/novkostya/quince/core/internal/store"
	"testing"
)

// G5 + G5c — THE STATE-HONESTY GATE for a rollback that does not happen (qn.6h D4).
//
// A failed reset must leave the world exactly as it found it and say so in words the operator can
// act on. What makes this worth a gate of its own is that the two failure modes want OPPOSITE
// remedies, and they were one branch in the spec until the 2026-08-08 measurement separated them:
//
//	answer B  busy mount / timeout  → stop or restart the container, then reset
//	answer C  a newer snapshot      → stopping the container does NOTHING; destroy the intervening
//	                                  snapshots, or do nothing, because the head is still resumable
//
// Answer C is the MEASURED one and the likely field case, because a host snapshotter fires every few
// minutes and a @quince-* snapshot stops being the most recent within minutes of being taken.

// planDirtyHeadWithVersion commits one version, then dirties the head — the state reset acts on.
func planDirtyHeadWithVersion(t *testing.T, m *Manager, backups string) string {
	t.Helper()
	commitGoodTree(t, m, testUDID)
	tree := seedTree(t, m, testUDID, "jobX")
	writeFile(t, filepath.Join(tree, "PARTIAL"), []byte("half a transfer"))
	if _, err := os.Stat(zfsWorkSentinel(backups, testUDID)); err != nil {
		t.Fatalf("precondition: the sentinel should exist for a live job: %v", err)
	}
	return tree
}

// assertNothingWasUndone is the half both answers share: non-2xx, the head still dirty, the sentinel
// still present, exactly one rollback attempted, and NO audit line — nothing was discarded, so
// nothing is owed a record.
func assertNothingWasUndone(t *testing.T, m *Manager, f *fakeZFS, st *store.Store,
	backups, tree string, status int, reason string) {
	t.Helper()
	if status >= 200 && status < 300 {
		t.Fatalf("a refused rollback answered %d — a reset that did not happen must not report success", status)
	}
	if _, err := os.Stat(filepath.Join(tree, "PARTIAL")); err != nil {
		t.Errorf("the dirty head was disturbed by a FAILED reset: %v", err)
	}
	if _, err := os.Stat(zfsWorkSentinel(backups, testUDID)); err != nil {
		t.Errorf("the sentinel was cleared by a FAILED reset — the job's resume state must survive: %v", err)
	}
	if n := countOp(f.calls, "rollback"); n != 1 {
		t.Errorf("recorded %d rollback attempts, want exactly 1 — quince does not retry this", n)
	}
	if audits, err := st.ListAudit(10); err == nil {
		for _, a := range audits {
			if a.Event == "working.reset" {
				t.Error("a failed reset wrote an audit line — nothing was discarded, so nothing happened to record")
			}
		}
	}
	if !hasVersion(m, testUDID) {
		t.Error("the committed version must survive a failed reset")
	}
	_ = reason
}

// ANSWER C — measured. The refusal must name C's remedy and must NOT offer B's.
func TestResetRefusalUnderAnswerCNamesCsRemedy(t *testing.T) {
	m, _, f, backups, st := newZFSManager(t, generousPolicy())
	tree := planDirtyHeadWithVersion(t, m, backups)

	// A FOREIGN snapshot, newer than quince's. This is the field case exactly: a host snapshotter
	// firing every few minutes, which quince neither controls nor can see (ListSnapshots filters to
	// quince-*), and which it must not destroy — it is not quince's to delete.
	foreign := filepath.Join(backups, testUDID, ".zfs", "snapshot", "zfs-auto-snap_frequent-2026-08-08-1200")
	if err := os.MkdirAll(foreign, 0o755); err != nil {
		t.Fatal(err)
	}

	status, reason := m.RepairWorking(testUDID, "")
	assertNothingWasUndone(t, m, f, st, backups, tree, status, reason)

	low := strings.ToLower(reason)
	if !strings.Contains(low, "more recent snapshots") {
		t.Errorf("zfs's own words must reach the operator verbatim, not a paraphrase: %q", reason)
	}
	if !strings.Contains(low, "resumable") {
		t.Errorf("answer C must say the dirty head is still resumable — that is why it is not a crisis: %q", reason)
	}
	if strings.Contains(low, "container") && !strings.Contains(low, "snapshotter") {
		t.Errorf("answer C must not offer answer B's remedy (stop the container), which does nothing "+
			"here: %q", reason)
	}
	if !strings.Contains(low, "destroy") {
		t.Errorf("answer C must name what WOULD clear it — destroying the intervening snapshots: %q", reason)
	}
}

// ANSWER B — never observed on real ZFS, kept because one host is not a proof. The remedy is the
// operator action on the host, and there is no in-product one.
func TestResetRefusalUnderAnswerBNamesBsRemedy(t *testing.T) {
	m, _, f, backups, st := newZFSManager(t, generousPolicy())
	tree := planDirtyHeadWithVersion(t, m, backups)
	f.failOp = "rollback" // a busy mount, or a timeout: both arrive here as an error

	status, reason := m.RepairWorking(testUDID, "")
	assertNothingWasUndone(t, m, f, st, backups, tree, status, reason)

	low := strings.ToLower(reason)
	if !strings.Contains(low, "container") {
		t.Errorf("answer B's remedy is an operator action on the host — stop or restart the container: %q", reason)
	}
	if !strings.Contains(low, "injected failure") {
		t.Errorf("zfs's own reason must be carried verbatim rather than paraphrased: %q", reason)
	}
	if strings.Contains(low, "more recent snapshots") {
		t.Errorf("answer B must not be described as answer C: %q", reason)
	}
}

// The rollback quince issues carries NO -r, and targets the newest @quince-* snapshot. Asserted on
// the recorded argv rather than on a call count, because a count passes on a rollback with the wrong
// target — and `-r` is the flag that destroys committed versions.
func TestResetRollbackArgvIsBounded(t *testing.T) {
	m, be, f, backups, _ := newZFSManager(t, generousPolicy())
	commitGoodTree(t, m, testUDID)
	v := m.Versions(testUDID)[0]
	seedTree(t, m, testUDID, "jobX")

	if status, reason := m.RepairWorking(testUDID, ""); status != 202 {
		t.Fatalf("reset: %d %s", status, reason)
	}
	_ = backups

	var argv []string
	for _, c := range f.calls {
		for _, a := range c {
			if a == "rollback" {
				argv = c
			}
		}
	}
	if argv == nil {
		t.Fatal("no rollback recorded")
	}
	want := be.cli.dataset(testUDID) + "@" + snapName(*v.ZFSSnapshot)
	if argv[len(argv)-1] != want {
		t.Errorf("rollback target = %q, want the newest @quince-* snapshot %q", argv[len(argv)-1], want)
	}
	for _, a := range argv {
		if strings.HasPrefix(a, "-") {
			t.Errorf("the rollback argv carries a flag %q — `-r` is what destroys committed versions, "+
				"and no flag belongs on this call", a)
		}
	}
}

func countOp(calls [][]string, op string) int {
	n := 0
	for _, c := range calls {
		for _, a := range c {
			if a == op {
				n++
				break
			}
		}
	}
	return n
}
