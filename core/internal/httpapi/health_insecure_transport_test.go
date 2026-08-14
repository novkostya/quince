package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// `GET /api/health` REPORTS WHETHER THE PLAIN-HTTP OPT-IN IS IN FORCE (quince#908 slice 6,
// Operator ruling 2026-08-14, contracts §1).
//
// The field drives a non-dismissible banner (quince#539), so the cases that matter are the ones
// where it must NOT follow the connection: an https caller is told too, because the relaxation is
// in force for the plain half of the mux whether or not this caller arrived over it.
func TestHealthReportsInsecureTransportAllowed(t *testing.T) {
	tests := []struct {
		name   string
		target string
		setup  func(d *Deps)
		want   bool
	}{
		{
			name:   "the shipping default: off",
			target: "http://quince.example:8968/api/health",
			want:   false,
		},
		{
			name:   "the opt-in is on",
			target: "http://quince.example:8968/api/health",
			setup:  func(d *Deps) { d.Auth.SetAllowInsecureTransport(true) },
			want:   true,
		},
		{
			// DAEMON-WIDE, NOT PER CONNECTION — the field directly above it in the payload is the
			// other way round, which is exactly the confusion this asserts against. The admin
			// reading Settings over https is precisely who should be told the plain door is open.
			name:   "https, opt-in on: still true, because the setting is not about this connection",
			target: "https://quince.example:8968/api/health",
			setup:  func(d *Deps) { d.Auth.SetAllowInsecureTransport(true) },
			want:   true,
		},
		{
			name:   "loopback, opt-in off: nothing was relaxed",
			target: "http://127.0.0.1:8968/api/health",
			want:   false,
		},
		{
			// TWO SWITCHES, TWO REASONS, AND THEY MUST NOT COLLAPSE. `SetInsecureCookies` is the
			// demo affordance that must never be set in production; this field reports the user's
			// own declared choice. A demo reporting the production degraded mode would put a
			// warning about the operator's network on a page nobody's network is behind.
			name:   "demo mode is not the opt-in",
			target: "http://quince.example:8968/api/health",
			setup:  func(d *Deps) { d.Auth.SetInsecureCookies(true) },
			want:   false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps(t)
			if tc.setup != nil {
				tc.setup(&deps)
			}
			if got := healthOf(t, NewRouter(deps), tc.target).InsecureTransportAllowed; got != tc.want {
				t.Errorf("insecure_transport_allowed = %v, want %v — this is what a "+
					"non-dismissible warning renders from, so a wrong answer either hides a "+
					"degraded mode or warns about one nobody turned on", got, tc.want)
			}
		})
	}
}

// THE TWO FIELDS ARE INVERSES ON THE INSTALL THAT MATTERS, AND THIS IS THE TEST THAT SAYS SO.
//
// It is the reason the second field exists at all: with the opt-in on, no cookie is discarded, so
// `insecure_origin` reports the reassuring answer on exactly the deployment whose login form must
// carry a warning. Anyone who later "simplifies" the banner to key off `insecure_origin` — the
// nearer-sounding field, one letter away in meaning — fails here rather than shipping a warning
// that is silent when it is needed.
func TestTheTwoInsecureFieldsDisagreeWhenTheOptInIsOn(t *testing.T) {
	deps := testDeps(t)
	deps.Auth.SetAllowInsecureTransport(true)

	got := healthOf(t, NewRouter(deps), "http://quince.example:8968/api/health")

	if got.InsecureOrigin {
		t.Errorf("insecure_origin = true, want false — the opt-in keeps the cookie, so nothing " +
			"is discarded; if this ever becomes true the banner may key off it instead")
	}
	if !got.InsecureTransportAllowed {
		t.Errorf("insecure_transport_allowed = false, want true — plain http with the opt-in on " +
			"is the whole case the field was added for")
	}
}

// LIVE, NOT CAPTURED AT WIRING. quince#900 made the setting applicable without a restart, so a
// banner must follow it within one poll. A value read once when the router was built would describe
// the config file as it was at startup — and the direction that fails silently is the dangerous
// one: turn the opt-in ON at runtime and a captured `false` shows no warning at all.
func TestHealthFollowsTheOptInWhileServing(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	const target = "http://quince.example:8968/api/health"

	if healthOf(t, router, target).InsecureTransportAllowed {
		t.Fatalf("insecure_transport_allowed = true before anything set it")
	}
	deps.Auth.SetAllowInsecureTransport(true)
	if !healthOf(t, router, target).InsecureTransportAllowed {
		t.Errorf("still false after the applier turned the opt-in ON — a captured value shows no " +
			"warning on an install that just relaxed its transport")
	}
	deps.Auth.SetAllowInsecureTransport(false)
	if healthOf(t, router, target).InsecureTransportAllowed {
		t.Errorf("still true after the opt-in went OFF — a banner that cannot be turned off by " +
			"fixing the setting is a banner people learn to ignore")
	}
}

func healthOf(t *testing.T, h http.Handler, target string) HealthResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status %d, want 200", target, rec.Code)
	}
	var got HealthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v — body %s", err, rec.Body.String())
	}
	return got
}
