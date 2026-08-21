package httpapi

import (
	"encoding/json"
	"io"
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
	session  wire.Session
	page     wire.BrowsePage
	entry    wire.FileEntry
	content  string
	code     string
	message  string
	lastPass string
	lastQ    wire.BrowseQuery
	lastFile string
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
