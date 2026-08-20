package deviceops

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// TestBackupThenRestoreRoundTrip is the amendment-1 unit proof: records written by a pair are
// backed up to $QUINCE_DATA and restored into a fresh (empty) system dir — the recreate case.
func TestBackupThenRestoreRoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	sysDir := filepath.Join(t.TempDir(), "lockdown")

	// A pair produced the host identity + a per-device record in the system dir.
	writeFile(t, filepath.Join(sysDir, "SystemConfiguration.plist"), "host-identity")
	writeFile(t, filepath.Join(sysDir, "SYNTHETIC-UDID.plist"), "device-record")

	l := NewLockdownStore(dataDir, sysDir, discard())
	l.Backup()

	// Both records now live under $QUINCE_DATA/lockdown.
	persist := filepath.Join(dataDir, "lockdown")
	if read(t, filepath.Join(persist, "SystemConfiguration.plist")) != "host-identity" {
		t.Fatal("host identity not persisted")
	}
	if read(t, filepath.Join(persist, "SYNTHETIC-UDID.plist")) != "device-record" {
		t.Fatal("device record not persisted")
	}

	// Container recreate: a brand-new empty system dir; Restore brings the pairings back.
	freshSys := filepath.Join(t.TempDir(), "lockdown")
	l2 := NewLockdownStore(dataDir, freshSys, discard())
	l2.Restore()
	if read(t, filepath.Join(freshSys, "SystemConfiguration.plist")) != "host-identity" {
		t.Fatal("host identity not restored after recreate")
	}
	if read(t, filepath.Join(freshSys, "SYNTHETIC-UDID.plist")) != "device-record" {
		t.Fatal("device record not restored after recreate")
	}
}

// TestRestoreDoesNotClobberLiveRecord: a live/bind-mounted system record wins over a persisted
// copy (we never overwrite on restore).
func TestRestoreDoesNotClobberLiveRecord(t *testing.T) {
	dataDir := t.TempDir()
	sysDir := filepath.Join(t.TempDir(), "lockdown")
	writeFile(t, filepath.Join(dataDir, "lockdown", "SystemConfiguration.plist"), "old-persisted")
	writeFile(t, filepath.Join(sysDir, "SystemConfiguration.plist"), "live-current")

	NewLockdownStore(dataDir, sysDir, discard()).Restore()
	if got := read(t, filepath.Join(sysDir, "SystemConfiguration.plist")); got != "live-current" {
		t.Fatalf("restore clobbered a live record: %q", got)
	}
}

// TestBackupOverwritesStaleCopy: Backup refreshes persistent storage (host identity can change).
func TestBackupOverwritesStaleCopy(t *testing.T) {
	dataDir := t.TempDir()
	sysDir := filepath.Join(t.TempDir(), "lockdown")
	writeFile(t, filepath.Join(dataDir, "lockdown", "SystemConfiguration.plist"), "stale")
	writeFile(t, filepath.Join(sysDir, "SystemConfiguration.plist"), "current")

	NewLockdownStore(dataDir, sysDir, discard()).Backup()
	if got := read(t, filepath.Join(dataDir, "lockdown", "SystemConfiguration.plist")); got != "current" {
		t.Fatalf("backup did not overwrite the stale copy: %q", got)
	}
}

func TestRestoreMissingPersistDirIsNoError(t *testing.T) {
	sysDir := filepath.Join(t.TempDir(), "lockdown")
	// No persist dir exists yet (first ever run) — Restore must be a quiet no-op.
	NewLockdownStore(t.TempDir(), sysDir, discard()).Restore()
	if entries, _ := os.ReadDir(sysDir); len(entries) != 0 {
		t.Fatalf("expected empty system dir, got %d entries", len(entries))
	}
}

// TestBackupSurvivesAliasedDirs is quince#1309's regression fixture, at the shape that produced it
// rather than at the function: a deployment that binds ONE host directory at both
// `$QUINCE_DATA/lockdown` and the system dir, which the shipped compose examples did.
//
// It asserts the RECORD, not the call. `Backup()` returning without error was already true while it
// was emptying the file — the failure was silent, and a test that watched the error would have
// passed throughout.
func TestBackupSurvivesAliasedDirs(t *testing.T) {
	data := t.TempDir()
	shared := filepath.Join(data, "lockdown") // sysDir == persistDir, as the two binds made them
	rec := filepath.Join(shared, "00008110-000E54EE0EDA801E.plist")
	writeFile(t, rec, "PAIRING-RECORD")

	NewLockdownStore(data, shared, discard()).Backup()

	if got := read(t, rec); got != "PAIRING-RECORD" {
		t.Fatalf("Backup() damaged the record through aliased dirs: got %q, want %q", got, "PAIRING-RECORD")
	}
}

// TestCopyFileRefusesToEmptyItself pins the guard itself, so the behaviour survives a caller being
// rewritten. Two different PATHS to one file is the case: `os.SameFile` is the only thing that can
// see it, since neither string tells you.
func TestCopyFileRefusesToEmptyItself(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "record.plist")
	writeFile(t, a, "PAIRING-RECORD")

	b := filepath.Join(dir, "alias.plist")
	if err := os.Link(a, b); err != nil {
		t.Skipf("hard links unavailable here: %v", err)
	}

	if err := copyFile(a, b); err != nil {
		t.Fatalf("copyFile over its own inode should be a no-op, got %v", err)
	}
	if got := read(t, a); got != "PAIRING-RECORD" {
		t.Fatalf("copyFile emptied the file it was reading: got %q", got)
	}
}
