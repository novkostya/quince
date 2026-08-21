package vaultsvc

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/wire"
)

type stubVersions struct {
	v   wire.Version
	ok  bool
	err error
}

func (s stubVersions) Version(string) (wire.Version, bool, error) { return s.v, s.ok, s.err }

// fakeVault is enough to drive the service: it lists one entry and streams content, and can
// be made to end a read with ErrIncompleteFile.
type fakeVault struct {
	incomplete bool
}

func (f *fakeVault) Unlock(context.Context, string) (vault.Info, error) {
	return vault.Info{DeviceName: "d", FileCount: 1, Encrypted: true}, nil
}
func (f *fakeVault) List(context.Context, vault.Query) (vault.Page, error) {
	return vault.Page{Entries: []vault.FileEntry{
		{FileID: "f1", Domain: "HomeDomain", RelativePath: "a", Kind: vault.KindFile, Size: 5},
	}}, nil
}
func (f *fakeVault) Stat(context.Context, string) (vault.FileEntry, error) {
	return vault.FileEntry{FileID: "f1", Kind: vault.KindFile, Size: 5}, nil
}
func (f *fakeVault) Open(context.Context, string) (io.ReadCloser, error) {
	if f.incomplete {
		return &shortReader{}, nil
	}
	return io.NopCloser(strings.NewReader("alpha")), nil
}
func (f *fakeVault) VerifyCanary(context.Context) error { return nil }
func (f *fakeVault) Close() error                       { return nil }

// shortReader delivers some bytes and then ends in ErrIncompleteFile — the shape a file
// captured mid-write produces.
type shortReader struct{ done bool }

func (s *shortReader) Read(p []byte) (int, error) {
	if s.done {
		return 0, vault.ErrIncompleteFile
	}
	s.done = true
	return copy(p, "al"), nil
}
func (s *shortReader) Close() error { return nil }

func newService(t *testing.T, v wire.Version, ok bool, fv *fakeVault) *Service {
	t.Helper()
	s, err := New(stubVersions{v: v, ok: ok}, filepath.Join(t.TempDir(), "scratch"),
		time.Hour, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.open = func(wire.Version, string) (vault.Vault, error) { return fv, nil }
	t.Cleanup(func() { _ = s.Registry().CloseAll() })
	return s
}

func encrypted() wire.Version {
	return wire.Version{ID: "01V", Encrypted: true, BrowseRoot: "/backups/x/latest"}
}

func TestUnlockAndBrowse(t *testing.T) {
	s := newService(t, encrypted(), true, &fakeVault{})

	sess, code, msg := s.Unlock("01V", "pw")
	if code != "" {
		t.Fatalf("Unlock: %s — %s", code, msg)
	}
	if sess.ID == "" || sess.VersionID != "01V" || sess.ExpiresAt == "" {
		t.Fatalf("session = %+v, want id/version/expiry", sess)
	}

	page, code, msg := s.Browse(sess.ID, wire.BrowseQuery{})
	if code != "" {
		t.Fatalf("Browse: %s — %s", code, msg)
	}
	if len(page.Entries) != 1 || page.Entries[0].FileID != "f1" {
		t.Fatalf("page = %+v", page)
	}
	if page.Entries[0].Mtime != "" {
		t.Errorf("mtime = %q, want empty for a record with no LastModified", page.Entries[0].Mtime)
	}
}

// D7's refusal, until slice 4: an unencrypted version is refused WITH THE REASON AND THE
// REMEDY, not with "unsupported". The troubleshooting rule is what this asserts.
func TestUnencryptedVersionIsRefusedWithAReason(t *testing.T) {
	v := encrypted()
	v.Encrypted = false
	s := newService(t, v, true, &fakeVault{})
	s.open = openFor // the real selection, which is what is under test

	_, code, msg := s.Unlock("01V", "")
	if code != "unsupported_version" {
		t.Fatalf("code = %q, want unsupported_version", code)
	}
	for _, want := range []string{"not encrypted", "ios-backup-crypt#8", "Encrypted versions"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message does not mention %q: %s", want, msg)
		}
	}
}

// An empty browse_root is storage's own cue that a version's content cannot be served. Using
// it beats inventing a second signal, and falling through would hand a session the live tree.
func TestAVersionWithNoBrowsableContentIsNotFound(t *testing.T) {
	v := encrypted()
	v.BrowseRoot = ""
	s := newService(t, v, true, &fakeVault{})

	_, code, msg := s.Unlock("01V", "pw")
	if code != "not_found" {
		t.Fatalf("code = %q, want not_found", code)
	}
	if !strings.Contains(msg, "snapshot") {
		t.Errorf("message does not say why: %s", msg)
	}
}

