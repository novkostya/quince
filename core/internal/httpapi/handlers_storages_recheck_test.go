package httpapi

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// idlessStorages is a storage list whose unreachable entry has NO id — which is what the live
// resolver produces for a declared path that cannot be read: `creation.go` returns before
// `StorageID` is ever set, because none was ever minted. That is correct, and it is the state
// quince#610 made unserviceable.
type idlessStorages struct{ rechecked []string }

func (s *idlessStorages) Storages(string) []wire.Storage {
	return []wire.Storage{
		{ID: "01JREAL", Name: "internal", Path: "/backups", Backend: "reflink", Default: true, Reachable: true},
		{ID: "", Name: "ghost", Path: "/nonexistent", Backend: "unknown", Reachable: false},
	}
}

func (s *idlessStorages) Recheck(name string) (wire.Storage, bool) {
	for _, st := range s.Storages("") {
		if st.Name == name {
			s.rechecked = append(s.rechecked, name)
			return st, true
		}
	}
	return wire.Storage{}, false
}

// quince#610 — THE REGRESSION, stated as the invariant rather than as the symptom.
//
// A storage quince has never reached has no id. Keying the route on the id therefore produced
// `POST /api/storages//recheck` from the client, and the button was unreachable for exactly the
// storage it exists to serve. The route keys on `name` now (quince#570's ruling), so the fix is
// that this request is ADDRESSABLE AT ALL.
func TestRecheckReachesAStorageThatHasNoID(t *testing.T) {
	deps := testDeps(t)
	fake := &idlessStorages{}
	deps.Storages = fake
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	req := newReq(t, http.MethodPost, srv.URL+"/api/storages/ghost/recheck", "")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	if code := doStatus(t, c, req); code != http.StatusOK {
		t.Fatalf("rechecking a storage with an EMPTY id = %d, want 200 — this is quince#610", code)
	}
	if len(fake.rechecked) != 1 || fake.rechecked[0] != "ghost" {
		t.Errorf("the handler must pass the NAME through; got %v", fake.rechecked)
	}

	req = newReq(t, http.MethodPost, srv.URL+"/api/storages/nosuch/recheck", "")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	if code := doStatus(t, c, req); code != http.StatusNotFound {
		t.Errorf("an undeclared name = %d, want 404", code)
	}
}

// rawStatusLine writes the request line VERBATIM over a socket.
//
// A RAW SOCKET IS THE ONLY WAY TO SEND A DOUBLED SLASH, and that is why quince#610 survived a
// release: `net/http`'s client normalises it out before the wire, so NO test using an ordinary Go
// client can reproduce it, and `curl` needs `--path-as-is` for the same reason. Forcing it with
// `URL.Opaque = "//api/..."` looks like it works and does not — a leading `//` makes Go parse the
// next segment as a HOST, so the server sees a different path.
func rawStatusLine(t *testing.T, addr, requestLine string) (status, location string) {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	_, _ = fmt.Fprintf(conn, "%s HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n", requestLine)

	sc := bufio.NewScanner(conn)
	if !sc.Scan() {
		t.Fatalf("no response to %q", requestLine)
	}
	status = sc.Text()
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			break
		}
		if after, found := strings.CutPrefix(line, "Location: "); found {
			location = strings.TrimSpace(after)
		}
	}
	return status, location
}

// CHARACTERISATION, not a defect: an empty path segment is a `307` from Go's own ServeMux and
// always will be. Keying the route on `name` does not change this and was never meant to — it
// removes the only way a CLIENT could produce such a URL.
//
// It is pinned because the 307 is what makes an empty key catastrophic rather than merely wrong: a
// 307 PRESERVES THE METHOD, so a browser re-sends the POST to a target matching no pattern and the
// user gets a silent 404. If a future route is keyed on anything that can be empty, this is what
// happens, and this test is where that is written down.
func TestAnEmptyPathSegmentRedirectsAndIsWhyRoutesAreKeyedOnName(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	addr := strings.TrimPrefix(srv.URL, "http://")

	status, location := rawStatusLine(t, addr, "POST /api/storages//recheck")
	if !strings.Contains(status, "307") {
		t.Errorf("expected the router's own path-clean redirect, got %q — if this has changed, "+
			"quince#610's reasoning needs rereading rather than this test relaxing", status)
	}
	if location != "/api/storages/recheck" {
		t.Errorf("redirect target = %q, want the cleaned path", location)
	}

	// AND THE TARGET IS A DEAD END, which is the second half of why the failure is silent.
	//
	// Asserted as "does not serve" rather than as `404`, because this probe is UNAUTHENTICATED and
	// the auth guard wraps the mux — so it answers `401` here before routing is ever consulted.
	// A logged-in browser, which is the only thing that reaches this in practice, gets the `404`:
	// measured on the staging stand at `main@afcc6a1` with a real session,
	// `final=404 after 1 redirect(s) -> /api/storages/recheck`.
	status, _ = rawStatusLine(t, addr, "POST /api/storages/recheck")
	if strings.Contains(status, "200") {
		t.Errorf("the cleaned path must not serve; got %q", status)
	}
}
