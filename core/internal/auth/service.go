// Package auth implements the single-admin authentication and web-security primitives
// (design §6): argon2id password (first-run set-password with a one-shot 409 guard),
// cookie sessions with per-client rotation-on-login and idle/absolute timeouts, per-IP login rate
// limiting, and double-submit CSRF. Admin-session timeouts are hardcoded this rung — schema v0
// has no key for them; a future `auth:` config section is noted for qn.6.
package auth

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
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
	// ErrNoCredential — the assertion named a passkey this quince does not hold (qn.6k). It is
	// deliberately NOT distinguishable by a caller from a credential registered against another
	// rpId: ResolveCredential checks existence FIRST, so the mismatch message is only ever shown
	// for a credential this quince actually has.
	ErrNoCredential = errors.New("auth: no such passkey")
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
	//
	// ATOMIC BECAUSE IT IS LIVE NOW (quince#900). It was a plain bool written once at startup
	// before anything served; the config applier writes it while every request in flight
	// reads it. `insecureCookies` above stays plain deliberately — it comes from a
	// command-line flag and has no live path to be written from.
	allowInsecureTransport atomic.Bool
	// proxies gates X-Forwarded-Proto in SecureOrigin (quince#555). Nil behaves as "unset",
	// which believes the header from anyone — today's behaviour, and the shipping default.
	proxies *TrustedProxies
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
// IT GOES BOTH WAYS, AND UNTIL quince#900 IT DID NOT. The setter has always accepted a
// `false`, but its only caller returned early on one — so nothing in a running process could
// lower the opt-in, and a settable field was not a live setting. The direction that could not
// be expressed is the one that matters: turn a certificate on, prove it works, and put the
// plaintext relaxation back. The config applier now calls this with whatever the file says.
func (s *Service) SetAllowInsecureTransport(v bool) { s.allowInsecureTransport.Store(v) }

// AllowsInsecureTransport reports the opt-in's current state, so GET /api/health can surface it
// and the UI can warn about it (quince#908 slice 6, Operator ruling 2026-08-14).
//
// FROM HERE RATHER THAN FROM THE CONFIG SNAPSHOT, which is the point of the method existing.
// `cfgSvc.Current().Sessions.AllowInsecureTransport` is the same fact read from the file's side,
// and reading it there would be a second implementation of one predicate — the hazard
// `RequireStorage` carries a paragraph about, from having been bitten by it. This returns the
// value `Secure` itself reads, so the warning and the behaviour it warns about cannot drift.
//
// NOT `Secure(r)` INVERTED, AND NOT `CookieWillBeDiscarded(r)`. Both answer a question about a
// REQUEST; this answers one about the DAEMON, and on the install that matters they disagree —
// with the opt-in on nothing is discarded, so the per-request predicate returns the reassuring
// answer on exactly the install a user must be warned about. HealthResponse says it again at
// the field, because that is where somebody will reach for the wrong one.
func (s *Service) AllowsInsecureTransport() bool { return s.allowInsecureTransport.Load() }

// Secure decides the Secure cookie flag for this request: the loopback-vs-https rule
// (cookie.go) relaxed by the user's own opt-in, and overridden off entirely in demo mode.
func (s *Service) Secure(r *http.Request) bool {
	if s.insecureCookies {
		return false
	}
	return secureCookie(r, s.allowInsecureTransport.Load(), s.proxies)
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
	return s.Secure(r) && !SecureOrigin(r, s.proxies)
}

// HasPassword reports whether the admin password has been set.
func (s *Service) HasPassword() (bool, error) {
	_, ok, err := s.store.GetSetting(settingPasswordHash)
	return ok, err
}

