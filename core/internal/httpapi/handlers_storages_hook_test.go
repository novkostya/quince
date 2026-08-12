package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// qn.6e PR 4 — the seam over storage.CheckHook. The four outcomes are gated against the REAL helper
// script in internal/storage (G8); what is proven here is the endpoint: the 422 line, the verdict
// reaching the wire, and the auth guard.

func hookReq(t *testing.T, srv *httptest.Server, c *http.Client, body string) *http.Request {
	t.Helper()
	req := newReq(t, http.MethodPost, srv.URL+"/api/storages/probe/hook", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(auth.CSRFHeaderName, csrfFromJar(t, c, srv))
	return req
}

// A verdict about a real pair is a 200 EVEN WHEN THE HELPER IS UNREACHABLE. A user who has not
// installed the helper yet has asked a perfectly good question, and the whole point of the button is
// to answer it — mapping that to a 5xx would make the ordinary failing case look like a broken
// quince.
func TestHookCheckUnreachableIsATwoHundred(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	resp, err := c.Do(hookReq(t, srv, c, `{"parent_dataset":"tank/backups","ssh_user":"u","ssh_host":"nonexistent.invalid"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 — an unreachable helper is the ANSWER", resp.StatusCode)
	}
	var out wire.StorageHookCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Check.Outcome != "unreachable" {
		t.Fatalf("outcome = %q, want unreachable", out.Check.Outcome)
	}
	if out.Check.Reason == "" {
		t.Errorf("a verdict must carry quince's own sentence")
	}
}

// A malformed QUESTION is a 422 — the same line the probe draws, in the same shared error shape.
func TestHookCheckRefusesAMalformedQuestion(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	for _, tc := range []struct{ name, body, wantPath string }{
		{"no fields", `{}`, "parent_dataset"},
		{"no parent", `{"ssh_user":"u","ssh_host":"h"}`, "parent_dataset"},
		{"no ssh host", `{"parent_dataset":"tank/backups","ssh_user":"u"}`, "ssh_host"},
		{"no ssh user", `{"parent_dataset":"tank/backups","ssh_host":"h"}`, "ssh_user"},
		{"unknown field", `{"parent_dataset":"t/b","ssh_user":"u","ssh_host":"h","force":true}`, "parent_dataset"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := c.Do(hookReq(t, srv, c, tc.body))
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
			if len(out.Errors) == 0 || out.Errors[0].Path != tc.wantPath {
				t.Fatalf("422 must name the offending field; got %+v, want path %q", out.Errors, tc.wantPath)
			}
		})
	}
}

// THIS ENDPOINT EXECUTES A REQUEST-SUPPLIED ARGV, so the guard is not incidental. Asserted rather
// than trusted for the same reason as the probe: `authExempt` matches five literal strings today,
// and the hazard is a future change to the MATCHER, which no handler test would notice.
func TestHookCheckIsNotAuthExempt(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	req := newReq(t, http.MethodPost, srv.URL+"/api/storages/probe/hook",
		`{"parent_dataset":"tank/backups","ssh_user":"u","ssh_host":"h"}`)
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("an UNAUTHENTICATED hook check got 200 — this route executes a command and must " +
			"never be auth-exempt")
	}
}

// An invalid dataset name is refused BEFORE anything is executed. `/bin/true` would answer happily,
// so a 200 with `ok` here would mean the guard did not run.
func TestHookCheckRefusesAnUnsafeDatasetWithoutExecuting(t *testing.T) {
	deps := testDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	c := authedClient(t, srv)

	resp, err := c.Do(hookReq(t, srv, c,
		`{"parent_dataset":"tank/backups; rm -rf /","ssh_user":"u","ssh_host":"h"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out wire.StorageHookCheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if out.Check.Outcome == "ok" {
		t.Fatalf("an unsafe dataset name produced %q — the name guard did not run", out.Check.Outcome)
	}
}
