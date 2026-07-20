package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"howett.net/plist"

	_ "modernc.org/sqlite"
)

// treeOpts controls the shape of a synthetic MobileBackup2 tree (fixtures use synthetic ids +
// names only — no real backup data; privacy hard rule).
type treeOpts struct {
	encrypted            bool
	full                 bool
	tornStatus           bool // SnapshotState != finished
	badManifestPlist     bool // unparseable Manifest.plist
	missingBlob          bool // unencrypted: a Files row pointing at an absent blob
	encPlaintextManifest bool // encrypted flag but Manifest.db is plaintext SQLite (red flag)
	emptyShards          bool // encrypted full but no blob shards
}

const fixtureFileID = "ab0000000000000000000000000000000000cafe" // 40 hex

// buildTree writes a synthetic backup tree into dir per opts.
func buildTree(t *testing.T, dir string, o treeOpts) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	snapState := "finished"
	if o.tornStatus {
		snapState = "uncompleted"
	}
	writePlist(t, filepath.Join(dir, "Status.plist"), map[string]any{
		"SnapshotState": snapState, "IsFullBackup": o.full, "Version": "3.3",
	})
	writePlist(t, filepath.Join(dir, "Info.plist"), map[string]any{
		"Device Name": "synthetic-iphone", "Product Type": "iPhone17,2",
	})
	if o.badManifestPlist {
		writeFile(t, filepath.Join(dir, "Manifest.plist"), []byte("not a plist at all \x00\x01"))
	} else {
		writePlist(t, filepath.Join(dir, "Manifest.plist"), map[string]any{
			"IsEncrypted": o.encrypted, "Version": "10.0",
		})
	}

	dbPath := filepath.Join(dir, "Manifest.db")
	if o.encrypted {
		if o.encPlaintextManifest {
			writeFile(t, dbPath, append([]byte(sqliteMagic), make([]byte, 200)...))
		} else {
			blob := make([]byte, 256)
			for i := range blob {
				blob[i] = 0xEE
			}
			writeFile(t, dbPath, blob)
		}
		if o.full && !o.emptyShards {
			writeFile(t, filepath.Join(dir, fixtureFileID[:2], fixtureFileID), []byte("blob"))
		}
	} else {
		writeSQLiteManifest(t, dbPath)
		if !o.missingBlob {
			writeFile(t, filepath.Join(dir, fixtureFileID[:2], fixtureFileID), []byte("blob"))
		}
	}
}

// goodEncryptedFull is the common gate input: an encrypted full backup that should verify.
func goodEncryptedFull(t *testing.T, dir string) {
	buildTree(t, dir, treeOpts{encrypted: true, full: true})
}

func writePlist(t *testing.T, path string, v any) {
	t.Helper()
	b, err := plist.MarshalIndent(v, plist.XMLFormat, "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, b)
}

func writeFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeSQLiteManifest creates a minimal real Manifest.db (Files + Properties, one Files row).
func writeSQLiteManifest(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	stmts := []string{
		`CREATE TABLE Files (fileID TEXT PRIMARY KEY, domain TEXT, relativePath TEXT, flags INTEGER, file BLOB)`,
		`CREATE TABLE Properties (key TEXT PRIMARY KEY, value BLOB)`,
		`INSERT INTO Files (fileID, domain, relativePath, flags) VALUES ('` + fixtureFileID + `', 'CameraRollDomain', 'Media/DCIM/x.HEIC', 1)`,
		`INSERT INTO Properties (key, value) VALUES ('salt', x'0011')`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("sqlite manifest: %v", err)
		}
	}
}

// monotonicClock returns a now() that advances one second per call (RFC3339 is second-precision,
// so distinct commits must land in distinct seconds → distinct versions/<ts> dirs).
func monotonicClock() func() time.Time {
	base := time.Date(2026, 7, 18, 2, 30, 0, 0, time.UTC)
	n := 0
	return func() time.Time {
		n++
		return base.Add(time.Duration(n) * time.Second)
	}
}

// seqID returns a deterministic, sortable version-id generator (v0000001, v0000002, …).
func seqID() func() string {
	n := 0
	return func() string {
		n++
		return fmt.Sprintf("v%06d", n)
	}
}
