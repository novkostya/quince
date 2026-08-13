package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// `GET /api/health` REPORTS WHETHER A CREDENTIAL CAN BE ESTABLISHED OVER THIS CONNECTION
// (quince#908, contracts §1).
//
// ONE POSITIVE AND FIVE NEGATIVES, and the negatives are the point. Every one of them is plain
// http where login works perfectly well, and a client that routed on `window.location.protocol`
// would misfire on all of them — which is why the fact has to come from here. A test with only
// the positive case would pass against a field hardcoded to `true`.
func TestHealthReportsInsecureOrigin(t *testing.T) {
	tests := []struct {
		name   string
		target string
		setup  func(d *Deps)
		want   bool
	}{
		{
			name:   "plain http to a LAN name: the cookie would be discarded",
			target: "http://quince.example:8968/api/health",
			want:   true,
		},
		{
			name:   "loopback: plain http, and the cookie survives",
			target: "http://127.0.0.1:8968/api/health",
			want:   false,
		},
		{
			name:   "https: nothing to warn about",
			target: "https://quince.example:8968/api/health",
			want:   false,
		},
		{
			name:   "demo mode: Secure is forced off, so nothing is discarded",
			target: "http://quince.example:8968/api/health",
			setup:  func(d *Deps) { d.Auth.SetInsecureCookies(true) },
			want:   false,
		},
		{
			name:   "the user's own plain-http opt-in: their declared choice, and it works",
			target: "http://quince.example:8968/api/health",
			setup:  func(d *Deps) { d.Auth.SetAllowInsecureTransport(true) },
			want:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps(t)
			if tc.setup != nil {
				tc.setup(&deps)
			}
			rec := httptest.NewRecorder()
			NewRouter(deps).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.target, nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200", rec.Code)
			}
			var got HealthResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v — body %s", err, rec.Body.String())
			}
			if got.InsecureOrigin != tc.want {
				t.Errorf("insecure_origin = %v, want %v — a client routes first-run on this, so a "+
					"wrong answer either strands a user on a form that cannot work or sends one "+
					"away from a form that would have", got.InsecureOrigin, tc.want)
			}
		})
	}
}

// THE FIELD AND THE 426 MUST NOT DISAGREE, because they are answers to the same question from two
// endpoints — and the whole reason the field exists is to predict the refusal. Asserted against the
// REFUSAL ITSELF rather than against a second copy of the predicate: if `refuseInsecureOrigin` ever
// changes what it gates on, this fails rather than quietly describing the old rule.
func TestHealthAgreesWithTheSetupRefusal(t *testing.T) {
	for _, target := range []string{
		"http://quince.example:8968",
		"http://127.0.0.1:8968",
		"https://quince.example:8968",
	} {
		t.Run(target, func(t *testing.T) {
			deps := testDeps(t)
			router := NewRouter(deps)

			health := httptest.NewRecorder()
			router.ServeHTTP(health, httptest.NewRequest(http.MethodGet, target+"/api/health", nil))
			var h HealthResponse
			if err := json.Unmarshal(health.Body.Bytes(), &h); err != nil {
				t.Fatalf("decode health: %v", err)
			}

			setup := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, target+"/api/auth/setup", nil)
			router.ServeHTTP(setup, req)
			refused := setup.Code == http.StatusUpgradeRequired

			if h.InsecureOrigin != refused {
				t.Errorf("health says insecure_origin=%v but POST /api/auth/setup answered %d "+
					"(426 = refused: %v) — the field predicts the refusal, and a client that "+
					"believes it would route a user into a dead end or out of a working one",
					h.InsecureOrigin, setup.Code, refused)
			}
		})
	}
}
