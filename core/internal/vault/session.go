package vault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

// Session-level errors, distinct from the seam's because they are about the SESSION
// rather than about the version it reads.
var (
	// ErrNoSession is an unknown or already-ended session id.
	ErrNoSession = errors.New("vault: no such session")

	// ErrSessionBusy is a second call arriving while one is in flight on the same session.
	// A Vault is not required to be safe for concurrent use, so the registry refuses rather
	// than serializing behind a lock a caller cannot see: a browse that blocks for the
	// length of a file download looks like a hang, and saying so is the actionable answer.
	ErrSessionBusy = errors.New("vault: this session is busy with another request")
)

// DefaultSessionTTL is the unlock lifetime when nothing configures one.
//
// FIFTEEN MINUTES because it is the shortest span that survives ordinary browsing — open a
// version, look through a few domains, download something — without leaving a decrypted
// index and a live key sitting in memory because somebody closed a laptop lid. The
// password is never persisted, so the cost of expiry is retyping it, not losing anything.
const DefaultSessionTTL = 15 * time.Minute

// Session is one unlocked version, as contracts.md §2 puts it on the wire.
type Session struct {
	ID        string
	VersionID string
	ExpiresAt time.Time
}

// registryEntry is a Session plus everything that must be torn down with it.
type registryEntry struct {
	session Session
	vault   Vault
	scratch string

	// busy guards the Vault, which is not required to be concurrency-safe. TryLock rather
	// than Lock — see ErrSessionBusy.
	busy sync.Mutex
}

// Registry owns every unlocked session and is the only thing that ends one.
//
// EVERY WAY A SESSION ENDS RUNS THE SAME TEARDOWN — an explicit lock, the TTL, and process
// shutdown. That is not tidiness: three exits with three teardowns is how one of them ends
// up not wiping scratch, and the one that gets forgotten is always the one nobody clicks.
type Registry struct {
	mu       sync.Mutex
	sessions map[string]*registryEntry
	ttl      time.Duration

	// scratchRoot is the parent of every session's scratch dir — contracts.md §5's
	// /cache/scratch. Sessions get <scratchRoot>/<id>/ and NOTHING is written outside it.
	scratchRoot string

	// now and freeOSMemory are injected so the TTL and the teardown can be tested without
	// sleeping and without measuring a process. freeOSMemory is nil-checked rather than
	// assumed: a zero Registry is not usable, but a partially built one in a test should
	// fail on the thing under test, not on a nil call.
	now          func() time.Time
	freeOSMemory func()
}

// NewRegistry builds a Registry over a scratch root.
//
// IT WIPES THE ROOT. Anything under it is the residue of sessions that did not end
// cleanly — a killed daemon, an OOM — and that residue is decrypted user content. Wiping
// at start is what makes the "nothing survives a lock" promise true across a CRASH and not
// only across a graceful exit, which a deferred cleanup cannot do by construction.
func NewRegistry(scratchRoot string, ttl time.Duration) (*Registry, error) {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	if err := os.RemoveAll(scratchRoot); err != nil {
		return nil, fmt.Errorf("vault: wiping the scratch root: %w", err)
	}
	if err := os.MkdirAll(scratchRoot, 0o700); err != nil {
		return nil, fmt.Errorf("vault: creating the scratch root: %w", err)
	}
	return &Registry{
		sessions:     make(map[string]*registryEntry),
		ttl:          ttl,
		scratchRoot:  scratchRoot,
		now:          time.Now,
		freeOSMemory: debug.FreeOSMemory,
	}, nil
}

// SetTTL applies a new session lifetime.
//
// IT DOES NOT MOVE A SESSION ALREADY RUNNING. ExpiresAt is stamped at unlock, which is
// what makes it a fact the UI can count down against rather than a value that could jump
// under a user because somebody saved config.yml in another window.
func (r *Registry) SetTTL(ttl time.Duration) {
	if ttl <= 0 {
		ttl = DefaultSessionTTL
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ttl = ttl
}

// ScratchFor is where a session's implementation may write, and the only place it may.
func (r *Registry) ScratchFor(sessionID string) string {
	return filepath.Join(r.scratchRoot, sessionID)
}

// Unlock opens a version and registers the session. The caller supplies the id and an
// unopened Vault; open() is given the scratch dir so an implementation that needs one
// never has to invent a path.
//
// ON ANY FAILURE NOTHING IS REGISTERED AND THE SCRATCH DIR IS GONE. A half-registered
// session is a session nothing will ever tear down.
func (r *Registry) Unlock(
	ctx context.Context,
	sessionID, versionID, password string,
	open func(scratchDir string) (Vault, error),
) (Session, Info, error) {
	scratch := r.ScratchFor(sessionID)
	if err := os.MkdirAll(scratch, 0o700); err != nil {
		return Session{}, Info{}, fmt.Errorf("vault: creating session scratch: %w", err)
	}

	v, err := open(scratch)
	if err != nil {
		r.wipe(scratch)
		return Session{}, Info{}, err
	}

	info, err := v.Unlock(ctx, password)
	if err != nil {
		_ = v.Close()
		r.wipe(scratch)
		return Session{}, Info{}, err
	}

	r.mu.Lock()
	ttl := r.ttl
	s := Session{ID: sessionID, VersionID: versionID, ExpiresAt: r.now().Add(ttl)}
	r.sessions[sessionID] = &registryEntry{session: s, vault: v, scratch: scratch}
	r.mu.Unlock()

	return s, info, nil
}

// With runs fn against a session's Vault, refusing rather than queueing if one is already
// in flight, and treating an expired session exactly as a locked one.
//
// THE BUSY LOCK IS TAKEN UNDER THE REGISTRY LOCK, and that is a correctness requirement
// rather than convenience: releasing the registry lock first would leave a window in which
// Lock or Sweep tears the session down, and fn would then run against a Vault that has been
// Closed and a scratch directory that has been removed. TryLock never blocks, so holding
// the registry lock across it costs nothing.
func (r *Registry) With(sessionID string, fn func(Vault) error) error {
	r.mu.Lock()
	e, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return ErrNoSession
	}
	if !e.session.ExpiresAt.After(r.now()) {
		r.mu.Unlock()
		// Reap it here rather than only on the sweep: a request arriving after expiry must
		// not be served, and the session must not linger until the next tick either.
		_ = r.Lock(sessionID)
		return ErrNoSession
	}
	if !e.busy.TryLock() {
		r.mu.Unlock()
		return ErrSessionBusy
	}
	r.mu.Unlock()
	defer e.busy.Unlock()

	return fn(e.vault)
}

