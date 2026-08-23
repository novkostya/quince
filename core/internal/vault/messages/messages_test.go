package messages_test

import (
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/novkostya/ios-backup-crypt/fixture"

	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/vault/messages"
	"github.com/novkostya/quince/core/internal/vault/messages/msgfixture"
	"github.com/novkostya/quince/core/internal/vault/parserfs"
)

func reader(t *testing.T, spec msgfixture.Spec) *messages.Reader {
	t.Helper()
	data, err := msgfixture.Build(t.TempDir(), spec)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return readerFor(t, []fixture.File{
		{Domain: msgfixture.Domain, RelativePath: msgfixture.RelativePath, Flags: 1, Data: data},
	})
}

func readerFor(t *testing.T, files []fixture.File) *messages.Reader {
	t.Helper()
	dir := t.TempDir()
	if _, err := fixture.Build(dir, fixture.Spec{Unencrypted: true, Files: files}); err != nil {
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
	fsys, err := parserfs.New(v, scratch)
	if err != nil {
		t.Fatalf("parserfs: %v", err)
	}
	r, err := messages.New(fsys, scratch)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

// D2's ruling, asserted rather than described: neither Available nor Chats may build the
// projection. If either did, an unlock that only wanted the file browser would pay 18 s.
func TestAvailableAndChatsDoNotBuildTheProjection(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	scratch := scratchOf(t, r)

	if _, err := r.Available(t.Context()); err != nil {
		t.Fatalf("Available: %v", err)
	}
	if n := projectionFiles(t, scratch); n != 0 {
		t.Errorf("Available built a projection (%d file(s)) — the lazy ruling is broken", n)
	}

	if _, _, err := r.Chats(t.Context()); err != nil {
		t.Fatalf("Chats: %v", err)
	}
	if n := projectionFiles(t, scratch); n != 0 {
		t.Errorf("Chats built a projection (%d file(s)) — the chats list is answerable live", n)
	}

	// THE CONTROL: something must build it, or the two assertions above pass vacuously.
	//
	// SEARCH, NOT Thread. Thread stopped building anything (quince#1531) — reading a
	// conversation is a ~1 ms page against the database, and the scan moved to the one
	// action that needs it. A control that no longer triggers the thing it controls for
	// is worse than no control, because it passes.
	if _, err := r.Search(t.Context(), "fixture", 10, nil); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if n := projectionFiles(t, scratch); n == 0 {
		t.Fatal("control failed: Search built no projection, so the assertions above prove nothing")
	}
}

func scratchOf(t *testing.T, r *messages.Reader) string {
	t.Helper()
	return messages.ScratchDirForTest(r)
}

func projectionFiles(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if len(e.Name()) >= 8 && e.Name()[:8] == "messages" {
			n++
		}
	}
	return n
}

func TestThreadReturnsNewestFirst(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	page, err := r.Thread(t.Context(), 1, "", 50, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Messages) == 0 {
		t.Fatal("no messages")
	}
	for i := 1; i < len(page.Messages); i++ {
		if page.Messages[i-1].Time.Before(page.Messages[i].Time) {
			t.Errorf("not newest-first at %d", i)
		}
	}
}

// A cursor round trip must visit every message exactly once. Off-by-one at a page boundary
// either drops a message or repeats one, and both look like a rendering bug much later.
func TestCursorRoundTripVisitsEveryMessageOnce(t *testing.T) {
	r := reader(t, msgfixture.Spec{Messages: 60})

	seen := map[int64]int{}
	cursor := ""
	pages := 0
	for {
		page, err := r.Thread(t.Context(), 1, cursor, 7, nil)
		if err != nil {
			t.Fatalf("page %d: %v", pages, err)
		}
		for _, m := range page.Messages {
			seen[m.ID]++
		}
		pages++
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
		if pages > 100 {
			t.Fatal("cursor never terminated")
		}
	}
	if pages < 2 {
		t.Fatalf("control failed: %d page(s) — a single page does not exercise a cursor", pages)
	}
	// chat 1 holds the two direct-chat messages plus all the padding.
	if len(seen) == 0 {
		t.Fatal("no messages seen")
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("message %d seen %d times, want exactly 1", id, n)
		}
	}
}

