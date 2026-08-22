package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// stubVault fixes each route's outcome and records what it was asked.
type stubVault struct {
	session         wire.Session
	page            wire.BrowsePage
	overview        wire.Overview
	versionOverview wire.VersionOverview
	entry           wire.FileEntry
	content         string
	code            string
	message         string
	lastPass        string
	lastQ           wire.BrowseQuery
	lastFile        string
	lastVersion     string
}

func (s *stubVault) Unlock(versionID, password string) (wire.Session, string, string) {
	s.lastPass = password
	return s.session, s.code, s.message
}
func (s *stubVault) Lock(string) (string, string) { return s.code, s.message }
func (s *stubVault) Browse(_ string, q wire.BrowseQuery) (wire.BrowsePage, string, string) {
	s.lastQ = q
	return s.page, s.code, s.message
}

func (s *stubVault) Overview(_ string, q wire.BrowseQuery) (wire.Overview, string, string) {
	s.lastQ = q
	return s.overview, s.code, s.message
}
func (s *stubVault) VersionOverview(versionID string) (wire.VersionOverview, string, string) {
	s.lastVersion = versionID
	return s.versionOverview, s.code, s.message
}

func (s *stubVault) OpenFile(_, fileID string) (io.ReadCloser, wire.FileEntry, string, string) {
	s.lastFile = fileID
	if s.code != "" {
		return nil, wire.FileEntry{}, s.code, s.message
	}
	return io.NopCloser(strings.NewReader(s.content)), s.entry, "", ""
}

func vaultServer(t *testing.T, sv *stubVault) (*httptest.Server, *http.Client) {
	t.Helper()
	deps := testDeps(t)
	deps.VaultBrowse = sv
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	return srv, authedClient(t, srv)
}

func TestVaultUnlockReturnsASession(t *testing.T) {
	sv := &stubVault{session: wire.Session{ID: "s1", VersionID: "01ABC", ExpiresAt: "2026-08-21T00:00:00Z"}}
	srv, c := vaultServer(t, sv)

	req := newReq(t, http.MethodPost, srv.URL+"/api/versions/01ABC/unlock", `{"password":"hunter2"}`)
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var got wire.Session
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "s1" || got.VersionID != "01ABC" {
		t.Errorf("session = %+v, want the stub's", got)
	}
	if sv.lastPass != "hunter2" {
		t.Errorf("the password did not reach the vault: %q", sv.lastPass)
	}
}

// A wrong BACKUP password is 403, not 401. The caller is authenticated — every route here is
// behind authGuard — and what failed is a different credential. A 401 would invite a client
// to re-run the login flow for a reason that has nothing to do with the session.
func TestVaultBadPasswordIs403NotUnauthorized(t *testing.T) {
	sv := &stubVault{code: "bad_password", message: "incorrect backup password"}
	srv, c := vaultServer(t, sv)

	req := newReq(t, http.MethodPost, srv.URL+"/api/versions/01ABC/unlock", `{"password":"wrong"}`)
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("401 would tell the client to log in again, which is the wrong credential")
	}
}

// Lock is idempotent and an unknown id is 204: the state the caller wanted is the state that
// exists, and a double-click must not look like a fault.
func TestVaultLockIsAlways204(t *testing.T) {
	sv := &stubVault{}
	srv, c := vaultServer(t, sv)

	for _, id := range []string{"s1", "never-existed"} {
		req := newReq(t, http.MethodPost, srv.URL+"/api/sessions/"+id+"/lock", "")
		req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNoContent {
			t.Errorf("lock %s: status = %d, want 204", id, resp.StatusCode)
		}
	}
}