func TestUnknownVersionIsNotFound(t *testing.T) {
	s := newService(t, wire.Version{}, false, &fakeVault{})
	if _, code, _ := s.Unlock("nope", "pw"); code != "not_found" {
		t.Errorf("code = %q, want not_found", code)
	}
}

// A registry that cannot answer is NOT the same as a version that does not exist — 500 rather
// than 404, and the seam keeps them apart.
func TestARegistryFailureIsIONotNotFound(t *testing.T) {
	s, err := New(stubVersions{err: errors.New("db is down")},
		filepath.Join(t.TempDir(), "scratch"), time.Hour, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Registry().CloseAll() })
	if _, code, _ := s.Unlock("01V", "pw"); code != "io" {
		t.Errorf("code = %q, want io", code)
	}
}

// Lock is idempotent all the way down, and an unknown session is not an error.
func TestLockIsIdempotent(t *testing.T) {
	s := newService(t, encrypted(), true, &fakeVault{})
	sess, _, _ := s.Unlock("01V", "pw")
	for _, id := range []string{sess.ID, sess.ID, "never-existed"} {
		if code, msg := s.Lock(id); code != "" {
			t.Errorf("Lock(%s) = %s — %s", id, code, msg)
		}
	}
}

func TestBrowsingAGoneSessionIsLocked(t *testing.T) {
	s := newService(t, encrypted(), true, &fakeVault{})
	if _, code, _ := s.Browse("nope", wire.BrowseQuery{}); code != "locked" {
		t.Errorf("code = %q, want locked", code)
	}
}

// D8.1a end to end: a short read is remembered on the session and the NEXT browse marks that
// entry — the surface the spec requires, and the one that would silently never appear if
// incompleteness travelled through the error code.
func TestAShortReadMarksTheEntryOnTheNextBrowse(t *testing.T) {
	s := newService(t, encrypted(), true, &fakeVault{incomplete: true})
	sess, _, _ := s.Unlock("01V", "pw")

	// Before reading, nothing is known to be incomplete.
	page, _, _ := s.Browse(sess.ID, wire.BrowseQuery{})
	if page.Entries[0].Incomplete {
		t.Fatal("an entry nobody has read is marked incomplete")
	}

	rc, _, code, msg := s.OpenFile(sess.ID, "f1")
	if code != "" {
		t.Fatalf("OpenFile: %s — %s", code, msg)
	}
	_, readErr := io.ReadAll(rc)
	_ = rc.Close()
	if !errors.Is(readErr, vault.ErrIncompleteFile) {
		t.Fatalf("read error = %v, want ErrIncompleteFile", readErr)
	}

	page, _, _ = s.Browse(sess.ID, wire.BrowseQuery{})
	if !page.Entries[0].Incomplete {
		t.Error("the entry is not marked incomplete after a short read — D8.1a's surface never fires")
	}
}

// A whole file is never marked, which is the control: the test above would pass against an
// implementation that marked everything.
func TestAWholeReadMarksNothing(t *testing.T) {
	s := newService(t, encrypted(), true, &fakeVault{})
	sess, _, _ := s.Unlock("01V", "pw")

	rc, _, _, _ := s.OpenFile(sess.ID, "f1")
	if _, err := io.ReadAll(rc); err != nil {
		t.Fatalf("reading: %v", err)
	}
	_ = rc.Close()

	page, _, _ := s.Browse(sess.ID, wire.BrowseQuery{})
	if page.Entries[0].Incomplete {
		t.Error("a whole file was marked incomplete")
	}
}

// The stream holds the session, so a browse during a download is refused rather than racing
// it. Asserted here because this is the layer where the two meet.
func TestABrowseDuringADownloadIsRefused(t *testing.T) {
	s := newService(t, encrypted(), true, &fakeVault{})
	sess, _, _ := s.Unlock("01V", "pw")

	rc, _, code, _ := s.OpenFile(sess.ID, "f1")
	if code != "" {
		t.Fatalf("OpenFile: %s", code)
	}
	defer func() { _ = rc.Close() }()

	if _, code, _ := s.Browse(sess.ID, wire.BrowseQuery{}); code != "busy" {
		t.Errorf("browse during a stream = %q, want busy", code)
	}
}
