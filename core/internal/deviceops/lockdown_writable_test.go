package deviceops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// qn.6p D7. The probe answers "can quince write a pairing record here", which is what the UI needs
// — not "is this mount ro", which is only one of the ways it fails.
func TestWritableProbesRatherThanReadingAMountFlag(t *testing.T) {
	sys := filepath.Join(t.TempDir(), "lockdown")
	l := NewLockdownStore(t.TempDir(), sys, discard())

	ok, reason := l.Writable()
	if !ok {
		t.Fatalf("a fresh dir reported unwritable: %s", reason)
	}
	if reason != "" {
		t.Errorf("writable carried a reason %q; want empty", reason)
	}

	// The probe must leave nothing behind: this directory holds private-key-grade secrets and a
	// stray file in it is at best confusing to whoever audits the mount.
	entries, err := os.ReadDir(sys)
	if err != nil {
		t.Fatalf("read sys dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("probe left %d file(s) behind: %v", len(entries), entries)
	}
}

// An unwritable directory is detected by PERMISSIONS, which sets no ST_RDONLY flag — the case a
// statfs-based check would call writable and then fail at the moment of pairing.
func TestWritableDetectsPermissionsNotJustReadOnlyMounts(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: mode bits do not refuse root, so this case is unobservable here")
	}
	sys := filepath.Join(t.TempDir(), "lockdown")
	if err := os.MkdirAll(sys, 0o500); err != nil { // r-x: listable, not writable
		t.Fatalf("mkdir: %v", err)
	}

	ok, reason := l(t, sys).Writable()
	if ok {
		t.Fatal("an unwritable directory reported writable — pairing would fail at the moment of use")
	}
	// The reason reaches an operator through the API, so it must name the path rather than only
	// the errno.
	if !strings.Contains(reason, sys) {
		t.Errorf("reason %q does not name the directory", reason)
	}
}

func l(t *testing.T, sys string) *LockdownStore {
	t.Helper()
	return NewLockdownStore(t.TempDir(), sys, discard())
}

// The permissions case above skips as root, and `make gates` runs as root — so on the ladder that
// arm is never exercised. This one holds for ANY uid: a path that already exists as a FILE cannot
// be a directory, and root does not get to override that.
//
// It is here because a test that silently skips in CI is a test the ladder does not have.
func TestWritableRefusesWhenTheDirCannotExist(t *testing.T) {
	sys := filepath.Join(t.TempDir(), "lockdown")
	if err := os.WriteFile(sys, nil, 0o600); err != nil { // occupy the path with a file
		t.Fatalf("write file: %v", err)
	}

	ok, reason := l(t, sys).Writable()
	if ok {
		t.Fatal("a path occupied by a file reported writable")
	}
	if !strings.Contains(reason, sys) {
		t.Errorf("reason %q does not name the path", reason)
	}
}
