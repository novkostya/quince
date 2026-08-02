// Package auth implements the single-admin authentication and web-security primitives
// (design §6): argon2id password (first-run set-password with a one-shot 409 guard),
// cookie sessions with per-client rotation-on-login and idle/absolute timeouts, per-IP login rate
// limiting, and double-submit CSRF. Admin-session timeouts are hardcoded this rung —
// schema v0 has no key for them (sessions.ttl_minutes is the vault-unlock TTL); a future
// `auth:` config section is noted for qn.6.
package auth

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/store"
)

const settingPasswordHash = "password_hash"

// Auth state strings for GET /api/auth/status (contracts §1, rung-ruled).
const (
	StateNeedsSetup    = "needs_setup"
	StateNeedsLogin    = "needs_login"
	StateAuthenticated = "authenticated"
)

// Sentinel errors; handlers map these to HTTP statuses.
var (
	ErrAlreadyConfigured = errors.New("auth: admin password already set") // → 409
	ErrNoPassword        = errors.New("auth: no admin password set")
	ErrBadPassword       = errors.New("auth: bad password")      // → 401
	ErrRateLimited       = errors.New("auth: too many attempts") // → 429
	ErrNoSession         = errors.New("auth: no such session")
	ErrSessionExpired    = errors.New("auth: session expired")
	ErrWeakPassword      = errors.New("auth: password too short") // → 422
)

// Service holds the auth dependencies and tunables.
type Service struct {
	store           *store.Store
	log             *slog.Logger
	now             func() time.Time
	limiter         *loginLimiter
	params          argonParams
	hash            func(string, argonParams) (string, error) // seam: tests count derivations
	idleTimeout     time.Duration
	absoluteTimeout time.Duration
	minPasswordLen  int
	insecureCookies bool // demo only: never set Secure, so cookies work over plain http
	// allowInsecureTransport is the user's `sessions.allow_insecure_transport` opt-in. Not
	// the same thing as insecureCookies above — see SetAllowInsecureTransport.
	allowInsecureTransport bool
}

// NewService returns a Service with production defaults.
func NewService(st *store.Store, log *slog.Logger) *Service {
	return &Service{
		store:           st,
		log:             log,
		now:             time.Now,
		limiter:         newLoginLimiter(10, time.Minute),
		params:          defaultArgonParams(),
		hash:            hashPassword,
		idleTimeout:     12 * time.Hour,      // single-user LAN: present but not aggressive
		absoluteTimeout: 30 * 24 * time.Hour, // hard cap regardless of activity
		minPasswordLen:  1,                   // non-empty only (test/demo use short passwords); strength policy deferred
	}
}

// SetInsecureCookies forces the Secure flag off (demo mode only, so login works over the
// plain-http address the e2e app and screenshots run on). Never set in production.
func (s *Service) SetInsecureCookies(v bool) { s.insecureCookies = v }

// SetAllowInsecureTransport applies `sessions.allow_insecure_transport` (qn.6f slice 8).
// Off by default, and DISTINCT from SetInsecureCookies even though both end in a cookie
// without Secure: this one is the user's own declared choice for a network they trust, it
// relaxes only the fallback, and it is surfaced as a degraded mode. Demo mode is a test
// affordance that must never be set in production. Two reasons, two switches, and
// collapsing them would make a production setting reachable from a demo flag.
//
// Applied at process start — schema v0 has no live config reload.
func (s *Service) SetAllowInsecureTransport(v bool) { s.allowInsecureTransport = v }

// Secure decides the Secure cookie flag for this request: the loopback-vs-https rule
// (cookie.go) relaxed by the user's own opt-in, and overridden off entirely in demo mode.
func (s *Service) Secure(r *http.Request) bool {
	if s.insecureCookies {
		return false
	}
	return secureCookie(r, s.allowInsecureTransport)
}

// CookieWillBeDiscarded reports whether a session cookie issued for THIS request would be
// marked Secure and then dropped by the browser for arriving over an insecure origin — the
// login-loop condition of quince#497. It is true for exactly one case: plain http to a
// non-loopback host, outside demo mode.
//
// Deliberately phrased as Secure(r) && !SecureOrigin(r) rather than re-deriving the host
// test. It is the same predicate asked from the other side, so a change to the Secure rule
// (or to demo mode) cannot leave this answer behind — and a second copy of a security
// predicate is a thing that drifts.
func (s *Service) CookieWillBeDiscarded(r *http.Request) bool {
	return s.Secure(r) && !SecureOrigin(r)
}

// HasPassword reports whether the admin password has been set.
func (s *Service) HasPassword() (bool, error) {
	_, ok, err := s.store.GetSetting(settingPasswordHash)
	return ok, err
}

// Status returns the tri-state for GET /api/auth/status given the request's session id
// ("" if no cookie).
func (s *Service) Status(sessionID string) (string, error) {
	has, err := s.HasPassword()
	if err != nil {
		return "", err
	}
	if !has {
		return StateNeedsSetup, nil
	}
	if sessionID != "" {
		if _, err := s.Authenticate(sessionID); err == nil {
			return StateAuthenticated, nil
		}
	}
	return StateNeedsLogin, nil
}

