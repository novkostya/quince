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
// style-src allows inline styles (React style attributes).
//
// script-src IS 'self' PLUS ONE HASH, AND THE HASH IS NOT A RELAXATION. `index.html` carries a
// single inline script — the theme class applied before the first paint — which is inline on
// purpose: an external file is a second round trip before paint and `defer`/`async` would land it
// after the moment it exists to precede. A hash admits exactly that script and nothing else; adding
// 'unsafe-inline' would admit every one, including any an injection manages to plant.
//
// THE HASH IS PINNED IN A TEST RATHER THAN TRUSTED, and that is the load-bearing half.
// `TestTheCSPAdmitsEveryInlineScriptTheUIShips` recomputes it from `ui/index.html` and fails with
// the correct value when the script changes. Without it the failure mode is the one this whole
// paragraph exists because of: the browser blocks the script, the page still renders, tests stay
// green, and only a console nobody reads says so.
func securityHeaders(next http.Handler) http.Handler {
	const csp = "default-src 'self'; " +
		"connect-src 'self' ws: wss:; " +
		"img-src 'self' data:; " +
		"style-src 'self' 'unsafe-inline'; " +
		"script-src 'self' 'sha256-MhkbBEF74pAaMUPN+tJQk2wgEbqcEzfZwpr8GnphCgY='; " +
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
		"GET /api/onboarding/https",
		// THE PROBE PAIR (Operator ruling 2026-08-14). Pre-auth for step 1's own reason: the page
		// that runs the probe is the page explaining why you cannot log in yet, so it sits outside
		// every guard and so must what it calls. Both are READS — the mint creates ceremony state,
		// not configuration.
		//
		// `/api/onboarding/` MEANT PRE-AUTH AND READ-ONLY WHEN THESE TWO WERE ADDED, and it stopped
		// meaning read-only on 2026-08-14 when the certificate CONFIRM landed under it (below). The
		// clause that was ever load-bearing survives: the prefix is not the exemption, every entry
		// here is an exact path, and this switch has no prefix support to widen by accident.
		"GET /api/onboarding/probe/nonce", "GET /api/onboarding/probe",
		// The offline certificate check, pre-auth for the same reason and Configured()-gated in the
		// handler. A READ of two files the caller names — see handlers_certprobe.go for why that is
		// bounded by the same argument as the pre-auth config write.
		"POST /api/onboarding/certificate",
		// THE TRIAL AND ITS PROOF (Operator ruling 2026-08-14, quince#908 slice 5).
		//
		// `apply` WRITES NO CONFIGURATION — it points the running Keeper at a pair and schedules a
		// return to the one `config.yml` names. So the one pre-auth WRITE in this product's
		// onboarding prefix is `confirm`, and only after a request has arrived on the TLS half
		// carrying the trial's token. Deferring the write is what keeps a certificate somebody tried
		// and abandoned out of a file D12 says holds only what the user set.
		//
		// BOTH ARE Configured()-GATED IN THE HANDLERS. Being exempt is what makes them reachable;
		// being one-shot is what makes them safe — an exemption here is only ever half of a decision.
		"POST /api/onboarding/certificate/apply", "POST /api/onboarding/certificate/confirm",
		// AND THE DECLINE, which is the confirm's other answer and reachable in the same window
		// (quince#1158). Refusing it here would leave "yes" pre-auth and "no" impossible, so the only
		// way out of a trial the user does not want would be to wait the window out.
		"POST /api/onboarding/certificate/cancel",
		// PASSKEY ASSERTION IS PRE-AUTH BY DEFINITION — it is how a session is obtained (qn.6k).
		// Registration is deliberately NOT here: it needs a session, which is what makes it the
		// half that touches none of these lists.
		"POST /api/auth/passkeys/login/begin", "POST /api/auth/passkeys/login/finish",
		// FIRST-RUN PASSKEY REGISTRATION IS PRE-AUTH BY THE SAME LOGIC AS `POST /api/auth/setup`
		// (qn.6m D5): first run has no session, and creating one is what it does. One-shot — 409 once
		// `Configured()` is true — so it closes the instant the install is claimed.
		"POST /api/auth/setup/passkey/begin", "POST /api/auth/setup/passkey/finish",
		// THE FIRST PRE-AUTH MUTATION IN THIS LIST THAT IS NOT ABOUT OBTAINING A CREDENTIAL
		// (Operator ruling 2026-08-14, quince#908 slice 6). It writes
		// `sessions.allow_insecure_transport` and nothing else, so a first-run user stranded on
		// plain http has an exit that is not a shell on the box.
		//
		// ITS BOUND IS `Configured()`, IN THE HANDLER, NOT IN THIS LIST. Being exempt is what makes
		// it reachable; being one-shot is what makes it safe, and on a claimed install it answers
		// 409 — the same guard the two routes above already carry. Read them together: an exemption
		// here is only ever half of a decision.
		//
		// UNDER `/api/config/` RATHER THAN `/api/onboarding/`, ruled. That prefix means pre-auth
		// and READ-ONLY, and the product's first pre-auth write beneath it would invite the prefix
		// to be read as the exemption. This one means AUTHENTICATED, so the exception is visible as
		// an exception — and this switch still has no prefix support to widen by accident.
		"POST /api/config/insecure-transport":
		return true
	}
	return false
}

