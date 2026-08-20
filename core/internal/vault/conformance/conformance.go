// Package conformance is the golden suite that canon names as the shipping gate for any
// `vault.Vault` implementation — quince#184, open since the suite was first named in five
// places across `contracts.md`, `quince.design.md` and `quince.stack.md`.
//
// IT DRIVES THE INTERFACE, NOT AN IMPLEMENTATION. `contracts.md` §4 says the core "talks to
// a vault.Vault Go interface; any implementation of it, in-process or over this RPC, must
// pass the golden conformance suite". So the suite takes a factory and knows nothing else:
// the in-process implementation passes it today, an RPC one passes the same file whenever
// the spike's number stops arguing for in-process (qn.8 spec D10.5).
//
// WHAT IT DOES NOT COVER, declared rather than implied. RPC FRAMING. The seam is the
// interface, so a wire implementation's marshalling, its `materialize`/`handle` round trip
// and its crash semantics are not exercised by anything here. That is not an oversight to
// fix later by adding cases — it is a consequence of where the seam is, and an RPC
// implementation owes its own framing tests on top of passing this.
//
// AND IT IS AN INSTRUMENT ONLY IF IT CAN FAIL. `Run` is paired with `RunMutantMustFail` in
// the package's own test: an all-pass from a suite nobody has seen reject anything proves
// that the suite ran, not that the implementation is right.
package conformance

import (
	"context"
	"errors"
	"io"

	"github.com/novkostya/quince/core/internal/vault"
)

// Fixture is one backup a suite run is given, plus what it takes to open it. The suite
// never builds a backup itself: what a fixture IS differs per implementation — a directory
// here, possibly a spawned process elsewhere — and a suite that constructed its own input
// would be testing the constructor.
type Fixture struct {
	// Open returns a locked Vault over the backup.
	Open func(t T) vault.Vault
	// Password unlocks it. Empty means the implementation takes none (spec D7).
	Password string
	// Encrypted is what Info.Encrypted must report.
	Encrypted bool

	// The golden facts about this backup's content, supplied by whoever built it.
	FileCount int64
	// Entries is every row the backup holds, in the seam's stable (domain, relativePath)
	// order. The suite asserts a full walk reproduces exactly this.
	Entries []vault.FileEntry
	// FileContent maps a file id to the bytes Open must return for it. It need not cover
	// every entry; it covers the ones worth asserting.
	FileContent map[string][]byte
	// ADirectory is the file id of an entry with no content, or empty if the backup has
	// none. Used for the not_a_file case, which must not collapse into not_found.
	ADirectory string
}

// Run executes the whole suite against one fixture. It is the gate.
func Run(t T, f Fixture) {
	t.Helper()
	// Sequential rather than sub-tests: each check runs under `guard`, so a Fatalf aborts
	// that check alone and the run continues. Sub-tests would tie the suite to *testing.T
	// and take its own control away (see reporter.go).
	guard(func() { testUnlock(t, f) })
	guard(func() { testUnlockIdempotent(t, f) })
	guard(func() { testLockedBeforeUnlock(t, f) })
	guard(func() { testLockedAfterClose(t, f) })
	guard(func() { testCloseIdempotent(t, f) })
	guard(func() { testFullWalk(t, f) })
	guard(func() { testPagingTotality(t, f) })
	guard(func() { testLimitClamp(t, f) })
	guard(func() { testGarbageCursor(t, f) })
	guard(func() { testStatAgreesWithList(t, f) })
	guard(func() { testStatUnknown(t, f) })
	guard(func() { testOpenContent(t, f) })
	guard(func() { testOpenDirectory(t, f) })
	guard(func() { testOpenUnknown(t, f) })
	guard(func() { testCanary(t, f) })
	guard(func() { testFilters(t, f) })
}

func unlocked(t T, f Fixture) vault.Vault {
	t.Helper()
	v := f.Open(t)
	if _, err := v.Unlock(context.Background(), f.Password); err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	return v
}

func testUnlock(t T, f Fixture) {
	v := f.Open(t)
	defer func() { _ = v.Close() }()

	info, err := v.Unlock(context.Background(), f.Password)
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if info.FileCount != f.FileCount {
		t.Errorf("FileCount = %d, want %d", info.FileCount, f.FileCount)
	}
	if info.Encrypted != f.Encrypted {
		t.Errorf("Encrypted = %v, want %v", info.Encrypted, f.Encrypted)
	}
	// ManifestSHA256 identifies the version. Not compared to a golden value — that would
	// pin the fixture builder's output rather than the seam — but it must be there and it
	// must be a hash rather than a placeholder.
	if len(info.ManifestSHA256) != 64 {
		t.Errorf("ManifestSHA256 = %q, want 64 hex characters", info.ManifestSHA256)
	}
}

