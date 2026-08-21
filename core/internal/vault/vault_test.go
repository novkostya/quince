package vault

import (
	"errors"
	"fmt"
	"testing"

	"github.com/novkostya/quince/core/internal/wire"
)

// The taxonomy must be TOTAL over the errors this package names, and every code it can
// produce must be one contracts.md §4 freezes. A future error added without a mapping
// falls through to `io` silently, which is the collapsed-diagnostic defect one layer down
// — so the enumeration is asserted rather than trusted.
func TestCodeIsTotalOverTheNamedErrors(t *testing.T) {
	frozen := map[string]bool{
		"bad_password":     true,
		"corrupt_manifest": true,
		"io":               true,
		"not_found":        true,
		"unsupported_ios":  true, // frozen, and deliberately unused by qn.8 — see the spec's D8
		"not_a_file":       true, // added by qn.8
		"locked":           true, // added by qn.8
	}

	named := map[error]string{
		ErrBadPassword:     "bad_password",
		ErrCorruptManifest: "corrupt_manifest",
		ErrFileNotFound:    "not_found",
		ErrNoCanary:        "not_found",
		ErrNotAFile:        "not_a_file",
		ErrLocked:          "locked",
		ErrIncompleteFile:  "", // NOT a failure: see Code's doc comment
	}

	for err, want := range named {
		got := Code(err)
		if got != want {
			t.Errorf("Code(%v) = %q, want %q", err, got, want)
		}
		if want != "" && !frozen[want] {
			t.Errorf("Code(%v) = %q, which is not in the frozen contracts.md §4 set", err, want)
		}
	}
}

// A wrapped error keeps its code — handlers wrap for context and must not lose the
// classification by doing so.
func TestCodeSeesThroughWrapping(t *testing.T) {
	wrapped := fmt.Errorf("reading version 01J: %w", ErrNotAFile)
	if got := Code(wrapped); got != "not_a_file" {
		t.Errorf("Code(wrapped) = %q, want %q", got, "not_a_file")
	}
}

// An error the seam does not name is `io` — honest about "something below us failed", and
// never a guess at a more specific cause.
func TestUnknownErrorIsIO(t *testing.T) {
	if got := Code(errors.New("disk fell over")); got != "io" {
		t.Errorf("Code(unknown) = %q, want %q", got, "io")
	}
	if got := Code(nil); got != "" {
		t.Errorf("Code(nil) = %q, want empty", got)
	}
}

// A directory is not a missing file. Asserted as its own test because collapsing these two
// is the specific defect D8 exists to prevent, and a shared table would let one of them
// change without anybody noticing which.
func TestNotAFileIsNotNotFound(t *testing.T) {
	if Code(ErrNotAFile) == Code(ErrFileNotFound) {
		t.Error("ErrNotAFile and ErrFileNotFound answer the same code; they have different remedies")
	}
	if errors.Is(ErrNotAFile, ErrFileNotFound) {
		t.Error("ErrNotAFile must not satisfy errors.Is(ErrFileNotFound)")
	}
}

func TestClampLimitDisclosesTheClamp(t *testing.T) {
	for _, tc := range []struct {
		requested   int
		wantEffect  int
		wantClamped bool
	}{
		{0, DefaultLimit, false},
		{-1, DefaultLimit, false},
		{1, 1, false},
		{DefaultLimit, DefaultLimit, false},
		{MaxLimit, MaxLimit, false},
		{MaxLimit + 1, MaxLimit, true},
		{1 << 20, MaxLimit, true},
	} {
		got, clamped := ClampLimit(tc.requested)
		if got != tc.wantEffect || clamped != tc.wantClamped {
			t.Errorf("ClampLimit(%d) = (%d, %v), want (%d, %v)",
				tc.requested, got, clamped, tc.wantEffect, tc.wantClamped)
		}
	}
}

// The cursor round-trips, and an empty one is the start of the sequence rather than an
// error — that is what a first request sends.
func TestCursorRoundTripsAndEmptyIsTheStart(t *testing.T) {
	in := cursor{Domain: "CameraRollDomain", Path: "Media/DCIM/100APPLE/IMG_0001.HEIC"}
	out, err := decodeCursor(encodeCursor(in))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out != in {
		t.Errorf("round trip = %+v, want %+v", out, in)
	}

	empty, err := decodeCursor("")
	if err != nil {
		t.Errorf("empty cursor should not be an error, got %v", err)
	}
	if empty != (cursor{}) {
		t.Errorf("empty cursor = %+v, want the zero value", empty)
	}
}

// A cursor a client invented is refused rather than read as the start of the sequence,
// which would silently serve page one to somebody who asked for page nine.
func TestGarbageCursorIsRefusedNotTreatedAsTheStart(t *testing.T) {
	for _, bad := range []string{"not-base64!!", "YWJj", "eyJk"} {
		if _, err := decodeCursor(bad); err == nil {
			t.Errorf("decodeCursor(%q) succeeded; a malformed cursor must be refused", bad)
		}
	}
}

// The seam's frozen set and the wire vocabulary must be ONE list, not two that agree today.
//
// `frozen` above and wire.VaultErrorCodes are both enumerations of contracts §4, written in
// different packages a rung apart — the shape that produced quince#1375 one layer up. This
// is a TEST-ONLY reference: the seam still imports no wire types in production code, because
// it is also the RPC contract and must not learn quince's JSON shapes to stay that.
func TestTheFrozenSetIsTheWireVocabulary(t *testing.T) {
	inWire := map[string]bool{}
	for _, c := range wire.VaultErrorCodes {
		inWire[c] = true
	}
	for _, err := range []error{
		ErrBadPassword, ErrCorruptManifest, ErrFileNotFound, ErrNoCanary, ErrNotAFile, ErrLocked,
	} {
		code := Code(err)
		if !inWire[code] {
			t.Errorf("Code(%v) = %q, which wire.VaultErrorCodes does not name — the HTTP status "+
				"table cannot be total over a code it has never heard of", err, code)
		}
	}
	if !inWire[Code(errors.New("unnamed"))] {
		t.Error("the default code is not in wire.VaultErrorCodes")
	}
}
