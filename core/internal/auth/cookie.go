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
// downgrade). X-Forwarded-Proto only ever upgrades to Secure, so trusting it cannot weaken
// anything.
func secureCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		return true
	}
	return !isLoopbackHost(r.Host)
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
