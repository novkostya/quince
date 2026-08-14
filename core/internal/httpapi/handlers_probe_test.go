package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/wire"
)

const probePath = "/api/onboarding/probe"

func getWith(t *testing.T, h http.Handler, target, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func mintNonce(t *testing.T, h http.Handler) string {
	t.Helper()
	rec := getWith(t, h, "http://quince.example:8968"+probePath+"/nonce", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("mint: status %d, want 200", rec.Code)
	}
	var got wire.ProbeNonce
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("mint: decode: %v", err)
	}
	if got.Nonce == "" {
		t.Fatal("mint returned an empty nonce")
	}
	return got.Nonce
}

// THE GATE IS THE NONCE, AND THIS IS THE SECURITY CLAIM OF THE WHOLE ENDPOINT (Operator ruling
// 2026-08-14). A legitimate page obtained its nonce same-origin from this quince; a drive-by page has
// none, gets no header, and reads nothing.
func TestTheProbeGrantsCORSOnlyToANonceThisDaemonMinted(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)
	good := mintNonce(t, router)

	for _, tc := range []struct {
		name  string
		query string
		grant bool
	}{
		{"a nonce this daemon minted", "?nonce=" + good, true},
		{"a nonce it did not", "?nonce=not-a-real-nonce", false},
		// NO NONCE AT ALL is the drive-by case, and it must not be a 4xx: the answer has to be
		// indistinguishable from a network error to the page that sent it.
		{"no nonce at all", "", false},
		{"an empty nonce", "?nonce=", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := getWith(t, router, "http://quince.example:8968"+probePath+tc.query, "http://evil.example")

			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200 — a refusal must not be distinguishable from a "+
					"network error, so the gate is the HEADER and never the status", rec.Code)
			}
			got := rec.Header().Get("Access-Control-Allow-Origin")
			if tc.grant && got != "http://evil.example" {
				t.Errorf("Access-Control-Allow-Origin = %q, want the caller's origin echoed", got)
			}
			if !tc.grant && got != "" {
				t.Errorf("Access-Control-Allow-Origin = %q on an ungated request — a page with no "+
					"nonce could read the answer", got)
			}
		})
	}
}

// THE ORIGIN IS ECHOED, NEVER `*`, AND `Vary: Origin` RIDES WITH IT — constraint 3 of the ruling. The
// header is origin-dependent by construction, so an intermediary caching without `Vary` can hand one
// origin's grant to another, quietly restoring the wildcard the ruling refused.
func TestTheProbeEchoesTheOriginAndVariesOnIt(t *testing.T) {
	router := NewRouter(testDeps(t))
	nonce := mintNonce(t, router)

	rec := getWith(t, router, "http://quince.example:8968"+probePath+"?nonce="+nonce, "https://named.example")

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://named.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want the caller's own origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" {
		t.Errorf("the wildcard is what the ruling refused")
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want Origin", got)
	}
	// NEVER CREDENTIALS — constraint 2. With them the widening becomes a cross-origin door onto a
	// cookie-bearing request, which is a different decision from the one that was taken.
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want absent", got)
	}
}

// AN UNGRANTED REQUEST STILL VARIES ON ORIGIN. Without this a cache could store the ungranted answer
// under a key that ignores `Origin` and serve it to the origin that WOULD have been granted — the
// same defect as the missing `Vary`, reached from the other side.
func TestAnUngrantedProbeStillVariesOnOrigin(t *testing.T) {
	router := NewRouter(testDeps(t))

	rec := getWith(t, router, "http://quince.example:8968"+probePath+"?nonce=nope", "https://named.example")

	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q on an ungranted request, want Origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("granted anyway: %q", got)
	}
}

// THE MINT IS NEVER CORS-READABLE, and that asymmetry IS the gate. A foreign origin may cause a mint
// — nothing stops it issuing the request — but it must not be able to READ the token, or the nonce
// stops distinguishing anybody from anybody.
func TestTheMintIsNeverCORSReadable(t *testing.T) {
	router := NewRouter(testDeps(t))

	rec := getWith(t, router, "http://quince.example:8968"+probePath+"/nonce", "http://evil.example")

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("the MINT granted %q — a drive-by page could read a nonce and then use it, which "+
			"defeats the whole gate", got)
	}
}

// THE BODY IS `{nonce, detected}` AND NOTHING ELSE — constraint 4, and the entire safety argument for
// the widening. Asserted as an EXACT key set, so a field added for convenience fails here rather than
// passing review as an improvement.
func TestTheProbeBodyCarriesExactlyTwoFields(t *testing.T) {
	router := NewRouter(testDeps(t))
	nonce := mintNonce(t, router)

	rec := getWith(t, router, "http://quince.example:8968"+probePath+"?nonce="+nonce, "https://named.example")

	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v — body %s", err, rec.Body.String())
	}
	if len(raw) != 2 {
		t.Fatalf("the body has %d fields, want exactly 2 — adding one is a contracts change AND "+
			"needs the ruling revisited, because `it leaks nothing` is the safety argument: %v",
			len(raw), raw)
	}
	if _, ok := raw["nonce"]; !ok {
		t.Error("no `nonce` — without the echo, success means only that a quince answered")
	}
	if _, ok := raw["detected"]; !ok {
		t.Error("no `detected` — quince#939 needs what quince SAW, not only that it answered")
	}
}

