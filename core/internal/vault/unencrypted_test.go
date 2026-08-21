package vault

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/ios-backup-crypt/fixture"
)

// prefixEnd decides the upper bound of a prefix query, and it is the one piece of this
// implementation the conformance suite never reaches: that suite filters by DOMAIN, so the
// prefix path went in at 0% covered. Tested directly because the interesting cases are
// byte-level and awkward to reach through a backup.
func TestPrefixEnd(t *testing.T) {
	for _, tc := range []struct {
		name, in, want string
		ok             bool
	}{
		{"ordinary", "Library/", "Library0", true}, // '/' is 0x2f, so the successor is 0x30
		{"single byte", "a", "b", true},
		{"trailing 0xff rolls to the byte before it", "a\xff", "b", true},
		{"all 0xff has no upper bound", "\xff\xff", "", false},
		{"empty has no upper bound", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := prefixEnd(tc.in)
			if ok != tc.ok {
				t.Fatalf("prefixEnd(%q) ok = %v, want %v", tc.in, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("prefixEnd(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// AND THE BOUND IS ACTUALLY APPLIED — a correct prefixEnd wired to nothing would still pass
// the test above. This drives a real unencrypted backup and asserts the query both INCLUDES
// what shares the prefix and EXCLUDES what merely sorts after it, which is the half a
// `relativePath >= prefix` with no upper bound would get wrong.
func TestUnencryptedPrefixFilterIsBounded(t *testing.T) {
	dir := t.TempDir()
	if _, err := fixture.Build(dir, fixture.Spec{
		Unencrypted: true,
		Files: []fixture.File{
			{Domain: "HomeDomain", RelativePath: "Library/Preferences/a.plist", Flags: 1, Data: []byte("a")},
			{Domain: "HomeDomain", RelativePath: "Library/Preferences/b.plist", Flags: 1, Data: []byte("b")},
			// Sorts AFTER "Library/" and does not share it. Without an upper bound this
			// would be returned too.
			{Domain: "HomeDomain", RelativePath: "Media/photo.heic", Flags: 1, Data: []byte("m")},
		},
	}); err != nil {
		t.Fatalf("fixture: %v", err)
	}

	v, err := OpenUnencrypted(dir)
	if err != nil {
		t.Fatalf("OpenUnencrypted: %v", err)
	}
	defer func() { _ = v.Close() }()
	if _, err := v.Unlock(context.Background(), ""); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	page, err := v.List(context.Background(), Query{Prefix: "Library/"})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Entries) != 2 {
		t.Fatalf("entries = %d, want 2 — got %+v", len(page.Entries), page.Entries)
	}
	for _, e := range page.Entries {
		if e.RelativePath == "Media/photo.heic" {
			t.Error("the prefix query returned a path that only sorts after the prefix; the " +
				"upper bound is not applied")
		}
	}
}

// A SHORT BLOB IS ErrIncompleteFile AFTER THE LAST BYTE, NEVER INSTEAD OF IT — measured to
// happen on real backups (~70 files per version, quince#1379), so this is the behaviour of a
// condition that occurs rather than a defensive branch. The bytes the backup DOES hold are
// delivered first; only then does the read report that it came up short.
func TestUnencryptedShortBlobDeliversWhatThereIsThenReportsIncomplete(t *testing.T) {
	dir := t.TempDir()
	res, err := fixture.Build(dir, fixture.Spec{
		Unencrypted: true,
		Files: []fixture.File{
			{Domain: "HomeDomain", RelativePath: "Library/big.bin", Flags: 1, Data: []byte("0123456789")},
		},
	})
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}

	// Truncate the blob behind the record's back: the manifest still says ten bytes.
	id := res.Files[0].FileID
	if err := truncate(filepath.Join(dir, id[:2], id), 4); err != nil {
		t.Fatalf("truncating the blob: %v", err)
	}

	v, err := OpenUnencrypted(dir)
	if err != nil {
		t.Fatalf("OpenUnencrypted: %v", err)
	}
	defer func() { _ = v.Close() }()
	if _, err := v.Unlock(context.Background(), ""); err != nil {
		t.Fatalf("Unlock: %v", err)
	}

	rc, err := v.Open(context.Background(), id)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()

	got, err := io.ReadAll(rc)
	if string(got) != "0123" {
		t.Errorf("delivered %q, want %q — every byte the backup holds must arrive", got, "0123")
	}
	if Code(err) != "" {
		t.Errorf("Code(err) = %q, want \"\": an incomplete file is a SUCCESSFUL read of an "+
			"incomplete artifact and travels as a field, not a code", Code(err))
	}
	if err == nil {
		t.Error("no error at all: the caller cannot tell the read came up short")
	}
}

func truncate(path string, size int64) error { return os.Truncate(path, size) }
