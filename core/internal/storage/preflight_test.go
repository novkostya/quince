package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// qn.6c story 7 — the pre-backup check. Three distinguishable, actionable failures, checked BEFORE
// anything touches the path, and never a re-probe.

// MISSING MEDIUM is the one that matters most: a readable path with the marker gone is an unplugged
// disk's bare mountpoint, and treating it as writable fills whatever filesystem it was mounted on.
func TestPreflightRefusesAMissingMedium(t *testing.T) {
	m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	if err := os.Remove(filepath.Join(backups, StorageMarkerName)); err != nil {
		t.Fatal(err)
	}

	err := m.slots[0].preflight()
	if err == nil {
		t.Fatal("a readable path with no marker must refuse — it is a bare mountpoint")
	}
	if !strings.Contains(err.Error(), "not mounted") {
		t.Errorf("the refusal must say the medium is not mounted, so the user knows what to do: %q", err)
	}

	// And it must refuse THROUGH the write path, not merely when asked directly.
	if _, err := m.Seed(testUDID, "job-1"); err == nil {
		t.Error("Seed must refuse on a missing medium — the check is upstream of Provision")
	}
	if _, _, err := m.PrepareWork(testUDID, "job-1"); err == nil {
		t.Error("PrepareWork must refuse on a missing medium")
	}
}

// A DIFFERENT storage at the same path — a swapped disk, or a mountpoint that now resolves
// elsewhere. Writing here would mix two storages' contents under one identity.
func TestPreflightRefusesAChangedMedium(t *testing.T) {
	m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	m.slots[0].StorageID = "01JSTORAGEEXPECTED000000"
	seedStorageMarker(t, backups, "01JSTORAGESOMEOTHER00000", m.slots[0].BackendName)

	err := m.slots[0].preflight()
	if err == nil {
		t.Fatal("a marker naming a different storage must refuse")
	}
	if !strings.Contains(err.Error(), "01JSTORAGESOMEOTHER00000") {
		t.Errorf("the refusal must name what it found, not just that it disagreed: %q", err)
	}
}

// BACKEND MISMATCH, compared against what the slot already holds. Versions committed here were made
// with the recorded backend, so adopting a different one changes what a latest/ exchange means.
func TestPreflightRefusesABackendMismatch(t *testing.T) {
	m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	seedStorageMarker(t, backups, "", BackendZFS) // slot holds copy

	err := m.slots[0].preflight()
	if err == nil {
		t.Fatal("a marker recording a different backend must refuse")
	}
	if !strings.Contains(err.Error(), "refusing rather than adopting") {
		t.Errorf("it must refuse rather than downgrade or adopt: %q", err)
	}
}

// A HEALTHY storage passes, which is the assertion that keeps the three above from being satisfied
// by a check that simply always refuses.
func TestPreflightPassesOnAHealthyStorage(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	if err := m.slots[0].preflight(); err != nil {
		t.Fatalf("a healthy storage must pass: %v", err)
	}
	if _, err := m.Seed(testUDID, "job-1"); err != nil {
		t.Errorf("Seed must succeed on a healthy storage: %v", err)
	}
}

// IT NEVER RE-CREATES THE MARKER. G5b's rule, at the pre-backup moment: a check that repaired what
// it found would turn every one of the refusals above into a silent adoption.
func TestPreflightNeverWritesAnything(t *testing.T) {
	m, _, backups, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	marker := filepath.Join(backups, StorageMarkerName)
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	_ = m.slots[0].preflight()
	_, _ = m.Seed(testUDID, "job-1")

	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("preflight re-created the storage marker — that is the operation G5b forbids")
	}
}
