// Package auth implements the single-admin authentication and web-security primitives
// (design §6): argon2id password (first-run set-password with a one-shot 409 guard),
// cookie sessions with rotation-on-login and idle/absolute timeouts, per-IP login rate
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
	idleTimeout     time.Duration
	absoluteTimeout time.Duration
	minPasswordLen  int
	insecureCookies bool // demo only: never set Secure, so cookies work over plain http
}

// NewService returns a Service with production defaults.
func NewService(st *store.Store, log *slog.Logger) *Service {
	return &Service{
		store:           st,
		log:             log,
		now:             time.Now,
		limiter:         newLoginLimiter(10, time.Minute),
		params:          defaultArgonParams(),
		idleTimeout:     12 * time.Hour,      // single-user LAN: present but not aggressive
		absoluteTimeout: 30 * 24 * time.Hour, // hard cap regardless of activity
		minPasswordLen:  1,                   // non-empty only (test/demo use short passwords); strength policy deferred
	}
}

// SetInsecureCookies forces the Secure flag off (demo mode only, so login works over the
// plain-http address the e2e app and screenshots run on). Never set in production.
func (s *Service) SetInsecureCookies(v bool) { s.insecureCookies = v }

// Secure decides the Secure cookie flag for this request: the loopback-vs-https rule
// (cookie.go), overridden off in demo mode.
func (s *Service) Secure(r *http.Request) bool {
	if s.insecureCookies {
		return false
	}
	return secureCookie(r)
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
func (s *Service) SetPassword(password string) error {
	if len(password) < s.minPasswordLen {
		return ErrWeakPassword
	}
	hash, err := hashPassword(password, s.params)
	if err != nil {
		return err
	}
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
func (s *Service) Login(password, clientIP string) (store.AuthSession, string, error) {
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
	// Rotation: a fresh login supersedes any prior session (single admin) — defeats fixation.
	if err := s.store.DeleteAllAuthSessions(); err != nil {
		return store.AuthSession{}, "", err
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
