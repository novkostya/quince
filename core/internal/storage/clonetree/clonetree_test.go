package clonetree

import (
	"os"
	"path/filepath"
	"testing"
)

// buildSrcTree lays down a small backup-shaped tree: a content blob in a two-hex shard dir, a
// Manifest.db (a MutatesInPlace class), and a nested dir. Returns the root.
func buildSrcTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.MkdirAll(filepath.Join(root, "ab"), 0o755))
	must(os.WriteFile(filepath.Join(root, "ab", "ab00cafe"), []byte("blob-content"), 0o644))
	must(os.WriteFile(filepath.Join(root, "Manifest.db"), []byte("SQLite format 3\x00manifest"), 0o644))
	must(os.WriteFile(filepath.Join(root, "Info.plist"), []byte("<plist/>"), 0o644))
	return root
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	fa, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := os.Stat(b)
	if err != nil {
		t.Fatal(err)
	}
	return os.SameFile(fa, fb)
}

func TestCloneCopyIsFaithfulAndIndependent(t *testing.T) {
	src := buildSrcTree(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(dst, src, Copy); err != nil {
		t.Fatalf("clone copy: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "ab", "ab00cafe"))
	if err != nil || string(got) != "blob-content" {
		t.Fatalf("blob content = %q err=%v", got, err)
	}
	if sameFile(t, filepath.Join(src, "ab", "ab00cafe"), filepath.Join(dst, "ab", "ab00cafe")) {
		t.Fatal("copy shares an inode with the source — not independent")
	}
	// Mutating the source must not change the copy.
	if err := os.WriteFile(filepath.Join(src, "ab", "ab00cafe"), []byte("CHANGED"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(dst, "ab", "ab00cafe"))
	if string(got) != "blob-content" {
		t.Fatalf("copy changed with source: %q", got)
	}
}

func TestCloneHardlinkSharesInodesExceptMutatingClasses(t *testing.T) {
	src := buildSrcTree(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(dst, src, Hardlink); err != nil {
		t.Fatalf("clone hardlink: %v", err)
	}
	// A content blob is hard-linked (shared inode) — cheap versioning.
	if !sameFile(t, filepath.Join(src, "ab", "ab00cafe"), filepath.Join(dst, "ab", "ab00cafe")) {
		t.Fatal("content blob was not hard-linked")
	}
	// Manifest.db is a MutatesInPlace class → copied, NOT shared (else a rewrite would corrupt
	// the committed version sharing the inode).
	if sameFile(t, filepath.Join(src, "Manifest.db"), filepath.Join(dst, "Manifest.db")) {
		t.Fatal("Manifest.db was hard-linked — an in-place-mutating class must be copied")
	}
	if sameFile(t, filepath.Join(src, "Info.plist"), filepath.Join(dst, "Info.plist")) {
		t.Fatal("Info.plist was hard-linked — a rewritten-metadata class must be copied")
	}
}

func TestCloneReflinkIndependentOrSkipped(t *testing.T) {
	dir := t.TempDir()
	if !ReflinkProbe(dir) {
		t.Skipf("reflink unsupported on the test filesystem (%s) — reflink content proof runs on the lab host, gate 12 (interface fact 1)", dir)
	}
	src := buildSrcTree(t)
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(dst, src, Reflink); err != nil {
		t.Fatalf("clone reflink: %v", err)
	}
	if !sameFileContent(t, filepath.Join(src, "ab", "ab00cafe"), filepath.Join(dst, "ab", "ab00cafe")) {
		t.Fatal("reflink content differs")
	}
	// Reflink clones are independent files: mutating the source leaves the clone intact.
	if err := os.WriteFile(filepath.Join(src, "ab", "ab00cafe"), []byte("CHANGED-XXXXX"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(dst, "ab", "ab00cafe"))
	if string(got) != "blob-content" {
		t.Fatalf("reflink clone changed with source: %q", got)
	}
}

func sameFileContent(t *testing.T, a, b string) bool {
	t.Helper()
	ca, _ := os.ReadFile(a)
	cb, _ := os.ReadFile(b)
	return string(ca) == string(cb)
}

func TestCloneRecreatesSymlinks(t *testing.T) {
	src := buildSrcTree(t)
	if err := os.Symlink("ab/ab00cafe", filepath.Join(src, "link-to-blob")); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	dst := filepath.Join(t.TempDir(), "out")
	if err := Clone(dst, src, Copy); err != nil {
		t.Fatalf("clone: %v", err)
	}
	target, err := os.Readlink(filepath.Join(dst, "link-to-blob"))
	if err != nil || target != "ab/ab00cafe" {
		t.Fatalf("symlink not recreated: target=%q err=%v", target, err)
	}
}

func TestCloneMissingSourceErrors(t *testing.T) {
	if err := Clone(filepath.Join(t.TempDir(), "out"), filepath.Join(t.TempDir(), "nope"), Copy); err == nil {
		t.Fatal("clone of a missing source should error")
	}
}

func TestCloneReflinkUnsupportedErrors(t *testing.T) {
	dir := t.TempDir()
	if ReflinkProbe(dir) {
		t.Skip("reflink supported here — the unsupported-error path is covered on non-reflink filesystems")
	}
	src := buildSrcTree(t)
	if err := Clone(filepath.Join(t.TempDir(), "out"), src, Reflink); err == nil {
		t.Fatal("reflink Clone on a non-reflink fs should error (no silent fallback)")
	}
}

func TestStrategyString(t *testing.T) {
	for s, want := range map[Strategy]string{Reflink: "reflink", Hardlink: "hardlink", Copy: "copy", Strategy(99): "unknown"} {
		if got := s.String(); got != want {
			t.Errorf("Strategy(%d).String() = %q, want %q", s, got, want)
		}
	}
}

func TestMutatesInPlace(t *testing.T) {
	inPlace := []string{
		"Manifest.db", "Manifest.db-wal", "Manifest.db-shm", "Status.plist", "Info.plist",
		"Manifest.plist", "foo.sqlite", "foo.sqlite-wal", "x/y/bar.db",
	}
	linkable := []string{
		"ab/ab00cafe", "cd/deadbeef", "ff/0011223344", "Snapshot/somefile",
	}
	for _, p := range inPlace {
		if !MutatesInPlace(p) {
			t.Errorf("MutatesInPlace(%q) = false, want true", p)
		}
	}
	for _, p := range linkable {
		if MutatesInPlace(p) {
			t.Errorf("MutatesInPlace(%q) = true, want false", p)
		}
	}
}
