package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/novkostya/quince/core/internal/auth"
)

// maxBodyBytes caps JSON request bodies (design §6: response/request size limits).
const maxBodyBytes = 1 << 20 // 1 MiB

type middleware func(http.Handler) http.Handler

// chain applies middlewares outermost-first: chain(h, a, b) == a(b(h)).
func chain(h http.Handler, mws ...middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// recoverMW turns a handler panic into a logged 500, so nothing escapes the process.
func recoverMW(log *slog.Logger) middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					log.Error("panic in handler", "error", rec, "path", r.URL.Path)
					w.Header().Set("Content-Type", "application/json; charset=utf-8")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"internal server error"}}`))
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// securityHeaders sets the baseline headers on every response (design §6): a strict CSP,
// frame denial, nosniff, no-referrer. connect-src allows same-origin ws/wss for /api/ws;
// style-src allows inline styles (React style attributes) — script-src stays 'self' only.
// The exact CSP is verified against the real Vite/Tailwind bundle at integration.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"connect-src 'self' ws: wss:; " +
		"img-src 'self' data:; " +
		"style-src 'self' 'unsafe-inline'; " +
		"script-src 'self'; " +
		"base-uri 'self'; " +
		"frame-ancestors 'none'; " +
		"object-src 'none'"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

func bodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		next.ServeHTTP(w, r)
	})
}

// authExempt is the set of endpoints reachable without a session (first-run + login path
// and the always-open health probe).
func authExempt(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET /api/health", "GET /api/auth/status", "POST /api/auth/login", "POST /api/auth/setup":
		return true
	}
	return false
}

// authGuard requires a valid session for everything except the exempt endpoints, and
// (re)issues the double-submit CSRF cookie so a subsequent mutation has a token.
func (d Deps) authGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authExempt(r) {
			if _, err := d.Auth.Authenticate(sessionCookieValue(r)); err != nil {
				writeError(w, d.Log, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}
		}
		next.ServeHTTP(w, ensureCSRF(w, r, d.Auth.Secure(r)))
	})
}

// csrfGuard enforces the double-submit token on state-changing methods, exempting the
// pre-session auth POSTs (login/setup have no CSRF cookie yet; they are protected by
// SameSite=Strict + the login rate limit).
func (d Deps) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) || csrfExempt(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !auth.CheckCSRF(r) {
			writeError(w, d.Log, http.StatusForbidden, "csrf", "missing or invalid CSRF token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func csrfExempt(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/auth/login", "/api/auth/setup":
		return true
	}
	return false
}

type ctxKey int

const csrfCtxKey ctxKey = iota

// ensureCSRF guarantees a CSRF cookie exists, minting one if absent, and stashes the token
// in the request context so a handler (e.g. auth/status) can echo it in its body.
func ensureCSRF(w http.ResponseWriter, r *http.Request, secure bool) *http.Request {
	tok := auth.CSRFTokenFromRequest(r)
	if tok == "" {
		tok = auth.NewCSRFToken()
		http.SetCookie(w, auth.CSRFCookie(tok, secure))
	}
	return r.WithContext(context.WithValue(r.Context(), csrfCtxKey, tok))
}

func csrfFromContext(r *http.Request) string {
	if v, ok := r.Context().Value(csrfCtxKey).(string); ok {
		return v
	}
	return ""
}
