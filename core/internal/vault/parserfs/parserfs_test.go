package parserfs

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/ios-backup-crypt/fixture"

	"github.com/novkostya/quince/core/internal/vault"
)

func newFS(t *testing.T, files []fixture.File) (*FS, string) {
	t.Helper()
	dir := t.TempDir()
	if _, err := fixture.Build(dir, fixture.Spec{Unencrypted: true, Files: files}); err != nil {
		t.Fatal(err)
	}
	v, err := vault.OpenUnencrypted(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = v.Close() })
	if _, err := v.Unlock(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	scratch := t.TempDir()
	f, err := New(v, scratch)
	if err != nil {
		t.Fatal(err)
	}
	return f, scratch
}

func TestExists(t *testing.T) {
	f, _ := newFS(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "Library/SMS/sms.db", Flags: 1, Data: []byte("db")},
	})
	for _, c := range []struct {
		domain, path string
		want         bool
	}{
		{"HomeDomain", "Library/SMS/sms.db", true},
		{"HomeDomain", "Library/SMS/sms.d", false},   // a PREFIX of a real path is not a hit
		{"HomeDomain", "Library/SMS/sms.dbx", false}, // nor is an extension of one
		{"OtherDomain", "Library/SMS/sms.db", false}, // right path, wrong domain
	} {
		got, err := f.Exists(c.domain, c.path)
		if err != nil {
			t.Fatalf("Exists(%q,%q): %v", c.domain, c.path, err)
		}
		if got != c.want {
			t.Errorf("Exists(%q,%q) = %v, want %v", c.domain, c.path, got, c.want)
		}
	}
}

// Materialize hands back a real, readable, PRIVATE copy — and brings the sidecars, without
// which SQLite would show a stale view of a database with a live WAL.
func TestMaterializeCopiesSidecars(t *testing.T) {
	f, scratch := newFS(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "Library/SMS/sms.db", Flags: 1, Data: []byte("main")},
		{Domain: "HomeDomain", RelativePath: "Library/SMS/sms.db-wal", Flags: 1, Data: []byte("wal")},
		{Domain: "HomeDomain", RelativePath: "Library/SMS/sms.db-shm", Flags: 1, Data: []byte("shm")},
	})
	path, err := f.Materialize("HomeDomain", "Library/SMS/sms.db")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ suffix, want string }{
		{"", "main"}, {"-wal", "wal"}, {"-shm", "shm"},
	} {
		b, err := os.ReadFile(path + c.suffix)
		if err != nil {
			t.Fatalf("read %s: %v", filepath.Base(path+c.suffix), err)
		}
		if string(b) != c.want {
			t.Errorf("%s = %q, want %q", filepath.Base(path+c.suffix), b, c.want)
		}
	}
	// INSIDE THE SESSION SCRATCH, so the teardown qn.8 already owns removes it without
	// learning about this package.
	if rel, err := filepath.Rel(scratch, path); err != nil || rel == ".." || filepath.IsAbs(rel) {
		t.Errorf("materialized to %q, which is not inside the scratch dir %q", path, scratch)
	}
	// A -journal that does not exist must not be invented.
	if _, err := os.Stat(path + "-journal"); !os.IsNotExist(err) {
		t.Errorf("-journal exists for a backup that has none (err=%v)", err)
	}
}

// The copy is PRIVATE: writing to it, which is what opening a SQLite db does, must not
// reach the backup. This is the never-mutate rule asserted rather than asserted-about.
func TestMaterializedCopyIsPrivate(t *testing.T) {
	f, _ := newFS(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "a.db", Flags: 1, Data: []byte("original")},
	})
	path, err := f.Materialize("HomeDomain", "a.db")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("MUTATED"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Re-materializing a DIFFERENT FS over the same backup must still see the original.
	f2, _ := newFS(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "a.db", Flags: 1, Data: []byte("original")},
	})
	p2, err := f2.Materialize("HomeDomain", "a.db")
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(p2)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "original" {
		t.Errorf("second materialize read %q — the first one's write escaped its copy", b)
	}
}

func TestMaterializeAbsentIsNotExist(t *testing.T) {
	f, _ := newFS(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "a.db", Flags: 1, Data: []byte("x")},
	})
	_, err := f.Materialize("HomeDomain", "nope.db")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Materialize of an absent file = %v, want fs.ErrNotExist", err)
	}
}

// Memoised: the same path materializes once. The parser may ask for one database from more
// than one place, and decrypting a large one twice per session buys nothing.
func TestMaterializeIsMemoised(t *testing.T) {
	f, _ := newFS(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "a.db", Flags: 1, Data: []byte("x")},
	})
	p1, err := f.Materialize("HomeDomain", "a.db")
	if err != nil {
		t.Fatal(err)
	}
	p2, err := f.Materialize("HomeDomain", "a.db")
	if err != nil {
		t.Fatal(err)
	}
	if p1 != p2 {
		t.Errorf("materialized twice to %q and %q", p1, p2)
	}
}

// ReadDir names DIRECT CHILDREN by their final element only — the parser joins the name
// back onto the directory, so a deeper path would address nothing.
func TestReadDirNamesDirectChildrenOnly(t *testing.T) {
	f, _ := newFS(t, []fixture.File{
		{Domain: "AppDomain-x", RelativePath: "Stores/Data-aaa.sqlite", Flags: 1, Data: []byte("1")},
		{Domain: "AppDomain-x", RelativePath: "Stores/Data-bbb.sqlite", Flags: 1, Data: []byte("2")},
		{Domain: "AppDomain-x", RelativePath: "Stores/nested/deep.sqlite", Flags: 1, Data: []byte("3")},
		{Domain: "AppDomain-x", RelativePath: "Elsewhere/other.sqlite", Flags: 1, Data: []byte("4")},
	})
	names, err := f.ReadDir("AppDomain-x", "Stores")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, n := range names {
		got[n] = true
	}
	for _, want := range []string{"Data-aaa.sqlite", "Data-bbb.sqlite", "nested"} {
		if !got[want] {
			t.Errorf("ReadDir missing %q; got %v", want, names)
		}
	}
	if got["deep.sqlite"] {
		t.Error("ReadDir returned a grandchild — the parser joins names onto the dir, so this addresses nothing")
	}
	if got["other.sqlite"] {
		t.Error("ReadDir returned an entry from outside the directory")
	}
	if len(names) != 3 {
		t.Errorf("ReadDir returned %d names, want 3: %v", len(names), names)
	}
}

func TestReadDirAbsentIsNotExist(t *testing.T) {
	f, _ := newFS(t, []fixture.File{
		{Domain: "AppDomain-x", RelativePath: "Stores/a.sqlite", Flags: 1, Data: []byte("1")},
	})
	if _, err := f.ReadDir("AppDomain-x", "Nowhere"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadDir of an absent dir = %v, want fs.ErrNotExist", err)
	}
}

func TestNewRefusesAMissingScratchDir(t *testing.T) {
	f, _ := newFS(t, []fixture.File{{Domain: "d", RelativePath: "a", Flags: 1, Data: []byte("x")}})
	if _, err := New(f.v, filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Error("New accepted a scratch dir that does not exist")
	}
}