func TestVaultBrowseReturnsEntriesAndPassesTheQuery(t *testing.T) {
	sv := &stubVault{page: wire.BrowsePage{
		Entries:    []wire.FileEntry{{FileID: "ab12", Domain: "HomeDomain", RelativePath: "a", Kind: "file", Size: 5}},
		NextCursor: "next",
	}}
	srv, c := vaultServer(t, sv)

	resp, err := c.Get(srv.URL + "/api/sessions/s1/browse?domain=HomeDomain&prefix=Lib&cursor=cur&limit=7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var got wire.BrowsePage
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Entries) != 1 || got.NextCursor != "next" {
		t.Errorf("page = %+v, want the stub's", got)
	}
	want := wire.BrowseQuery{Domain: "HomeDomain", Prefix: "Lib", Cursor: "cur", Limit: 7}
	if sv.lastQ != want {
		t.Errorf("query reaching the vault = %+v, want %+v", sv.lastQ, want)
	}
}

// Entries is never null on the wire: a client iterating a page should not have to
// distinguish "no entries" from "field absent".
func TestVaultBrowseEntriesIsNeverNull(t *testing.T) {
	srv, c := vaultServer(t, &stubVault{page: wire.BrowsePage{}})
	resp, err := c.Get(srv.URL + "/api/sessions/s1/browse")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), `"entries":null`) {
		t.Errorf("body carries a null entries array: %s", body)
	}
}

// A limit that is not a number means the default rather than a 400 — the caller gets a page
// either way, and a clamp is disclosed when one happens.
func TestVaultBrowseTolerantOfAJunkLimit(t *testing.T) {
	sv := &stubVault{}
	srv, c := vaultServer(t, sv)
	resp, err := c.Get(srv.URL + "/api/sessions/s1/browse?limit=abc")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	if sv.lastQ.Limit != 0 {
		t.Errorf("limit = %d, want 0 (meaning the default)", sv.lastQ.Limit)
	}
}

// The clamp reaches the client. "No silent caps or fallbacks" as a wire field.
func TestVaultBrowseDisclosesAClamp(t *testing.T) {
	srv, c := vaultServer(t, &stubVault{page: wire.BrowsePage{EffectiveLimit: 2000}})
	resp, err := c.Get(srv.URL + "/api/sessions/s1/browse?limit=99999")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var got wire.BrowsePage
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.EffectiveLimit != 2000 {
		t.Errorf("effective_limit = %d, want 2000 — a clamp must reach the client", got.EffectiveLimit)
	}
}

