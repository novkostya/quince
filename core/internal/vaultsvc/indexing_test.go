package vaultsvc

import (
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/vault"
	"github.com/novkostya/quince/core/internal/vault/messages"
	"github.com/novkostya/quince/core/internal/wire"
)

// recordingPub captures published envelopes. It is deliberately a Publisher and not a *bus.Bus:
// what this package promises is "frames go to the publisher", and testing through a real bus
// would be testing the bus.
type recordingPub struct {
	mu   sync.Mutex
	sent []wire.Envelope
}

func (r *recordingPub) PublishEvent(typ string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, wire.NewEnvelope(typ, data))
}

func (r *recordingPub) frames() []wire.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]wire.Envelope{}, r.sent...)
}

// TestIndexingFrameCarriesItsDeviceAndCount is the whole claim of this slice in one test: a scan
// callback becomes a wire frame that names the session, the device and how far it has got.
func TestIndexingFrameCarriesItsDeviceAndCount(t *testing.T) {
	pub := &recordingPub{}
	p := &indexingPublisher{pub: pub, sessionID: "sess-1", udid: "DEV-A", now: time.Now}

	p.onProgress(messages.Progress{Messages: 40000})

	frames := pub.frames()
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if frames[0].Type != wire.EventMessagesIndexing {
		t.Errorf("type = %q, want %q", frames[0].Type, wire.EventMessagesIndexing)
	}
	got, ok := frames[0].Data.(wire.MessagesIndexing)
	if !ok {
		t.Fatalf("data is %T, want wire.MessagesIndexing", frames[0].Data)
	}
	if got.SessionID != "sess-1" || got.UDID != "DEV-A" || got.Messages != 40000 {
		t.Errorf("payload = %+v, want {sess-1 DEV-A 40000}", got)
	}

	// THE SOCKET MUST BE ABLE TO SCOPE IT. Classification is asserted in the wire package; what
	// is asserted here is that THIS producer's payload answers it, which is the half that lives
	// on this side of the seam and the half quince#1380 found unheld for two other events.
	udid, scoped := wire.EventDevice(frames[0])
	if !scoped || udid != "DEV-A" {
		t.Errorf("EventDevice = (%q, %v), want (DEV-A, true)", udid, scoped)
	}
}

// TestIndexingThrottleHoldsTheContractRate pins the ≤2/s the WS table promises. The clock is
// injected rather than slept on: a test that sleeps 500 ms to prove a 500 ms throttle is a test
// that gets deleted the first time the suite is timed.
func TestIndexingThrottleHoldsTheContractRate(t *testing.T) {
	pub := &recordingPub{}
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	p := &indexingPublisher{pub: pub, sessionID: "s", udid: "DEV-A", now: func() time.Time { return now }}

	// The first frame always goes: it is what tells a user anything is happening.
	p.onProgress(messages.Progress{Messages: 10000})
	// Four more STRICTLY inside one window — the rate the reader actually calls at (~4/s).
	//
	// A FIFTH OF THE WINDOW, NOT A QUARTER: four quarter-steps land exactly ON the boundary,
	// where a frame is correctly allowed, and the first version of this test read that as a
	// throttle failure. The boundary itself is asserted below, deliberately and separately.
	for i := 2; i <= 5; i++ {
		now = now.Add(indexingThrottle / 5)
		p.onProgress(messages.Progress{Messages: int64(i * 10000)})
	}
	if n := len(pub.frames()); n != 1 {
		t.Fatalf("got %d frames inside one throttle window, want 1", n)
	}

	// EXACTLY ON THE BOUNDARY, which is the case the arithmetic above got wrong and the one
	// the contract's "≤2/s" actually names: at a full window the frame goes.
	now = now.Add(indexingThrottle / 5) // → t0 + 500ms, one whole window since the first frame
	p.onProgress(messages.Progress{Messages: 250000})
	frames := pub.frames()
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(frames))
	}
	if got := frames[1].Data.(wire.MessagesIndexing).Messages; got != 250000 {
		t.Errorf("second frame count = %d, want 250000 (the live count, not a suppressed one)", got)
	}
}

// TestIndexingWithoutAPublisherIsSilentAndSafe — nil is a supported wiring, and the callback must
// not panic under it. A daemon built without a bus still serves the route.
func TestIndexingWithoutAPublisherIsSilentAndSafe(t *testing.T) {
	p := &indexingPublisher{pub: nil, sessionID: "s", udid: "DEV-A", now: time.Now}
	p.onProgress(messages.Progress{Messages: 1}) // must not panic

	var nilp *indexingPublisher
	nilp.onProgress(messages.Progress{Messages: 1}) // nor must the nil receiver
}

// TestIndexingForResolvesTheSessionsDevice walks the real chain — session → version → device —
// rather than the publisher unit, because that chain is what the ruling rests on: the event is
// device-scoped only if a session can actually name its device.
func TestIndexingForResolvesTheSessionsDevice(t *testing.T) {
	pub := &recordingPub{}
	v := encrypted()
	v.UDID = "UDID-FIXTURE-1"
	s, err := New(stubVersions{v: v, ok: true}, filepath.Join(t.TempDir(), "scratch"),
		time.Hour, slog.New(slog.DiscardHandler), pub)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s.open = func(wire.Version, string) (vault.Vault, error) { return &fakeVault{}, nil }
	t.Cleanup(func() { _ = s.Registry().CloseAll() })

	sess, code, msg := s.Unlock("01V", "pw")
	if code != "" {
		t.Fatalf("Unlock: %s — %s", code, msg)
	}

	cb := s.indexingFor(sess.ID)
	if cb == nil {
		t.Fatal("indexingFor returned nil for a live session — the scan would be unnarrated")
	}
	cb(messages.Progress{Messages: 123})

	frames := pub.frames()
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	got := frames[0].Data.(wire.MessagesIndexing)
	if got.UDID != "UDID-FIXTURE-1" || got.SessionID != sess.ID {
		t.Errorf("payload = %+v, want the session's own id and device", got)
	}
}

// TestIndexingForIsNilWhenTheSessionIsGone — the read still SERVES when the scan cannot be
// narrated, and nil is the reader's documented way of saying "no progress callback". The
// alternative, refusing the read, would trade the feature for the narration of it.
func TestIndexingForIsNilWhenTheSessionIsGone(t *testing.T) {
	pub := &recordingPub{}
	s, err := New(stubVersions{v: encrypted(), ok: true}, filepath.Join(t.TempDir(), "scratch"),
		time.Hour, slog.New(slog.DiscardHandler), pub)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Registry().CloseAll() })

	if cb := s.indexingFor("no-such-session"); cb != nil {
		t.Error("indexingFor returned a callback for a session that does not exist")
	}
	if _, ok := s.udidFor("no-such-session"); ok {
		t.Error("udidFor claimed a device for a session that does not exist")
	}
}

// TestIndexingForIsNilWithoutAPublisher — no bus, no callback, and therefore no per-row work
// done to produce frames nobody receives.
func TestIndexingForIsNilWithoutAPublisher(t *testing.T) {
	s := newService(t, encrypted(), true, &fakeVault{})
	sess, code, _ := s.Unlock("01V", "pw")
	if code != "" {
		t.Fatalf("Unlock: %s", code)
	}
	if cb := s.indexingFor(sess.ID); cb != nil {
		t.Error("indexingFor returned a callback with no publisher wired")
	}
}