// Configured reports whether this install has been CLAIMED — a password hash exists, OR at least
// one passkey does. It is the predicate behind `needs_setup` and behind the one-shot guard on
// `POST /api/auth/setup`, and it is qn.6m D3.
//
// WITHOUT THE PASSKEY HALF, PASSWORDLESS IS AN UNAUTHENTICATED ADMIN TAKEOVER. Every one of these
// three facts predates this function, and none of them consulted the credentials table:
//
//	Status()                          → needs_setup on password absence ALONE
//	SetPassword()                     → 409 guard was HasPassword() + SetSettingIfAbsent
//	"POST /api/auth/setup"            → PRE-AUTH, by exact path (middleware.go)
//
// So the moment a passwordless install can exist, the password row is gone and an anonymous visitor
// is told `needs_setup`, shown the first-run screen, and allowed to complete setup — after which
// issueSessionResponse hands them an admin session. It also falsifies a promise already written in
// contracts §1: that setup "can never be an unauthenticated password reset".
//
// COUNTED WITHOUT AN rpId FILTER, WHICH IS THE OPPOSITE OF WHAT existingCredentials DOES, and the
// difference is the whole subtlety. Those two ask different questions:
//
//	can this credential SIGN IN here?      → filter by rpId. A credential bound elsewhere cannot.
//	has this install ever been CLAIMED?    → do NOT filter. It has been, wherever it was claimed.
//
// A quince reachable at two addresses whose only passkey is bound to the OTHER one must offer
// LOGIN at this one — which then fails honestly with qn.6k D2's "registered for <domain>" message —
// rather than offering first-run setup to a stranger. Filtering here would reopen the takeover
// through the second address, which is why the plainest-looking reuse in this package is wrong.
func (s *Service) Configured() (bool, error) {
	has, err := s.HasPassword()
	if err != nil {
		return false, err
	}
	if has {
		return true, nil
	}
	// PASSWORD FIRST, deliberately: on the overwhelmingly common configured install this returns
	// before touching the credentials table at all, so the check costs one setting read rather than
	// a second query — and SetPassword's amplifier guard (quince#463) depends on being cheap.
	n, err := s.store.CountPasskeys()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// Status returns the tri-state for GET /api/auth/status given the request's session id
// ("" if no cookie).
func (s *Service) Status(sessionID string) (string, error) {
	// Configured(), NOT HasPassword() — qn.6m D3. A passwordless install has been claimed and must
	// answer `needs_login`, so that an anonymous visitor is offered login rather than first run.
	has, err := s.Configured()
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
	// Configured(), NOT HasPassword() — qn.6m D3, and THIS is the line that closes the takeover.
	// Setup succeeds exactly once per install, and "once" has to mean "once it has been claimed by
	// any credential", or removing the password re-opens first-run setup to anyone who can load the
	// page. SetSettingIfAbsent below remains the atomic authority for the password row; this is an
	// additional refusal in front of it, exactly as the HasPassword short-circuit already was.
	has, err := s.Configured()
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

// ChangePassword sets a NEW admin password on an install that is already configured — qn.6m D4.
//
// SESSION REQUIRED (the handler enforces it) AND the CURRENT password besides, and the second is
// not belt-and-braces. A session is proof of a PAST authentication, not of present possession, and
// the one irreversible thing an attacker holding a stolen cookie can do is change the password and
// keep the owner out. It costs the legitimate user one field they already know.
//
// `current` MAY BE EMPTY, AND EXACTLY WHEN NO PASSWORD EXISTS. On a passwordless install "change"
// IS "set", and the state deciding which spelling applies is server-side anyway — so a separate
// add-a-password endpoint would be a fourth spelling of one idea. Where a password DOES exist, an
// empty `current` is simply a wrong one and takes the same 401.
//
// RATE-LIMITED ON THE SAME BUCKET as login and setup: it verifies a password, so it is a credential
// endpoint, and somebody holding a session must not get a fresh budget to guess the current password
// in. NOT reset on success — Login resets because a correct password is evidence the client is the
// owner, where here the session already said that, so a reset would only hand budget back.
// SUPERSEDED IN ONE CLAUSE BY qn.6n — the ruling on quince#888 item 3. Everything above remains the
// reasoning for demanding a present credential; what changed is that a PASSKEY is now an equal
// alternative, and that the empty-`current` case is no longer unguarded.
//
// *"`current` MAY BE EMPTY, AND EXACTLY WHEN NO PASSWORD EXISTS"* WAS A HOLE, and quince#888's table
// named it: on a passwordless install this path accepted **no proof at all** and minted a credential
// the owner could not revoke without console access. Rule 1 closes it — adding a password to a
// passwordless install now requires presenting the passkey.
//
// AND RULE 3 IS NOT A RELAXATION, though beside "the current password" it reads like one. The same
// authority was already reachable in two steps: `DELETE /api/auth/password`, needing only a passkey
// ROW, then this endpoint with an empty `current`, needing nothing at all. Rule 3 is that authority
// in one prompt instead of two, with the two-step path closed.
func (s *Service) ChangePassword(proofs *Proofs, pres Presented, next, sessionID, clientIP string) error {
	if !s.limiter.allow(clientIP, s.now()) {
		return ErrRateLimited
	}
	if len(next) < s.minPasswordLen {
		return ErrWeakPassword
	}
	// THE PRESENT CREDENTIAL, BEFORE ANYTHING IS WRITTEN. `RequirePresent` carries the first-run
	// exemption, the rate limit on the password branch and the proof's own bindings. The audit line
	// stays here because "a password change was refused" is this operation's event, not the
	// verifier's — the same reason the two have different names.
	if _, err := s.RequirePresent(proofs, pres, OpSetPassword, "", sessionID, clientIP); err != nil {
		if errors.Is(err, ErrBadPassword) {
			s.audit("password_change_failed", clientIP)
		}
		return err
	}
	newHash, err := s.hash(next, s.params)
	if err != nil {
		return err
	}
	// A plain Set, NOT SetSettingIfAbsent — this is the one path allowed to OVERWRITE the hash,
	// which is why it is a distinct method rather than a flag on SetPassword. SetPassword's one-shot
	// guard stays exactly as strict, because that is the PRE-AUTH one.
	if err := s.store.SetSetting(settingPasswordHash, newHash); err != nil {
		return err
	}
	s.audit("password_changed", clientIP)
	return nil
}

// ErrLastCredential — removing the password would leave no way to sign in at THIS address.
//
// It carries what was actually found, so the refusal can name the addresses the credentials it DID
// find belong to rather than answering "no passkeys" at a box that visibly has some. Same reasoning
// as ErrRPIDMismatch: the bare version of this message reads as "quince is broken".
type ErrLastCredential struct {
	Presented string   // the rpId this request arrived on
	Elsewhere []string // rpIds of credentials that exist but are bound elsewhere
}

func (e ErrLastCredential) Error() string {
	if len(e.Elsewhere) == 0 {
		return fmt.Sprintf("removing the password would leave no way to sign in: this quince holds "+
			"no passkey for %q. Add a passkey first.", e.Presented)
	}
	return fmt.Sprintf("removing the password would leave no way to sign in at %q: the passkeys "+
		"this quince holds are registered for %s, and a passkey only works at the address it was "+
		"created on. Add a passkey for %q first.",
		e.Presented, strings.Join(e.Elsewhere, ", "), e.Presented)
}

// ErrSelfRemoval — RULE 2: what was presented IS what is being removed, so it establishes nothing
// about whether anything would be left.
//
// A SEPARATE REFUSAL FROM ErrLastCredential/ErrLastPasskey BECAUSE THE REMEDIES DIFFER, which is the
// same test contracts.md applies when it refuses a second code for the two lockout paths. There the
// remedy is identical — add the other kind of credential — so one code carries both. Here it is not:
// this one says *try again with something else, which you have*, and the lockout pair says *you have
// nothing else, go and make one*. A client that could not tell them apart would offer a retry that
// cannot succeed, or fail to offer one that can.
type ErrSelfRemoval struct {
	// Detail is the whole user-facing sentence. Built where the alternatives are known rather than
	// assembled at the wire, so the two removal paths can each name their own remedy.
	Detail string
}

func (e ErrSelfRemoval) Error() string { return e.Detail }

// RemovePassword makes this install PASSWORDLESS — qn.6m D4, permitted by ruling B on quince#841,
// which superseded qn.6k's "a passkey is an addition, never a replacement".
//
// RULE 2 (qn.6n D1/D2): IT DEMANDS A PASSKEY ASSERTION, and the password cannot authorise its own
// removal. That one comparison replaces the row check this function used to make, and the
// replacement is strictly stronger for the reason D2 gives: **a dead row cannot produce an
// assertion**. The old check asked whether a row EXISTED for this rpId and its comment claimed the
// credential was usable — quince#888 made the comment honest and left the check, pending the ruling
// that arrived. A passkey deleted from the phone, or a wiped device, left a row behind that
// satisfied it, and console access was then the only way back.
//
// SO THE rpId FILTER IS GONE FROM THIS FUNCTION, and the asymmetry it used to state is not lost —
// it moved to where it can be enforced rather than inferred:
//
//	Configured()  has this install been CLAIMED?   → counts rows, does NOT filter. Guards first run.
//	rule 2        can the user still SIGN IN here? → counts nothing. An assertion IS the proof.
//
// A credential bound to another domain still cannot remove this password, because it cannot assert
// at this address — the browser will not offer it and FinishReauth would refuse it. That is the same
// guarantee the filter was reaching for, obtained from the authenticator instead of from a table.
//
// RATE-LIMITED THROUGH RequirePresent when a password is presented, which is new: this path verified
// no credential before, and now it may verify one on its way to refusing it.
func (s *Service) RemovePassword(proofs *Proofs, pres Presented, rpID, sessionID, clientIP string) error {
	subject, err := s.RequirePresent(proofs, pres, OpRemovePassword, "", sessionID, clientIP)
	if err != nil {
		return err
	}
	// RULE 2, IN ONE COMPARISON. A correct password reaches here and is refused, which is the point:
	// the thing being removed cannot vouch for the state it leaves behind.
	//
	// The zero subject — an install with NO credentials at all — is not `Password`, so it falls
	// through and removes nothing that exists. That is RequirePresent's documented first-run
	// exemption, and it must stay reachable: demanding an assertion on an unclaimed install would
	// make `quince auth reset` recoverable only by a credential the reset just destroyed.
	if subject.Password {
		return s.passwordRemovalRefusal(rpID)
	}
	if _, err := s.store.DeleteSetting(settingPasswordHash); err != nil {
		return err
	}
	s.audit("password_removed", clientIP)
	return nil
}

// passwordRemovalRefusal picks WHICH refusal a rule-2 violation on this path earns.
//
// IT READS ROWS, AND THAT IS NOT THE GUARD COMING BACK. The removal was already refused one line
// above, on the subject alone; this decides only what to SAY about it, and the two answers are
// different instructions rather than different verdicts. D2 keeps ErrLastCredential for exactly this
// — "a refusal that says only *present another credential* is correct and less useful".
func (s *Service) passwordRemovalRefusal(rpID string) error {
	rows, err := s.store.ListPasskeys()
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(rows))
	elsewhere := make([]string, 0, len(rows))
	for _, p := range rows {
		if p.RPID == rpID {
			return ErrSelfRemoval{Detail: "the password cannot authorise its own removal — " +
				"confirm with a passkey instead."}
		}
		if !seen[p.RPID] {
			seen[p.RPID] = true
			elsewhere = append(elsewhere, p.RPID)
		}
	}
	return ErrLastCredential{Presented: rpID, Elsewhere: elsewhere}
}

// ErrLastPasskey — no credential at this address can authorise removing THIS passkey.
//
// A separate type from ErrLastCredential, carrying the same wire code, because the two differ only
// in the remedy they can offer: there, add a passkey; here, set a password OR add another passkey.
// Both mean "this removal would leave you locked out", which is why `last_credential` is the honest
// code for both — quince#888 proposed a second one, and a client already knows which endpoint it
// called, so the code would distinguish nothing the caller does not have.
//
// SINCE qn.6n IT IS A MESSAGE RATHER THAN A GUARD (D2), and `HasPassword` is what the message turns
// on. The two states it distinguishes are not both lockouts:
//
//	no password  → nothing can remove this passkey. A genuine dead end: set a password or add one.
//	a password   → the PASSWORD can remove it. Not a lockout at all; the ceremony just cannot help.
//
// THE FIELD EXISTS BECAUSE THE CLIENT FALLS BACK ON IT. `Passkeys.tsx` tries the assertion first and
// drops to the password form on this refusal, so a message saying *"this quince has no password"* to
// somebody who has one would be both false and the opposite of the instruction they need.
type ErrLastPasskey struct {
	Presented string   // the rpId this request arrived on
	Elsewhere []string // rpIds of the passkeys that WOULD REMAIN, none of which works here
	// HasPassword is whether the admin password exists — the difference between a dead end and a
	// redirection to the other factor.
	HasPassword bool
}

func (e ErrLastPasskey) Error() string {
	if e.HasPassword {
		// NOT A LOCKOUT. The removal is possible; this ceremony is not the way to it.
		if len(e.Elsewhere) == 0 {
			return fmt.Sprintf("a passkey cannot authorise its own removal, and this quince holds no "+
				"other passkey for %q. Confirm with your password instead.", e.Presented)
		}
		return fmt.Sprintf("a passkey cannot authorise its own removal, and the other passkeys this "+
			"quince holds are registered for %s — a passkey only works at the address it was created "+
			"on. Confirm with your password instead.", strings.Join(e.Elsewhere, ", "))
	}
	if len(e.Elsewhere) == 0 {
		return fmt.Sprintf("removing this passkey would leave no way to sign in: this quince has no "+
			"password, and no other passkey for %q. Set a password first, or add another passkey.",
			e.Presented)
	}
	return fmt.Sprintf("removing this passkey would leave no way to sign in at %q: this quince has "+
		"no password, and the passkeys it would still hold are registered for %s — a passkey only "+
		"works at the address it was created on. Set a password first, or add a passkey for %q.",
		e.Presented, strings.Join(e.Elsewhere, ", "), e.Presented)
}

// RemovePasskey deletes one credential, DEMANDING A DIFFERENT ONE — rule 2, qn.6n slice 6b.
//
// THE COMPARISON IS THE WHOLE RULE, AND IT IS ONE LINE: refuse when the subject that proved this
// request IS the credential being removed. `ProofSubject.IsCredential` owns the comparison so there
// is exactly one spelling of it.
//
// IT REPLACES THE LOCKOUT COUNT quince#888 ADDED, and the replacement is strictly stronger for D2's
// reason — **a dead row cannot produce an assertion**. The old guard counted rows that would REMAIN
// usable at this rpId, which a wiped device or a deleted keychain entry satisfies while signing
// nobody in. Rule 2 asks the credential itself.
//
// SO THE quince#888 TAKEOVER STAYS CLOSED, BY CONSTRUCTION RATHER THAN BY A COUNT. Emptying the
// credential set needs a last removal, a last removal needs a different credential presented, and on
// a one-credential install there is none. The two-click password → passkey path cannot reach
// `Configured() == false`.
//
// THE 204 FOR "already gone" SURVIVES WITH NO SPECIAL CASE, as it did before: an id matching no row
// still has to be proven by something other than itself, and `IsCredential` is false for a subject
// that is not it — so the guard passes and `DeletePasskey` reports the row was already absent.
func (s *Service) RemovePasskey(proofs *Proofs, pres Presented, credentialID, rpID, sessionID,
	clientIP string) (bool, error) {
	// A DEAD END IS ITS OWN REFUSAL, AND IT IS DECIDED BEFORE A PROOF IS DEMANDED — D4, where the
	// ORDERING is the whole of it. Operator-reported 2026-08-14 from the running stand: one passkey,
	// no password, press Remove, and the answer was `reauth_required` — *"authenticate again"* — for
	// an operation nothing on the install could ever authorise.
	//
	// `RequirePresent` RAN FIRST AND WON. Nothing was presented, so it returned `ErrNoProof` before
	// anything asked whether a proof was POSSIBLE. `ErrLastPasskey` is produced by `provable`, which
	// only the CEREMONY path calls — so this path could never reach the refusal that explains itself,
	// and the client was handed a demand it could not render a challenge for: `accepts` was empty,
	// which D4 says must never reach the wire.
	//
	// IT ASKS `Accepts` RATHER THAN RE-DERIVING, for the reason slice 2 gave when it asked
	// `allowedForRemoval`: that predicate decides what the WIRE says would work, so a second spelling
	// here could disagree with the field the client reads — on exactly the case hardest to notice.
	//
	// ONLY ON A CONFIGURED INSTALL. Unclaimed is `RequirePresent`'s documented exemption and holds no
	// credentials to compare; refusing there would break first run to fix a message.
	configured, err := s.Configured()
	if err != nil {
		return false, err
	}
	if configured {
		accepts, err := s.Accepts(OpRemovePasskey, rpID, credentialID)
		if err != nil {
			return false, err
		}
		// `passkeyRemovalRefusal` answers nil when something CAN prove it, which cannot happen under
		// an empty `accepts`. Guarded anyway: a nil arriving here falls through to the proof demand
		// rather than becoming a silent success.
		if len(accepts) == 0 {
			if refusal := s.passkeyRemovalRefusal(credentialID, rpID); refusal != nil {
				return false, refusal
			}
		}
	}

	subject, err := s.RequirePresent(proofs, pres, OpRemovePasskey, credentialID, sessionID, clientIP)
	if err != nil {
		return false, err
	}
	// RULE 2. The zero subject — an unclaimed install — is not this credential, so first run falls
	// through exactly as it does on the password path, and removes nothing that exists.
	if subject.IsCredential(credentialID) {
		return false, ErrSelfRemoval{Detail: "a passkey cannot authorise its own removal — " +
			"use your password, or a different passkey."}
	}
	return s.store.DeletePasskey(credentialID)
}

// passkeyRemovalRefusal builds the message for a `remove_passkey` ceremony that cannot produce a
// usable proof — no credential at this address other than the target.
//
// IT READS ROWS AND IT IS NOT THE GUARD. The guard is `IsCredential` above, on the subject. This
// decides only what to SAY when the ceremony is refused before it starts, which is the message-carrier
// role D2 keeps `ErrLastPasskey` for.
func (s *Service) passkeyRemovalRefusal(credentialID, rpID string) error {
	hasPassword, err := s.HasPassword()
	if err != nil {
		return err
	}
	rows, err := s.store.ListPasskeys()
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(rows))
	elsewhere := make([]string, 0, len(rows))
	for _, p := range rows {
		if p.CredentialID == credentialID {
			continue // the one on its way out — it cannot prove its own removal
		}
		if p.RPID == rpID {
			// SOMETHING HERE CAN PROVE IT, so there is nothing to refuse.
			return nil
		}
		if !seen[p.RPID] {
			seen[p.RPID] = true
			elsewhere = append(elsewhere, p.RPID)
		}
	}
	return ErrLastPasskey{Presented: rpID, Elsewhere: elsewhere, HasPassword: hasPassword}
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

// SetTrustedProxies supplies the proxy trust list (quince#555). Applied at process start from
// QUINCE_TRUSTED_PROXIES; nil or unset means believe X-Forwarded-Proto from anyone, which is the
// pre-quince#555 behaviour and what every direct deployment does.
func (s *Service) SetTrustedProxies(t *TrustedProxies) { s.proxies = t }
