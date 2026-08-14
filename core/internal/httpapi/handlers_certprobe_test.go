package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

const certProbePath = "/api/onboarding/certificate"

// postCertProbe does the double submit a browser does.
//
// THIS ENDPOINT IS NOT `csrfExempt`, AND THAT IS DELIBERATE — unlike `POST /api/auth/setup` and
// `POST /api/config/insecure-transport`, which are. `ensureCSRF` mints the cookie on EVERY request
// including the exempt ones, so a page that has loaded at all already holds a token and `api.post`
// sends it back. Being pre-auth does not mean being unable to double-submit, so the protection is
// kept rather than waived.
//
// The first version of this suite skipped the ceremony and every case came back `403 csrf` — which
// is the guard working, and is why the helper exists rather than an exemption.
func postCertProbe(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()

	// A GET first, exactly as a browser loading the page does, to be handed the cookie.
	warm := httptest.NewRecorder()
	h.ServeHTTP(warm, httptest.NewRequest(http.MethodGet, "http://quince.example:8968/api/auth/status", nil))
	var token string
	for _, c := range warm.Result().Cookies() {
		if c.Name == auth.CSRFCookieName {
			token = c.Value
		}
	}
	if token == "" {
		t.Fatal("no CSRF cookie was minted by a pre-auth GET — the double submit cannot be performed")
	}

	req := httptest.NewRequest(http.MethodPost, "http://quince.example:8968"+certProbePath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: token})
	req.Header.Set(auth.CSRFHeaderName, token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// THE WINDOW IS THE UNCLAIMED INSTALL, AND THIS IS THE SECURITY CLAIM OF THE ENDPOINT.
//
// It opens a path the caller names, before authentication, so it can tell a stranger whether a file
// exists and whether it parses. That is bounded by the SAME argument the Operator ruled for the
// pre-auth config write (quince#908 §3): in that window `POST /api/auth/setup` is itself authExempt
// and one-shot, so anyone reaching the port can claim the install outright — and an admin can point
// `tls.cert_file` anywhere and read the same load error. `Configured()` shuts it the instant the
// install is claimed, and asserting that is the whole of this test.
func TestTheCertificateProbeClosesOnceTheInstallIsClaimed(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"a well-formed request", `{"cert_file":"/tmp/c.pem","key_file":"/tmp/c.key"}`},
		// THE BODY IS NEVER READ ON A CLAIMED INSTALL. If the 409 moved below the decode or the path
		// validation, these would be 400 and 422 — which tells a caller the endpoint exists and
		// reached its parser, and distinguishes a claimed install from an unclaimed one by the shape
		// of the refusal.
		{"a body that does not parse", `{"cert_file":`},
		{"a relative path, which is otherwise a 422", `{"cert_file":"c.pem","key_file":"c.key"}`},
		{"no body at all", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps(t)
			if err := deps.Auth.SetPassword("test", "127.0.0.1"); err != nil {
				t.Fatalf("SetPassword: %v", err)
			}
			if rec := postCertProbe(t, NewRouter(deps), tc.body); rec.Code != http.StatusConflict {
				t.Fatalf("status %d, want 409 — body %s", rec.Code, rec.Body.String())
			}
		})
	}
}

// A MALFORMED QUESTION IS A 422 AND NAMES THE FIELD; everything else is an ANSWER carried in the
// body. That split is `StorageProbe`'s and it is what lets a form highlight the offending input
// rather than showing one sentence above the whole thing.
func TestTheCertificateProbeRefusesAMalformedQuestion(t *testing.T) {
	for _, tc := range []struct{ name, body, wantField string }{
		{"no cert_file", `{"key_file":"/tmp/c.key"}`, "cert_file"},
		{"no key_file", `{"cert_file":"/tmp/c.pem"}`, "key_file"},
		// A RELATIVE PATH NAMES A FILE THE OPERATOR AND QUINCE CANNOT AGREE ON — quince's working
		// directory is not theirs, so checking `./cert.pem` would check a different file from the one
		// that would be served.
		{"a relative cert path", `{"cert_file":"c.pem","key_file":"/tmp/c.key"}`, "cert_file"},
		{"a relative key path", `{"cert_file":"/tmp/c.pem","key_file":"c.key"}`, "key_file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postCertProbe(t, NewRouter(testDeps(t)), tc.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status %d, want 422 — body %s", rec.Code, rec.Body.String())
			}
			var got struct {
				Errors []wire.ConfigError `json:"errors"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v — %s", err, rec.Body.String())
			}
			if len(got.Errors) != 1 || got.Errors[0].Path != tc.wantField {
				t.Errorf("errors = %+v, want one at %q", got.Errors, tc.wantField)
			}
		})
	}
}

// A CERTIFICATE THAT IS SIMPLY WRONG IS A 200 WITH A VERDICT, not an HTTP error. "That file is not
// there" is the ANSWER to the question asked — the client renders it as a result, and a 4xx would
// make a form treat a successful check as a failed request.
func TestAnUnreadablePairIsAnAnswerRatherThanAnError(t *testing.T) {
	rec := postCertProbe(t, NewRouter(testDeps(t)),
		`{"cert_file":"/nonexistent/quince.pem","key_file":"/nonexistent/quince.key","hostname":"quince.example"}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — a wrong certificate is an answer, not a failed request", rec.Code)
	}
	var got wire.CertificateProbe
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v — %s", err, rec.Body.String())
	}
	if got.Outcome != "unreadable" {
		t.Errorf("outcome = %q, want unreadable — %s", got.Outcome, got.Reason)
	}
	// THE REASON NAMES THE FILE, which is the rule the offline inspector's own test also pins and
	// the thing quince#940's sweep found missing across this surface.
	if !strings.Contains(got.Reason, "/nonexistent/quince.pem") {
		t.Errorf("reason does not name the file: %q", got.Reason)
	}
	// AND THE REQUEST IS ECHOED, so a form can show the verdict beside the user's own typing.
	if got.CertFile != "/nonexistent/quince.pem" || got.Hostname != "quince.example" {
		t.Errorf("request not echoed: %+v", got)
	}
}