// The last page must not advertise a next one. A cursor that yields an empty page is how an
// infinite scroll spins forever.
func TestLastPageIsTerminal(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	page, err := r.Thread(t.Context(), 1, "", 200, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if page.NextCursor != "" {
		t.Errorf("NextCursor = %q on a page holding every message", page.NextCursor)
	}
}

func TestLimitClampIsDisclosed(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	page, err := r.Thread(t.Context(), 1, "", 100000, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !page.LimitClamped {
		t.Error("limit was clamped and the page does not say so — that is a silent cap")
	}
}

func TestMalformedCursorIsRefused(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	if _, err := r.Thread(t.Context(), 1, "!!!not-base64!!!", 10, nil); err == nil {
		t.Error("want an error for a malformed cursor")
	}
}

// An unknown body must arrive as UNKNOWN, not as an empty message. This is the state the
// parser's own charter and qn.10 D7 both single out.
func TestUndecodableBodyIsUnknownNotEmpty(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	msgs := allOf(t, r, 1)
	var found bool
	for _, m := range msgs {
		if m.GUID == "invented-msg-3" {
			found = true
			if !m.BodyUnknown {
				t.Error("BodyUnknown is false — a surface would render this as an empty bubble")
			}
		}
	}
	if !found {
		t.Fatal("control failed: the undecodable message is not in chat 1")
	}
}

// An attachment whose bytes are absent must be marked absent, so the surface offers no link.
func TestAttachmentPresenceDistinguishesMissingBytes(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	msgs := allOf(t, r, 2)
	var present, absent int
	for _, m := range msgs {
		for _, a := range m.Attachments {
			if a.Present {
				present++
			} else {
				absent++
			}
		}
	}
	if present == 0 || absent == 0 {
		t.Fatalf("present=%d absent=%d — the fixture must exercise BOTH or this proves nothing",
			present, absent)
	}
}

func allOf(t *testing.T, r *messages.Reader, chatID int64) []messages.Message {
	t.Helper()
	page, err := r.Thread(t.Context(), chatID, "", 200, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	return page.Messages
}

// D5's guard, end to end: a stale cache must produce a WARNING naming both numbers, not a
// quietly shorter list. The healthy fixture is the control.
// TestStaleAttachmentCacheWarns — ON THE SEARCH PATH, WHICH IS THE ONE THAT STILL BUILDS.
//
// THIS TEST MOVED, AND THE MOVE IS A DISCLOSURE LOSS WORTH KNOWING ABOUT (quince#1535). The
// warning comes from the projection build's reconcile: `cache_has_attachments` says a message
// has attachments and the join has none, so rows were dropped and the build says so.
//
// Thread no longer builds (quince#1531), so **a reader who only opens conversations is no
// longer told**. The parser gates `fillAttachments` on that same flag and returns an empty set
// without reporting the discrepancy, so quince cannot detect it on the page path without the
// parser exposing the flag — which is a library change, not a line here.
//
// Kept pointed at Search rather than deleted, because the reconcile is still real and still
// worth guarding; what is gone is its reach, not the check.
func TestStaleAttachmentCacheWarns(t *testing.T) {
	healthy, err := reader(t, msgfixture.Spec{}).Search(t.Context(), "fixture", 200, nil)
	if err != nil {
		t.Fatalf("healthy: %v", err)
	}
	if len(healthy.Warnings) != 0 {
		t.Fatalf("control failed: the healthy fixture warns %v, so a warning proves nothing", healthy.Warnings)
	}

	stale, err := reader(t, msgfixture.Spec{NoAttachedCache: true}).Search(t.Context(), "fixture", 200, nil)
	if err != nil {
		t.Fatalf("stale: %v", err)
	}
	if len(stale.Warnings) == 0 {
		t.Fatal("a stale attachment cache dropped rows with no warning — that is a silent cap")
	}
}

// A schema with no chat tables must NOT surface as an empty list — that would tell the user
// they have no conversations when nobody looked. It must also not surface as "quince cannot
// read your messages", because it can: only the grouping is missing.
//
// The two errors are asserted to be DISTINCT here. An earlier version of this test expected
// one error for both, and the code was right and the test was wrong.
func TestNoChatsIsItsOwnCauseNotUnsupported(t *testing.T) {
	r := reader(t, msgfixture.Spec{NoChats: true})
	_, _, err := r.Chats(t.Context())
	if err == nil {
		t.Fatal("Chats returned no error on a schema with no chat tables")
	}
	if !errors.Is(err, messages.ErrChatsUnavailable) {
		t.Errorf("err = %v, want ErrChatsUnavailable", err)
	}
	if errors.Is(err, messages.ErrUnsupported) {
		t.Error("a missing chats table reported as an unreadable database — two causes, one sentence")
	}

	// The database IS readable: the capability report says so, and names what is absent.
	cap, err := r.Available(t.Context())
	if err != nil {
		t.Fatalf("Available: %v", err)
	}
	if !cap.Supported {
		t.Error("Supported is false — the database is readable, only the grouping is missing")
	}
	if !slices.Contains(cap.Missing, "chats") {
		t.Errorf("Missing = %v, want it to name chats", cap.Missing)
	}
}

// A backup with no Messages database at all is ErrUnsupported, and the surface says "no
// messages in this backup" rather than failing. Device A is the real instance.
func TestNoDatabaseIsUnsupported(t *testing.T) {
	r := readerFor(t, []fixture.File{
		{Domain: "HomeDomain", RelativePath: "Library/Preferences/unrelated.plist", Flags: 1, Data: []byte("x")},
	})
	if _, err := r.Available(t.Context()); !errors.Is(err, messages.ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

// The build happens ONCE. A second read must not rescan — on a real backup that would be
// another 18 seconds.
func TestProjectionIsBuiltOnlyOnce(t *testing.T) {
	r := reader(t, msgfixture.Spec{Messages: 40})
	builds := 0
	onProgress := func(messages.Progress) { builds++ }

	if _, err := r.Search(t.Context(), "fixture", 10, onProgress); err != nil {
		t.Fatalf("first: %v", err)
	}
	first := builds
	if first == 0 {
		t.Fatal("control failed: no progress callback fired, so a second build would be undetectable")
	}
	if _, err := r.Search(t.Context(), "fixture", 10, onProgress); err != nil {
		t.Fatalf("second: %v", err)
	}
	if builds != first {
		t.Errorf("progress fired again (%d then %d) — the projection was rebuilt", first, builds)
	}
}

// A cursor quince did not issue is the CALLER's fault and must be distinguishable from an
// unreadable backup: one means "reload the page", the other means "this backup is damaged".
// Reporting both as an IO error would put the wrong sentence on the screen.
func TestMalformedCursorIsItsOwnError(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	_, err := r.Thread(t.Context(), 1, "!!!not-base64!!!", 10, nil)
	if err == nil {
		t.Fatal("want an error")
	}
	if !errors.Is(err, messages.ErrBadCursor) {
		t.Errorf("err = %v, want ErrBadCursor", err)
	}
	// A well-formed base64 string that is not JSON is the same class.
	_, err = r.Thread(t.Context(), 1, "bm90LWpzb24", 10, nil)
	if !errors.Is(err, messages.ErrBadCursor) {
		t.Errorf("err = %v, want ErrBadCursor for valid base64 that is not a cursor", err)
	}
	// THE CONTROL: a cursor quince DID issue must still work, or the two assertions above
	// would pass on an implementation that rejects everything.
	page, err := r.Thread(t.Context(), 1, "", 1, nil)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if page.NextCursor == "" {
		t.Fatal("control failed: no cursor was issued, so nothing proves a good one is accepted")
	}
	if _, err := r.Thread(t.Context(), 1, page.NextCursor, 1, nil); err != nil {
		t.Errorf("a cursor quince issued was rejected: %v", err)
	}
}

// TestPlainMessagesAreNotEditedOrRetracted is a REPLAY OF A BUG FOUND ON HARDWARE, written before
// the fix per the hard rule. On the Operator's own backup, 792 of 799 sampled messages came back
// with BOTH `retracted` and `edited` true, and 778 of those carried a non-empty body — a message
// cannot be unsent and still hold its text. The surface renders `retracted` first, so every
// conversation displayed as a wall of *"This message was unsent."* and no message content was
// readable at all.
//
// THE CAUSE IS `time.Time{}.UnixNano()`, WHICH IS NOT ZERO. It is -6795364578871345152: the zero
// time is year 1, far outside the range UnixNano can represent. The projection wrote
// `msg.DateEdited.UnixNano()` unconditionally and read it back as `edited != 0`, so a message that
// was never edited stored a huge negative number and read as edited.
//
// WHY NOTHING CAUGHT IT: every existing assertion was in the POSITIVE direction — msg-7 HAS an
// edit date, msg-8 HAS a retract date — and at the parser rather than through the projection. The
// negative was never asserted, so a round trip that turns every message into "edited and unsent"
// passed. That is this rung's recurring shape: the pair, not the branch.
func TestPlainMessagesAreNotEditedOrRetracted(t *testing.T) {
	r := reader(t, msgfixture.Spec{})
	page, err := r.Thread(t.Context(), 1, "", 200, nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(page.Messages) == 0 {
		t.Fatal("no messages")
	}

	var edited, retracted, plain int
	for _, m := range page.Messages {
		if m.Edited {
			edited++
		}
		if m.Retracted {
			retracted++
		}
		if !m.Edited && !m.Retracted {
			plain++
		}
		// THE CONTRADICTION THE OPERATOR ACTUALLY SAW, asserted directly: a retracted message
		// holding text is not a state iOS produces, and it is the signature of this defect.
		if m.Retracted && m.Body != "" {
			t.Errorf("message %d is retracted AND carries a body — a message cannot be unsent and still hold its text", m.ID)
		}
	}

	// The fixture has ONE edited and ONE retracted message (msgfixture msg-7 and msg-8), and they
	// are not in this chat's default page in every spec — so the assertion is that the flags are
	// RARE, not that they are absent. Before the fix these counted every message in the page.
	if plain == 0 {
		t.Errorf("no plain messages: %d/%d edited, %d/%d retracted — every message claims to have "+
			"been edited or unsent, which is the hardware defect this test replays",
			edited, len(page.Messages), retracted, len(page.Messages))
	}
}
