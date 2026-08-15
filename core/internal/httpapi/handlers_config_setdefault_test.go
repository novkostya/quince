package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/config"
)

// quince#722 — POST /api/config/storage/{name}/default, the third case Add and Forget both point at.
//
// The harness is `seedStorages` + `twoStorages` from the forget test, deliberately: this route is
// that one's sibling, and a second seeding helper is how the two would drift apart.

func setDefaultStorage(t *testing.T, srv *httptest.Server, c *http.Client, name string) (int, []byte) {
	t.Helper()
	req := newReq(t, http.MethodPost, srv.URL+"/api/config/storage/"+name+"/default", "")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, body
}

func decodeConfigBody(t *testing.T, body []byte) config.Config {
	t.Helper()
	var got struct {
		Config config.Config `json:"config"`
		Source config.Source `json:"source"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("response is not the config shape: %v: %s", err, body)
	}
	if got.Source.Path == "" {
		t.Error("the body must carry `source`, like every other config endpoint")
	}
	return got.Config
}

// THE FLAG MOVES AND THE ORDER DOES NOT. That is the ruling in one assertion: `shuttle` becomes the
// default while staying the SECOND entry in the file, because `declaredStorages` hoists on the flag
// when slots are built and the document itself does not have to be reordered (quince#722).
//
// Asserting the order explicitly rather than only the flag, because a splice-and-move implementation
// would satisfy every other check here while quietly making file position meaningful again — the
// thing this ruling removed, and the reason a hand-edit is now one edit.
func TestSetDefaultMovesTheFlagAndLeavesTheFileOrderAlone(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	code, body := setDefaultStorage(t, srv, c, "shuttle")
	if code != http.StatusOK {
		t.Fatalf("POST .../shuttle/default = %d, want 200: %s", code, body)
	}

	cfg := decodeConfigBody(t, body)
	if cfg.Storage == nil || len(*cfg.Storage) != 2 {
		t.Fatalf("the body must carry both storages; got %+v", cfg.Storage)
	}
	entries := *cfg.Storage
	if entries[0].Name != "pool" || entries[1].Name != "shuttle" {
		t.Errorf("file order must be untouched — want [pool shuttle], got [%s %s]",
			entries[0].Name, entries[1].Name)
	}
	if entries[0].Default {
		t.Error("pool must no longer be default — exactly one entry carries the flag")
	}
	if !entries[1].Default {
		t.Error("shuttle must be the default; the flag is what decides, not the position")
	}
}

// EXACTLY ONE ENTRY IS FLAGGED, which is what `Validate` requires and what makes the 422 path
// unreachable here. Asserted on the response rather than trusted from the loop that sets it: a
// future edit that forgot to clear the incumbent would produce a document `PUT` itself would refuse.
func TestSetDefaultLeavesExactlyOneFlaggedEntry(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	_, body := setDefaultStorage(t, srv, c, "shuttle")
	cfg := decodeConfigBody(t, body)

	var n int
	for _, e := range *cfg.Storage {
		if e.Default {
			n++
		}
	}
	if n != 1 {
		t.Errorf("exactly one storage must be marked default, got %d", n)
	}
}

// THE SURVIVING ENTRY KEEPS THE KEYS NO CARD RENDERS — `zfs:` and `retention:`.
//
// This is gap B's argument in test form, and it is the whole reason this is a narrow route rather
// than a `PUT`. A client reconstructing the document from what it displayed would zero both, because
// no storage surface in the UI shows either. The splice happens server-side over the live parsed
// config, so the values that come back are the values that were loaded.
func TestSetDefaultKeepsKeysNoUISurfaceRenders(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	_, body := setDefaultStorage(t, srv, c, "shuttle")
	cfg := decodeConfigBody(t, body)

	for _, e := range *cfg.Storage {
		if e.Retention == nil {
			t.Fatalf("storage %q lost its retention block — the splice must preserve it", e.Name)
		}
		if e.Retention.KeepRecent != 10 || e.Retention.KeepDaily != 30 || e.Retention.KeepWeekly != 12 {
			t.Errorf("storage %q retention changed: %+v", e.Name, *e.Retention)
		}
		if e.ZFS.Seed != "auto" {
			t.Errorf("storage %q lost its zfs settings: %+v", e.Name, e.ZFS)
		}
	}
}

// ALREADY THE DEFAULT IS A 200 AND A NO-OP — the implementer's call, delegated by the ruling.
//
// It asserts a STATE rather than issuing a command, so a request for the state the system is
// already in has been satisfied. The alternative was a 422, whose remedy would be *do nothing* —
// the unfollowable-remedy shape that is the entire subject of quince#722.
func TestSetDefaultOnTheStorageThatAlreadyIsOneAnswers200(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	code, body := setDefaultStorage(t, srv, c, "pool")
	if code != http.StatusOK {
		t.Fatalf("POST .../pool/default when pool already is = %d, want 200: %s", code, body)
	}

	cfg := decodeConfigBody(t, body)
	entries := *cfg.Storage
	if !entries[0].Default || entries[1].Default {
		t.Errorf("the declaration must be unchanged — want pool default and shuttle not, got %v/%v",
			entries[0].Default, entries[1].Default)
	}
}

// 404 for a name nobody declared, matching the DELETE. It is a fact about the request, not a
// refusal with a remedy, so it does not use the config-error shape.
func TestSetDefaultOnAnUnknownStorageIs404(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	code, body := setDefaultStorage(t, srv, c, "no-such-disk")
	if code != http.StatusNotFound {
		t.Fatalf("POST .../no-such-disk/default = %d, want 404: %s", code, body)
	}
}

// THE REMEDY THE FORGET REFUSAL NAMES NOW WORKS, END TO END — which is the issue, not a detail.
//
// `DELETE` refuses the default with *"Make another storage the default first, then forget this
// one."* Before this route that sentence named a control the product did not have. This drives it:
// refuse, re-designate, forget, and the storage that could not be removed is gone.
func TestTheForgetRefusalsRemedyIsNowFollowable(t *testing.T) {
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, twoStorages)

	if code, body := deleteStorage(t, srv, c, "pool"); code != http.StatusUnprocessableEntity {
		t.Fatalf("setup: forgetting the default must refuse first = %d, want 422: %s", code, body)
	}
	if code, body := setDefaultStorage(t, srv, c, "shuttle"); code != http.StatusOK {
		t.Fatalf("the remedy the refusal names = %d, want 200: %s", code, body)
	}
	code, body := deleteStorage(t, srv, c, "pool")
	if code != http.StatusOK {
		t.Fatalf("forgetting the former default after re-designation = %d, want 200: %s", code, body)
	}

	cfg := decodeConfigBody(t, body)
	if cfg.Storage == nil || len(*cfg.Storage) != 1 {
		t.Fatalf("one storage must remain; got %+v", cfg.Storage)
	}
	if (*cfg.Storage)[0].Name != "shuttle" || !(*cfg.Storage)[0].Default {
		t.Errorf("the survivor must be shuttle and still default, got %+v", (*cfg.Storage)[0])
	}
}
