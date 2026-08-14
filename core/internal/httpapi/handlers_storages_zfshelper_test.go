package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/wire"
)

// quince#818 piece C — GET /api/storages/zfs/helper. quince#985 took its parameter away.

func getHelper(t *testing.T, srv *httptest.Server, c *http.Client) *http.Response {
	t.Helper()
	resp, err := c.Get(srv.URL + "/api/storages/zfs/helper")
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// THE WHOLE SCRIPT, SAVEABLE AS-IS, AND THE PATH IT GOES TO.
//
// The assertion that used to matter here was the absence of the placeholder dataset, because the
// script was rendered per install. It is now a constant, so what matters instead is that the served
// bytes ARE the embedded bytes — a response that merely looked like a script would install fine and
// refuse everything at the first backup.
func TestZFSHelperEndpointServesTheEmbeddedScript(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	resp := getHelper(t, srv, c)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got wire.StorageZFSHelperResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Script != storage.ZFSHelperScript() {
		t.Error("the served script is not the embedded one, which is the file the G8 gate executes")
	}
	// IT IS THE WHOLE SCRIPT, not an excerpt: the operator saves this file and nothing else.
	if !strings.HasPrefix(got.Script, "#!/bin/sh") {
		t.Error("the response is not a complete script — it must be saveable as-is")
	}
	for _, arm := range []string{"create)", "snapshot)", "destroy)", "rollback)", "list)", "capacity)"} {
		if !strings.Contains(got.Script, arm) {
			t.Errorf("the served helper is missing the %s arm", arm)
		}
	}
	// THE DESTINATION IS HALF THE INSTRUCTION, and it is the same constant the authorized_keys line
	// pins — a helper saved anywhere else is simply never reached.
	if got.Path != storage.ZFSHelperPath {
		t.Errorf("path = %q, want %q", got.Path, storage.ZFSHelperPath)
	}
}

// NOTHING INSTALLATION-SPECIFIC COMES BACK, which is what quince#985 bought and what piece 3 rests
// on: two operators on the same version get identical bytes, so the script can be compared by hash,
// linked to, or rendered on a page without asking whose it is.
//
// ASSERTED AS AN EQUALITY ACROSS TWO REQUESTS WITH DIFFERENT STORAGES CONFIGURED, rather than as a
// substring check for a dataset that happens not to be there. A response that varied by config would
// pass a "does it contain tank/backups" test on an install where nobody had typed that.
func TestZFSHelperEndpointAnswerDoesNotDependOnTheInstall(t *testing.T) {
	first := func() string {
		srv := httptest.NewServer(NewRouter(testDeps(t)))
		defer srv.Close()
		resp := getHelper(t, srv, authedClient(t, srv))
		defer func() { _ = resp.Body.Close() }()
		var got wire.StorageZFSHelperResponse
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		return got.Script
	}
	a, b := first(), first()
	if a != b {
		t.Fatal("two installs got different helper scripts — the file is meant to be static since " +
			"quince#985, and one path per host is only safe while it is")
	}
	if a == "" {
		t.Fatal("the served script is empty; this test would pass vacuously")
	}
	// The old per-install marker, asserted absent by NAME so a re-rendered `PARENT=` cannot creep
	// back without this failing.
	if strings.Contains(a, `PARENT="tank`) || strings.Contains(a, "pool/path/to/iphone-backup") {
		t.Error("the served script names a dataset — the parent belongs in the forced command")
	}
}
