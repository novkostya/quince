package storage

import (
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
	"github.com/novkostya/quince/core/internal/store"
)

// qn.4c story 9 / qn.4a finding (v): Device.last_backup is derived from the newest COMMITTED
// VERSION — the only source that survives a restart and that can explain an ADOPTED version (a
// replicated or restored dataset), which has no job row. The defect this replaces: a device with
// five real versions rendering "No backups yet".

func versionRow(id, udid string, createdAt time.Time, jobID *string, missing bool) store.VersionRow {
	return store.VersionRow{
		ID: id, UDID: udid, Backend: BackendCopy, CreatedAt: createdAt, JobID: jobID,
		Kind: "full", Encrypted: true, IsLatest: false, Missing: missing,
	}
}

func TestLastBackupUsesTheNewestCommittedVersion(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	udid := "SYNTHETIC-UDID-AAAA-0001"
	older, newer := "JOB-OLD", "JOB-NEW"
	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	mustInsert(t, st, versionRow("V1", udid, base, &older, false))
	mustInsert(t, st, versionRow("V2", udid, base.Add(time.Hour), &newer, false))

	got, ok := m.LastBackup(udid)
	if !ok {
		t.Fatal("LastBackup: ok=false; want the device's newest version")
	}
	if got.At != "2026-07-20T13:00:00Z" {
		t.Fatalf("at = %q; want the NEWEST version's created_at", got.At)
	}
	if got.JobID == nil || *got.JobID != newer {
		t.Fatalf("job_id = %v; want %q", got.JobID, newer)
	}
	if got.Status != "succeeded" {
		t.Fatalf("status = %q; want succeeded (a version exists only after verify+commit)", got.Status)
	}
}

// An adopted version carries no job — the field must be null, never a fabricated id.
func TestLastBackupFromAdoptedVersionHasNullJobID(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	udid := "SYNTHETIC-UDID-BBBB-0002"
	mustInsert(t, st, versionRow("V1", udid, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), nil, false))

	got, ok := m.LastBackup(udid)
	if !ok || got.JobID != nil {
		t.Fatalf("LastBackup = (%+v, %v); want a value with a null job_id", got, ok)
	}
}

// A version the registry knows is MISSING on disk must not be claimed as the last backup —
// claiming a backup whose artifact is gone is exactly the overclaim the project forbids.
func TestLastBackupSkipsMissingVersions(t *testing.T) {
	m, _, _, st := newNSManager(t, clonetree.Copy, generousPolicy())
	udid := "SYNTHETIC-UDID-CCCC-0003"
	job := "JOB-1"
	base := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	mustInsert(t, st, versionRow("V1", udid, base, &job, false))
	mustInsert(t, st, versionRow("V2", udid, base.Add(time.Hour), &job, true)) // newest, but gone

	got, ok := m.LastBackup(udid)
	if !ok || got.At != "2026-07-20T12:00:00Z" {
		t.Fatalf("LastBackup = (%+v, %v); want the newest version that is still on disk", got, ok)
	}
}

// No versions → no claim.
func TestLastBackupWithNoVersions(t *testing.T) {
	m, _, _, _ := newNSManager(t, clonetree.Copy, generousPolicy())
	if got, ok := m.LastBackup("SYNTHETIC-UDID-DDDD-0004"); ok {
		t.Fatalf("LastBackup = %+v; want ok=false (\"No backups yet\" is the honest answer here)", got)
	}
}

func mustInsert(t *testing.T, st *store.Store, row store.VersionRow) {
	t.Helper()
	if err := st.InsertVersion(row); err != nil {
		t.Fatalf("insert version %s: %v", row.ID, err)
	}
}
