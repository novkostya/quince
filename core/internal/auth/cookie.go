package auth

import (
	"net"
	"net/http"
	"strings"

	"github.com/novkostya/quince/core/internal/store"
)

const (
	// SessionCookieName is the HttpOnly admin session cookie.
	SessionCookieName = "quince_session"
	// CSRFCookieName is the readable (non-HttpOnly) double-submit CSRF cookie.
	CSRFCookieName = "quince_csrf"
	// CSRFHeaderName is where the client echoes the CSRF token on mutations.
	CSRFHeaderName = "X-CSRF-Token"
)

// SessionCookie builds the session cookie for a freshly issued session. It is HttpOnly +
// SameSite=Strict; the caller decides Secure via Service.Secure (below).
func SessionCookie(sess store.AuthSession, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    sess.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  sess.ExpiresAt,
	}
}

// ClearSessionCookie expires the session cookie (logout).
func ClearSessionCookie(secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	}
}

// CSRFCookie builds the readable double-submit cookie. Not HttpOnly (the SPA must read it
// to echo it in the header); still SameSite=Strict.
func CSRFCookie(token string, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     CSRFCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	}
}

// secureCookie decides the Secure flag. Rule (rung-ruled, touches the security baseline):
// Secure unless the request is loopback over plain HTTP — so `--demo` on localhost works
// while any LAN/production access is either HTTPS (direct or via a trusted proxy setting
// X-Forwarded-Proto) and gets Secure, or plain-HTTP-to-a-non-loopback-host and gets a
// Secure cookie the browser won't send (correct: canon requires HTTPS, we never silently
// downgrade). X-Forwarded-Proto only ever upgrades to Secure — AND IS BELIEVED ONLY FROM A
// TRUSTED PEER (quince#555); an unset list believes anyone, which is the default and the old
// behaviour. See SecureOrigin below for why the unqualified form was wrong: it was true of the
// COOKIE and false of the two consumers that invert the predicate.
//
// allowInsecure is `sessions.allow_insecure_transport` — the off-by-default opt-in for a
// user on a network they trust (qn.6f slice 8, Operator ruling 2026-08-02, option (b)).
// It is a PARAMETER rather than a package variable so the rule stays a pure function of
// its inputs and every test states the mode it is asserting about.
//
// NOTE WHERE IT SITS: strictly below SecureOrigin and strictly above the host test. That
// ordering is the ruling, not a preference — it relaxes the FALLBACK only, so a positive
// signal still wins and *the header can only ever upgrade* holds, now qualified by who is
// allowed to say it (quince#555). Moving
// this branch above SecureOrigin would let the flag strip Secure from a genuine HTTPS
// session, which is a different and much worse setting than the one that was ruled.
func secureCookie(r *http.Request, allowInsecure bool, trusted *TrustedProxies) bool {
	if SecureOrigin(r, trusted) {
		return true
	}
	if allowInsecure {
		return false
	}
	return !isLoopbackHost(r.Host)
}

// SecureOrigin reports whether the browser reached us over an origin IT considers secure —
// TLS terminated here, or terminated by a proxy we trust to say so.
//
// It is the half of secureCookie that describes the CONNECTION rather than the policy. The
// remaining !isLoopbackHost branch is a deliberate choice to mark the cookie Secure on a
// plain-http LAN address even though the browser will then discard it, and separating the
// two is what lets Service.CookieWillBeDiscarded name exactly that case (quince#497).
//
// EXPORTED because onboarding step 1 asks the same question (qn.6f slice 2): "is this origin
// already encrypted, so the step is complete with no buttons?" is exactly this predicate.
// Exporting it beats re-deriving `r.TLS != nil || X-Forwarded-Proto` in httpapi, which would
// be a second copy of a security predicate — the thing quince#497's design note refused to
// do for the loopback test, for the same reason: copies drift, and this one decides both
// whether a cookie survives and whether a user is told their setup is finished.
//
// It is a package function rather than a Service method on purpose: it is a pure function of
// the request, with no dependency on demo mode or the plain-http opt-in. Service.Secure is
// where those live, and conflating the two would make a POLICY answer look like a FACT.
// THE PROXY ARGUMENT IS GATED ON WHO IS SPEAKING (quince#555). `X-Forwarded-Proto` is believed only
// from a peer in `trusted`, and only when `trusted` is CONFIGURED — an unset list means believe the
// header from anyone, which is exactly today's behaviour, so no existing deployment changes.
//
// The old justification was *"X-Forwarded-Proto only ever upgrades to Secure, so trusting it cannot
// weaken"*. True of the COOKIE, and false of the two consumers that now exist:
//
//   - `Service.CookieWillBeDiscarded` is `Secure(r) && !SecureOrigin(r)`, so it INVERTS. An injected
//     header makes it report false while the browser still discards the cookie — suppressing the
//     quince#497 login-loop warning in precisely the case that warning exists to name.
//   - Onboarding step 1 reports `Complete` from this predicate (quince#554), so an injected header
//     tells the operator their setup is finished when it is not.
//
// Both fail toward *everything is fine* on a connection that is not encrypted, which is why a header
// that can only add `Secure` stopped being harmless.
func SecureOrigin(r *http.Request, trusted *TrustedProxies) bool {
	if r.TLS != nil {
		return true
	}
	if !strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return false
	}
	// Unset list → believe it, as before. Configured → only from a peer the operator named.
	return !trusted.Configured() || trusted.TrustsPeer(r)
}

func isLoopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
