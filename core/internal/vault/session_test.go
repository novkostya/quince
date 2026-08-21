package vault

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeVault is a Vault that records what the registry did to it. The point of this PR is
// the LIFECYCLE, not any decryption, so the seam is exercised against something that
// cannot fail for reasons of its own.
type fakeVault struct {
	scratch string

	mu        sync.Mutex
	closed    int
	unlockErr error
	statErr   error

	// blockOpen, when non-nil, holds Open until it is closed — so a test can have an
	// operation genuinely in flight while it tries to tear the session down.
	blockOpen chan struct{}
}

func (f *fakeVault) Unlock(context.Context, string) (Info, error) {
	if f.unlockErr != nil {
		return Info{}, f.unlockErr
	}
	return Info{DeviceName: "test-device", IOSVersion: "26.0", FileCount: 3, Encrypted: true}, nil
}
func (f *fakeVault) List(context.Context, Query) (Page, error) { return Page{}, nil }
func (f *fakeVault) Stat(context.Context, string) (FileEntry, error) {
	if f.statErr != nil {
		return FileEntry{}, f.statErr
	}
	return FileEntry{FileID: "f1", Kind: KindFile, Size: 7}, nil
}
func (f *fakeVault) VerifyCanary(context.Context) error { return nil }

func (f *fakeVault) Open(context.Context, string) (io.ReadCloser, error) {
	if f.blockOpen != nil {
		<-f.blockOpen
	}
	return io.NopCloser(strings.NewReader("content")), nil
}

func (f *fakeVault) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func (f *fakeVault) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// newTestRegistry builds a Registry over a temp dir with a clock the test drives and a
// counter in place of debug.FreeOSMemory.
func newTestRegistry(t *testing.T, ttl time.Duration) (*Registry, *time.Time, *int) {
	t.Helper()
	r, err := NewRegistry(filepath.Join(t.TempDir(), "scratch"), ttl)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	freed := 0
	r.now = func() time.Time { return now }
	r.freeOSMemory = func() { freed++ }
	return r, &now, &freed
}

func unlockOne(t *testing.T, r *Registry, id string) (*fakeVault, Session) {
	t.Helper()
	fv := &fakeVault{}
	s, info, err := r.Unlock(context.Background(), id, "ver-"+id, "pw", func(scratch string) (Vault, error) {
		fv.scratch = scratch
		return fv, nil
	})
	if err != nil {
		t.Fatalf("Unlock: %v", err)
	}
	if info.DeviceName != "test-device" {
		t.Fatalf("Info not returned from the vault: %+v", info)
	}
	return fv, s
}

// Story 9: lock wipes. The scratch dir is gone, the vault is closed, and the id no longer
// resolves.
func TestLockClosesWipesAndForgets(t *testing.T) {
	r, _, freed := newTestRegistry(t, time.Hour)
	fv, s := unlockOne(t, r, "s1")

	if _, err := os.Stat(fv.scratch); err != nil {
		t.Fatalf("scratch should exist while the session is open: %v", err)
	}
	if _, ok := r.Get(s.ID); !ok {
		t.Fatal("session should resolve while it is open")
	}

	if err := r.Lock(s.ID); err != nil {
		t.Fatalf("Lock: %v", err)
	}

	if fv.closeCount() != 1 {
		t.Errorf("vault Close called %d times, want 1", fv.closeCount())
	}
	if _, err := os.Stat(fv.scratch); !os.IsNotExist(err) {
		t.Errorf("scratch survived the lock: %v", err)
	}
	if _, ok := r.Get(s.ID); ok {
		t.Error("session still resolves after lock")
	}
	if *freed != 1 {
		t.Errorf("teardown called FreeOSMemory %d times, want 1 (spec D10.3 clause (c))", *freed)
	}
}

// Locking twice is not an error: contracts.md §1 answers 204, and a double-click must not
// look like a fault.
func TestLockIsIdempotentAndUnknownIsNotAnError(t *testing.T) {
	r, _, _ := newTestRegistry(t, time.Hour)
	_, s := unlockOne(t, r, "s1")

	if err := r.Lock(s.ID); err != nil {
		t.Fatalf("first Lock: %v", err)
	}
	if err := r.Lock(s.ID); err != nil {
		t.Errorf("second Lock should be a no-op, got %v", err)
	}
	if err := r.Lock("never-existed"); err != nil {
		t.Errorf("unknown id should be a no-op, got %v", err)
	}
}

