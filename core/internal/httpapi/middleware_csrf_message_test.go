package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// THE REFUSAL NAMES A CAUSE THE READER CAN ACT ON, NOT THE MECHANISM.
//
// A double-submit failure on plain http is not a mystery: the cookie is issued `Secure`, a browser
// will not keep it on an insecure origin, and quince already holds the predicate that says so. The
// user meeting this is on the certificate step — trying to get OFF plain http — so an answer naming
// a token they cannot see leaves them with nothing to do.
func TestTheCSRFRefusalSaysWhyWhenQuinceKnows(t *testing.T) {
	for _, tc := range []struct {
		name, url       string
		wantContains    string
		wantNotContains string
	}{
		{
			// PLAIN HTTP TO A NON-LOOPBACK HOST — `CookieWillBeDiscarded`'s one true case, and the
			// one the Operator hit on the rig.
			name:            "plain http to a name",
			url:             "http://quince.example:8968/api/onboarding/certificate",
			wantContains:    "plain http",
			wantNotContains: "token",
		},
		{
			// LOOPBACK IS A SECURE ORIGIN, so the cookie is kept and something else went wrong —
			// a stale tab, a rotated token. One remedy covers those and quince does not guess.
			name:            "loopback, where the cookie is kept",
			url:             "http://127.0.0.1:8968/api/onboarding/certificate",
			wantContains:    "reload the page",
			wantNotContains: "plain http",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.url, strings.NewReader(`{"cert_file":"/tls/c.pem","key_file":"/tls/c.key"}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			// NO CSRF COOKIE AND NO HEADER — the shape a browser produces when it discarded the
			// cookie, which is what makes this the real case rather than a forged one.
			NewRouter(testDeps(t)).ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status %d, want 403 — %s", rec.Code, rec.Body.String())
			}
			var got struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v — %s", err, rec.Body.String())
			}
			if got.Error.Code != "csrf" {
				t.Errorf("code = %q, want csrf — the CODE is the machine-readable half", got.Error.Code)
			}
			if !strings.Contains(got.Error.Message, tc.wantContains) {
				t.Errorf("message = %q, want it to contain %q", got.Error.Message, tc.wantContains)
			}
			if strings.Contains(strings.ToLower(got.Error.Message), tc.wantNotContains) {
				t.Errorf("message = %q, should not contain %q", got.Error.Message, tc.wantNotContains)
			}
			// THE ACRONYM NEVER REACHES A SCREEN. The code carries it for a client; the sentence is
			// for a person.
			if strings.Contains(strings.ToUpper(got.Error.Message), "CSRF") {
				t.Errorf("message names the mechanism: %q", got.Error.Message)
			}
		})
	}
}