func testUnlockIdempotent(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	first, err := v.Unlock(context.Background(), f.Password)
	if err != nil {
		t.Fatalf("second Unlock: %v", err)
	}
	second, err := v.Unlock(context.Background(), f.Password)
	if err != nil {
		t.Fatalf("third Unlock: %v", err)
	}
	if first != second {
		t.Errorf("Unlock is not idempotent: %+v then %+v", first, second)
	}
}

func testLockedBeforeUnlock(t T, f Fixture) {
	v := f.Open(t)
	defer func() { _ = v.Close() }()
	if _, err := v.List(context.Background(), vault.Query{}); !errors.Is(err, vault.ErrLocked) {
		t.Errorf("List before Unlock = %v, want ErrLocked", err)
	}
	if _, err := v.Stat(context.Background(), "whatever"); !errors.Is(err, vault.ErrLocked) {
		t.Errorf("Stat before Unlock = %v, want ErrLocked", err)
	}
	if _, err := v.Open(context.Background(), "whatever"); !errors.Is(err, vault.ErrLocked) {
		t.Errorf("Open before Unlock = %v, want ErrLocked", err)
	}
	if err := v.VerifyCanary(context.Background()); !errors.Is(err, vault.ErrLocked) {
		t.Errorf("VerifyCanary before Unlock = %v, want ErrLocked", err)
	}
}

func testLockedAfterClose(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	if err := v.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := v.List(context.Background(), vault.Query{}); !errors.Is(err, vault.ErrLocked) {
		t.Errorf("List after Close = %v, want ErrLocked", err)
	}
	if _, err := v.Unlock(context.Background(), f.Password); !errors.Is(err, vault.ErrLocked) {
		t.Errorf("Unlock after Close = %v, want ErrLocked", err)
	}
}

func testCloseIdempotent(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	if err := v.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := v.Close(); err != nil {
		t.Errorf("second Close = %v, want nil", err)
	}
}

// walkAll pages through everything at the given limit and returns the entries.
func walkAll(t T, v vault.Vault, q vault.Query, limit int) []vault.FileEntry {
	t.Helper()
	var all []vault.FileEntry
	q.Limit = limit
	for {
		page, err := v.List(context.Background(), q)
		if err != nil {
			t.Fatalf("List(limit=%d, cursor=%q): %v", limit, q.Cursor, err)
		}
		all = append(all, page.Entries...)
		if page.NextCursor == "" {
			return all
		}
		if len(page.Entries) == 0 {
			t.Fatalf("List returned a cursor with no entries — a walk that cannot terminate")
		}
		q.Cursor = page.NextCursor
	}
}

func testFullWalk(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	got := walkAll(t, v, vault.Query{}, len(f.Entries)+10)
	assertEntries(t, got, f.Entries)
}

// testPagingTotality is the property that matters most about a cursor: paging at ANY size
// must yield exactly the full walk, once each, in the same order. A cursor that skips or
// repeats an entry loses a file from a browser without failing anything.
func testPagingTotality(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	want := walkAll(t, v, vault.Query{}, len(f.Entries)+10)

	for _, limit := range []int{1, 2, 3, 5, len(f.Entries) - 1, len(f.Entries)} {
		if limit < 1 {
			continue
		}
		got := walkAll(t, v, vault.Query{}, limit)
		if len(got) != len(want) {
			t.Errorf("limit=%d yielded %d entries, want %d", limit, len(got), len(want))
			continue
		}
		for i := range got {
			if got[i].FileID != want[i].FileID {
				t.Errorf("limit=%d entry %d = %s, want %s", limit, i, got[i].FileID, want[i].FileID)
				break
			}
		}
	}
}