// `detected` DESCRIBES THE PROBE REQUEST'S OWN CONNECTION and comes from `detectHTTPS`, the same
// function `GET /api/onboarding/https` uses. Asserted against that endpoint rather than against a
// second copy of the expectation: if the detector ever changes, this fails rather than quietly
// describing the old rule.
func TestTheProbeAgreesWithTheOnboardingDetector(t *testing.T) {
	router := NewRouter(testDeps(t))
	nonce := mintNonce(t, router)

	for _, target := range []string{
		"http://quince.example:8968",  // plain http to a name — the nginx caveat's shape
		"https://quince.example:8968", // quince's own TLS
		"http://127.0.0.1:8968",       // loopback is deliberately NOT complete
	} {
		t.Run(target, func(t *testing.T) {
			var step wire.OnboardingHTTPS
			rec := getWith(t, router, target+"/api/onboarding/https", "")
			if err := json.Unmarshal(rec.Body.Bytes(), &step); err != nil {
				t.Fatalf("onboarding decode: %v", err)
			}

			var probe wire.ProbeResult
			rec = getWith(t, router, target+probePath+"?nonce="+nonce, "https://named.example")
			if err := json.Unmarshal(rec.Body.Bytes(), &probe); err != nil {
				t.Fatalf("probe decode: %v", err)
			}

			if probe.Detected != step.Detected {
				t.Errorf("probe says %q, the onboarding step says %q — two three-ways for one "+
					"question is the defect this codebase names most often",
					probe.Detected, step.Detected)
			}
			if probe.Nonce != nonce {
				t.Errorf("nonce echoed as %q, want %q", probe.Nonce, nonce)
			}
		})
	}
}

// MULTI-USE WITHIN THE TTL, which is the one place this diverges from a ceremony and was left to the
// spec by the ruling. One probe may legitimately try more than one NAME, and a challenge spent on the
// first attempt would make the second look like a failure — a probe reporting "that name is wrong"
// about a name that is right is worse than no probe.
func TestAProbeNonceSurvivesMoreThanOneUse(t *testing.T) {
	router := NewRouter(testDeps(t))
	nonce := mintNonce(t, router)

	for i := range 3 {
		rec := getWith(t, router, "http://quince.example:8968"+probePath+"?nonce="+nonce, "https://named.example")
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "" {
			t.Fatalf("use %d was refused — the nonce was consumed, so a caller trying a second "+
				"hostname would read a failure that is not about the hostname", i+1)
		}
	}
}

// AND THE TTL IS THE WHOLE BOUND, so it has to actually bound. Driven through the store's own clock
// rather than by sleeping: a test that waits two minutes is a test nobody runs.
func TestAProbeNonceExpires(t *testing.T) {
	store := newProbeNonces()
	base := time.Now()
	store.now = func() time.Time { return base }

	nonce, err := store.mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	if !store.valid(nonce) {
		t.Fatal("a freshly minted nonce is not valid")
	}

	store.now = func() time.Time { return base.Add(probeNonceTTL + time.Second) }
	if store.valid(nonce) {
		t.Error("an expired nonce still answers — the TTL is the only bound this token has")
	}
}

// THE MINT IS UNAUTHENTICATED, SO IT IS CAPPED. Without a cap it is an unbounded allocation any
// visitor can drive. Eviction is oldest-first rather than a refusal, so a flood costs the flooder
// their own oldest token instead of denying the mint to the legitimate caller behind them.
func TestTheNonceStoreIsBoundedAndEvictsRatherThanRefusing(t *testing.T) {
	store := newProbeNonces()
	base := time.Now()
	var tick int
	// A DISTINCT EXPIRY PER MINT, so "oldest" is well defined. Real clocks give this for free; a
	// frozen one would make every entry equally old and the assertion vacuous.
	store.now = func() time.Time { tick++; return base.Add(time.Duration(tick) * time.Millisecond) }

	first, err := store.mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	for range probeNonceMax {
		if _, err := store.mint(); err != nil {
			t.Fatalf("mint under load: %v — the cap must EVICT, never refuse", err)
		}
	}

	if len(store.in) > probeNonceMax {
		t.Errorf("the store holds %d nonces, cap is %d", len(store.in), probeNonceMax)
	}
	if store.valid(first) {
		t.Error("the oldest nonce survived the flood — eviction is not oldest-first")
	}
}
