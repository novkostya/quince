package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	"howett.net/plist"

	_ "modernc.org/sqlite" // read-only Manifest.db open on the unencrypted verify branch
)

// sqliteMagic is the 16-byte header of a plaintext SQLite file. An "encrypted" Manifest.db
// that opens as plain SQLite is a red flag (A1).
var sqliteMagic = []byte("SQLite format 3\x00")

// minManifestSize is the "non-trivial size" floor for an encrypted Manifest.db (a real one is
// kilobytes+; anything tiny is a torn/empty write).
const minManifestSize = 64

// Verify is quince's structural verification (design §4/D3, passwordless, automatic). It is the
// tree-inspection half of verification and lives in storage BECAUSE adoption and reconciliation
// verify trees that have no process exit code to consult (ruling 3). The DB checks branch on
// Manifest.plist.IsEncrypted (A1 / decisions (bc)): encrypted backups (the product default)
// encrypt Manifest.db itself since iOS 10.2, so passwordless open-and-sample is impossible —
// per-record blob resolution is deferred to the content level (qn.8 unlock).
//
// kind is the AUTHORITATIVE full|incremental|unknown value the caller supplies (qn.5b, finding
// #9(a)): for a live backup it is derived from whether working/ was seeded from an existing
// latest/ (the seed sentinel), NOT from Status.plist.IsFullBackup — which the lab proved lies (a
// first 33 GB backup writes IsFullBackup:false). Verify uses kind only to gate the encrypted
// blob-shard check (asserted on a genuine full backup, where an absent shard is definitely a bug;
// skipped on an incremental, where few/no new blobs is legitimate). An honest "full" here is what
// makes that check actually run on a first backup.
func Verify(treeDir, kind string) VerifyResult {
	fail := func(detail string) VerifyResult { return VerifyResult{OK: false, Detail: detail} }

	// Status.plist parses with SnapshotState == "finished".
	var status struct {
		SnapshotState string `plist:"SnapshotState"`
	}
	if err := readPlist(filepath.Join(treeDir, "Status.plist"), &status); err != nil {
		return fail("Status.plist does not parse: " + err.Error())
	}
	if status.SnapshotState != "finished" {
		return fail(fmt.Sprintf("SnapshotState is %q, want finished", status.SnapshotState))
	}

	// Info.plist parses.
	var info map[string]any
	if err := readPlist(filepath.Join(treeDir, "Info.plist"), &info); err != nil {
		return fail("Info.plist does not parse: " + err.Error())
	}

	// Manifest.plist parses; IsEncrypted selects the DB-check variant.
	var manifest struct {
		IsEncrypted bool `plist:"IsEncrypted"`
	}
	if err := readPlist(filepath.Join(treeDir, "Manifest.plist"), &manifest); err != nil {
		return fail("Manifest.plist does not parse: " + err.Error())
	}

	res := VerifyResult{Encrypted: manifest.IsEncrypted, Kind: kind, LogicalBytes: dirSize(treeDir)}

	dbPath := filepath.Join(treeDir, "Manifest.db")
	if manifest.IsEncrypted {
		if detail := verifyEncryptedDB(treeDir, dbPath, kind); detail != "" {
			return fail(detail)
		}
	} else {
		if detail := verifyPlainDB(treeDir, dbPath); detail != "" {
			return fail(detail)
		}
	}
	res.OK = true
	return res
}

// verifyEncryptedDB checks an encrypted Manifest.db without opening it (impossible passwordless):
// exists + non-trivial size + NOT plaintext SQLite magic + blob-shard sanity on a full backup.
func verifyEncryptedDB(treeDir, dbPath, kind string) string {
	fi, err := os.Stat(dbPath)
	if err != nil {
		return "Manifest.db missing: " + err.Error()
	}
	if fi.Size() < minManifestSize {
		return fmt.Sprintf("Manifest.db is only %d bytes (encrypted manifest expected non-trivial)", fi.Size())
	}
	head, err := readHead(dbPath, len(sqliteMagic))
	if err != nil {
		return "Manifest.db unreadable: " + err.Error()
	}
	if string(head) == string(sqliteMagic) {
		return "Manifest.plist says IsEncrypted but Manifest.db is plaintext SQLite (red flag)"
	}
	if kind == "full" && !hasNonEmptyShard(treeDir) {
		return "no non-empty blob-shard directory on a full encrypted backup"
	}
	return ""
}

// verifyPlainDB opens an unencrypted Manifest.db read-only, checks the required tables, and
// samples a few Files records to existing blobs.
func verifyPlainDB(treeDir, dbPath string) string {
	if _, err := os.Stat(dbPath); err != nil {
		return "Manifest.db missing: " + err.Error()
	}
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&immutable=1")
	if err != nil {
		return "Manifest.db will not open: " + err.Error()
	}
	defer func() { _ = db.Close() }()
	for _, table := range []string{"Files", "Properties"} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name = ?`, table).Scan(&name)
		if err != nil {
			return fmt.Sprintf("Manifest.db missing required table %q: %v", table, err)
		}
	}
	rows, err := db.Query(`SELECT fileID FROM Files LIMIT 8`)
	if err != nil {
		return "Manifest.db Files query failed: " + err.Error()
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var fileID string
		if err := rows.Scan(&fileID); err != nil {
			return "Files row scan failed: " + err.Error()
		}
		if len(fileID) < 2 {
			continue
		}
		blob := filepath.Join(treeDir, fileID[:2], fileID)
		if _, err := os.Stat(blob); err != nil {
			return fmt.Sprintf("Manifest record %s resolves to a missing blob %s", fileID, blob)
		}
	}
	if err := rows.Err(); err != nil {
		return err.Error()
	}
	return ""
}

// hasNonEmptyShard reports whether treeDir has at least one non-empty two-hex-char blob dir.
func hasNonEmptyShard(treeDir string) bool {
	entries, err := os.ReadDir(treeDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.IsDir() && hexShardDir(e.Name()) && !isEmptyDir(filepath.Join(treeDir, e.Name())) {
			return true
		}
	}
	return false
}

func readPlist(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = plist.Unmarshal(b, v)
	return err
}

func readHead(path string, n int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, n)
	m, err := f.Read(buf)
	if err != nil && m == 0 {
		return nil, err
	}
	return buf[:m], nil
}
