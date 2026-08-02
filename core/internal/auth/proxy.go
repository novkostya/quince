package auth

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
)

// TrustedProxies decides whether an `X-Forwarded-For` header may be believed, and resolves the
// client address when it may (design §6: *"reverse-proxy trust headers only from configured
// addresses"*; ruled on quince#464).
//
// WHY THIS EXISTS. The per-IP login limiter buckets on the peer address. Behind a reverse proxy
// every visitor IS the same peer, so ten wrong guesses deny the login route to everybody — the
// correct password included. Measured on quince#464; it is the defect that breaks a public demo's
// only purpose.
//
// WHY NOT JUST READ THE HEADER. Trusting `X-Forwarded-For` unconditionally is WORSE than the bug:
// any client could then mint unlimited buckets by varying it, which deletes the rate limit instead
// of fixing it. Only a peer the operator has named may be believed.
type TrustedProxies struct {
	nets []*net.IPNet
}

// NewTrustedProxies parses the configured list. Entries may be bare IPs (`10.0.0.5`) or CIDRs
// (`10.0.0.0/8`); a bare IP becomes a /32 or /128. Unparseable entries are RETURNED as warnings
// rather than dropped — a proxy the operator believes is configured, and is not, is exactly the
// silent degradation `no silent caps or fallbacks` forbids.
func NewTrustedProxies(entries []string) (*TrustedProxies, []string) {
	tp := &TrustedProxies{}
	var bad []string
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(e); err == nil {
			tp.nets = append(tp.nets, n)
			continue
		}
		if ip := net.ParseIP(e); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			tp.nets = append(tp.nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		bad = append(bad, e)
	}
	return tp, bad
}

// Configured reports whether any proxy is trusted. Empty is the shipping default and means
// "believe nobody", which is byte-for-byte the pre-quince#464 behaviour.
func (t *TrustedProxies) Configured() bool { return t != nil && len(t.nets) > 0 }

func (t *TrustedProxies) trusts(ip net.IP) bool {
	if t == nil || ip == nil {
		return false
	}
	for _, n := range t.nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ClientIP resolves the address the rate limiter buckets on.
//
// If the PEER is not trusted, the header is not read at all and the peer address is returned —
// so an attacker connecting directly can never influence their own bucket.
//
// If the peer IS trusted, walk `X-Forwarded-For` RIGHT TO LEFT past entries that are themselves
// trusted proxies, and take the first one that is not. That entry is the nearest hop quince has a
// reason to believe.
//
// RIGHTMOST-UNTRUSTED, NEVER LEFTMOST. The leftmost entry is whatever the client sent before any
// proxy appended to it — fully attacker-controlled. Taking it would hand every client its own
// bucket namespace, which is the "worse than the bug" case above.
func (t *TrustedProxies) ClientIP(r *http.Request) string {
	peer := peerHost(r)
	if !t.Configured() {
		return peer
	}
	if !t.trusts(net.ParseIP(peer)) {
		return peer
	}
	for _, hop := range reverse(splitForwarded(r.Header.Get("X-Forwarded-For"))) {
		ip := net.ParseIP(hop)
		if ip == nil {
			continue // a malformed hop proves nothing; keep walking left
		}
		if !t.trusts(ip) {
			return ip.String()
		}
	}
	// Every hop was a trusted proxy, or the header was absent. The peer is the best answer left,
	// and it is a truthful one rather than a guess.
	return peer
}

// UnconfiguredProxy reports a request that CARRIES `X-Forwarded-For` from a peer quince does not
// trust. That is the signature of a proxy the operator put in front and never told quince about,
// and today it produces the reported lockout SILENTLY. Callers log it once — `no silent caps or
// fallbacks` makes a degraded mode something to surface, and this is the cheapest part of the fix.
func (t *TrustedProxies) UnconfiguredProxy(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") == "" {
		return false
	}
	return !t.trusts(net.ParseIP(peerHost(r)))
}

func peerHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func splitForwarded(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func reverse(in []string) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

// WarnUnconfiguredProxy logs ONCE per process when a request carrying `X-Forwarded-For` arrives
// from a peer quince does not trust.
//
// That combination means a proxy is in front and `QUINCE_TRUSTED_PROXIES` does not name it, which
// produces quince#464's exact symptom — every visitor sharing one login bucket — and produces it
// SILENTLY. `no silent caps or fallbacks` makes a degraded mode something to surface, and this is
// the cheapest half of the fix and probably the most valuable: the operator learns the setting
// exists at the moment it would have helped.
//
// THE REMEDY MUST NAME THE THING THAT EXISTS. This said `server.trusted_proxies` — a config key
// that lived for one afternoon and never shipped, retired to a bootstrap env var by quince#549 in
// the same rung. A warning whose fix instruction points at a key the reader cannot find is worse
// than no warning: it costs them an edit to `config.yml`, a restart, and the same warning again.
//
// Once, not per request: a proxied deployment would otherwise log on every login attempt, and a
// line repeated per request is one nobody reads.
func (t *TrustedProxies) WarnUnconfiguredProxy(log *slog.Logger, r *http.Request) {
	if !t.UnconfiguredProxy(r) {
		return
	}
	proxyWarnOnce.Do(func() {
		log.Warn("a request carried X-Forwarded-For from an address that is not in "+
			"QUINCE_TRUSTED_PROXIES — the login rate limiter is bucketing every visitor together, "+
			"because it cannot see past your proxy. Set QUINCE_TRUSTED_PROXIES to the proxy's "+
			"address to fix it",
			"peer", peerHost(r), "var", "QUINCE_TRUSTED_PROXIES")
	})
}

var proxyWarnOnce sync.Once

// TrustsPeer reports whether the request's PEER is one of the configured proxies. It is the
// question SecureOrigin asks about `X-Forwarded-Proto` (quince#555), where ClientIP asks the
// related but different question of which forwarded hop to bill.
func (t *TrustedProxies) TrustsPeer(r *http.Request) bool {
	return t.trusts(net.ParseIP(peerHost(r)))
}
