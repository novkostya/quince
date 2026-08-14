package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
)

// qn.6e PR 9a — the first-run setup mode. Operator ruling 2026-08-07, option (a): any zero-storage
// start IS the onboarding state, so quince SERVES and refuses everything outside `setupAllowed`.
//
// What is proven here is the guard. That the daemon reaches this state at all — rather than exiting
// — is `main.go`'s, and is asserted separately.

func storagelessDeps(t *testing.T, required bool) Deps {
	t.Helper()
	d := testDeps(t)
	d.StorageRequired = func() bool { return required }
	return d
}

// THE MODE REFUSES, and this is the half that makes it honest. A daemon that listed devices and
// accepted pairing while having nowhere to put a backup is what `storagereq.go` calls "looks
// healthy and silently protects nothing" — the mode says so once, in one place, rather than relying
// on every subsystem to be separately honest.
func TestSetupModeRefusesEverythingOutsideSetup(t *testing.T) {
	srv := httptest.NewServer(NewRouter(storagelessDeps(t, true)))
	defer srv.Close()
	c := authedClient(t, srv)

	for _, path := range []string{"/api/devices", "/api/jobs", "/api/versions", "/api/storages"} {
		t.Run(path, func(t *testing.T) {
			req := newReq(t, http.MethodGet, srv.URL+path, "")
			code := doStatus(t, c, req)
			if code != http.StatusServiceUnavailable {
				t.Fatalf("%s = %d, want 503 while no storage is declared", path, code)
			}
		})
	}
}

// AND THE SETUP SURFACE STAYS OPEN — including the two probes, which are not an afterthought: the
// storage step's entire job is to let a user check a path and a helper BEFORE declaring anything, so
// a mode that refused them would serve a form that cannot fill itself in.
func TestSetupModeLeavesTheSetupSurfaceOpen(t *testing.T) {
	srv := httptest.NewServer(NewRouter(storagelessDeps(t, true)))
	defer srv.Close()
	c := authedClient(t, srv)

	if code := doStatus(t, c, newReq(t, http.MethodGet, srv.URL+"/api/config", "")); code != http.StatusOK {
		t.Errorf("GET /api/config = %d, want 200 — setup cannot proceed without it", code)
	}
	if code := doStatus(t, c, newReq(t, http.MethodGet, srv.URL+"/api/health", "")); code != http.StatusOK {
		t.Errorf("GET /api/health = %d, want 200 — a container healthcheck must not read a "+
			"waiting daemon as a dead one", code)
	}

	probe := newReq(t, http.MethodPost, srv.URL+"/api/storages/probe", `{"path":"/tmp"}`)
	probe.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	if code := doStatus(t, c, probe); code != http.StatusOK {
		t.Errorf("POST /api/storages/probe = %d, want 200 — the form cannot work without it", code)
	}

	add := newReq(t, http.MethodPost, srv.URL+"/api/config/storage",
		`{"name":"first","path":"/backups","backend":"copy"}`)
	add.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	if code := doStatus(t, c, add); code == http.StatusServiceUnavailable {
		t.Errorf("POST /api/config/storage was refused BY THE SETUP GUARD — the one write that " +
			"ends the mode must never be inside it")
	}
}

// THE ZFS BRANCH OF THE SAME FORM, WHICH WAS A DEADLOCK ON A CLEAN INSTALL (quince#818 B and C).
//
// The two probes above were exempted and these two were not, so on a genuinely storageless stand
// the zfs path 503'd at both buttons: no key could be generated and no helper could be rendered.
// Neither is skippable — `probe/hook` cannot answer until the helper is installed on the host, and
// the helper cannot be installed without the key and the script these two serve. The form offered
// three controls and the only one that worked was the one you cannot use yet.
//
// IT SURVIVED EVERY EXISTING GATE because the demo server the e2e drives always HAS a storage, so
// this guard never fires there. Found by walking onboarding on a clean stand instead, which is the
// one path nobody exercises twice.
func TestSetupModeLeavesTheZFSSetupSurfaceOpen(t *testing.T) {
	srv := httptest.NewServer(NewRouter(storagelessDeps(t, true)))
	defer srv.Close()
	c := authedClient(t, srv)

	key := newReq(t, http.MethodPost, srv.URL+"/api/storages/zfs/key",
		`{"parent_dataset":"tank/backups"}`)
	key.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	if code := doStatus(t, c, key); code == http.StatusServiceUnavailable {
		t.Errorf("POST /api/storages/zfs/key was refused BY THE SETUP GUARD — the first-run zfs " +
			"form cannot show the operator a key to install without it")
	}

	helper := newReq(t, http.MethodGet, srv.URL+"/api/storages/zfs/helper", "")
	if code := doStatus(t, c, helper); code == http.StatusServiceUnavailable {
		t.Errorf("GET /api/storages/zfs/helper was refused BY THE SETUP GUARD — the operator " +
			"cannot install a helper they cannot be shown")
	}
}

// THE GUARD RUNS AFTER auth, not before, and the order is a disclosure decision rather than a
// detail. Setup mode is a fact about a configured-but-unfinished install; a stranger who can reach
// the port must get a 401, not "this quince is not set up yet".
func TestSetupModeDoesNotLeakToAnUnauthenticatedCaller(t *testing.T) {
	srv := httptest.NewServer(NewRouter(storagelessDeps(t, true)))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/api/devices")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("an anonymous request got %d, want 401 — setup state is not public", resp.StatusCode)
	}
}

// THE MODE CLEARS WITHOUT A RESTART. `StorageRequired` is a function read per request, not a
// boolean captured at wiring time, precisely so a daemon that has just been configured stops
// refusing its own API. Flipping the closure is what a successful add does in production.
func TestSetupModeEndsWhenAStorageExists(t *testing.T) {
	required := true
	d := testDeps(t)
	d.StorageRequired = func() bool { return required }
	srv := httptest.NewServer(NewRouter(d))
	defer srv.Close()
	c := authedClient(t, srv)

	if code := doStatus(t, c, newReq(t, http.MethodGet, srv.URL+"/api/devices", "")); code != http.StatusServiceUnavailable {
		t.Fatalf("precondition: want 503 while storageless, got %d", code)
	}

	required = false

	if code := doStatus(t, c, newReq(t, http.MethodGet, srv.URL+"/api/devices", "")); code == http.StatusServiceUnavailable {
		t.Fatalf("STILL REFUSING after a storage exists — the mode was captured rather than read, " +
			"which would leave a configured daemon needing the restart this rung removes")
	}
}

// NIL IS NEVER REQUIRED, so `--demo` and every test that does not wire it are untouched. Asserted
// because the guard is in the shared chain: a default that refused would break every existing
// caller at once.
func TestSetupGuardIsInertWhenUnwired(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	if code := doStatus(t, c, newReq(t, http.MethodGet, srv.URL+"/api/devices", "")); code == http.StatusServiceUnavailable {
		t.Fatalf("an unwired StorageRequired refused a request; nil must mean never required")
	}
}
