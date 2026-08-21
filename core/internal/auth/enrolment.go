package auth

import (
	"crypto/subtle"
	"errors"
	"sync"
	"time"

	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/store"
)

// Enrolment secrets — qn.13 slice 9a, spec D4 and gate G4.
//
// WHAT THIS IS. The admin generates a QR on a device page; a household member scans it and registers
// a passkey confined to that one device. The QR carries a URL, and the secret in that URL is a
// ONE-SHOT AUTHORIZATION FOR ONE REGISTRATION — not a durable token. The durable credential is the
// passkey that lands in the phone's secure enclave; this exists only to authorize its creation.
//
// IN MEMORY, FOLLOWING Proofs AND PasskeyCeremonies, and D4's own heading says why — *not a token
// quince stores*. Three consequences, all of them the safe direction:
//
//   - A restart voids every outstanding QR. That is authority DISAPPEARING, which is the direction a
//     bearer token should fail in. The remedy is one tap on the device page.
//   - There is no table whose rows are credential-equivalents, which is the cost Proofs weighed and
//     declined to pay for a shorter-lived token than this one.
//   - The listing D4's review ruled must exist (*"an issued-and-unscanned QR is live authority, and
//     authority nobody can see is authority nobody revokes"*) is a listing of what is live NOW,
//     which is the honest scope of a store that does not outlive the process.
//
// KEYED BY ID, NOT BY TOKEN, and the token is compared in constant time. Proofs keys its map by the
// token, which is fine for a value the map alone can produce; here the id is the handle the admin
// LISTS and REVOKES by, so it has to be a first-class key anyway — and once it is, keying by id
// costs a scan over a set bounded by how many QRs one household has outstanding, and buys a lookup
// with no map-timing behaviour over a secret. `subtle.ConstantTimeCompare` is the same primitive
// ProofSubject.IsCredential already uses for the same reason.
const (
	// enrolmentTTL bounds how long an unscanned QR stays live.
	//
	// MINUTES, NOT HOURS (D4). The clock here is a human walking across a room with a phone, which
	// is the same kind of interval challengeTTL measures and a longer one — a QR is generated, then
	// carried, then scanned, and the scan may need a second attempt. Ten minutes is the smallest
	// number that does not make the ordinary case fail.
	enrolmentTTL = 10 * time.Minute

	// enrolmentGrace is how long a SPENT, REVOKED or EXPIRED record is kept after it stops being
	// usable, and it exists so the refusal can name its cause.
	//
	// WITHOUT IT, EVERY DEAD SECRET IS "UNKNOWN", which is *troubleshooting is actionable* failing
	// at the one moment it matters. The four causes have four different remedies, and one of them
	// is not a remedy at all: *already used* tells the person holding the QR that somebody else
	// enrolled with it, which is an incident rather than a retry. Collapsing that into "no such
	// link" would be true and useless — the defect that rule names explicitly.
	//
	// A KEPT RECORD GRANTS NOTHING. It is refused by state before its token is ever compared, so
	// the retention window widens no authority; it widens only what quince can say.
	enrolmentGrace = 30 * time.Minute

	// enrolmentTokenBytes is the secret's size — a full 256 bits, as `id.Token`'s own comment
	// requires for anything unguessable. This one travels in a URL that D4 assumes will leak.
	enrolmentTokenBytes = 32
)

