package httpapi

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"

	"github.com/novkostya/quince/core/internal/auth"
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

// quince#940 §2 + quince#939 §7 — THE FOUR CAUSES BEHIND `detected: none`.
//
// All four rendered as **Not encrypted** and nothing else until this landed, and two of them send the
// user to a remedy that is not merely vaguer but WRONG: `proxy_reports_plain` means the proxy is
// correct and the old copy told the operator it was broken. `CLAUDE.md`: a diagnostic that collapses
// distinguishable causes is a defect even when every word of it is true.
func TestTheUnencryptedCodeSeparatesTheFourCauses(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		// proxies is the trusted-proxy list, and the peer address the request appears to come
		// from. httptest.NewRequest's default RemoteAddr is 192.0.2.1:1234.
		proxies []string
		want    string
	}{
		{
			// NO EVIDENCE OF A PROXY AT ALL. The remedy is a proxy in front, or quince's own
			// certificate — not a config line in something that may not exist.
			name: "no forwarding headers at all",
			want: "no_proxy_seen",
		},
		{
			// SOMETHING IS IN FRONT AND IS ADDING FORWARDING HEADERS, just not the one that
			// matters. This is the nginx caveat, and it is a HINT: nginx does not set
			// X-Forwarded-For by default either, so a correct deployment can land in the row
			// above instead. The client must not state it as fact.
			name:    "X-Forwarded-For present, X-Forwarded-Proto absent",
			headers: map[string]string{"X-Forwarded-For": "203.0.113.7"},
			want:    "proxy_not_forwarding_scheme",
		},
		{
			// THE ROW THE GAP BLOCK SAID "CANNOT CURRENTLY OCCUR", because TrustedProxies is nil
			// in production today and an unset list believes the header from anyone. It occurs
			// the moment an operator sets QUINCE_TRUSTED_PROXIES, which is the configuration the
			// docs recommend — so it is tested rather than deferred.
			name:    "the header says https but the peer is not on the trust list",
			headers: map[string]string{"X-Forwarded-Proto": "https"},
			proxies: []string{"198.51.100.4/32"},
			want:    "proxy_untrusted",
		},
		{
			// THE CRUEL ONE. The proxy is behaving perfectly and reporting the truth: the client
			// reached IT over plain http. quince used to answer this by telling the operator
			// their proxy was broken.
			name:    "the proxy reports that the client reached it over plain http",
			headers: map[string]string{"X-Forwarded-Proto": "http"},
			want:    "proxy_reports_plain",
		},
		{
			// ANY NON-https VALUE IS THE SAME ANSWER, deliberately. A header that does not say
			// https is not https whatever else it says, and a fifth code for a value nobody sets
			// would be a remedy nobody needs.
			name:    "a value that is neither http nor https",
			headers: map[string]string{"X-Forwarded-Proto": "ftp"},
			want:    "proxy_reports_plain",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps(t)
			if len(tc.proxies) > 0 {
				p, err := auth.NewTrustedProxies(tc.proxies)
				if err != nil {
					t.Fatalf("trusted proxies: %v", err)
				}
				deps.Proxies = p
			}
			rec, body := getOnboardingHTTPS(t, deps, "quince.example", tc.headers, false)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if body["complete"] != false || body["detected"] != "none" {
				t.Fatalf("complete=%v detected=%v — this case is not the one under test",
					body["complete"], body["detected"])
			}
			if body["unencrypted_code"] != tc.want {
				t.Errorf("unencrypted_code = %v, want %q", body["unencrypted_code"], tc.want)
			}
		})
	}
}

// THE FIELD IS ABSENT WHEN THERE IS NOTHING TO EXPLAIN. `omitempty` rather than an empty string,
// because a key that is always present invites a client to render it, and on a secure origin there
// is no question being answered.
func TestTheUnencryptedCodeIsAbsentOnASecureOrigin(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
		withTLS bool
	}{
		{name: "quince terminated TLS itself", withTLS: true},
		{name: "a trusted proxy said so", headers: map[string]string{"X-Forwarded-Proto": "https"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, body := getOnboardingHTTPS(t, testDeps(t), "quince.example", tc.headers, tc.withTLS)
			if body["complete"] != true {
				t.Fatalf("complete = %v — this case is not the one under test", body["complete"])
			}
			if _, present := body["unencrypted_code"]; present {
				t.Errorf("unencrypted_code = %v, want the key absent", body["unencrypted_code"])
			}
		})
	}
}

// THE CROSS-ORIGIN PROBE IS UNTOUCHED, and this is the assertion that makes door 2 door 2.
//
// `OnboardingProbe.Detected` takes the same values as `OnboardingHTTPS.Detected` and comes from the
// same function, so widening the ENUM would have widened the probe silently — the CORS ruling froze
// that body at `{nonce, detected}` on the argument that it leaks nothing. A second FIELD cannot do
// that, and this test is what keeps it true if someone later moves the classification into `detected`.
func TestTheProbeBodyDoesNotCarryTheUnencryptedCode(t *testing.T) {
	deps := testDeps(t)
	h := NewRouter(deps)

	mint := httptest.NewRecorder()
	h.ServeHTTP(mint, httptest.NewRequest(http.MethodGet, "http://quince.example/api/onboarding/probe/nonce", nil))
	var minted map[string]any
	if err := json.Unmarshal(mint.Body.Bytes(), &minted); err != nil {
		t.Fatalf("decode nonce: %v — body %s", err, mint.Body.String())
	}
	nonce, _ := minted["nonce"].(string)
	if nonce == "" {
		t.Fatalf("no nonce minted: %s", mint.Body.String())
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet,
		"http://quince.example/api/onboarding/probe?nonce="+nonce, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode probe: %v — body %s", err, rec.Body.String())
	}
	if len(body) != 2 {
		t.Fatalf("probe body has %d fields, want exactly 2 {nonce, detected}: %v", len(body), body)
	}
	if _, present := body["unencrypted_code"]; present {
		t.Error("the cross-origin probe carries unencrypted_code — the frozen CORS body was widened")
	}
}
