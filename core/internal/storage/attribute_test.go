package storage

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
	"github.com/novkostya/quince/core/internal/store"
)

// qn.6c story 5a (quince#439, quince#428). Attribution moved OUT of a startup sweep that took one
// storage id and filled every NULL, and INTO reconciliation, where `Scan` has just walked a root so
// "which storage" is observed rather than chosen.

const attrStorageID = "01JSTORAGEATTR0000000000"

// attributed makes the Manager speak for a storage, which is what an upgraded deployment looks like
// once its declared storage has a marker.
func attributed(m *Manager) *Manager {
	m.slots[0].StorageID = attrStorageID
	return m
}

// The rule itself: a NULL row whose artifact is in THIS root is attributed to it. The deleted sweep
// would have reached the same answer here only by luck — it was passed a storage id chosen before
// any artifact was located.
func TestReconcileAttributesANullRowWhoseArtifactIsHere(t *testing.T) {
	m, _, backups, st := newNSManager(t, clonetree.Copy, generousPolicy())
	attributed(m)

	verDir := nsVersionDir(backups, testUDID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "01VHERE", "", testUDID, BackendCopy)

	// The pre-qn.6c shape: a real row, committed by an older build, with no storage_id.
	if err := st.InsertVersion(store.VersionRow{
		ID: "01VHERE", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), Kind: "full",
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	row, _, err := st.GetVersion("01VHERE")
	if err != nil {
		t.Fatal(err)
	}
	if row.StorageID == nil || *row.StorageID != attrStorageID {
		t.Fatalf("a NULL row whose artifact is under this root must be attributed to it, got %v", row.StorageID)
	}
	// Attribution must not have gone via adopt: an adopted row has a nil JobID, and this one was
	// committed by a job. Losing that would silently un-protect it from retention.
	if row.JobID == nil {
		t.Error("the existing row was replaced rather than attributed — its job_id is gone")
	}
}

// The honest non-answer, and the half most likely to be "fixed" into a guess later: an artifact this
// scan cannot see teaches it nothing, so the row stays NULL. That is the true record for a version
// whose disk is unplugged or was never declared.
func TestReconcileLeavesANullRowWhoseArtifactIsElsewhere(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	attributed(m)

	// A row with no artifact anywhere under this root.
	if err := st.InsertVersion(store.VersionRow{
		ID: "01VAWAY", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), Kind: "full",
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	row, _, err := st.GetVersion("01VAWAY")
	if err != nil {
		t.Fatal(err)
	}
	if row.StorageID != nil {
		t.Fatalf("a version this scan never saw must stay unattributed, got %v — that is an invented fact", *row.StorageID)
	}
	// And it must NOT be marked missing on this storage's behalf: absent from a root it was never
	// on is not evidence the artifact is gone.
	if row.Missing {
		t.Error("a version belonging to no known storage was marked missing by a scan of another root")
	}
}

// quince#428. The adopt predicate is "no row AT ALL", not "no row I own". With the old predicate an
// artifact whose row belongs to another storage fell through to adopt, which inserts by primary key
// — `UNIQUE constraint failed: versions.id`, once per version, every startup.
func TestReconcileDoesNotAdoptAVersionOwnedByAnotherStorage(t *testing.T) {
	m, _, backups, st := newNSManager(t, clonetree.Copy, generousPolicy())
	attributed(m)

	verDir := nsVersionDir(backups, testUDID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "01VOTHER", "", testUDID, BackendCopy)

	other := "01JSTORAGEOTHER000000000"
	if err := st.InsertVersion(store.VersionRow{
		ID: "01VOTHER", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), Kind: "full", StorageID: &other,
	}); err != nil {
		t.Fatal(err)
	}

	// ASSERT THE ATTEMPT, NOT THE OUTCOME — and the first version of this test got that wrong.
	//
	// It checked the row afterwards and PASSED with the guard disabled: `adopt` fails on the
	// duplicate primary key, logs, and returns, so the row is untouched either way. Post-state
	// cannot distinguish "did not adopt" from "tried to adopt and failed", and only the second is
	// the bug. Caught by mutation, not by reading.
	var buf bytes.Buffer
	m.log = captureLogs(&buf)

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatalf("reconcile must not fail on another storage's version: %v", err)
	}

	if strings.Contains(buf.String(), "adopt insert failed") {
		t.Error("adopt was ATTEMPTED on a version that already has a row — quince#428's predicate is back")
	}
	if !strings.Contains(buf.String(), "attributed to ANOTHER storage") {
		t.Error("the cross-storage sighting must be reported, not silently skipped")
	}

	row, _, err := st.GetVersion("01VOTHER")
	if err != nil {
		t.Fatal(err)
	}
	// First scan wins. The artifact being present under two roots is ambiguous input — a bind mount
	// or a replica — and the resolution is to keep what is recorded and warn, never to re-point a
	// committed backup at whichever root was scanned last.
	if row.StorageID == nil || *row.StorageID != other {
		t.Fatalf("another storage's attribution was overwritten: %v", row.StorageID)
	}
}

// An UNATTRIBUTED Manager — a storage with no marker yet — must not attribute anything. It has no
// id to attribute TO, and writing "" would turn "not yet known" into a claim.
func TestReconcileAttributesNothingWhenTheManagerHasNoStorageID(t *testing.T) {
	m, _, backups, st := newNSManager(t, clonetree.Copy, generousPolicy())

	verDir := nsVersionDir(backups, testUDID, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC))
	goodEncryptedFull(t, verDir)
	mustMarker(t, verDir, "01VNOID", "", testUDID, BackendCopy)

	if err := st.InsertVersion(store.VersionRow{
		ID: "01VNOID", UDID: testUDID, Backend: BackendCopy, CreatedAt: time.Now().UTC(),
		JobID: strPtrLocal("j"), Kind: "full",
	}); err != nil {
		t.Fatal(err)
	}

	if err := m.Reconcile(context.Background()); err != nil {
		t.Fatal(err)
	}

	row, _, err := st.GetVersion("01VNOID")
	if err != nil {
		t.Fatal(err)
	}
	if row.StorageID != nil {
		t.Fatalf("an unattributed Manager must not attribute, got %v", *row.StorageID)
	}
}

// captureLogs returns a logger writing into buf, so a test can assert on what the code SAID.
//
// It exists because the first version of TestReconcileDoesNotAdoptAVersionOwnedByAnotherStorage
// asserted post-state and passed with the guard disabled: `adopt` fails on the duplicate primary
// key, LOGS, and returns, so the row is unchanged either way. The observable difference between
// "did not adopt" and "tried to adopt and failed" is the log line, and nothing else.
func captureLogs(buf *bytes.Buffer) *slog.Logger {
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
}
