package messages_test

import (
	"testing"

	"github.com/novkostya/ios-backup-crypt/fixture"
	parser "github.com/novkostya/ios-backup-parser/messages"

	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/vault/messages"
	"github.com/novkostya/quince/core/internal/vault/messages/msgfixture"
	"github.com/novkostya/quince/core/internal/vault/parserfs"
)

// countingReader builds a Reader over a CountingFS, so a test can see exactly which files the
// projection scan asks to materialize.
func countingReader(t *testing.T, spec msgfixture.Spec) (*messages.Reader, *msgfixture.CountingFS) {
	t.Helper()
	data, err := msgfixture.Build(t.TempDir(), spec)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	dir := t.TempDir()
	if _, err := fixture.Build(dir, fixture.Spec{Unencrypted: true, Files: []fixture.File{
		{Domain: msgfixture.Domain, RelativePath: msgfixture.RelativePath, Flags: 1, Data: data},
	}}); err != nil {
		t.Fatalf("backup: %v", err)
	}
	v, err := vault.OpenUnencrypted(dir)
	if err != nil {
		t.Fatalf("vault: %v", err)
	}
	t.Cleanup(func() { _ = v.Close() })
	if _, err := v.Unlock(t.Context(), ""); err != nil {
		t.Fatalf("unlock: %v", err)
	}
	scratch := t.TempDir()
	inner, err := parserfs.New(v, scratch)
	if err != nil {
		t.Fatalf("parserfs: %v", err)
	}
	counting := msgfixture.NewCountingFS(inner)
	r, err := messages.New(counting, scratch)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r, counting
}

// THE PROPERTY qn.10 D2b's SAFETY RESTS ON, CHECKED RATHER THAN DOCUMENTED.
//
// D2b holds the vault only long enough to materialize the Messages database and runs the scan
// outside the session lock. That is safe because parserfs memoises — but the memo only helps
// for the key that was pre-materialized. A scan that asks for ANY other file misses the memo,
// reaches the vault outside registry.With, and races whatever else holds that session.
//
// A race need not produce an error, so "the build ran and nothing failed" is not evidence.
// What has to be asserted is the key set, and that is what this does.
func TestScanMaterializesNothingBeyondThePreMaterializedFile(t *testing.T) {
	r, counting := countingReader(t, msgfixture.Spec{Messages: 40})
	want := msgfixture.Key{Domain: parser.Domain, RelativePath: parser.RelativePath}

	// Phase 1 — what the service does under the session lock.
	if _, err := counting.Materialize(want.Domain, want.RelativePath); err != nil {
		t.Fatalf("pre-materialize: %v", err)
	}

	// Forget it, so what follows measures the SCAN alone.
	counting.Reset()

	// Phase 2 — the build, which the service runs OUTSIDE the lock.
	if _, err := r.Thread(t.Context(), 1, "", 10, nil); err != nil {
		t.Fatalf("Thread: %v", err)
	}

	// THE CONTROL. If the scan asked for nothing at all, the assertion below would pass
	// vacuously — and it would also mean this test is not exercising the path it names.
	keys := counting.Keys()
	if len(keys) == 0 {
		t.Fatal("control failed: the scan materialized nothing, so this test proves nothing")
	}

	if off := counting.OffKey(want); len(off) != 0 {
		t.Errorf("the scan materialized %d file(s) beyond the pre-materialized one: %v\n"+
			"each of those misses parserfs's memo, reaches the vault OUTSIDE registry.With, "+
			"and races any other request on this session (qn.10 D2b)", len(off), off)
	}
}

// The guard must be able to FAIL, or it is decoration. An off-key materialize is exactly what
// a later slice would introduce — slice 5 joins attachments, which live under MediaDomain —
// so this asserts the detector sees one.
func TestTheGuardDetectsAnOffKeyMaterialize(t *testing.T) {
	_, counting := countingReader(t, msgfixture.Spec{})
	want := msgfixture.Key{Domain: parser.Domain, RelativePath: parser.RelativePath}

	if _, err := counting.Materialize(want.Domain, want.RelativePath); err != nil {
		t.Fatalf("pre-materialize: %v", err)
	}
	counting.Reset()

	// Stand in for what a future attachment join would do. The error is irrelevant — the
	// file need not exist for the REQUEST to have been made, and it is the request that
	// reaches the vault.
	_, _ = counting.Materialize("MediaDomain", "Library/SMS/Attachments/aa/00/invented.heic")

	off := counting.OffKey(want)
	if len(off) != 1 {
		t.Fatalf("guard saw %d off-key materialize(s), want 1 — it cannot detect the thing it exists for", len(off))
	}
	if off[0].Domain != "MediaDomain" {
		t.Errorf("off-key = %+v, want the MediaDomain request", off[0])
	}
}
