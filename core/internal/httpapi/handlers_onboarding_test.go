package httpapi

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func getOnboardingHTTPS(t *testing.T, deps Deps, host string, headers map[string]string, withTLS bool) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/onboarding/https", nil)
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if withTLS {
		req.TLS = &tls.ConnectionState{}
	}
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)

	var body map[string]any
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode onboarding/https body %q: %v", rec.Body.String(), err)
		}
	}
	return rec, body
}

// G1: a request over an already-secure origin completes step 1 with NO buttons. The top-tier
// user — a reverse proxy or `tailscale serve` — must meet zero friction and never be asked to
// confirm something quince can see for itself.
func TestOnboardingHTTPSCompleteOnASecureOrigin(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		withTLS  bool
		detected string
	}{
		{name: "quince terminated TLS itself", withTLS: true, detected: "tls"},
		{name: "a proxy terminated it and said so", headers: map[string]string{"X-Forwarded-Proto": "https"}, detected: "forwarded_proto"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, body := getOnboardingHTTPS(t, testDeps(t), "quince.example", tc.headers, tc.withTLS)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if body["complete"] != true {
				t.Errorf("complete = %v, want true", body["complete"])
			}
			if body["detected"] != tc.detected {
				t.Errorf("detected = %v, want %q", body["detected"], tc.detected)
			}
		})
	}
}

// Plain http is not complete — the tiers get offered. LOOPBACK IS INCLUDED HERE ON PURPOSE:
// a session cookie works fine on http://localhost, so it is tempting to call the step done,
// but the step asks whether a PHONE can reach quince and a browser on localhost cannot answer
// that on its behalf. Saying "complete" there would be the same false assurance one layer up
// that this rung exists to remove.
func TestOnboardingHTTPSIncompleteOnPlainHTTPIncludingLoopback(t *testing.T) {
	for _, host := range []string{"quince.example:8968", "127.0.0.1:8968", "localhost:8968"} {
		t.Run(host, func(t *testing.T) {
			rec, body := getOnboardingHTTPS(t, testDeps(t), host, nil, false)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if body["complete"] != false {
				t.Errorf("complete = %v on %s, want false", body["complete"], host)
			}
			if body["detected"] != "none" {
				t.Errorf("detected = %v, want none", body["detected"])
			}
		})
	}
}

// The ruling: step 1 is PRE-AUTH. The chicken-and-egg is the whole rung — on plain http to a
// LAN address the browser discards the session cookie, so the page explaining exactly that
// cannot sit behind the door the defect locks.
func TestOnboardingHTTPSIsReachableWithoutASession(t *testing.T) {
	rec, body := getOnboardingHTTPS(t, testDeps(t), "quince.example:8968", nil, false)
	if rec.Code == http.StatusUnauthorized {
		t.Fatal("step 1 is behind the auth guard, which is the deadlock the ruling removes")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if _, ok := body["complete"]; !ok {
		t.Error("no complete field in the unauthenticated response")
	}
}

// The exemption is BY EXACT PATH. authExempt switches on method+path with no prefix support,
// so this pins that nothing ELSE under /api/onboarding/ is reachable without a session — the
// constraint the ruling attached, and the one a later step will be tempted to loosen.
func TestOnlyTheHTTPSCheckIsExemptUnderOnboarding(t *testing.T) {
	for _, path := range []string{
		"/api/onboarding/devices", // a plausible future sibling
		"/api/onboarding/",
		"/api/onboarding/https/extra",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Host = "quince.example:8968"
			rec := httptest.NewRecorder()
			NewRouter(testDeps(t)).ServeHTTP(rec, req)

			// 401 from the guard, or 404 because no such route exists — either is fine.
			// What must NOT happen is a 200 to an unauthenticated caller.
			if rec.Code == http.StatusOK {
				t.Errorf("%s answered 200 without a session; the exemption has become a prefix", path)
			}
		})
	}
}

// A POST to the exempt path must not be exempt either: the switch keys on method+path
// together, and this pins that the pair is what was exempted rather than the path alone.
func TestOnboardingHTTPSExemptionIsMethodSpecific(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/onboarding/https", nil)
	req.Host = "quince.example:8968"
	rec := httptest.NewRecorder()
	NewRouter(testDeps(t)).ServeHTTP(rec, req)

	if rec.Code == http.StatusOK {
		t.Error("POST to onboarding/https answered 200 unauthenticated; the exemption is not method-scoped")
	}
}
