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