// authGuard requires a valid session for everything except the exempt endpoints, and
// (re)issues the double-submit CSRF cookie so a subsequent mutation has a token.
func (d Deps) authGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !authExempt(r) {
			// THE SESSION IS NO LONGER DISCARDED. It was `if _, err := ...` until qn.13 slice 3,
			// which quince#1342 read as a principal being thrown away — but the value had no
			// identity in it until 0014_session_principal.sql gave it one. Now it does, and this
			// is the single place it enters a request.
			sess, err := d.Auth.Authenticate(sessionCookieValue(r))
			if err != nil {
				writeError(w, d.Log, http.StatusUnauthorized, "unauthorized", "authentication required")
				return
			}
			// NOTHING READS THIS YET, and that is the slice boundary rather than an oversight:
			// scope arrives in slice 4 and the routes that consult it in slice 9. Binding it here
			// first means those slices add a capability check to a request that already knows who
			// it is, instead of each one re-deriving a principal its own way.
			r = r.WithContext(withPrincipal(r.Context(), auth.PrincipalOf(sess)))
		}
		// `CSRFSecure`, NOT `Secure` — the double-submit cookie's flag is a different decision from
		// the session cookie's (Operator ruling 2026-08-17, quince#1156). The reasoning is at that
		// method; what matters here is that these two lines must not be collapsed back into one.
		next.ServeHTTP(w, ensureCSRF(w, r, d.Auth.CSRFSecure(r)))
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
			writeError(w, d.Log, http.StatusForbidden, "csrf", d.csrfRefusal(r))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// csrfRefusal is the sentence a person reads when the double submit fails, and it is a sentence