// Content-Length comes from the RECORDED size, which is what makes a short read detectable
// rather than silently truncated (spec D8.1).
func TestVaultFileDeclaresTheRecordedLength(t *testing.T) {
	sv := &stubVault{
		entry:   wire.FileEntry{FileID: "ab12", Size: 12, Kind: "file"},
		content: "twelve bytes",
	}
	srv, c := vaultServer(t, sv)

	resp, err := c.Get(srv.URL + "/api/sessions/s1/file/ab12")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := resp.Header.Get("Content-Length"); got != strconv.Itoa(12) {
		t.Errorf("Content-Length = %q, want 12 — from the recorded size", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store — nothing derived from backup content is cached", got)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "twelve bytes" {
		t.Errorf("body = %q, want the content", body)
	}
	if sv.lastFile != "ab12" {
		t.Errorf("file id reaching the vault = %q", sv.lastFile)
	}
}

// D8.1a: THE INCOMPLETENESS SURFACE MUST ACTUALLY FIRE. If it travelled through Code()
// instead of a field it would be invisible — Code(nil) and Code(ErrIncompleteFile) are both
// "" — and every other test here would still pass. This is the test the spec asks for by
// name.
func TestVaultBrowseSurfacesIncompleteness(t *testing.T) {
	srv, c := vaultServer(t, &stubVault{page: wire.BrowsePage{Entries: []wire.FileEntry{
		{FileID: "whole", Kind: "file", Size: 5},
		{FileID: "short", Kind: "file", Size: 99, Incomplete: true},
	}}})

	resp, err := c.Get(srv.URL + "/api/sessions/s1/browse")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"incomplete":true`) {
		t.Fatalf("the incomplete flag never reached the wire: %s", body)
	}
	// And it is absent rather than false for a file nobody has read short — "not known to be
	// incomplete" is the honest reading, and it keeps the common row clean.
	var page wire.BrowsePage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatal(err)
	}
	if page.Entries[0].Incomplete {
		t.Error("a whole file is marked incomplete")
	}
	if !page.Entries[1].Incomplete {
		t.Error("the short file is not marked incomplete")
	}
}

// not_a_file is 404 and must stay distinguishable from not_found in the BODY, because the
// status alone collapses them and the two have different remedies (spec D8).
func TestVaultFileDirectoryIsNotAFileNotNotFound(t *testing.T) {
	srv, c := vaultServer(t, &stubVault{code: "not_a_file", message: "entry has no content (directory or symlink)"})
	resp, err := c.Get(srv.URL + "/api/sessions/s1/file/adir")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "not_a_file") {
		t.Errorf("the body does not carry not_a_file, so it is indistinguishable from not_found: %s", body)
	}
}

// An expired or unknown session is 409, not 404: the id may be perfectly real and simply
// over, and "conflict with the current state" is what that is.
func TestVaultLockedSessionIs409(t *testing.T) {
	srv, c := vaultServer(t, &stubVault{code: "locked", message: "no such session"})
	resp, err := c.Get(srv.URL + "/api/sessions/gone/browse")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("status = %d, want 409", resp.StatusCode)
	}
}

// With no vault wired every route refuses honestly rather than pretending. This is the
// default Deps take, so it is what --demo and a storage-less daemon answer.
func TestVaultRoutesRefuseHonestlyWhenNothingIsWired(t *testing.T) {
	deps := testDeps(t) // VaultBrowse left nil → UnavailableVaultBrowse
	srv := httptest.NewServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	c := authedClient(t, srv)

	for _, tc := range []struct{ method, path string }{
		{http.MethodPost, "/api/versions/01ABC/unlock"},
		{http.MethodPost, "/api/sessions/s1/lock"},
		{http.MethodGet, "/api/sessions/s1/browse"},
		{http.MethodGet, "/api/sessions/s1/file/ab12"},
	} {
		body := ""
		if tc.method == http.MethodPost && strings.HasSuffix(tc.path, "/unlock") {
			body = `{"password":"x"}`
		}
		req := newReq(t, tc.method, srv.URL+tc.path, body)
		if tc.method == http.MethodPost {
			req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("%s %s: status = %d, want 503", tc.method, tc.path, resp.StatusCode)
		}
	}
}

// The status table must be TOTAL over the vocabulary the vault surface can answer with.
//
// This is the test quince#1375 did not have. Every other test in this file asserts one
// code-to-status pair, and each of them passed while `unsupported_version` and `busy` fell
// through to 500 — because a test that names the pairs it knows about cannot report the pair
// nobody thought of. The enumeration is what closes that, so a code added to
// wire.VaultErrorCodes without a case here fails HERE rather than on a user's browse.
func TestVaultStatusTableIsTotalOverTheWireCodes(t *testing.T) {
	for _, code := range wire.VaultErrorCodes {
		if _, ok := vaultCodeStatus(code); !ok {
			t.Errorf("vaultCodeStatus(%q) has no case: it would answer 500, which reports a "+
				"failure that did not happen", code)
		}
	}
}

// And the reverse direction: a status that is 500 must be a DECISION, not a fall-through.
// `io` is the only code that legitimately means "something below failed and quince will not
// guess what" (contracts §1).
func TestOnlyIOAnswers500(t *testing.T) {
	for _, code := range wire.VaultErrorCodes {
		status, ok := vaultCodeStatus(code)
		if ok && status == http.StatusInternalServerError && code != wire.VaultCodeIO {
			t.Errorf("code %q maps to 500; only %q may", code, wire.VaultCodeIO)
		}
	}
}

// The two codes that shipped unmapped, pinned by name so a regression is legible as itself
// rather than as a totality count that moved.
func TestUnsupportedVersionIs422AndBusyIs409(t *testing.T) {
	if got := statusForVaultCode(wire.VaultCodeUnsupportedVersion); got != http.StatusUnprocessableEntity {
		t.Errorf("unsupported_version = %d, want %d: the request is well-formed and the "+
			"artifact is a class this build cannot open", got, http.StatusUnprocessableEntity)
	}
	if got := statusForVaultCode(wire.VaultCodeBusy); got != http.StatusConflict {
		t.Errorf("busy = %d, want %d: the session is real and occupied, and the caller can retry",
			got, http.StatusConflict)
	}
}

// An unrecognised code still answers 500 rather than panicking — the wrapper's contract,
// asserted because the split above could otherwise drop it silently.
func TestAnUnknownCodeIs500NotAPanic(t *testing.T) {
	if got := statusForVaultCode("a_code_no_version_of_this_table_has_seen"); got != http.StatusInternalServerError {
		t.Errorf("unknown code = %d, want %d", got, http.StatusInternalServerError)
	}
}

// A file with MORE bytes than its record says must not be logged as one with fewer.
//
// quince#1379: `io.Copy` fails in both directions — short reads end the body early, and a
// long file trips net/http's refusal to write past the declared Content-Length — and one
// message said "ended early" for both. For the long case that message is not merely
// collapsed, it is FALSE, which is the shape the troubleshooting rule names as a defect even
// when every word is true.
//
// MEASURED, and it corrects what quince#1379 and its ruling both assumed: this is NOT a
// silent truncation. Both described the client receiving exactly Content-Length bytes with
// the header agreeing, so that nothing on the wire showed a problem. What actually happens
// is that net/http tears the response: the client gets a 200 and a correct Content-Length
// header, then reads ZERO bytes and an `unexpected EOF`. Checked at both 4 bytes declared
// and 65536, i.e. either side of the response buffer, in case a flushed prefix could arrive
// looking complete. It cannot.
//
// So the download FAILS VISIBLY and the user is not handed corrupt data. What they are not
// given is any way to find out why — which is what this log line is for, and why it has to
// say the true thing.
//
// THE STUB IS THE ENCRYPTED SHAPE, AND THAT IS THE SCOPE OF THIS TEST (quince#1433). The
// unbounded `strings.NewReader` overruns the declared length, which is what `encrypted.Open`
// does — it pipes DecryptFile through with no bound. The UNENCRYPTED backend cannot reach
// this case at all: `boundedFile` stops at the record and the registry wrapper turns
// ErrOverlongFile into io.EOF (quince#1400), so the handler sees a clean success.
//
// SO THIS TEST IS ALSO WHAT PROVES THE `http.ErrContentLength` ARM IS STILL LIVE. Reading
// the unencrypted behaviour on hardware makes that arm look vestigial — quince#1433 was
// filed believing it might be — and the assertion below that the log names the long case is
// the thing that says otherwise. A green run here means the branch was taken.
//
// WHAT IS NOT COVERED, deliberately: the unencrypted path's own success. It is asserted
// where it is produced, in `TestOverlongIsRecordedThenSwallowedSoTheTransferReadsAsThe
// SuccessItIs` and `TestUnencryptedOverlongBlobIsReportedRatherThanSilentlyTruncated`, and
// duplicating it here would test the stub rather than the handler.
func TestVaultFileLongerThanItsRecordIsNotReportedAsEndingEarly(t *testing.T) {
	var logged bytes.Buffer
	deps := testDeps(t)
	deps.Log = slog.New(slog.NewTextHandler(&logged, nil))
	deps.VaultBrowse = &stubVault{
		entry:   wire.FileEntry{FileID: "f1", Size: 4},
		content: "far more bytes on disk than the record claims",
	}
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	resp, err := authedClient(t, srv).Get(srv.URL + "/api/sessions/s1/file/f1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, readErr := io.ReadAll(resp.Body)

	// Content-Length stays the RECORDED size — this PR changes the diagnosis, not the bytes.
	// Pinned so a later change cannot quietly start serving the on-disk length, which would
	// destroy short-read detectability (contracts §1).
	if got := resp.Header.Get("Content-Length"); got != "4" {
		t.Errorf("Content-Length = %q, want %q (the recorded size)", got, "4")
	}
	// The transfer must not look like a clean success. If this ever passes with a nil error
	// and a full body, the long case HAS become the silent truncation everyone assumed it
	// already was, and the remedy has to change with it.
	if readErr == nil && int64(len(body)) == 4 {
		t.Error("a file longer than its record was delivered as a clean, complete-looking " +
			"download; the truncation is now silent and this is no longer only a logging bug")
	}

	out := logged.String()
	if strings.Contains(out, "ended early") {
		t.Errorf("a file LONGER than its record was logged as ending early:\n%s", out)
	}
	if !strings.Contains(out, "longer than its manifest record") {
		t.Errorf("the long-file case was not reported; the client learns only `unexpected EOF`, "+
			"so this log line is the only place the reason exists:\n%s", out)
	}
}

// The short case must keep its own words, so the fix above did not simply move the problem.
func TestVaultFileShorterThanItsRecordStillReportsEndingEarly(t *testing.T) {
	var logged bytes.Buffer
	deps := testDeps(t)
	deps.Log = slog.New(slog.NewTextHandler(&logged, nil))
	deps.VaultBrowse = &stubVault{
		entry:   wire.FileEntry{FileID: "f1", Size: 4096},
		content: "only a few bytes",
	}
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	resp, err := authedClient(t, srv).Get(srv.URL + "/api/sessions/s1/file/f1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.ReadAll(resp.Body)

	if out := logged.String(); strings.Contains(out, "longer than its manifest record") {
		t.Errorf("a SHORT file was reported as longer than its record:\n%s", out)
	}
}

// SessionVersion — the stub answers from the session it was seeded with (qn.13 slice 8b-2).
func (s *stubVault) SessionVersion(string) (string, bool) {
	if s.session.ID == "" {
		return "", false
	}
	return s.session.VersionID, true
}

// A RELATIVE PATH IS DEVICE CONTENT AND IT REACHES A RESPONSE HEADER. Header splitting must be
// impossible where the value is BUILT — not caught later by an HTTP writer that may or may not
// be looking, and whose behaviour is not this package's to rely on (quince#1397 ruling).
func TestContentDispositionCannotSplitTheHeader(t *testing.T) {
	for _, name := range []string{
		"Library/ok\r\nX-Injected: yes",
		"Library/ok\nSet-Cookie: a=b",
		"Library/quote\"name.txt",
		"Library/back\\slash.txt",
		"Library/nul\x00byte.txt",
		"Library/bell\x07.txt",
	} {
		got := contentDisposition(name)
		for _, bad := range []string{"\r", "\n", "\x00"} {
			if strings.Contains(got, bad) {
				t.Errorf("contentDisposition(%q) contains %q — a header this value can terminate "+
					"is a header-splitting vector", name, bad)
			}
		}
		// The ASCII fallback is quoted, so a bare quote inside it would close the parameter
		// early and hand the rest of the name to the parser as syntax.
		if strings.Count(got, `"`) != 2 {
			t.Errorf("contentDisposition(%q) = %s — the quoted fallback must contain exactly the "+
				"two delimiting quotes", name, got)
		}
	}
}

// THE BASENAME, ALWAYS — never the path, and never conditionally on collisions. A name that
// depends on what else you have downloaded is not a property anyone can reason about.
func TestContentDispositionUsesTheBasename(t *testing.T) {
	got := contentDisposition("Library/Preferences/com.apple.example.plist")
	if !strings.Contains(got, `filename="com.apple.example.plist"`) {
		t.Errorf("got %s, want the basename in the ASCII fallback", got)
	}
	if strings.Contains(got, "Library") {
		t.Errorf("got %s — the path leaked into the filename; the domain and path belong in the "+
			"browse row", got)
	}
}

// NON-ASCII SURVIVES IN filename*, WHICH IS THE WHOLE REASON IT IS THERE. The fallback loses
// it, deliberately — a fallback is a best effort and an unambiguous `_` beats an encoding a
// client might mis-parse.
func TestContentDispositionCarriesNonASCIIInFilenameStar(t *testing.T) {
	got := contentDisposition("Media/фото.jpg")
	if !strings.Contains(got, "filename*=UTF-8''") {
		t.Fatalf("got %s, want an RFC 5987 filename*", got)
	}
	// UTF-8 for `ф` is D1 84; the extension must survive unescaped.
	if !strings.Contains(got, "%D1%84") || !strings.Contains(got, ".jpg") {
		t.Errorf("got %s — the encoded name must round-trip the non-ASCII bytes and keep .jpg", got)
	}
	if !strings.Contains(got, `filename="`) {
		t.Errorf("got %s — an ASCII fallback must still be present for clients without RFC 5987", got)
	}
}

// ALWAYS `attachment`. `inline` would render backup content — HTML, SVG — inside quince's own
// origin with the session cookie in scope, which is stored XSS reachable by anyone who can put
// a file on the device. This asserts it because the Content-Type control and this one must not
// drift apart.
func TestContentDispositionIsAlwaysAttachment(t *testing.T) {
	for _, name := range []string{"a.html", "b.svg", "c.jpg", ""} {
		if got := contentDisposition(name); !strings.HasPrefix(got, "attachment;") {
			t.Errorf("contentDisposition(%q) = %s, want attachment", name, got)
		}
	}
}

// A record with no usable basename still downloads as SOMETHING, and that something is not the
// file id — the id is not a name, and using it here would reproduce the defect this fixes.
func TestContentDispositionFallsBackToAPlaceholderNotTheID(t *testing.T) {
	for _, name := range []string{"", "/", "."} {
		if got := contentDisposition(name); !strings.Contains(got, `filename="file"`) {
			t.Errorf("contentDisposition(%q) = %s, want the `file` placeholder", name, got)
		}
	}
}

// And the header actually reaches the response, which none of the above proves.
func TestVaultFileResponseCarriesTheDisposition(t *testing.T) {
	sv := &stubVault{
		entry:   wire.FileEntry{FileID: "f1", RelativePath: "Media/DCIM/IMG_0001.HEIC", Size: 5},
		content: "hello",
	}
	srv, client := vaultServer(t, sv)
	resp, err := client.Get(srv.URL + "/api/sessions/s1/file/f1")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if got := resp.Header.Get("Content-Disposition"); !strings.Contains(got, `filename="IMG_0001.HEIC"`) {
		t.Errorf("Content-Disposition = %q, want the basename", got)
	}
}

// The pre-unlock route reaches the seam with the VERSION id and needs no session.
func TestVersionOverviewRouteServesTheVersionTier(t *testing.T) {
	sv := &stubVault{versionOverview: wire.VersionOverview{
		VersionID: "01V",
		Kind:      "full",
		Device:    wire.VersionDevice{Present: true, Name: "Study Tablet"},
		Apps:      wire.VersionApps{Present: true, BundleIDs: []string{"com.example.notes"}},
	}}

	srv, client := vaultServer(t, sv)
	res, err := client.Get(srv.URL + "/api/versions/01V/overview")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if sv.lastVersion != "01V" {
		t.Errorf("seam was asked for %q, want the path's version id", sv.lastVersion)
	}

	var got wire.VersionOverview
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Device.Name != "Study Tablet" || !got.Apps.Present {
		t.Errorf("body = %+v, want the tier the seam returned", got)
	}
}

// file_count is an explicit null on the wire, not an omitted key and not a zero (story 7, G2).
func TestVersionOverviewSendsFileCountAsAnExplicitNull(t *testing.T) {
	sv := &stubVault{versionOverview: wire.VersionOverview{VersionID: "01V", FileCount: nil}}
	srv, client := vaultServer(t, sv)
	res, err := client.Get(srv.URL + "/api/versions/01V/overview")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), `"file_count":null`) {
		t.Errorf("body has no explicit null file_count — an absent key says nothing and a 0 "+
			"would claim quince counted a table it cannot reach.\nbody = %s", body)
	}
}

// With no vault wired the route reports 503 honestly rather than an empty overview.
func TestVersionOverviewIsUnavailableWithNoVaultWired(t *testing.T) {
	deps := testDeps(t) // VaultBrowse left nil → UnavailableVaultBrowse
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	client := authedClient(t, srv)

	res, err := client.Get(srv.URL + "/api/versions/01V/overview")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", res.StatusCode)
	}
}
