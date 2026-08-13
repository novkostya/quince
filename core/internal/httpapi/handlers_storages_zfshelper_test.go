package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/wire"
)

// quince#818 piece C — GET /api/storages/zfs/helper.

func getHelper(t *testing.T, srv *httptest.Server, c *http.Client, parent string) *http.Response {
	t.Helper()
	u := srv.URL + "/api/storages/zfs/helper?parent_dataset=" + url.QueryEscape(parent)
	resp, err := c.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// THE HAPPY PATH, and the assertion that matters is the ABSENCE of the placeholder. An operator
// installing a script that still says `pool/path/to/iphone-backup` gets a file that runs, refuses
// nothing it should refuse, and points every backup at a dataset that is not theirs.
func TestZFSHelperEndpointRendersTheOperatorsParent(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	resp := getHelper(t, srv, c, "tank/backups/iphone")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var got wire.StorageZFSHelperResponse
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Script, `PARENT="tank/backups/iphone"`) {
		t.Error("the operator's dataset is not in the served script")
	}
	if strings.Contains(got.Script, "pool/path/to/iphone-backup") {
		t.Error("the placeholder survived into the response — that script backs up to the wrong place")
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

// A DATASET NAME THAT COULD BREAK OUT OF THE QUOTES IS A 422, NOT A 500 AND NOT A RENDER.
//
// This is the sharpest edge on this endpoint: the value lands inside a double-quoted assignment in a
// script the operator runs as root on their storage host. Refusing is right rather than escaping —
// every legal ZFS name already passes, so nothing valid is lost, and the refusal names the field.
func TestZFSHelperEndpointRefusesAnUnsafeParent(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	for _, bad := range []string{
		`tank"; rm -rf /; x="`,
		"tank/backups; id",
		"tank/backups $(id)",
		"",
	} {
		resp := getHelper(t, srv, c, bad)
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("parent_dataset=%q → %d, want 422", bad, resp.StatusCode)
		}
		_ = resp.Body.Close()
	}
}

// THE REFUSAL NAMES THE FIELD THE OPERATOR FILLED IN, so the form can put the message where they are
// looking. A 422 whose path points at some other field sends them to re-check something correct —
// the defect quince#865 was reviewed for on this same form.
func TestZFSHelperEndpointRefusalNamesParentDataset(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	resp := getHelper(t, srv, c, "tank/backups; id")
	defer func() { _ = resp.Body.Close() }()

	var got struct {
		Errors []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Errors) != 1 || got.Errors[0].Path != "parent_dataset" {
		t.Fatalf("errors = %+v, want one naming parent_dataset", got.Errors)
	}
}

// THE REFUSAL NAMES THE MISTAKE, NOT JUST THE RULE.
//
// A filesystem path is what a user actually types here, because the field DIRECTLY ABOVE this one
// on the same form takes one — `/backups` — so carrying it down is the obvious move. The message
// read "must be a valid ZFS dataset name": true, unarguable, and no use at all to somebody looking
// at `/backups` and seeing nothing wrong with it. Reported from a phone on a first-run stand,
// 2026-08-13, where it was the only thing between the operator and a working storage.
//
// ASSERTED PER FACT rather than as one substring of the whole sentence, so a reword that drops the
// path-vs-dataset distinction fails even if what remains still reads well.
func TestZFSHelperRefusalTellsThemItIsNotAPath(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)

	// The exact value a user carries down from the Path field above it.
	resp := getHelper(t, srv, c, "/backups")
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}

	var got struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Errors) != 1 {
		t.Fatalf("errors = %+v, want exactly one", got.Errors)
	}
	msg := got.Errors[0].Message

	for _, want := range []struct{ fact, why string }{
		{"no leading `/`", "the single thing wrong with the value they typed"},
		{"field above", "which field they took it from — both are on one screen"},
		{"rpool/quince", "an example, because the rule alone does not show the shape"},
	} {
		if !strings.Contains(msg, want.fact) {
			t.Errorf("the refusal does not carry %q — %s.\ngot: %s", want.fact, want.why, msg)
		}
	}
}
