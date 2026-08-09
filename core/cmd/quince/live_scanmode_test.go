package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/storage"
)

// THE WIRING GATE FOR qn.6i D2: `serve` DEFERS THE SCAN AND THE CLIs DO NOT.
//
// `storage` owns the split and gates it directly (TestRollForwardDoesNotScanAndTheScanDoesNotRoll…).
// What THIS file gates is that the mode actually reaches it — a build where `buildStorage` ignored
// its `scanMode` would pass every test in that package and reintroduce quince#592 in full, because
// the ~48-second walk would be back in front of the listener with nothing complaining.
//
// The observable is an on-disk version with no registry row: only a scan adopts it. So
// `scanSynchronous` must have adopted it by the time buildStorage returns, and `scanDeferred` must
// not have.
func TestBuildStorageDefersTheScanForServeAndRunsItForTheCLIs(t *testing.T) {
	for _, tc := range []struct {
		name        string
		mode        scanMode
		wantAdopted bool
	}{
		{"serve defers it", scanDeferred, false},
		{"the CLIs run it", scanSynchronous, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			seedAdoptableVersion(t, root)

			e := config.StorageEntry{Name: "disk", Path: root, Default: true, Backend: "copy"}.Resolved()
			path := filepath.Join(t.TempDir(), "config.yml")
			cfg := config.Default()
			entries := []config.StorageEntry{e}
			cfg.Storage = &entries
			data, err := config.Marshal(cfg)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if err := os.WriteFile(path, data, 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}

			st := testStore(t)
			cfgSvc := config.NewService(path, quietLog())
			if _, _, err := buildStorage(context.Background(), config.Bootstrap{}, cfgSvc, st,
				bus.New(), quietLog(), tc.mode); err != nil {
				t.Fatalf("buildStorage: %v", err)
			}

			_, adopted, _ := st.GetVersion(scanModeVersionID)
			if adopted != tc.wantAdopted {
				if tc.wantAdopted {
					t.Fatal("the CLI path returned WITHOUT scanning — `versions verify` would then " +
						"report on a registry nobody repaired")
				}
				t.Fatal("the serve path scanned before returning — that is quince#592 exactly, the " +
					"~48-second walk back in front of the listener")
			}
		})
	}
}

const (
	scanModeUDID      = "SYNTHETIC-UDID-6I01"
	scanModeVersionID = "01VADOPTMEIFYOUSCAN00000"
)

// seedAdoptableVersion writes a committed version tree with a valid marker and NO registry row, in
// the layout the namespace backend scans: <root>/<udid>/versions/<ts>/.
//
// Written by hand rather than through the Manager, deliberately: committing through the Manager
// would insert the row, which is the very thing whose absence this fixture is for.
func seedAdoptableVersion(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, scanModeUDID, "versions", "2026-07-01T000000Z")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// The four files a structural verify wants, plus the marker that makes it a quince version.
	for name, body := range map[string]string{
		"Info.plist":     "<plist/>",
		"Manifest.plist": "<plist/>",
		"Status.plist":   "<plist/>",
		"Manifest.db":    "SQLite format 3\x00",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	// THROUGH `storage.WriteMarker`, NOT HAND-ROLLED JSON. The first version of this fixture wrote
	// the marker literally and the test failed: a Marker carries a `checksum` over its own fields and
	// `ReadMarker` verifies it, so a hand-written one is not a marker — `Scan` skips it and the
	// version is invisible to BOTH modes, which makes the test pass for the deferred case by
	// accident and fail for the synchronous one for the wrong reason.
	if err := storage.WriteMarker(dir, storage.Marker{
		VersionID: scanModeVersionID, UDID: scanModeUDID, Backend: "copy",
		CreatedAt: "2026-07-01T00:00:00Z", Kind: "full", Encrypted: true,
		StructureVerifiedAt: "2026-07-01T00:00:00Z", AppVersion: "test",
	}); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}