// The four refusals, deliberately NOT collapsed into one.
//
// Proofs collapses its three on the stated grounds that *"the UI remedy is identical in all three
// cases"*. That reasoning does not carry here, and the difference is the point: these reach an
// UNAUTHENTICATED scanner who is holding a link somebody handed them, and what they should do next
// differs in every case. See enrolmentGrace.
var (
	// ErrEnrolmentUnknown — no such secret, or one whose grace window has passed. The link is for
	// another install, was mistyped, or is old enough that quince no longer remembers it.
	ErrEnrolmentUnknown = errors.New("auth: no such enrolment link — ask for a new one")

	// ErrEnrolmentExpired — the secret was real and its window closed. Ordinary; the remedy is a
	// new QR, and nothing is wrong.
	ErrEnrolmentExpired = errors.New("auth: this enrolment link has expired — ask for a new one")

	// ErrEnrolmentSpent — a passkey was ALREADY registered with this secret. The one refusal that
	// is not a retry: whoever holds this link is not whoever used it.
	ErrEnrolmentSpent = errors.New("auth: this enrolment link has already been used to add a passkey")

	// ErrEnrolmentRevoked — the admin cancelled it before anyone used it.
	ErrEnrolmentRevoked = errors.New("auth: this enrolment link was cancelled")

	// ErrEnrolmentAdminScope — refused at MINT. Nothing in this rung issues an admin credential by
	// QR, and D4 requires that a scoped enrolment can never mint an admin one. Making the store
	// unable to hold an admin-scoped secret is that requirement as a structural fact rather than a
	// check somewhere downstream: the escalation has no representation to travel in.
	//
	// If a later rung wants admin enrolment by QR, this is the line to revisit, and revisiting it
	// is a decision rather than a default.
	ErrEnrolmentAdminScope = errors.New("auth: an enrolment link cannot carry admin scope")

	// ErrEnrolmentNotFound — Revoke was given an id that names nothing live.
	ErrEnrolmentNotFound = errors.New("auth: no live enrolment link with that id")
)

// Enrolment is what the ADMIN sees: an outstanding QR, with no secret in it.
//
// THE TOKEN IS NOT A FIELD. It is returned exactly once, by Mint, and never again — so a listing
// cannot re-display a QR and a log line cannot carry one. The admin can see that authority exists
// and cancel it, which is what the ruling asked for; being able to re-show it is not.
type Enrolment struct {
	// ID names this secret for listing and revocation. Not a secret: it is safe in a response
	// body, a URL path and a log line, which is exactly what the token is not.
	ID string
	// ScopeUDID is the device the credential this mints will be confined to. Never empty — an
	// admin-scoped enrolment is refused at Mint.
	ScopeUDID string
	CreatedAt time.Time
	ExpiresAt time.Time
}

type enrolmentState int

const (
	enrolmentLive enrolmentState = iota
	enrolmentSpent
	enrolmentRevoked
)

type enrolmentRecord struct {
	token   string
	scope   string
	created time.Time
	expires time.Time
	state   enrolmentState
	// deadAt is when a spent or revoked record stops being remembered at all. Zero while live;
	// an expired-but-live record is swept from `expires` instead.
	deadAt time.Time
}

// Enrolments holds outstanding enrolment secrets.
type Enrolments struct {
	mu  sync.Mutex
	now func() time.Time
	in  map[string]*enrolmentRecord // by Enrolment.ID
}

// NewEnrolments builds the store.
func NewEnrolments() *Enrolments {
	return &Enrolments{now: time.Now, in: map[string]*enrolmentRecord{}}
}

