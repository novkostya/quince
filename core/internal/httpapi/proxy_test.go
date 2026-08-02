package httpapi

import (
	"net/http"
	"testing"
)

func req(remote, xff string) *http.Request {
	r := &http.Request{RemoteAddr: remote, Header: http.Header{}}
	if xff != "" {
		r.Header.Set("X-Forwarded-For", xff)
	}
	return r
}

// TestClientIPIgnoresHeaderFromUntrustedPeer is THE regression guard, and it is the one the
// quince#464 ruling singles out: "tests for the three shapes above INCLUDING the
// attacker-varies-the-header case, which is the one that must not regress".
//
// An attacker connecting DIRECTLY is not in trusted_proxies, so their header is never read. If this
// breaks, every client can mint its own rate-limit bucket by varying a header — which deletes the
// login limiter rather than fixing it, and is strictly worse than the bug quince#464 reports.
func TestClientIPIgnoresHeaderFromUntrustedPeer(t *testing.T) {
	tp, bad := NewTrustedProxies([]string{"10.0.0.1"})
	if len(bad) != 0 {
		t.Fatalf("unexpected bad entries: %v", bad)
	}
	for _, spoof := range []string{"1.2.3.4", "9.9.9.9, 8.8.8.8", "", "not-an-ip"} {
		got := tp.ClientIP(req("203.0.113.7:5555", spoof))
		if got != "203.0.113.7" {
			t.Errorf("X-Forwarded-For %q from an untrusted peer: got %q, want the peer address — "+
				"an attacker must not be able to choose their own bucket", spoof, got)
		}
	}
}

// TestClientIPTakesRightmostUntrustedHop is the ruling's algorithm: walk right to left past hops
// that are themselves trusted proxies, take the first that is not.
//
// RIGHTMOST, never leftmost: the leftmost entry is whatever the client sent before any proxy
// appended to it, so it is attacker-controlled even when the peer is a genuine proxy.
func TestClientIPTakesRightmostUntrustedHop(t *testing.T) {
	tp, _ := NewTrustedProxies([]string{"10.0.0.0/8"})
	for _, tc := range []struct{ name, xff, want string }{
		{"single hop", "198.51.100.4", "198.51.100.4"},
		{"attacker prepended a fake hop", "1.1.1.1, 198.51.100.4", "198.51.100.4"},
		{"two trusted proxies in the chain", "198.51.100.4, 10.0.0.9, 10.0.0.8", "198.51.100.4"},
		{"malformed hop is skipped, not trusted", "198.51.100.4, garbage", "198.51.100.4"},
	} {
		if got := tp.ClientIP(req("10.0.0.1:4444", tc.xff)); got != tc.want {
			t.Errorf("%s: X-Forwarded-For %q → %q, want %q", tc.name, tc.xff, got, tc.want)
		}
	}
}

// TestEmptyTrustedProxiesIsTodaysBehaviour pins the upgrade promise. The shipping default is an
// empty list, and it must be byte-for-byte what quince did before quince#464 — otherwise a direct
// LAN deployment changes behaviour on upgrade, which is the one thing the ruling forbids.
func TestEmptyTrustedProxiesIsTodaysBehaviour(t *testing.T) {
	for _, tp := range []*TrustedProxies{mustProxies(t), nil} {
		if tp.Configured() {
			t.Fatal("an empty/nil TrustedProxies reports Configured()")
		}
		if got := tp.ClientIP(req("192.0.2.9:1111", "1.2.3.4")); got != "192.0.2.9" {
			t.Errorf("with nothing trusted: got %q, want the peer address 192.0.2.9", got)
		}
	}
}

// TestUnconfiguredProxyIsDetected drives the warn-once. A request CARRYING X-Forwarded-For from an
// untrusted peer is the signature of a proxy the operator never told quince about — which produces
// quince#464's symptom silently, and `no silent caps or fallbacks` says a degraded mode is
// surfaced.
func TestUnconfiguredProxyIsDetected(t *testing.T) {
	none := mustProxies(t)
	if !none.UnconfiguredProxy(req("192.0.2.9:1111", "1.2.3.4")) {
		t.Error("X-Forwarded-For from an untrusted peer was not flagged")
	}
	if none.UnconfiguredProxy(req("192.0.2.9:1111", "")) {
		t.Error("a request with no X-Forwarded-For was flagged as a proxy")
	}
	configured, _ := NewTrustedProxies([]string{"192.0.2.9"})
	if configured.UnconfiguredProxy(req("192.0.2.9:1111", "1.2.3.4")) {
		t.Error("a CONFIGURED proxy was flagged as unconfigured")
	}
}

// TestNewTrustedProxiesReportsBadEntries — an unparseable entry is returned so the caller can warn.
// Dropping it silently would leave the operator believing a proxy is trusted when it is not, which
// is the failure this whole change exists to remove.
func TestNewTrustedProxiesReportsBadEntries(t *testing.T) {
	tp, bad := NewTrustedProxies([]string{"10.0.0.1", "  ", "nonsense", "2001:db8::/32"})
	if len(bad) != 1 || bad[0] != "nonsense" {
		t.Fatalf("bad entries = %v, want exactly [nonsense]", bad)
	}
	if !tp.Configured() {
		t.Fatal("valid entries alongside a bad one were all discarded")
	}
	if got := tp.ClientIP(req("[2001:db8::1]:9999", "198.51.100.4")); got != "198.51.100.4" {
		t.Errorf("IPv6 CIDR trust: got %q, want 198.51.100.4", got)
	}
}

func mustProxies(t *testing.T) *TrustedProxies {
	t.Helper()
	tp, bad := NewTrustedProxies(nil)
	if len(bad) != 0 {
		t.Fatalf("unexpected bad entries: %v", bad)
	}
	return tp
}
