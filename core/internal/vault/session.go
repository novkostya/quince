package vault

import (
	"context"
	"errors"
	"fmt"
	"io"
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

	// incomplete remembers files this session read short — D8.1a. Guarded by the REGISTRY
	// mutex, not by busy: a browse may read it while a stream is still running, which is the
	// whole point of remembering.
	incomplete map[string]bool
	// overlong is its mirror — files whose blob held MORE bytes than the index records
	// (quince#1379). Same lifetime, same guard, opposite sign.
	overlong map[string]bool
	scratch  string

	// derived holds anything BUILT FROM this session's decrypted content that must not
	// outlive it — today the qn.9 capability report.
	//
	// IT IS AN io.Closer RATHER THAN A CONCRETE TYPE BECAUSE OF AN IMPORT CYCLE, and the
	// cycle is a fact about the design rather than an accident: the capability report is
	// built over parserfs, parserfs is built over this package, so this package cannot
	// name either. A closer is the narrowest seam that lets the SINGLE teardown below
	// stay single — which is the property that matters, because three exits with three
	// teardowns is how one of them ends up not wiping.
	derived []io.Closer

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
// FOR BOUNDED OPERATIONS ONLY — ONE THAT COMPLETES BEFORE fn RETURNS. The busy lock is
// released when fn returns, so anything still running after that is running unprotected:
// teardown may then Close the Vault and wipe the scratch tree underneath it. A STREAM IS
// NOT BOUNDED, which is what OpenStream is for. Do not reach for With and hand the reader
// out of fn; that shape compiles and tears (quince#1365 review).
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

	// DERIVED STATE FIRST, because it may hold files materialized out of the vault and
	// closing the vault under it would be the torn read this function exists to avoid.
	for _, d := range e.derived {
		_ = d.Close()
	}
	e.derived = nil

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

// OpenStream opens one file for reading and holds the session BUSY until the returned
// reader is Closed.
//
// THIS EXISTS BECAUSE `With` CANNOT EXPRESS A STREAM. `Open` returns immediately and the
// decrypt continues afterwards, so a caller that takes the reader out of a `With` block
// leaves it running with the busy lock already released — and teardown then Closes the
// Vault and wipes the scratch tree while bytes are still moving. That was a REAL hazard in
// the natural HTTP shape (hand the reader to the response and return), and it was
// conditional on an obligation stated nowhere (quince#1365 review).
//
// SO THE GUARANTEE IS MADE UNCONDITIONAL RATHER THAN DOCUMENTED. The lock's lifetime is the
// STREAM's lifetime, not the call's, because that is what the stream actually needs. A
// caller cannot get it wrong by writing the obvious thing.
//
// THE COST, NAMED: a reader that is never Closed holds the session busy forever, and
// teardown blocks on exactly that lock — so a leaked reader is a session that cannot be
// locked. That is deliberate and it is the better failure: it is visible (the session stays
// listed, `lock` does not return), where the alternative is a silent torn read of somebody's
// backup. Every caller in this repository closes with `defer`, and the HTTP handler does it
// before it writes a byte.
//
// CANCELLATION IS Close's JOB, NOT ctx's. The context bounds the OPENING of the file; the
// decrypt goroutine behind the reader ends when the file ends or the pipe is closed.
// Cancelling ctx after this returns does not stop it — close the reader.
func (r *Registry) OpenStream(ctx context.Context, sessionID, fileID string) (io.ReadCloser, FileEntry, error) {
	r.mu.Lock()
	e, ok := r.sessions[sessionID]
	if !ok {
		r.mu.Unlock()
		return nil, FileEntry{}, ErrNoSession
	}
	if !e.session.ExpiresAt.After(r.now()) {
		r.mu.Unlock()
		_ = r.Lock(sessionID)
		return nil, FileEntry{}, ErrNoSession
	}
	if !e.busy.TryLock() {
		r.mu.Unlock()
		return nil, FileEntry{}, ErrSessionBusy
	}
	r.mu.Unlock()

	// From here every path must either hand the lock to the reader or release it, or the
	// session is wedged. Hence the explicit unlocks rather than a defer.
	entry, err := e.vault.Stat(ctx, fileID)
	if err != nil {
		e.busy.Unlock()
		return nil, FileEntry{}, err
	}
	rc, err := e.vault.Open(ctx, fileID)
	if err != nil {
		e.busy.Unlock()
		return nil, FileEntry{}, err
	}
	return &streamHold{ReadCloser: rc, release: e.busy.Unlock}, entry, nil
}

// streamHold releases the session's busy lock when the stream is Closed, exactly once.
type streamHold struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (s *streamHold) Close() error {
	err := s.ReadCloser.Close()
	// ONCE, because a double Close is ordinary in HTTP handling — a `defer rc.Close()` plus
	// an explicit one on an error path — and releasing a mutex twice panics. The reader's
	// own Close is idempotent by the io.Closer convention; the release must be made so.
	s.once.Do(s.release)
	return err
}

// WatchIncomplete wraps a stream so that a read ending in ErrIncompleteFile is REMEMBERED on
// the session, and returns the wrapped reader.
//
// THIS IS WHERE D8.1a's PROMISE IS KEPT. An incomplete file is a successful read of an
// incomplete artifact: every recovered byte is delivered, and a retry cannot change it. It
// cannot be known before the file is read — the index records a size and only decrypting the
// blob shows fewer bytes are there — so the browse listing can only mark it AFTERWARDS, and
// something has to remember. The session does, for its own lifetime and no longer: nothing
// derived from backup content is persisted (design §7).
//
// AN UNKNOWN SESSION IS NOT AN ERROR HERE. The stream is already open and the caller is about
// to read it; refusing would break a live download to record a note. The note is simply lost,
// which is the right trade for a session that is ending anyway.
func (r *Registry) WatchIncomplete(sessionID, fileID string, rc io.ReadCloser) io.ReadCloser {
	return &incompleteWatcher{
		ReadCloser: rc,
		mark:       func() { r.markIncomplete(sessionID, fileID) },
		markLong:   func() { r.markOverlong(sessionID, fileID) },
	}
}

// IncompleteIn reports which files this session has read short. The map is a COPY: a caller
// rendering a page must not hold a reference into the session's own state.
func (r *Registry) IncompleteIn(sessionID string) map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[sessionID]
	if !ok || len(e.incomplete) == 0 {
		return nil
	}
	out := make(map[string]bool, len(e.incomplete))
	for k, v := range e.incomplete {
		out[k] = v
	}
	return out
}

