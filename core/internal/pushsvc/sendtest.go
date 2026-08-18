package pushsvc

import (
	"context"
	"errors"

	"github.com/novkostya/quince/core/internal/notify"
	"github.com/novkostya/quince/core/internal/wire"
)

// SendTest delivers one notification to every live subscription and reports what happened to each.
//
// IT IS WHY THE FEATURE IS DEBUGGABLE AT ALL. Without it, *"are notifications working?"* is
// answerable only by waiting for a device to go stale — three days by default — which is no use
// during setup or a support conversation. It is also what the rung's click-list uses.
//
// THE COPY IS DELIBERATELY BORING. It has to read as a test on a lock screen three seconds after
// somebody tapped a button they are still looking at, and it must not be mistakable for a real
// reminder about a device.
//
// AND IT DOES NOT SAY "quince", BECAUSE iOS ALREADY DOES. A Home Screen web app's notification is
// rendered as `<title> from <app name>`, so a title beginning with the app's own name produced
// *"quince notifications are working from quince"* on a real lock screen — measured 2026-08-18, on
// the first notification quince ever delivered. Nothing in a test could have caught it: the
// attribution is the platform's, not quince's, and it exists only on the device.
func (s *Service) SendTest(ctx context.Context) ([]wire.PushDeliveryResult, error) {
	sender, ok := s.store.(Sender)
	if !ok {
		// A store that cannot record delivery outcomes cannot deliver honestly — a 410 would go
		// unrecorded and the dead phone would stay listed as live. That is a WIRING mistake rather
		// than a runtime condition, so it fails loudly here instead of half-sending.
		return nil, ErrStoreCannotSend
	}
	results, err := s.Deliver(ctx, sender, notify.Decision{
		Kind:  KindTest,
		Title: testTitle,
		Body:  "This is a test. Nothing needs backing up because of it.",
		// BACK TO THE PAGE THE BUTTON IS ON, which is where the person who pressed it is. It used to
		// be "/" on the reasoning that a test belongs to no device and deep-linking to one would lie
		// about why it arrived — the first half of that is right and the conclusion was wrong. The
		// notifications page is not a device page: it is the surface the test was requested from, so
		// a tap returns you to your own result. Operator-reported 2026-08-18.
		Navigate: "/settings/notifications",
	}, s.subject)
	if err != nil {
		return nil, err
	}
	return toWire(results), nil
}

// KindTest is the payload kind for a test notification.
//
// NOT ONE OF THE FIVE FROZEN KINDS, deliberately: those are contract surface with per-category
// switches and precedence rules, and a test is none of those things. It rides in the same field so
// the service worker needs no special case, and it is deliberately absent from `notify.Enabled` —
// a test the user asked for is not something to suppress on their behalf.
const KindTest notify.Kind = "test"

// toWire renders delivery outcomes for the API, by LABEL and never by endpoint.
//
// THREE STATES, NOT A BOOLEAN. `expired` and `error` are different facts with different remedies —
// one means re-subscribe on that device, the other means try again — and a caller that cannot tell
// them apart cannot report either honestly.
func toWire(results []Result) []wire.PushDeliveryResult {
	out := make([]wire.PushDeliveryResult, 0, len(results))
	for _, r := range results {
		w := wire.PushDeliveryResult{Label: r.Label, State: "sent"}
		switch {
		case r.Expired:
			w.State = "expired"
		case r.Err != nil || !r.Sent:
			w.State = "error"
			if r.Err != nil {
				// SAFE TO RETURN because every delivery error is built through
				// `push.RedactEndpoint`, so it carries an origin at most. That is a property of
				// send.go asserted by its own test, and this line depends on it.
				w.Error = r.Err.Error()
			}
		}
		out = append(out, w)
	}
	return out
}

// ErrStoreCannotSend is a wiring mistake: a store that cannot expire or mark rows must not deliver,
// because a 410 would go unrecorded and the dead device would stay listed as live.
var ErrStoreCannotSend = errors.New(
	"pushsvc: this service's store cannot record delivery outcomes, so it must not send")

// testTitle is the lock-screen title of a test notification. A constant so its own test can read it
// without a delivery round trip, which would prove the transport rather than the copy.
const testTitle = "Notifications are working"
