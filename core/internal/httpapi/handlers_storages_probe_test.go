package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/wire"
)

// qn.6e PR 3 — POST /api/storages/probe.
//
// The probe's own logic is gated in internal/storage (G1–G4b). What is proven HERE is the seam: the
// route exists and is addressable, the 422 line falls where the contract says, refusals arrive as
// 200 with the daemon's sentence, and the endpoint is behind the auth guard.

func probeReq(t *testing.T, srv *httptest.Server, c *http.Client, body string) *http.Request {
	t.Helper()
	req := newReq(t, http.MethodPost, srv.URL+"/api/storages/probe", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	return req
}

func doProbe(t *testing.T, srv *httptest.Server, c *http.Client, body string) (int, wire.StorageProbe) {
	t.Helper()
	resp, err := c.Do(probeReq(t, srv, c, body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, wire.StorageProbe{}
	}
	var out wire.StorageProbeResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding a 200: %v", err)
	}
	return resp.StatusCode, out.Probe
}

// A REFUSAL ABOUT THE WORLD IS A 200, carrying the daemon's own sentence.
//
// This is the contract decision most likely to be "corrected" by someone who reads a non-existent
// path as an error: it is the ANSWER to what-is-this-path, and a form renders it beside the same
// field, in the same place, as a success. Mapping it to a 404 would also drop `marker` and the
// free-space figures, which are reported on refusals too.
func TestProbeAnswersRefusalsWithTwoHundred(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	missing := filepath.Join(t.TempDir(), "no", "such", "dir")
	code, p := doProbe(t, srv, c, `{"path":`+jsonString(missing)+`}`)

	if code != http.StatusOK {
		t.Fatalf("a missing path = %d, want 200 — the refusal IS the answer", code)
	}
	if p.Outcome != string(storage.InspectMissing) {
		t.Fatalf("outcome = %q, want %q", p.Outcome, storage.InspectMissing)
	}
	if !strings.Contains(p.Reason, missing) {
		t.Errorf("reason %q does not name the path (quince#514)", p.Reason)
	}
	if p.Backend != "" {
		t.Errorf("a refusal carried a backend recommendation: %q", p.Backend)
	}
	// AND THE PATH IS STILL ABSENT. The endpoint inherits Inspect's guarantee, and asserting it
	// here too is the point: this is the first caller, and a future handler that "helpfully"
	// creates the directory would pass every test in internal/storage.
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("THE ENDPOINT CREATED THE PATH: stat err = %v", err)
	}
}

func TestProbeReportsAnExistingDirectory(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	dir := t.TempDir()
	code, p := doProbe(t, srv, c, `{"path":`+jsonString(dir)+`}`)

	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if p.Outcome != string(storage.InspectNew) {
		t.Fatalf("outcome = %q, want %q (reason %q)", p.Outcome, storage.InspectNew, p.Reason)
	}
	switch p.Backend {
	case storage.BackendReflink, storage.BackendHardlink, storage.BackendCopy, storage.BackendZFS:
	default:
		t.Fatalf("backend = %q, want a concrete backend", p.Backend)
	}
	if p.Marker != nil {
		t.Errorf("a markerless dir reported a marker: %+v", p.Marker)
	}
	if p.CleanPath == "" || p.Path == "" {
		t.Errorf("both path forms must be reported; got %q / %q", p.Path, p.CleanPath)
	}
	if p.FilesystemTotalBytes == 0 {
		t.Errorf("filesystem_total_bytes = 0; statfs should have answered")
	}
}

// The marker reaches the wire as a SUBSET — no checksum, no app_version. Publishing those would
// freeze quince's own integrity detail and its version history into a form's contract.
func TestProbePublishesTheMarkerAsASubset(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	dir := t.TempDir()
	if err := storage.WriteStorageMarker(dir, storage.StorageMarker{
		StorageID: "01JSTOR", Backend: storage.BackendZFS,
		CreatedAt: "2026-08-07T00:00:00Z", AppVersion: "test",
	}); err != nil {
		t.Fatal(err)
	}

	resp, err := c.Do(probeReq(t, srv, c, `{"path":`+jsonString(dir)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var raw map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	probe, _ := raw["probe"].(map[string]any)
	marker, ok := probe["marker"].(map[string]any)
	if !ok {
		t.Fatalf("marker absent from an adopt: %v", probe)
	}
	if got := probe["outcome"]; got != string(storage.InspectAdopt) {
		t.Errorf("outcome = %v, want adopt", got)
	}
	if got := probe["backend"]; got != storage.BackendZFS {
		t.Errorf("backend = %v, want the marker's %q", got, storage.BackendZFS)
	}
	for _, banned := range []string{"checksum", "app_version"} {
		if _, present := marker[banned]; present {
			t.Errorf("marker published %q; it is a SUBSET on purpose", banned)
		}
	}
	for _, want := range []string{"storage_id", "backend", "created_at"} {
		if _, present := marker[want]; !present {
			t.Errorf("marker is missing %q", want)
		}
	}
}

// A MALFORMED QUESTION IS A 422 — and only a malformed question. The non-absolute case sits on this
// side deliberately, matching config/validate.go, so the form's refusal and the config's refusal say
// the same thing about the same string rather than disagreeing.
func TestProbeRefusesAMalformedQuestionWith422(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	for _, tc := range []struct{ name, body string }{
		{"no path field", `{}`},
		{"empty path", `{"path":""}`},
		{"relative path", `{"path":"relative/dir"}`},
		{"not an object", `"/backups"`},
		{"unknown field", `{"path":"/backups","recurse":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := c.Do(probeReq(t, srv, c, tc.body))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", resp.StatusCode)
			}
			var out struct {
				Errors []wire.ConfigError `json:"errors"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			// The SAME shape as every other 422 in the API, so a client that renders one renders
			// this one — contracts §1's {errors:[{path,message}]}.
			if len(out.Errors) == 0 || out.Errors[0].Path != "path" || out.Errors[0].Message == "" {
				t.Fatalf("422 body is not the shared shape: %+v", out.Errors)
			}
		})
	}
}

// THE ENDPOINT IS BEHIND THE AUTH GUARD, and this asserts it rather than trusting that adding a
// route to the mux was enough. `authExempt` is five literal method+path strings; the hazard is a
// future change to the MATCHER (a prefix rule, say) silently exempting this one, which no test of
// the handler itself would notice.
//
// It matters more here than for a read: the probe touches the filesystem on a caller-supplied path.
func TestProbeIsNotAuthExempt(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	anon := srv.Client()
	req := newReq(t, http.MethodPost, srv.URL+"/api/storages/probe", `{"path":"/backups"}`)
	req.Header.Set("Content-Type", "application/json")
	resp, err := anon.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("an UNAUTHENTICATED probe got 200 — this route must not be auth-exempt")
	}
}

// `/probe` is a literal segment beside `/{name}/recheck`. They differ in segment count, so a storage
// may be NAMED "probe" without shadowing the endpoint — asserted because "add a literal beside a
// wildcard" is exactly the shape that bites in a router that patterns on prefixes.
func TestProbeDoesNotShadowRecheck(t *testing.T) {
	deps := testDeps(t)
	fake := &idlessStorages{}
	deps.Storages = fake
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	req := newReq(t, http.MethodPost, srv.URL+"/api/storages/probe/recheck", "")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	// Not a 200 (no storage is named "probe" in the fake), but it must reach the RECHECK handler and
	// be told so, rather than being swallowed by the probe route.
	if code := doStatus(t, c, req); code != http.StatusNotFound {
		t.Fatalf("POST /api/storages/probe/recheck = %d, want 404 from the recheck handler", code)
	}
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
