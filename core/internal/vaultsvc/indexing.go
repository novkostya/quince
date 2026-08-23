package vaultsvc

import (
	"sync"
	"time"

	"github.com/novkostya/quince/core/internal/vault/messages"
	"github.com/novkostya/quince/core/internal/wire"
)

// Publisher is the event surface this package needs: one envelope, stamped and fanned out.
// Consumer-defined, so vaultsvc imports no bus — the same shape as Versions above (*bus.Bus
// satisfies it).
type Publisher interface {
	PublishEvent(typ string, data any)
}

// indexingThrottle is the minimum gap between two messages.indexing frames.
//
// IT MATCHES job.updated's PROMISE BECAUSE THE TABLE MAKES ONE (contracts §3: "progress
// throttled to ≤2/s"), and a second progress event on the same socket answering to a
// different rate would make that column mean nothing.
//
// THROTTLED HERE RATHER THAN IN THE READER, and the distinction is not stylistic. The
// reader's `progressEvery` is a ROW COUNT (10,000) and this is a RATE; a row count cannot
// hold a rate, because what varies between machines is the per-row cost. On the measured
// backup 10,000 rows is ~0.25 s — about four frames a second, twice what the table
// promises — and on a faster disk it is more. Time is the only unit that keeps the promise
// on hardware nobody has measured.
const indexingThrottle = 500 * time.Millisecond

// indexingPublisher turns the reader's onProgress callback into throttled wire frames.
//
// ONE PER BUILD, NOT ONE PER SERVICE: the throttle clock has to start when a scan starts, or
// the first frame of the second scan in a session is suppressed by the last frame of the
// first. It is stateful and single-scan by construction.
type indexingPublisher struct {
	pub       Publisher
	sessionID string
	udid      string
	now       func() time.Time // a field so the throttle is testable without sleeping

	mu       sync.Mutex
	lastSent time.Time
	started  bool
}

// onProgress is the callback handed to messages.Reader.
//
// THE FINAL CALLBACK CAN BE DROPPED BY THE THROTTLE, AND THAT IS ACCEPTED RATHER THAN
// OVERLOOKED. There is deliberately no terminal frame: the HTTP response that the scan was
// blocking IS the completion signal, and it arrives milliseconds later. A "done" event would
// be a second way to say what the response already says, free to disagree with it.
//
// THE COUNT IS PASSED THROUGH UNTOUCHED and carries no total, because the parser counts
// nothing up front (messages.Progress). Inventing a percentage here would be inventing one
// on the wire.
func (p *indexingPublisher) onProgress(pr messages.Progress) {
	if p == nil || p.pub == nil {
		return
	}
	p.mu.Lock()
	now := p.now()
	// THE FIRST FRAME IS NEVER THROTTLED. A zero lastSent would pass the gap test anyway, but
	// only by accident of the epoch being far away; saying so makes it a property rather than
	// a coincidence — and the first frame is the one that proves to a user that anything is
	// happening at all.
	if p.started && now.Sub(p.lastSent) < indexingThrottle {
		p.mu.Unlock()
		return
	}
	p.started = true
	p.lastSent = now
	p.mu.Unlock()

	p.pub.PublishEvent(wire.EventMessagesIndexing, wire.MessagesIndexing{
		SessionID: p.sessionID,
		UDID:      p.udid,
		Messages:  pr.Messages,
	})
}

// indexingFor builds the callback for one scan on one session, or nil if this build cannot be
// reported on.
//
// NIL IS A LEGITIMATE ANSWER AND THE READER TAKES IT — `onProgress may be nil` is the seam's
// documented contract, and slice 4 passed nil for every call. A session whose device cannot be
// resolved therefore still SERVES; it just serves without a progress frame. Refusing the read
// because the socket cannot be told about it would trade the feature for the narration of it.
func (s *Service) indexingFor(sessionID string) func(messages.Progress) {
	if s.pub == nil {
		return nil
	}
	udid, ok := s.udidFor(sessionID)
	if !ok {
		return nil
	}
	p := &indexingPublisher{pub: s.pub, sessionID: sessionID, udid: udid, now: time.Now}
	return p.onProgress
}

// udidFor resolves session → version → device.
//
// BOTH HOPS ARE LIVE READS, NOT A CACHE, for the reason SessionVersion already gives: a
// session that has been locked or has expired reports false. A frame published under a
// session that no longer exists would name a device the socket would then scope it to.
func (s *Service) udidFor(sessionID string) (string, bool) {
	versionID, ok := s.SessionVersion(sessionID)
	if !ok {
		return "", false
	}
	v, found, err := s.versions.Version(versionID)
	if err != nil || !found {
		return "", false
	}
	return v.UDID, v.UDID != ""
}