func testLimitClamp(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	page, err := v.List(context.Background(), vault.Query{Limit: vault.MaxLimit + 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.EffectiveLimit != vault.MaxLimit {
		t.Errorf("EffectiveLimit = %d, want %d — a clamp must be disclosed", page.EffectiveLimit, vault.MaxLimit)
	}

	page, err = v.List(context.Background(), vault.Query{Limit: 1})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if page.EffectiveLimit != 0 {
		t.Errorf("EffectiveLimit = %d for an unclamped request, want 0", page.EffectiveLimit)
	}
}

func testGarbageCursor(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	if _, err := v.List(context.Background(), vault.Query{Cursor: "not-a-cursor!!"}); err == nil {
		t.Errorf("a malformed cursor was accepted; it must be refused rather than read as the start")
	}
}

func testStatAgreesWithList(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	for _, want := range f.Entries {
		got, err := v.Stat(context.Background(), want.FileID)
		if err != nil {
			t.Errorf("Stat(%s): %v", want.FileID, err)
			continue
		}
		if got.FileID != want.FileID || got.Domain != want.Domain ||
			got.RelativePath != want.RelativePath || got.Kind != want.Kind || got.Size != want.Size {
			t.Errorf("Stat(%s) = %+v, want %+v", want.FileID, got, want)
		}
	}
}

func testStatUnknown(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	_, err := v.Stat(context.Background(), "0000000000000000000000000000000000000000")
	if !errors.Is(err, vault.ErrFileNotFound) {
		t.Errorf("Stat(unknown) = %v, want ErrFileNotFound", err)
	}
	if code := vault.Code(err); code != "not_found" {
		t.Errorf("Code = %q, want not_found", code)
	}
}

func testOpenContent(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	for fileID, want := range f.FileContent {
		rc, err := v.Open(context.Background(), fileID)
		if err != nil {
			t.Errorf("Open(%s): %v", fileID, err)
			continue
		}
		got, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Errorf("reading %s: %v", fileID, err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("Open(%s) returned %d bytes, want %d", fileID, len(got), len(want))
		}
	}
}

// testOpenDirectory is the case D8 exists for: an entry that EXISTS and has no content is
// not_a_file, never not_found. The two have different remedies, and collapsing them is a
// defect even when every word of the message is true.
func testOpenDirectory(t T, f Fixture) {
	if f.ADirectory == "" {
		t.Skipf("fixture has no directory entry")
	}
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	_, err := v.Open(context.Background(), f.ADirectory)
	if !errors.Is(err, vault.ErrNotAFile) {
		t.Fatalf("Open(directory) = %v, want ErrNotAFile", err)
	}
	if errors.Is(err, vault.ErrFileNotFound) {
		t.Errorf("Open(directory) also satisfies ErrFileNotFound; the two must stay distinguishable")
	}
	if code := vault.Code(err); code != "not_a_file" {
		t.Errorf("Code = %q, want not_a_file", code)
	}
}

func testOpenUnknown(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	_, err := v.Open(context.Background(), "0000000000000000000000000000000000000000")
	if !errors.Is(err, vault.ErrFileNotFound) {
		t.Errorf("Open(unknown) = %v, want ErrFileNotFound", err)
	}
}

func testCanary(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	if err := v.VerifyCanary(context.Background()); err != nil {
		t.Errorf("VerifyCanary: %v", err)
	}
}

func testFilters(t T, f Fixture) {
	v := unlocked(t, f)
	defer func() { _ = v.Close() }()
	if len(f.Entries) == 0 {
		t.Skipf("fixture has no entries")
	}
	domain := f.Entries[0].Domain

	got := walkAll(t, v, vault.Query{Domain: domain}, len(f.Entries)+10)
	for _, e := range got {
		if e.Domain != domain {
			t.Errorf("domain filter %q returned an entry in %q", domain, e.Domain)
		}
	}
	var want int
	for _, e := range f.Entries {
		if e.Domain == domain {
			want++
		}
	}
	if len(got) != want {
		t.Errorf("domain filter returned %d entries, want %d", len(got), want)
	}
}

func assertEntries(t T, got, want []vault.FileEntry) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("walk yielded %d entries, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i].FileID != want[i].FileID {
			t.Errorf("entry %d = %s (%s/%s), want %s (%s/%s)", i,
				got[i].FileID, got[i].Domain, got[i].RelativePath,
				want[i].FileID, want[i].Domain, want[i].RelativePath)
			continue
		}
		if got[i].Kind != want[i].Kind || got[i].Size != want[i].Size {
			t.Errorf("entry %s = kind %s size %d, want kind %s size %d",
				got[i].FileID, got[i].Kind, got[i].Size, want[i].Kind, want[i].Size)
		}
		if !want[i].MTime.IsZero() && !got[i].MTime.Equal(want[i].MTime) {
			t.Errorf("entry %s MTime = %v, want %v", got[i].FileID, got[i].MTime, want[i].MTime)
		}
	}
}
