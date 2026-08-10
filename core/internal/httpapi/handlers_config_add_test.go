package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// qn.6e PR 5 — POST /api/config/storage, Forget's mirror.
//
// config.AddStorage's own rules are gated in internal/config, INCLUDING the splice-keeps-siblings
// claim, which is asserted there on the file itself. What is proven here is the seam: the route
// exists and is reachable, a refusal is a 422 in the shared shape carrying the CALLER'S field, a
// success returns the config-endpoint body, and the route is behind the auth guard.

// oneStorage is seedStorages' single-entry counterpart to twoStorages, carrying the same fully
// specified shape a client sends.
const oneStorage = `[{"name":"one","path":"/backups-a","default":true,"backend":"reflink",` +
	`"zfs":{"parent_dataset":"","mode":"hook","hook_cmd":"","seed":"auto"},` +
	`"retention":{"keep_recent":10,"keep_daily":30,"keep_weekly":12}}]`

func addStorage(t *testing.T, srv *httptest.Server, c *http.Client, body string) (int, []byte) {
	t.Helper()
	req := newReq(t, http.MethodPost, srv.URL+"/api/config/storage", body)
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, b
}

func storageNames(t *testing.T, srv *httptest.Server, c *http.Client) []string {
	t.Helper()
	req := newReq(t, http.MethodGet, srv.URL+"/api/config", "")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out struct {
		Config struct {
			Storage []struct {
				Name    string `json:"name"`
				Backend string `json:"backend"`
			} `json:"storage"`
		} `json:"config"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(out.Config.Storage))
	for _, s := range out.Config.Storage {
		names = append(names, s.Name)
	}
	return names
}

func TestAddStorageEndpointAddsAndReturnsTheConfigBody(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, oneStorage)

	code, body := addStorage(t, srv, c, `{"name":"second","path":"/backups-b","backend":"reflink"}`)
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", code, body)
	}

	// THE CONFIG-ENDPOINT BODY, not a 201 with the entry: this is a config mutation, and the client
	// re-renders from the same payload GET, PUT and DELETE hand it.
	var out struct {
		Config   map[string]any `json:"config"`
		Warnings []any          `json:"warnings"`
		Source   map[string]any `json:"source"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decoding the 200: %v — %s", err, body)
	}
	if out.Config == nil || out.Source == nil {
		t.Fatalf("want the {config, warnings, source} body, got: %s", body)
	}

	got := storageNames(t, srv, c)
	if len(got) != 2 || got[0] != "one" || got[1] != "second" {
		t.Fatalf("storages after the add = %v, want [one second]", got)
	}
}

// A REFUSAL NAMES THE CALLER'S FIELD, not an index in the merged list. A client adding one entry
// cannot map `storage[3].path` back to the box the user typed into.
func TestAddStorageEndpointRefusesAtTheCallersField(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, oneStorage)

	for _, tc := range []struct{ name, body, wantPath string }{
		{"duplicate name", `{"name":"one","path":"/backups-b","backend":"copy"}`, "name"},
		{"duplicate path", `{"name":"two","path":"/backups-a","backend":"copy"}`, "path"},
		{"relative path", `{"name":"two","path":"rel/dir","backend":"copy"}`, "path"},
		{"no backend", `{"name":"two","path":"/backups-b"}`, "backend"},
		{"claims default", `{"name":"two","path":"/backups-b","backend":"copy","default":true}`, "default"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, body := addStorage(t, srv, c, tc.body)
			if code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422: %s", code, body)
			}
			var out struct {
				Errors []wire.ConfigError `json:"errors"`
			}
			if err := json.Unmarshal(body, &out); err != nil {
				t.Fatal(err)
			}
			if len(out.Errors) == 0 || out.Errors[0].Path != tc.wantPath {
				t.Fatalf("422 must name the caller's field; got %+v, want %q", out.Errors, tc.wantPath)
			}
		})
	}

	// AND NOTHING WAS ADDED by any of them. Asserted after the whole table rather than per case,
	// because the failure this catches is cumulative: a refusal that still writes.
	if got := storageNames(t, srv, c); len(got) != 1 {
		t.Fatalf("a REFUSED add still changed the config: %v", got)
	}
}

// Behind the auth guard, like every other config mutation.
func TestAddStorageEndpointIsNotAuthExempt(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	req := newReq(t, http.MethodPost, srv.URL+"/api/config/storage",
		`{"name":"two","path":"/backups-b","backend":"copy"}`)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("an UNAUTHENTICATED add got 200 — this writes config and must never be auth-exempt")
	}
}