// Mint issues a secret for one registration, with its scope fixed here and nowhere else.
//
// THE SCOPE IS BAKED IN AT GENERATION (D4): *a token whose scope is chosen by the scanner is not a
// scoped token*. The scanner sends no scope and cannot; the ceremony reads it off the record.
func (e *Enrolments) Mint(scope store.Scope) (string, Enrolment, error) {
	if scope.IsAdmin() || scope.UDID() == "" {
		return "", Enrolment{}, ErrEnrolmentAdminScope
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	e.sweepLocked(now)

	rec := &enrolmentRecord{
		token:   id.Token(enrolmentTokenBytes),
		scope:   scope.UDID(),
		created: now,
		expires: now.Add(enrolmentTTL),
		state:   enrolmentLive,
	}
	key := id.New()
	e.in[key] = rec
	return rec.token, rec.public(key), nil
}

// Check validates a secret WITHOUT consuming it, and is what the ceremony's BEGIN calls.
//
// THE SPLIT IS THE REQUIREMENT, NOT A CONVENIENCE (D4): *single-use, consumed at registration, not
// at scan — a scan that fails must not burn it.* A phone that opens the page and then fails Face ID,
// or loses the network mid-ceremony, must be able to try again with the same QR. Only Spend burns.
func (e *Enrolments) Check(token string) (Enrolment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lookupLocked(token)
}

// Spend validates a secret and consumes it, and is what the ceremony's FINISH calls once a
// credential has actually been registered.
func (e *Enrolments) Spend(token string) (Enrolment, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	key, rec, err := e.findLocked(token)
	if err != nil {
		return Enrolment{}, err
	}
	rec.state = enrolmentSpent
	rec.deadAt = e.now().Add(enrolmentGrace)
	return rec.public(key), nil
}

// Revoke cancels an unused secret, by the id the admin sees.
//
// BEFORE USE ONLY, and a spent one reports ErrEnrolmentSpent rather than pretending to cancel
// something that has already happened. Revoking a spent secret would be a no-op wearing the shape of
// a remedy — the credential it minted is what needs removing, and that is the passkey list's job.
func (e *Enrolments) Revoke(enrolmentID string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	e.sweepLocked(now)

	rec, ok := e.in[enrolmentID]
	if !ok {
		return ErrEnrolmentNotFound
	}
	if err := rec.usable(now); err != nil {
		return err
	}
	rec.state = enrolmentRevoked
	rec.deadAt = now.Add(enrolmentGrace)
	return nil
}

// List returns the LIVE secrets for one device — issued, unscanned, unexpired, not cancelled.
//
// RULED AS A REQUIREMENT at spec review, and named there as the part not to trade away.
func (e *Enrolments) List(udid string) []Enrolment {
	e.mu.Lock()
	defer e.mu.Unlock()
	now := e.now()
	e.sweepLocked(now)

	out := []Enrolment{}
	for key, rec := range e.in {
		if rec.scope != udid {
			continue
		}
		if rec.usable(now) != nil {
			continue
		}
		out = append(out, rec.public(key))
	}
	return out
}

// lookupLocked resolves a token to a live record, or names why it is not one.
func (e *Enrolments) lookupLocked(token string) (Enrolment, error) {
	key, rec, err := e.findLocked(token)
	if err != nil {
		return Enrolment{}, err
	}
	return rec.public(key), nil
}

// findLocked is the one place a token is compared, so Check and Spend cannot disagree about what a
// valid secret is.
func (e *Enrolments) findLocked(token string) (string, *enrolmentRecord, error) {
	now := e.now()
	e.sweepLocked(now)

	// AN EMPTY TOKEN IS REFUSED BEFORE THE SCAN. ConstantTimeCompare of two empty strings returns
	// 1, so a request with no secret at all would match a record whose token was somehow empty.
	// `id.Token` cannot produce one, which is exactly why this belongs here rather than resting on
	// that: the guard costs nothing and does not depend on a fact from another file.
	if token == "" {
		return "", nil, ErrEnrolmentUnknown
	}
	for key, rec := range e.in {
		if subtle.ConstantTimeCompare([]byte(rec.token), []byte(token)) != 1 {
			continue
		}
		if err := rec.usable(now); err != nil {
			return "", nil, err
		}
		return key, rec, nil
	}
	return "", nil, ErrEnrolmentUnknown
}

// usable is THE predicate, named here and called from every site that asks the question.
//
// NAMED RATHER THAN RESTATED, because a test that spells out the same condition passes when the
// condition is wrong — it mirrors the code instead of checking it. The tests call this through the
// exported surface and assert the ERROR, which is the thing a caller acts on.
func (r *enrolmentRecord) usable(now time.Time) error {
	switch r.state {
	case enrolmentSpent:
		return ErrEnrolmentSpent
	case enrolmentRevoked:
		return ErrEnrolmentRevoked
	}
	if !now.Before(r.expires) {
		return ErrEnrolmentExpired
	}
	return nil
}

func (r *enrolmentRecord) public(key string) Enrolment {
	return Enrolment{ID: key, ScopeUDID: r.scope, CreatedAt: r.created, ExpiresAt: r.expires}
}

// sweepLocked forgets records that are past saying anything useful.
//
// ON ACCESS RATHER THAN ON A TIMER, for PasskeyCeremonies' reason: this map is bounded by how often
// a human generates a QR. A live record is dropped once its expiry plus the grace window has passed
// — the grace is what lets an expired secret still be reported AS expired rather than as unknown.
func (e *Enrolments) sweepLocked(now time.Time) {
	for key, rec := range e.in {
		dead := rec.deadAt
		if dead.IsZero() {
			dead = rec.expires.Add(enrolmentGrace)
		}
		if !now.Before(dead) {
			delete(e.in, key)
		}
	}
}
