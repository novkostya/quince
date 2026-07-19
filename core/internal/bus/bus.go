// Package bus is the in-process event bus (design §2): every state change is published as
// a wire.Envelope and fanned out to subscribers over per-subscriber buffered channels. A
// slow subscriber is DROPPED (its Dropped channel closes) rather than blocking the
// publisher — the WS handler tears the connection down, the client reconnects and
// GET-refreshes (contracts §3: events are notifications, not a replayable log).
package bus

import (
	"sync"

	"github.com/novkostya/quince/core/internal/wire"
)

// Subscription is a handle to the event stream. Read from C(); on Dropped() the subscriber
// fell behind and must tear down.
type Subscription struct {
	ch      chan wire.Envelope
	dropped chan struct{}
	once    sync.Once
}

// C is the delivery channel (closed by Unsubscribe).
func (s *Subscription) C() <-chan wire.Envelope { return s.ch }

// Dropped is closed once if this subscription overflowed and was dropped.
func (s *Subscription) Dropped() <-chan struct{} { return s.dropped }

func (s *Subscription) dropOnce() { s.once.Do(func() { close(s.dropped) }) }

// Bus fans out envelopes to all current subscribers.
type Bus struct {
	mu   sync.RWMutex
	subs map[*Subscription]struct{}
}

// New returns an empty bus.
func New() *Bus { return &Bus{subs: map[*Subscription]struct{}{}} }

// Subscribe registers a subscriber with the given buffer (min 1).
func (b *Bus) Subscribe(buffer int) *Subscription {
	if buffer < 1 {
		buffer = 1
	}
	s := &Subscription{
		ch:      make(chan wire.Envelope, buffer),
		dropped: make(chan struct{}),
	}
	b.mu.Lock()
	b.subs[s] = struct{}{}
	b.mu.Unlock()
	return s
}

// Unsubscribe removes a subscriber and closes its channel. Idempotent. Because Publish
// holds the read lock while sending and Unsubscribe holds the write lock while closing, a
// send can never race a close — send-on-closed-channel is impossible by construction.
func (b *Bus) Unsubscribe(s *Subscription) {
	b.mu.Lock()
	if _, ok := b.subs[s]; ok {
		delete(b.subs, s)
		close(s.ch)
	}
	b.mu.Unlock()
}

// Publish delivers env to every subscriber without ever blocking: a subscriber whose
// buffer is full is dropped.
func (b *Bus) Publish(env wire.Envelope) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for s := range b.subs {
		select {
		case s.ch <- env:
		default:
			s.dropOnce()
		}
	}
}

// PublishEvent is a convenience wrapper that stamps and publishes.
func (b *Bus) PublishEvent(typ string, data any) {
	b.Publish(wire.NewEnvelope(typ, data))
}