func (r *Registry) markIncomplete(sessionID, fileID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[sessionID]
	if !ok {
		return
	}
	if e.incomplete == nil {
		e.incomplete = make(map[string]bool)
	}
	e.incomplete[fileID] = true
}

// incompleteWatcher records the one error that is not a failure.
type incompleteWatcher struct {
	io.ReadCloser
	mark     func()
	markLong func()
	once     sync.Once
	onceLong sync.Once
}

func (w *incompleteWatcher) Read(p []byte) (int, error) {
	n, err := w.ReadCloser.Read(p)
	// Both arrive as the TERMINAL error of the stream, AFTER the bytes, and both are checked
	// before io.EOF rather than after — such a read ends in one of these rather than in EOF,
	// so testing for EOF first would skip them.
	switch {
	case err == nil:
	case errors.Is(err, ErrIncompleteFile):
		w.once.Do(w.mark)
	case errors.Is(err, ErrOverlongFile):
		// WITHOUT THIS THE CASE IS SILENT. The reader stops at the recorded size, so nothing
		// overruns Content-Length and the HTTP layer sees an ordinary success — the user gets
		// a truncated file and every signal agrees (quince#1379).
		w.onceLong.Do(w.markLong)
		// AND THEN IT IS SWALLOWED, because at the HTTP layer nothing DID fail: the body is
		// exactly the declared Content-Length, so the transfer is a complete success. Passing
		// the error on makes the handler log a failure that did not happen — measured on the
		// stand, it logged "file stream ended early … holds FEWER bytes" about a file with too
		// MANY, which is quince#1381's own defect arriving from the other side.
		//
		// Nothing is lost by swallowing it: the condition is recorded on the session one line
		// above and travels as the `overlong` FIELD, which is the report (contracts §2). The
		// SEAM still returns ErrOverlongFile — this is the registry wrapper, whose job is
		// exactly to convert that error into the session note, and having done so it is done.
		return n, io.EOF
	}
	return n, err
}

// OverlongIn reports which files this session read that hold MORE bytes than their record.
// A COPY, for the same reason IncompleteIn returns one.
func (r *Registry) OverlongIn(sessionID string) map[string]bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[sessionID]
	if !ok || len(e.overlong) == 0 {
		return nil
	}
	out := make(map[string]bool, len(e.overlong))
	for k, v := range e.overlong {
		out[k] = v
	}
	return out
}

func (r *Registry) markOverlong(sessionID, fileID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[sessionID]
	if !ok {
		return
	}
	if e.overlong == nil {
		e.overlong = make(map[string]bool)
	}
	e.overlong[fileID] = true
}

// AttachDerived registers something built from this session's decrypted content, to be
// closed when the session ends by ANY route — explicit lock, TTL sweep, or shutdown.
//
// It returns false when the session does not exist, so a caller cannot silently attach to
// nothing and believe its cleanup is wired.
func (r *Registry) AttachDerived(sessionID string, c io.Closer) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[sessionID]
	if !ok {
		return false
	}
	e.derived = append(e.derived, c)
	return true
}