// Story 10: the TTL tears down by the same path as an explicit lock — same close, same
// wipe, same memory return.
func TestExpiryTearsDownLikeALock(t *testing.T) {
	r, now, freed := newTestRegistry(t, 15*time.Minute)
	fv, s := unlockOne(t, r, "s1")

	if got, want := s.ExpiresAt, now.Add(15*time.Minute); !got.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got, want)
	}

	*now = now.Add(16 * time.Minute)

	if n := r.Sweep(); n != 1 {
		t.Fatalf("Sweep ended %d sessions, want 1", n)
	}
	if fv.closeCount() != 1 {
		t.Errorf("vault Close called %d times, want 1", fv.closeCount())
	}
	if _, err := os.Stat(fv.scratch); !os.IsNotExist(err) {
		t.Errorf("scratch survived expiry: %v", err)
	}
	if *freed != 1 {
		t.Errorf("expiry called FreeOSMemory %d times, want 1", *freed)
	}
}

// An expired session must not be served even before the sweep reaches it — otherwise the
// TTL means "until the next tick" rather than what it says.
func TestExpiredSessionIsRefusedBeforeTheSweep(t *testing.T) {
	r, now, _ := newTestRegistry(t, 15*time.Minute)
	fv, s := unlockOne(t, r, "s1")

	*now = now.Add(16 * time.Minute)

	if _, ok := r.Get(s.ID); ok {
		t.Error("Get resolved an expired session")
	}
	err := r.With(s.ID, func(Vault) error { return nil })
	if !errors.Is(err, ErrNoSession) {
		t.Errorf("With on an expired session = %v, want ErrNoSession", err)
	}
	// And the refusal reaped it rather than leaving it for the ticker.
	if fv.closeCount() != 1 {
		t.Errorf("the refusal did not tear the session down (Close called %d times)", fv.closeCount())
	}
	if _, err := os.Stat(fv.scratch); !os.IsNotExist(err) {
		t.Errorf("scratch survived the refusal: %v", err)
	}
}

// A Vault is not required to be concurrency-safe, so a second in-flight call is refused
// with a reason rather than silently queued behind an unbounded download.
func TestSecondConcurrentCallIsRefusedNotQueued(t *testing.T) {
	r, _, _ := newTestRegistry(t, time.Hour)
	fv, s := unlockOne(t, r, "s1")
	fv.blockOpen = make(chan struct{})

	entered := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- r.With(s.ID, func(v Vault) error {
			close(entered)
			_, err := v.Open(context.Background(), "f1")
			return err
		})
	}()
	<-entered

	if err := r.With(s.ID, func(Vault) error { return nil }); !errors.Is(err, ErrSessionBusy) {
		t.Errorf("second concurrent call = %v, want ErrSessionBusy", err)
	}

	close(fv.blockOpen)
	if err := <-done; err != nil {
		t.Fatalf("first call: %v", err)
	}

	// And the session is usable again once the first call returns.
	if err := r.With(s.ID, func(Vault) error { return nil }); err != nil {
		t.Errorf("session should be free again, got %v", err)
	}
}

// Story 11's registry half: teardown waits for work in flight instead of closing a Vault
// out from under a reader.
func TestTeardownWaitsForAnInFlightRead(t *testing.T) {
	r, _, _ := newTestRegistry(t, time.Hour)
	fv, s := unlockOne(t, r, "s1")
	fv.blockOpen = make(chan struct{})

	entered := make(chan struct{})
	readDone := make(chan struct{})
	go func() {
		_ = r.With(s.ID, func(v Vault) error {
			close(entered)
			_, err := v.Open(context.Background(), "f1")
			close(readDone)
			return err
		})
	}()
	<-entered

	lockReturned := make(chan struct{})
	go func() { _ = r.Lock(s.ID); close(lockReturned) }()

	select {
	case <-lockReturned:
		t.Fatal("Lock returned while a read was still in flight")
	case <-time.After(50 * time.Millisecond):
	}

	close(fv.blockOpen)
	<-readDone
	<-lockReturned

	if fv.closeCount() != 1 {
		t.Errorf("vault Close called %d times, want 1", fv.closeCount())
	}
}

// A failed unlock leaves nothing behind — no session to look up, and no scratch directory
// for a teardown that will never run.
func TestFailedUnlockRegistersNothingAndLeavesNoScratch(t *testing.T) {
	r, _, _ := newTestRegistry(t, time.Hour)
	fv := &fakeVault{unlockErr: ErrBadPassword}

	_, _, err := r.Unlock(context.Background(), "s1", "v1", "wrong", func(scratch string) (Vault, error) {
		fv.scratch = scratch
		return fv, nil
	})
	if !errors.Is(err, ErrBadPassword) {
		t.Fatalf("Unlock = %v, want ErrBadPassword", err)
	}
	if _, ok := r.Get("s1"); ok {
		t.Error("a failed unlock registered a session")
	}
	if fv.closeCount() != 1 {
		t.Errorf("a failed unlock did not close the vault (Close called %d times)", fv.closeCount())
	}
	if _, err := os.Stat(r.ScratchFor("s1")); !os.IsNotExist(err) {
		t.Errorf("a failed unlock left its scratch dir behind: %v", err)
	}
}

