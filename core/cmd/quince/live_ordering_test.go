package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/store"
)

// G8 — A COMMIT THAT ROLLED FORWARD IS `succeeded`, NEVER `connection_lost`.
//
// This is the rung's sharpest hazard, and it is one THIS PR CREATED. Before the split, storage
// reconciliation was a single call and the ordering could not be inverted; now it is two —
// `RollForwardAll` and `ReconcileScan` — and a later change could move, reorder, or fold the first
// into the runner. Every other test in the repository would stay green while a completed multi-hour
// backup started being written to the database as *interrupted by a restart*.
//
// `Engine.Reconcile` asks `VersionForJob`: a version means the commit completed, so the job becomes
// `succeeded`; no version means `connection_lost`. That question is only answerable if roll-forward
// has already registered the version — which is why the order is the thing under test and not the
// engine's own logic.
//
// IT ENTERS AT `buildLiveStack`, DELIBERATELY. A unit test of `Engine.Reconcile` would pass with the
// ordering inverted, because it never exercises the wiring where the inversion lives. The seam is the
// gate.
func TestARolledForwardCommitLandsSucceededAndNotConnectionLost(t *testing.T) {
	const (
		udid    = "SYNTHETIC-UDID-6I08"
		jobID   = "01JOBCRASHEDMIDCOMMIT000"
		version = "01VROLLEDFORWARD0000000"
	)

	root := t.TempDir()
	seedArchivedCommit(t, root, udid, jobID, version)

	st := testStore(t)
	// The crash-orphaned row: non-terminal, written by a process that no longer exists.
	if err := st.InsertJob(store.JobRow{
		ID: jobID, UDID: udid, Kind: "backup", Transport: "wifi",
		State: backup.StateCommitting, Phase: backup.StateCommitting, Liveness: backup.LivenessActive,
		StartedAt: time.Now().UTC().Add(-2 * time.Hour), IntentID: jobID, Attempt: 1,
	}); err != nil {
		t.Fatalf("insert job: %v", err)
	}

	cfgSvc := storageConfigService(t, root)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := buildLiveStack(ctx, config.Bootstrap{Data: t.TempDir()}, cfgSvc, st,
		bus.New(), quietLog(), scanDeferred); err != nil {
		t.Fatalf("buildLiveStack: %v", err)
	}

	row, ok, err := st.GetJob(jobID)
	if err != nil || !ok {
		t.Fatalf("get job: ok=%v err=%v", ok, err)
	}
	if row.State == backup.StateConnectionLost {
		t.Fatal("a backup that transferred, verified and crashed one phase from done was recorded as " +
			"`connection_lost` — roll-forward ran AFTER the job reconciler, so the job was judged " +
			"before its commit was completed. This is D1, and it is the defect the rung would " +
			"introduce BY fixing quince#592")
	}
	if row.State != backup.StateSucceeded {
		t.Fatalf("job state = %q, want %q", row.State, backup.StateSucceeded)
	}
	if row.VersionID == nil || *row.VersionID != version {
		t.Fatalf("job version = %v, want %q — succeeded without naming the version it produced",
			row.VersionID, version)
	}
}

// seedArchivedCommit stages the on-disk state a crash one phase from done leaves behind: the version
// content committed into `latest/` with its marker, and the commit journal still present at
// `archived`.
//
// `PhaseArchived` is chosen because it is the phase where the ARTIFACT ALREADY EXISTS and only the
// registry write is missing — the case the roll-forward principle exists for, and the one where
// judging the job early does the most damage.
//
// The journal is written as plain JSON on purpose: unlike a Marker it carries no checksum, so this is
// the file the backend itself writes, not an approximation of it.
func seedArchivedCommit(t *testing.T, root, udid, jobID, versionID string) {
	t.Helper()
	dev := filepath.Join(root, udid)
	latest := filepath.Join(dev, "latest")
	if err := os.MkdirAll(latest, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, body := range map[string]string{
		"Info.plist":     "<plist/>",
		"Manifest.plist": "<plist/>",
		"Status.plist":   "<plist/>",
		"Manifest.db":    "SQLite format 3\x00",
	} {
		if err := os.WriteFile(filepath.Join(latest, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	created := "2026-08-01T00:00:00Z"
	if err := storage.WriteMarker(latest, storage.Marker{
		VersionID: versionID, JobID: jobID, UDID: udid, Backend: "copy",
		CreatedAt: created, Kind: "full", Encrypted: true,
		StructureVerifiedAt: created, AppVersion: "test",
	}); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	j := map[string]any{
		"version_id": versionID, "udid": udid, "backend": "copy", "job_id": jobID,
		"phase": "archived", "created_at": created, "kind": "full", "encrypted": true,
		"structure_verified_at": created, "device_dir": dev,
	}
	b, err := json.Marshal(j)
	if err != nil {
		t.Fatalf("marshal journal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dev, ".quince-commit.json"), b, 0o644); err != nil {
		t.Fatalf("write journal: %v", err)
	}
}

// storageConfigService writes a config.yml declaring one copy-backend storage at root and returns a
// service over it, the way `serve` reads one.
func storageConfigService(t *testing.T, root string) *config.Service {
	t.Helper()
	cfg := config.Default()
	entries := []config.StorageEntry{
		config.StorageEntry{Name: "disk", Path: root, Default: true, Backend: "copy"}.Resolved(),
	}
	cfg.Storage = &entries
	data, err := config.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	path := filepath.Join(t.TempDir(), "config.yml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return config.NewService(path, quietLog())
}
