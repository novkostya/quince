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
	// The fifth route, and the ONLY onboarding path exempted — by exact path, step 1 only
	// (Operator ruling 2026-08-02, quince#501). A prefix would silently exempt every future
	// onboarding step, and this switch has no prefix support to do it with by accident.
	case "GET /api/health", "GET /api/auth/status", "POST /api/auth/login", "POST /api/auth/setup",
		"GET /api/onboarding/https":
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

// setupAllowed is the set of endpoints reachable while quince has NO STORAGE DECLARED (qn.6e,
// Operator ruling 2026-08-07 — option (a): any zero-storage start IS the onboarding state).
//
// BY EXACT PATH, and for the same reason `authExempt` is: a prefix would silently widen this every
// time a route is added, and the whole point of the mode is that it is narrow. Adding one is a
// deliberate act.
//
// The set is auth, onboarding, config — and THE TWO PROBES. The probes are not an afterthought: the
// onboarding storage step cannot function without them, since its entire job is to let a user check
// a path and a helper before declaring anything. Omitting them would leave the mode serving a form
// that cannot fill itself in.
//
// `GET /api/health` stays reachable so a container healthcheck does not report a daemon down when it
// is up and waiting to be configured.
func setupAllowed(r *http.Request) bool {
	switch r.Method + " " + r.URL.Path {
	case "GET /api/health",
		"GET /api/auth/status", "POST /api/auth/login", "POST /api/auth/setup", "POST /api/auth/logout",
		"GET /api/onboarding/https",
		"GET /api/config", "PUT /api/config", "POST /api/config/storage",
		"POST /api/storages/probe", "POST /api/storages/probe/hook":
		return true
	}
	return false
}

// setupGuard refuses everything outside setupAllowed while no storage is declared.
//
// THE ZERO-STORAGE CONDITION IS THE STATE — there is no flag, no persisted step and no UI-only
// state, which is the shape the ruling was taken on. It is read per request from the live config, so
// the mode ENDS the moment a storage is added, without a restart and without anything to reset.
//
// WHY REFUSE AT ALL, rather than let the empty list answer naturally: it mostly would — `Storages`
// returns nothing and `ResolveChoice` already 409s on an empty list (qn.6g). But "mostly" is the
// problem. A daemon that lists devices and accepts pairing while having nowhere to put a backup is
// exactly the thing `storagereq.go`'s refusal calls "looks healthy and silently protects nothing".
// The mode says so once, in one place, instead of relying on every subsystem to be honest
// separately.
//
// 503 rather than 409 or 422: this is a statement about the SERVER's readiness, not about the
// request, and `Retry-After` semantics are the right family — the condition clears when the operator
// finishes setup, not when the client changes anything.
func (d Deps) setupGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if d.StorageRequired != nil && d.StorageRequired() && !setupAllowed(r) {
			writeError(w, d.Log, http.StatusServiceUnavailable, "storage_required",
				"quince has no storage declared yet — finish setup by adding one before using this")
			return
		}
		next.ServeHTTP(w, r)
	})
}
