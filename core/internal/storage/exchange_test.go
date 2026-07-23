package storage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExchangeSwapsAtomically is story 1: the exchange primitive swaps two directories' contents
// in one call (renameat2 RENAME_EXCHANGE) — the atomic-`latest` foundation. It also DOUBLES as the
// in-CI proof that the test filesystem supports RENAME_EXCHANGE (the "test the layer you'll run in"
// lesson): if this fails on the CI tmpdir, every commit test would too.
func TestExchangeSwapsAtomically(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	b := filepath.Join(root, "b")
	mustMkdir(t, a)
	mustMkdir(t, b)
	mustWrite(t, filepath.Join(a, "marker"), "A")
	mustWrite(t, filepath.Join(b, "marker"), "B")

	if err := exchange(a, b); err != nil {
		t.Fatalf("exchange: %v (does the test filesystem support RENAME_EXCHANGE?)", err)
	}

	if got := mustRead(t, filepath.Join(a, "marker")); got != "B" {
		t.Fatalf("after exchange a/marker = %q, want B", got)
	}
	if got := mustRead(t, filepath.Join(b, "marker")); got != "A" {
		t.Fatalf("after exchange b/marker = %q, want A", got)
	}
}

// TestExchangeMissingPathErrors: the operation on a missing path errors, never partially applies.
func TestExchangeMissingPathErrors(t *testing.T) {
	root := t.TempDir()
	a := filepath.Join(root, "a")
	mustMkdir(t, a)
	if err := exchange(a, filepath.Join(root, "does-not-exist")); err == nil {
		t.Fatal("exchange with a missing path should error")
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", p, err)
	}
}

func mustWrite(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read %s: %v", p, err)
	}
	return string(b)
}