// Story 11: a crash leaves residue, and the next start is what clears it. A deferred
// cleanup cannot do this by construction, which is why the wipe is in NewRegistry.
func TestNewRegistryWipesResidueFromAPreviousRun(t *testing.T) {
	root := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(filepath.Join(root, "dead-session"), 0o700); err != nil {
		t.Fatal(err)
	}
	residue := filepath.Join(root, "dead-session", "Manifest.db")
	if err := os.WriteFile(residue, []byte("decrypted index"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := NewRegistry(root, time.Hour); err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	if _, err := os.Stat(residue); !os.IsNotExist(err) {
		t.Errorf("residue from a previous run survived a fresh start: %v", err)
	}
	if _, err := os.Stat(root); err != nil {
		t.Errorf("the scratch root should exist after the wipe: %v", err)
	}
}

// CloseAll is process shutdown, and it runs the same teardown as everything else.
func TestCloseAllTearsDownEverySession(t *testing.T) {
	r, _, freed := newTestRegistry(t, time.Hour)
	a, _ := unlockOne(t, r, "s1")
	b, _ := unlockOne(t, r, "s2")

	if err := r.CloseAll(); err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	for name, fv := range map[string]*fakeVault{"s1": a, "s2": b} {
		if fv.closeCount() != 1 {
			t.Errorf("%s: Close called %d times, want 1", name, fv.closeCount())
		}
		if _, err := os.Stat(fv.scratch); !os.IsNotExist(err) {
			t.Errorf("%s: scratch survived shutdown: %v", name, err)
		}
	}
	if *freed != 2 {
		t.Errorf("CloseAll called FreeOSMemory %d times, want 2", *freed)
	}
}

// SetTTL governs the NEXT session and never moves one already running: ExpiresAt is a fact
// stamped at unlock, not a value recomputed under the user.
func TestSetTTLDoesNotMoveARunningSession(t *testing.T) {
	r, now, _ := newTestRegistry(t, 15*time.Minute)
	_, first := unlockOne(t, r, "s1")

	r.SetTTL(time.Hour)
	_, second := unlockOne(t, r, "s2")

	if got, want := first.ExpiresAt, now.Add(15*time.Minute); !got.Equal(want) {
		t.Errorf("running session moved: ExpiresAt = %v, want %v", got, want)
	}
	if got, want := second.ExpiresAt, now.Add(time.Hour); !got.Equal(want) {
		t.Errorf("new session did not take the new TTL: ExpiresAt = %v, want %v", got, want)
	}
}

// A session writes under its own directory and nowhere else — the property design §7 calls
// the scratch jail, asserted here at the path level for the in-process case.
func TestScratchIsPerSessionAndUnderTheRoot(t *testing.T) {
	r, _, _ := newTestRegistry(t, time.Hour)
	a, _ := unlockOne(t, r, "s1")
	b, _ := unlockOne(t, r, "s2")

	if a.scratch == b.scratch {
		t.Fatal("two sessions were handed the same scratch directory")
	}
	for _, dir := range []string{a.scratch, b.scratch} {
		rel, err := filepath.Rel(r.scratchRoot, dir)
		if err != nil || strings.HasPrefix(rel, "..") {
			t.Errorf("scratch %q is not under the root %q", dir, r.scratchRoot)
		}
	}
}

// Zero means the default, not "never expires": a session holds live keys, and an unbounded
// one must not be reachable by typing a zero.
func TestTTLFromMinutes(t *testing.T) {
	for _, tc := range []struct {
		minutes int
		want    time.Duration
	}{
		{0, DefaultSessionTTL},
		{-30, DefaultSessionTTL},
		{15, 15 * time.Minute},
		{1, time.Minute},
		{1440, 24 * time.Hour},
	} {
		if got := TTLFromMinutes(tc.minutes); got != tc.want {
			t.Errorf("TTLFromMinutes(%d) = %v, want %v", tc.minutes, got, tc.want)
		}
	}
}

// The streaming guarantee is UNCONDITIONAL: teardown must not close the vault or wipe the
// scratch tree while a stream handed OUT of the call is still open.
//
// This is the shape that tore before OpenStream existed — take the reader, return, then
// lock — and the natural way to write an HTTP download.
func TestTeardownWaitsForAStreamHandedOutOfTheCall(t *testing.T) {
	r, _, _ := newTestRegistry(t, time.Hour)
	fv, s := unlockOne(t, r, "s1")

	rc, _, err := r.OpenStream(context.Background(), s.ID, "f1")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}

	// The reader is open and the call that produced it has returned — exactly the case
	// `With` could not protect.
	lockReturned := make(chan struct{})
	go func() { _ = r.Lock(s.ID); close(lockReturned) }()

	select {
	case <-lockReturned:
		t.Fatal("Lock returned while a stream was still open — the vault would close under the reader")
	case <-time.After(50 * time.Millisecond):
	}

	if fv.closeCount() != 0 {
		t.Errorf("the vault was closed with a stream open (Close called %d times)", fv.closeCount())
	}
	if _, err := os.Stat(fv.scratch); err != nil {
		t.Errorf("scratch was wiped with a stream open: %v", err)
	}

	if err := rc.Close(); err != nil {
		t.Fatalf("closing the stream: %v", err)
	}
	<-lockReturned

	if fv.closeCount() != 1 {
		t.Errorf("after the stream closed, Close called %d times, want 1", fv.closeCount())
	}
}

// A second operation is refused while a stream is open — the stream holds the session, so
// the busy rule applies to it exactly as it does to a call.
func TestAnOpenStreamMakesTheSessionBusy(t *testing.T) {
	r, _, _ := newTestRegistry(t, time.Hour)
	_, s := unlockOne(t, r, "s1")

	rc, _, err := r.OpenStream(context.Background(), s.ID, "f1")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	defer func() { _ = rc.Close() }()

	if err := r.With(s.ID, func(Vault) error { return nil }); !errors.Is(err, ErrSessionBusy) {
		t.Errorf("With during a stream = %v, want ErrSessionBusy", err)
	}
}

// Closing twice is ordinary in HTTP handling — a defer plus an explicit close on an error
// path — and releasing a mutex twice panics. The hold must absorb it.
func TestClosingAStreamTwiceIsSafe(t *testing.T) {
	r, _, _ := newTestRegistry(t, time.Hour)
	_, s := unlockOne(t, r, "s1")

	rc, _, err := r.OpenStream(context.Background(), s.ID, "f1")
	if err != nil {
		t.Fatalf("OpenStream: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := rc.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
	// And the session is usable again rather than wedged.
	if err := r.With(s.ID, func(Vault) error { return nil }); err != nil {
		t.Errorf("session should be free after the stream closed, got %v", err)
	}
}

// A failure to open releases the lock rather than wedging the session — the path where the
// reader that would have carried the lock never exists.
func TestAFailedOpenStreamDoesNotWedgeTheSession(t *testing.T) {
	r, _, _ := newTestRegistry(t, time.Hour)
	fv, s := unlockOne(t, r, "s1")
	fv.statErr = ErrFileNotFound

	if _, _, err := r.OpenStream(context.Background(), s.ID, "missing"); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("OpenStream = %v, want ErrFileNotFound", err)
	}
	if err := r.With(s.ID, func(Vault) error { return nil }); err != nil {
		t.Errorf("the session is wedged after a failed open: %v", err)
	}
}

// THE WRAPPER SWALLOWS ErrOverlongFile AFTER RECORDING IT, and that is not tidiness — it is
// what stops the HTTP layer logging a failure that did not happen.
//
// Measured on the stand before this: the error reached `io.Copy`, fell to the handler's
// default arm, and logged "file stream ended early — the backup holds FEWER bytes than its
// index records" about a file with too MANY. That is quince#1381's own defect arriving from
// the other side, reintroduced by the fix for quince#1379.
//
// The transfer genuinely succeeded: the body is exactly the declared Content-Length. So the
// stream ends in io.EOF, and the CONDITION travels as the `overlong` field instead.
func TestOverlongIsRecordedThenSwallowedSoTheTransferReadsAsTheSuccessItIs(t *testing.T) {
	r, _, _ := newTestRegistry(t, time.Hour)
	_, sess := unlockOne(t, r, "S1")

	rc := r.WatchIncomplete(sess.ID, "f1", io.NopCloser(&overlongReader{}))
	n, readErr := io.ReadAll(rc)

	if string(n) != "abcd" {
		t.Errorf("delivered %q, want %q — every byte the record promises must arrive", n, "abcd")
	}
	// io.ReadAll treats io.EOF as normal termination, so a nil error here IS the claim: the
	// handler sees an ordinary success and logs nothing.
	if readErr != nil {
		t.Errorf("read returned %v, want nil — the body is exactly Content-Length, so nothing "+
			"failed and a handler that logs on error would report a failure that did not happen",
			readErr)
	}
	if !r.OverlongIn(sess.ID)["f1"] {
		t.Error("the condition was swallowed WITHOUT being recorded — that is the silent " +
			"truncation this whole path exists to prevent")
	}
}

// overlongReader delivers the recorded bytes and then reports that the backup holds more,
// which is what boundedFile does when it finds a byte past the record.
type overlongReader struct{ done bool }

func (o *overlongReader) Read(p []byte) (int, error) {
	if o.done {
		return 0, ErrOverlongFile
	}
	o.done = true
	return copy(p, []byte("abcd")), nil
}