// SetPassword sets the admin password on first run only. It returns ErrAlreadyConfigured
// (→ 409) if a password already exists — setup succeeds exactly once, so it can never be
// an unauthenticated password reset (Operator ruling).
// It is RATE-LIMITED, and quince#520 changed why rather than removing the need. On a CONFIGURED
// instance the 409 is now cheap — the existence check runs before the derivation, so a probe costs
// a database read. On an UNCONFIGURED one it is not: until somebody sets a password every request
// legitimately reaches the 64 MiB derivation, so a fresh instance is still the amplifier quince#463
// measured at 9 MB → 2063 MB RSS over sixty requests of ~100 bytes.
//
// That window is exactly first-run — the one moment the route must stay open, and the one moment
// nobody is watching the box.
//
// The LOGIN limiter, shared deliberately: they are the same resource (a pre-auth credential
// endpoint) and the same attacker, and giving setup its own bucket would let one client spend both
// budgets. Post-quince#547 the bucket is per-visitor rather than per-proxy, which is what makes
// sharing safe — before that, one visitor exhausting it denied setup to everybody.
func (s *Service) SetPassword(password, clientIP string) error {
	if !s.limiter.allow(clientIP, s.now()) {
		return ErrRateLimited
	}
	if len(password) < s.minPasswordLen {
		return ErrWeakPassword
	}
	// ASK BEFORE DERIVING. argon2id is deliberately expensive — 64 MiB and ~85 ms with the
	// production params — and on a configured instance the 409 below is already decided, so
	// deriving first made POST /api/auth/setup a remote amplifier: ~100 bytes in, 64 MiB and
	// ~85 ms out, on a route that is pre-auth and carries no rate limit because first-run setup
	// must be reachable with no session. Measured before this check existed: 9 MB → 2063 MB RSS
	// over 60 requests, tracking peak concurrency × the argon2 memory parameter (quince#463).
	has, err := s.HasPassword()
	if err != nil {
		return err
	}
	if has {
		return ErrAlreadyConfigured
	}
	hash, err := s.hash(password, s.params)
	if err != nil {
		return err
	}
	// SetSettingIfAbsent REMAINS the authority, and the check above is not a substitute for it:
	// the two are not atomic together, so concurrent first-run setups both pass the check, both
	// derive, and exactly one insert wins. That is the pre-existing behaviour and the reason this
	// cannot be collapsed into a plain Set.
	inserted, err := s.store.SetSettingIfAbsent(settingPasswordHash, hash)
	if err != nil {
		return err
	}
	if !inserted {
		return ErrAlreadyConfigured
	}
	return nil
}

// Login verifies the password (rate-limited first) and, on success, rotates to a fresh
// session and returns it plus a new CSRF token. clientIP is used for rate limiting + audit.
//
// priorSessionID is the session the CALLING client already holds, or "" if it holds none —
// it is the only session this login supersedes. Other devices keep theirs (quince#373).
func (s *Service) Login(password, clientIP, priorSessionID string) (store.AuthSession, string, error) {
	now := s.now()
	if !s.limiter.allow(clientIP, now) {
		return store.AuthSession{}, "", ErrRateLimited
	}
	hash, ok, err := s.store.GetSetting(settingPasswordHash)
	if err != nil {
		return store.AuthSession{}, "", err
	}
	if !ok {
		return store.AuthSession{}, "", ErrNoPassword
	}
	match, err := verifyPassword(password, hash)
	if err != nil {
		return store.AuthSession{}, "", err
	}
	if !match {
		s.audit("login_failed", clientIP)
		return store.AuthSession{}, "", ErrBadPassword
	}
	// Rotation is PER CLIENT: the caller's own prior session is superseded, and every OTHER
	// device's session is left alone (Operator ruling, quince#373).
	//
	// What defeats fixation is the fresh id minted immediately below — an id planted in the
	// victim's browser beforehand is not the id they end up holding, and it never becomes
	// authenticated. Deleting OTHER sessions adds nothing to that: it is a separate "one
	// concurrent session" policy that was carrying fixation's justification, while costing
	// something canon promises — ui.design.md calls the iPhone a first-class client, and a
	// second first-class client that evicts the first is not one. The Operator hit it in real
	// use: driving the app from an iPad signed the desktop out.
	//
	// priorSessionID is "" for a client arriving without a session cookie (the ordinary first
	// login), and then nothing is deleted rather than the delete being widened.
	if priorSessionID != "" {
		if err := s.store.DeleteAuthSession(priorSessionID); err != nil {
			return store.AuthSession{}, "", err
		}
	}
	sess := store.AuthSession{
		ID:         id.Token(32),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.absoluteTimeout),
	}
	if err := s.store.CreateAuthSession(sess); err != nil {
		return store.AuthSession{}, "", err
	}
	s.limiter.reset(clientIP)
	s.audit("login", clientIP)
	return sess, NewCSRFToken(), nil
}

// Authenticate validates a session id, enforcing absolute and idle expiry, and idle-touches
// it (throttled). Returns the live session or an error.
func (s *Service) Authenticate(sessionID string) (store.AuthSession, error) {
	sess, ok, err := s.store.GetAuthSession(sessionID)
	if err != nil {
		return store.AuthSession{}, err
	}
	if !ok {
		return store.AuthSession{}, ErrNoSession
	}
	now := s.now()
	if now.After(sess.ExpiresAt) || now.Sub(sess.LastSeenAt) > s.idleTimeout {
		_ = s.store.DeleteAuthSession(sessionID)
		return store.AuthSession{}, ErrSessionExpired
	}
	if now.Sub(sess.LastSeenAt) > time.Minute { // throttle writes
		if err := s.store.TouchAuthSession(sessionID, now); err == nil {
			sess.LastSeenAt = now
		}
	}
	return sess, nil
}

// Logout deletes the session.
func (s *Service) Logout(sessionID string) error {
	if sessionID == "" {
		return nil
	}
	s.audit("logout", "")
	return s.store.DeleteAuthSession(sessionID)
}

func (s *Service) audit(event, clientIP string) {
	detail := ""
	if clientIP != "" {
		detail = "ip=" + clientIP
	}
	if err := s.store.AppendAudit(store.AuditEntry{
		ID: id.New(), TS: s.now(), Event: event, Detail: detail,
	}); err != nil {
		s.log.Error("audit append failed", "event", event, "error", err)
	}
}
