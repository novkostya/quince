package pushsvc

import (
	"context"
	"fmt"

	"github.com/novkostya/quince/core/internal/notify"
)

// DeliverDecision sends one notifier decision to every live subscription, satisfying
// `notify.Deliverer` — the seam between the half that decides and the half that dials.
//
// THE NOTIFIER NEVER LEARNS AN ENDPOINT, and that is the whole reason this method exists rather than
// `notify` calling `Deliver` itself. A subscription endpoint is capability-grade: anyone holding one
// can push to that phone. `notify` gets a boolean-shaped answer and never sees the list.
//
// A PER-DEVICE FAILURE IS REPORTED, NOT SWALLOWED. `Deliver` deliberately never returns early — one
// dead phone must not stop the others — so its per-device outcomes come back in a slice that this
// method is the last chance to look at. Returning only the global error would make "the push service
// refused every one of your devices" indistinguishable from a clean send, in the one caller that has
// a logger. What the runner does with the error is unchanged either way: it logs and records the
// track, because a retry storm costs more than a missed reminder (see `notify.Runner.send`).
//
// EXPIRY IS NOT FAILURE. A 410 means that phone unsubscribed; the row is marked, the settings
// surface names it, and nothing is wrong with this daemon. Counting it here would make an ordinary
// uninstall look like an outage in the logs forever.
func (s *Service) DeliverDecision(ctx context.Context, d notify.Decision) error {
	sender, ok := s.store.(Sender)
	if !ok {
		// Same refusal `SendTest` makes, for the same reason: a store that cannot record a 410 would
		// leave a dead phone listed as live. A wiring mistake, so it is loud rather than degraded.
		return ErrStoreCannotSend
	}
	results, err := s.Deliver(ctx, sender, d, s.subject)
	if err != nil {
		return err
	}
	failed := 0
	for _, r := range results {
		if !r.Sent && !r.Expired {
			failed++
		}
	}
	if failed > 0 {
		// NO LABEL AND NO ENDPOINT IN THIS STRING. It reaches a log line through the runner, and the
		// count is what tells an operator whether this is one flaky phone or a dead push service.
		return fmt.Errorf("pushsvc: %d of %d subscriptions did not receive this notification",
			failed, len(results))
	}
	return nil
}
