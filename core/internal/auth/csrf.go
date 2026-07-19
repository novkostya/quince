package auth

import (
	"crypto/subtle"
	"net/http"

	"github.com/novkostya/quince/core/internal/id"
)

// NewCSRFToken mints a fresh double-submit CSRF token.
func NewCSRFToken() string { return id.Token(32) }

// CSRFTokenFromRequest returns the token in the CSRF cookie, or "" if absent.
func CSRFTokenFromRequest(r *http.Request) string {
	c, err := r.Cookie(CSRFCookieName)
	if err != nil {
		return ""
	}
	return c.Value
}

// CheckCSRF validates the double-submit invariant: a non-empty CSRF cookie whose value
// matches the request header, compared in constant time. SameSite=Strict is the primary
// defense; this is transparent defense-in-depth (an off-origin page can neither read the
// cookie nor set the custom header).
func CheckCSRF(r *http.Request) bool {
	cookie := CSRFTokenFromRequest(r)
	header := r.Header.Get(CSRFHeaderName)
	if cookie == "" || header == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(cookie), []byte(header)) == 1
}