// rather than a constant because quince KNOWS WHY. Naming the mechanism — a token, a cookie, an
// acronym — tells the reader about machinery they have no view of and hands them no remedy.
//
// ONE CAUSE IS KNOWABLE AND IT IS THE ONE THAT BITES. `CookieWillBeDiscarded` is true for exactly
// plain http to a non-loopback host, where the cookie is issued `Secure` and a browser will not keep
// it — so every mutation from such a page fails this check, INCLUDING the certificate step, which is
// how somebody gets off plain http in the first place. Asked here rather than re-derived: it is the
// same predicate the login loop of quince#497 is guarded by, and a second copy of a security
// predicate is a thing that drifts.
//
// THE REST STAY ONE SENTENCE, deliberately. A stale tab, a rotated token, a request that came from
// somewhere else entirely — one remedy covers them, and enumerating causes would be guessing in
// prose about a request quince cannot see the origin of.
func (d Deps) csrfRefusal(r *http.Request) string {
	if d.Auth.CookieWillBeDiscarded(r) {
		return "this page is plain http, so your browser will not keep the cookie quince uses to " +
			"recognise its own pages — and without it quince cannot tell that this request came " +
			"from one. Reach quince over https, or allow plain HTTP first"
	}
	return "quince could not tell that this request came from one of its own pages — reload the page and try again"
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func csrfExempt(r *http.Request) bool {
	switch r.URL.Path {
	case "/api/auth/login", "/api/auth/setup",
		// THE THIRD EXACT-PATH LIST, which the qn.6k spec did not name — it said "both". There is
		// no CSRF cookie before login, and the assertion pair is how a passkey user gets one, so
		// omitting it here would make passkey sign-in fail on the double-submit check with a
		// message about CSRF rather than about anything the user did.
		"/api/auth/passkeys/login/begin", "/api/auth/passkeys/login/finish",
		// First-run passkey registration (qn.6m D5): no CSRF cookie exists before a session does.
		"/api/auth/setup/passkey/begin", "/api/auth/setup/passkey/finish",
		// The pre-auth transport opt-in, for the same reason as everything else here: it is
		// reachable before a session exists, so there is no CSRF cookie to double-submit.
		//
		// WHAT PROTECTS IT INSTEAD IS NOT SameSite. The comment above this switch names
		// `SameSite=Strict` plus the login rate limit as what stands in for CSRF on the auth
		// POSTs; neither applies here — there is no cookie to be strict about and no limiter on
		// this path. The bound is `Configured()`: on an unclaimed install a cross-site forgery
		// achieves what any visitor could achieve directly, and on a claimed one the route answers
		// 409 before reading the body. That is why the guard has to be the FIRST thing the handler
		// does rather than a check somewhere in the middle.
		"/api/config/insecure-transport",
		// THE CERTIFICATE CONFIRM, AND ONLY THE CONFIRM (quince#908 slice 5). It is reached on a
		// DIFFERENT ORIGIN from the page that applied — `https://name:port` where the apply happened
		// on `http://host:port` — so the CSRF cookie set on the plain origin is not sent with it, and
		// requiring a double-submit would refuse the one request the whole ceremony exists to receive.
		//
		// ITS APPLY IS DELIBERATELY NOT HERE, which is why these are two entries rather than a prefix.
		// The apply is same-origin with the page that sends it, so it holds a token and can
		// double-submit — `ensureCSRF` mints the cookie on every request including exempt ones, which
		// is why being pre-auth never implied being unable to (handlers_certprobe.go makes the same
		// argument for the offline check).
		//
		// THE DECLINE ARRIVES FROM THE SAME PAGE ON THE SAME ORIGIN as the confirm, so the same
		// reasoning covers it: the CSRF cookie was set on the plain origin the apply happened on and
		// is not sent with either.
		"/api/onboarding/certificate/confirm", "/api/onboarding/certificate/cancel":
		return true
	}
	return false
}

type ctxKey int

const csrfCtxKey ctxKey = iota

// ensureCSRF guarantees a CSRF cookie exists, minting one if absent, and stashes the token
// in the request context so a handler (e.g. auth/status) can echo it in its body.
//
// THE COOKIE IS RE-SENT EVERY TIME, NOT ONLY WHEN MINTED, AND THE FLAG IS WHY. A browser tells the
// server nothing about a cookie's attributes — only its value comes back — so a token minted while
// the origin was insecure would keep the flag it was born with for as long as it survives, however
// the origin changes underneath it. That is a live sequence rather than a hypothetical: this product
// exists to move an install from http to https, and it can happen without a restart the moment a
// certificate is confirmed.
//
// The VALUE is minted once and reused; only the attributes are refreshed, so nothing depends on the
// token staying put across a scheme change.
func ensureCSRF(w http.ResponseWriter, r *http.Request, secure bool) *http.Request {
	tok := auth.CSRFTokenFromRequest(r)
	if tok == "" {
		tok = auth.NewCSRFToken()
	}
	http.SetCookie(w, auth.CSRFCookie(tok, secure))
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
		// The probe pair, for the same reason the HTTPS step is here: a zero-storage first run is
		// exactly when somebody is working out how to reach quince securely, and 503ing the probe
		// would leave that step unable to check the name it is about to recommend.
		"GET /api/onboarding/probe/nonce", "GET /api/onboarding/probe",
		// The offline certificate check, pre-auth for the same reason and Configured()-gated in the
		// handler. A READ of two files the caller names — see handlers_certprobe.go for why that is
		// bounded by the same argument as the pre-auth config write.
		"POST /api/onboarding/certificate",
		// AND THE TRIAL PAIR (quince#908 slice 5), for the reason the transport opt-in gives below: a
		// zero-storage first run is exactly the install where somebody is working out how to reach
		// quince securely, and 503ing the apply would leave the https step able to CHECK a certificate
		// and unable to use it. Leaving out the confirm would be worse than leaving out both — the
		// trial would start and could never be kept.
		"POST /api/onboarding/certificate/apply", "POST /api/onboarding/certificate/confirm",
		// The decline belongs beside them: a trial that can start on a zero-storage install must be
		// escapable on one.
		"POST /api/onboarding/certificate/cancel",
		"GET /api/config", "PUT /api/config", "POST /api/config/storage",
		// The pre-auth transport opt-in (quince#908 slice 6). A zero-storage first run is exactly
		// the install it exists for — the user has declared nothing yet and cannot even set a
		// password — so leaving it out would 503 the one escape from the dead end, which is
		// quince#898 and quince#912's shape a third time.
		"POST /api/config/insecure-transport",
		"POST /api/storages/probe", "POST /api/storages/probe/hook",
		// THE ZFS BRANCH OF THAT SAME FORM (quince#818 pieces B and C). `probe` and `probe/hook`
		// were exempted and these two were not, which made the zfs path of the first-run screen
		// unreachable ON THE FIRST RUN: pressing either button returned this guard's 503, so the
		// key could not be generated and the helper could not be rendered — and neither can be
		// skipped, because the helper cannot answer `probe/hook` until its key is installed. A
		// chicken-and-egg with no way out of it, found by walking onboarding on a clean stand.
		//
		// NO GATE IS BEING WEAKENED. Both are already behind `RequireAuth`; this guard is about
		// SERVER READINESS, not authorisation. `zfs/key` writes only to quince's own `/data/keys`
		// and takes no caller path (see handleStorageZFSKey), and `zfs/helper` is a pure function
		// of the embedded script plus one validated query parameter.
		"POST /api/storages/zfs/key", "GET /api/storages/zfs/helper",
		// The host-key ceremony, for the same reason: it is the LAST step of the first-run zfs
		// form, so 503ing it would reproduce quince#898 one endpoint over (quince#912).
		"POST /api/storages/zfs/hostkey", "POST /api/storages/zfs/hostkey/trust",
		// A STORAGELESS INSTALL IS EXACTLY WHERE ONBOARDING OFFERS A PASSKEY, so signing in with
		// one has to work here or the offer leads somewhere unusable (qn.6k).
		"POST /api/auth/passkeys/login/begin", "POST /api/auth/passkeys/login/finish",
		// First-run passkey registration (qn.6m D5). A storageless install IS the onboarding state,
		// which is exactly where the passwordless option is offered — omitting these would 503 the
		// one path this rung exists to open.
		"POST /api/auth/setup/passkey/begin", "POST /api/auth/setup/passkey/finish":
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