// Get reports a session without touching its Vault. An expired session is reported as
// absent, so nothing reads a session the sweep has not reached yet.
func (r *Registry) Get(sessionID string) (Session, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[sessionID]
	if !ok || !e.session.ExpiresAt.After(r.now()) {
		return Session{}, false
	}
	return e.session, true
}

// Lock ends a session and runs the teardown.
//
// IDEMPOTENT, AND AN UNKNOWN ID IS NOT AN ERROR: the state the caller wanted is the state
// that exists, and contracts.md §1 answers 204 for it. A 404 here would make a
// double-click look like a fault.
func (r *Registry) Lock(sessionID string) error {
	r.mu.Lock()
	e, ok := r.sessions[sessionID]
	delete(r.sessions, sessionID)
	r.mu.Unlock()
	if !ok {
		return nil
	}
	return r.teardown(e)
}

// Sweep ends every session past its expiry and reports how many it ended. The daemon calls
// it on a ticker; it is safe to call at any time.
func (r *Registry) Sweep() int {
	r.mu.Lock()
	now := r.now()
	var due []*registryEntry
	for id, e := range r.sessions {
		if !e.session.ExpiresAt.After(now) {
			due = append(due, e)
			delete(r.sessions, id)
		}
	}
	r.mu.Unlock()

	for _, e := range due {
		// A teardown failure is not a reason to abandon the rest: each session's scratch is
		// its own, and stopping here would leave decrypted content behind for every session
		// after the first failure.
		_ = r.teardown(e)
	}
	return len(due)
}

// CloseAll ends every session — process shutdown, through the same teardown as everything
// else.
func (r *Registry) CloseAll() error {
	r.mu.Lock()
	all := make([]*registryEntry, 0, len(r.sessions))
	for id, e := range r.sessions {
		all = append(all, e)
		delete(r.sessions, id)
	}
	r.mu.Unlock()

	var firstErr error
	for _, e := range all {
		if err := r.teardown(e); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// teardown is the ONE way a session ends: close the vault, wipe its scratch, then hand the
// memory back.
//
// THE ORDER MATTERS in one place — Close before the wipe, because an implementation's
// Close is what releases a decrypted index that lives in that directory, and removing the
// tree underneath an open file is how you get a file that is unlinked but still allocated.
func (r *Registry) teardown(e *registryEntry) error {
	// WAIT FOR ANY IN-FLIGHT OPERATION. By the time teardown runs the entry is already out
	// of the map, so nothing new can reach it — but a download started a moment ago may
	// still be streaming, and closing its Vault or removing its scratch tree underneath it
	// would turn an orderly lock into a torn read. Blocking here is correct where TryLock
	// is correct in With: a caller asking for a second operation should be refused, and a
	// caller ending the session should wait.
	e.busy.Lock()
	defer e.busy.Unlock()

	err := e.vault.Close()
	r.wipe(e.scratch)

	// qn.8 spec D10.3 clause (c). The vault's allocations are in THIS daemon's heap, and Go
	// returns freed pages to the OS on the scavenger's schedule rather than at lock — so
	// without this, a browse session sets a high-water mark that the multi-hour backup
	// running afterwards pays for. A stop-the-world cost at a rare event, never in a hot
	// path and never during a backup.
	//
	// WHETHER IT REACHES THE BAR IS UNMEASURED and is owed to G7: returned pages are not
	// the whole of RSS, and fragmentation can hold an address space open regardless, which
	// is why the clause is written as "within 32 MB of baseline" rather than "back to it".
	// The clause itself is RULED — Operator, 2026-08-20, quince#1344, all three of D10.3's
	// clauses confirmed as written — and the alternative, accepting retention with a stated
	// reason, was weighed and REFUSED rather than never raised. It will be re-proposed the
	// first time this is expensive to meet; D10.3c is where to read why it lost.
	if r.freeOSMemory != nil {
		r.freeOSMemory()
	}
	return err
}

// wipe removes a session's scratch tree. Failure is deliberately not returned: a caller
// cannot act on it, the session is over either way, and the next daemon start clears the
// root wholesale (see NewRegistry).
func (r *Registry) wipe(dir string) {
	if dir == "" || dir == string(filepath.Separator) {
		return // never a plausible scratch dir; refuse rather than recurse from the root
	}
	_ = os.RemoveAll(dir)
}

// TTLFromMinutes resolves the `vault.session_ttl_minutes` key to a duration.
//
// IT LIVES HERE rather than in the config package because the meaning of a non-positive
// value is the Registry's rule, not the file's: zero means the default and never "no
// expiry", and putting that decision beside the thing it protects is what stops a second
// caller inventing a different reading of the same number.
func TTLFromMinutes(minutes int) time.Duration {
	if minutes <= 0 {
		return DefaultSessionTTL
	}
	return time.Duration(minutes) * time.Minute
}
