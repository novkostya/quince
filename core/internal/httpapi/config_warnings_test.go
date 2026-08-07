package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
)

// EVERY config response carries `warnings` as an ARRAY, never `null`.
//
// Operator-reported as an `Unexpected Application Error` on Settings —
// `null is not an object (evaluating 'n.warnings.length')` — that went away on refresh.
//
// A NIL GO SLICE MARSHALS AS `null`, NOT `[]`, and the wire type says array. `ConfigView` reads
// `data.warnings.length` and TypeScript offered no protection, because `ConfigResponse.warnings` is
// declared non-nullable: the TYPE was telling the truth about the contract while the server broke
// it. That is why this is asserted on the RAW JSON — a typed decode cannot tell `null` from `[]`,
// so a test that unmarshals into a struct passes against the bug.
//
// It is reached by the ordinary path rather than an edge: `Service.Snapshot` returns
// `append([]Warning(nil), s.warnings...)`, nil when there are none, and `Replace` CLEARS the
// warnings on every successful write — so the first save on a clean config hands the next reader a
// `null`. Refreshing fixed it because a re-read of an unwritten config had warnings again.
func TestConfigResponsesNeverSendNullWarnings(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)
	seedStorages(t, srv, c, oneStorage)

	csrf := func() string { return csrfFromJar(t, c, srv) }

	rawWarnings := func(t *testing.T, req *http.Request) json.RawMessage {
		t.Helper()
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var body map[string]json.RawMessage
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decoding %s %s: %v", req.Method, req.URL.Path, err)
		}
		w, ok := body["warnings"]
		if !ok {
			t.Fatalf("%s %s has no `warnings` key at all", req.Method, req.URL.Path)
		}
		return w
	}

	// The four responses that carry the config body. A write is included in each direction, because
	// the null is PRODUCED by a successful write clearing the warnings.
	add := newReq(t, http.MethodPost, srv.URL+"/api/config/storage",
		`{"name":"second","path":"/backups-b","backend":"copy"}`)
	add.Header.Set(auth.CSRFHeaderName, csrf())
	del := newReq(t, http.MethodDelete, srv.URL+"/api/config/storage/second", "")
	del.Header.Set(auth.CSRFHeaderName, csrf())

	for _, tc := range []struct {
		name string
		req  *http.Request
	}{
		{"POST /api/config/storage", add},
		{"DELETE /api/config/storage/{name}", del},
		{"GET /api/config", newReq(t, http.MethodGet, srv.URL+"/api/config", "")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := string(rawWarnings(t, tc.req))
			if got == "null" {
				t.Fatalf("%s sent `\"warnings\": null` — the wire type says array, and a client "+
					"reading `.warnings.length` crashes on it", tc.name)
			}
		})
	}
}
