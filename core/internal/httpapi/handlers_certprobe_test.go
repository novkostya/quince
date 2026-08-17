package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// `names` IS AN ARRAY ON EVERY OUTCOME, AND THIS ASSERTS THE BYTES RATHER THAN THE DECODED VALUE.
//
// THAT IS THE WHOLE POINT OF THE TEST. `json.Unmarshal` into a `[]string` cannot tell `null` from
// `[]` — both leave the field empty — so a Go test that decodes the response agrees with a client
// that crashes on it. The test directly above this one already covered the unreadable pair and was
// green while the page it serves died on `names.length`.
//
// The cases are the outcomes that return before there is a leaf to read names from: the pair does
// not load, and the pair loads as PEM but is not a certificate.
func TestTheProbeReportsNamesAsAnArrayOnEveryFailure(t *testing.T) {
	dir := t.TempDir()
	junkCert := filepath.Join(dir, "junk.pem")
	junkKey := filepath.Join(dir, "junk.key")
	for _, f := range []string{junkCert, junkKey} {
		if err := os.WriteFile(f, []byte("not pem at all\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
	}

	for _, tc := range []struct{ name, body, wantOutcome string }{
		{
			"a pair that is not there",
			`{"cert_file":"/nonexistent/quince.pem","key_file":"/nonexistent/quince.key"}`,
			"unreadable",
		},
		{
			"files that hold no PEM",
			`{"cert_file":"` + junkCert + `","key_file":"` + junkKey + `"}`,
			"malformed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := postCertProbe(t, NewRouter(testDeps(t)), tc.body)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 — %s", rec.Code, rec.Body.String())
			}

			var raw map[string]json.RawMessage
			if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
				t.Fatalf("decode: %v — %s", err, rec.Body.String())
			}
			if got := string(raw["outcome"]); got != `"`+tc.wantOutcome+`"` {
				t.Errorf("outcome = %s, want %q", got, tc.wantOutcome)
			}
			if got := string(raw["names"]); got != "[]" {
				t.Errorf("names = %s, want [] — a client reading this as a list gets nothing to read", got)
			}
		})
	}
}

// THE PROBE REPORTS THE ADDRESS THE CALLER IS STANDING ON, AND WHETHER THE PAIR COVERS IT.
//
// That is the question an empty `hostname` leaves unanswered — and empty means *keep using the
// address I am on*, so it is the only case where the answer decides anything. The verdict is
// untouched by it: a certificate that does not cover the caller's address is still `usable`, because
// a self-signed pair or an IP-only LAN install is legitimate and this route must not refuse one.
func TestTheProbeReportsTheAddressTheCallerIsOn(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "quince.example", time.Now().Add(24*time.Hour))

	body := `{"cert_file":"` + certFile + `","key_file":"` + keyFile + `"}`
	rec := postCertProbe(t, NewRouter(testDeps(t)), body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — %s", rec.Code, rec.Body.String())
	}

	var got wire.CertificateProbe
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v — %s", err, rec.Body.String())
	}
	// THE HOST WITHOUT ITS PORT. `postCertProbe` sends to `quince.example:8968`, and a certificate
	// covers a name rather than a name and a port.
	if got.CurrentHost != "quince.example" {
		t.Errorf("current_host = %q, want quince.example", got.CurrentHost)
	}
	if !got.CurrentHostCovered {
		t.Error("current_host_covered = false for a certificate issued to exactly this host")
	}
	if got.Outcome != "usable" {
		t.Errorf("outcome = %q, want usable — %s", got.Outcome, got.Reason)
	}
	// AND THE HOSTNAME FIELD IS STILL EMPTY IN THE ECHO. Reporting where they are must not look like
	// filling in where they are going (quince#908 §5).
	if got.Hostname != "" {
		t.Errorf("hostname = %q, want empty — the field was not sent", got.Hostname)
	}
}

// AN UNCOVERED ADDRESS IS REPORTED, NOT REFUSED. This is the walk that found it: a wildcard for
// another domain, checked from an IP, called usable with nothing said about the address in play.
func TestAnAddressTheCertificateDoesNotCoverIsStillUsable(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := writeCertPair(t, dir, "nothing.invalid", time.Now().Add(24*time.Hour))

	body := `{"cert_file":"` + certFile + `","key_file":"` + keyFile + `"}`
	rec := postCertProbe(t, NewRouter(testDeps(t)), body)

	var got wire.CertificateProbe
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v — %s", err, rec.Body.String())
	}
	if got.Outcome != "usable" {
		t.Fatalf("outcome = %q, want usable — coverage of the caller's address is not a refusal", got.Outcome)
	}
	if got.CurrentHostCovered {
		t.Error("current_host_covered = true for a certificate that covers nothing.invalid")
	}
	if got.CurrentHost != "quince.example" {
		t.Errorf("current_host = %q, want quince.example", got.CurrentHost)
	}
}
