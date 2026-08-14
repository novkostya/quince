package httpapi

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/storage"
)

// GET /zfs/helper — the plain-text door, for the machine that is not holding the browser.

// RETURNS THE STATUS AND HEADERS RATHER THAN THE RESPONSE, so the body is closed exactly once, here,
// and no caller can forget. Handing back an `*http.Response` whose body this function already closed
// reads as a leak at every call site, to a linter and to a person.
func getPlainHelper(t *testing.T, srv *httptest.Server) (int, http.Header, string) {
	t.Helper()
	// srv.Client() carries NO cookie jar: this is an anonymous request, which is the whole point.
	resp, err := srv.Client().Get(srv.URL + "/zfs/helper")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(body)
}

// AN ANONYMOUS FETCH GETS THE SCRIPT, because the host that installs it has no session.
//
// The authenticated endpoint beside this one answers `401` to a `curl` from the ZFS host — correctly,
// and measured — which is why this route exists rather than that one being opened.
func TestZFSHelperPlainIsReachableWithoutASession(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()

	status, _, body := getPlainHelper(t, srv)
	if status != http.StatusOK {
		t.Fatalf("anonymous status = %d, want 200 — a host with no browser cannot install a file "+
			"it cannot fetch", status)
	}
	if body != storage.ZFSHelperScript() {
		t.Error("the bytes served here are not the bytes the form shows and the gate executes")
	}
	if !strings.HasPrefix(body, "#!/bin/sh") {
		t.Error("the response is not a script — it must be saveable with `curl -o` as-is")
	}
}

// IT IS `text/plain` AND NOT AN ATTACHMENT, so `curl <url>` PRINTS it.
//
// That is the argument for offering this at all: a file you are about to run as root is one you can
// read first. A `Content-Disposition` would make the browser's default action a silent download,
// which is the shape the Operator declined — the fetch must not be the only way to meet the file.
func TestZFSHelperPlainCanBeReadRatherThanOnlyDownloaded(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()

	_, hdr, _ := getPlainHelper(t, srv)
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain — this is a thing to READ", ct)
	}
	if cd := hdr.Get("Content-Disposition"); cd != "" {
		t.Errorf("Content-Disposition = %q — that turns reading it into downloading it", cd)
	}
	// The body is a program, so sniffing it as anything else is the one content-type risk here.
	if got := hdr.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// THE EXEMPTION IS ONLY SAFE WHILE THE ANSWER IS A CONSTANT, AND THIS IS THE GUARD ON THAT.
//
// The route discloses to any caller who can reach the port. That costs nothing today: the script is
// a compile-time constant, identical for every install of a version, and already public in the
// repository — so what a stranger learns is *"a quince of about this version is here"*, which the
// login page tells them anyway.
//
// **The day the script carries one operator's dataset again, this route becomes a disclosure** and
// must move behind the session with it. That is a plausible future — it is exactly what quince#985
// undid — so the property is asserted rather than trusted, and it FAILS rather than warns.
//
// ASSERTED TWO WAYS, because either alone passes for the wrong reason: the response must not vary
// with the install, and it must not contain the marks of a per-install render.
func TestZFSHelperPlainServesAConstant(t *testing.T) {
	fetch := func() string {
		srv := httptest.NewServer(NewRouter(testDeps(t)))
		defer srv.Close()
		_, _, body := getPlainHelper(t, srv)
		return body
	}
	a, b := fetch(), fetch()
	if a == "" {
		t.Fatal("the served script is empty; every assertion below would pass vacuously")
	}
	if a != b {
		t.Fatal("TWO INSTALLS GOT DIFFERENT SCRIPTS. This route is unauthenticated on the argument " +
			"that its answer carries nothing about the install — if that has stopped being true, the " +
			"route must move behind the session, not have this test relaxed.")
	}
	// A rendered `PARENT="…"` is what per-install looked like the last time. Named literally so the
	// specific regression that motivated quince#985 cannot come back quietly.
	if strings.Contains(a, `PARENT="pool/`) || strings.Contains(a, `PARENT="tank`) ||
		strings.Contains(a, `PARENT="rpool`) {
		t.Error("the served script names a dataset — the parent belongs in the forced command, and " +
			"a script that carries one must not be served anonymously")
	}
}

// A NEAR MISS ON THE ADDRESS IS A 404, WHICH IS WHAT MAKES `curl -f` MEAN ANYTHING.
//
// MEASURED ON THE RIG, which is why this is a test rather than a hypothesis. With only the helper's
// own two patterns registered, a mistyped path fell through to the SPA catch-all — which answers
// `200 text/html` to every unrouted address, correctly, since it serves a client-routed app. So:
//
//	curl -fsSL http://…/zfs/helperr -o /usr/local/sbin/quince-zfs-helper && chmod +x …
//
// exited **0**, wrote `<!doctype html>…` to the destination, and made it executable. `-f` could not
// help: there was no HTTP error to fail on. The install then fails much later as `unreachable`,
// which is indistinguishable from a wrong key.
//
// The fetch command on the form advertises `-f` as the guard against exactly this, so the guard has
// to be real. It covers the NEIGHBOURHOOD of the address quince prints — `/zfs-helper` is outside
// the prefix and still reaches the SPA, which is why the page also offers the link and the script.
func TestZFSHelperPlainNearMissIsNotTheSPA(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()

	for _, miss := range []string{"/zfs/helperr", "/zfs/helpe", "/zfs/", "/zfs/helper/extra"} {
		resp, err := srv.Client().Get(srv.URL + miss)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode == http.StatusOK {
			t.Errorf("GET %s = 200 — `curl -f` cannot fail on this, so a one-character typo installs "+
				"whatever came back and chmods it +x", miss)
		}
		if strings.Contains(string(body), "<!doctype html") {
			t.Errorf("GET %s served the app's HTML; that is what gets written to /usr/local/sbin "+
				"and made executable", miss)
		}
	}
}

// ONLY GET, AND ONLY THIS PATH. The route sits outside the `/api/` chain, so it has no `authGuard`
// and no `csrfGuard` above it; what keeps that narrow is that it answers one method at one path and
// writes nothing. A POST here must not fall through to the SPA and answer 200.
func TestZFSHelperPlainIsGETOnly(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()

	resp, err := srv.Client().Post(srv.URL+"/zfs/helper", "text/plain", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("POST /zfs/helper = 200 — this route is a GET, and a 200 to anything else means it "+
			"fell through to a handler that was not asked. status = %d", resp.StatusCode)
	}
}
