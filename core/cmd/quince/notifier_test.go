package main

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/notify"
	"github.com/novkostya/quince/core/internal/pushsvc"
	"github.com/novkostya/quince/core/internal/wire"
)

// A TYPED NIL IS NOT NIL, and this daemon expresses `--demo` as a nil interface in four places. If
// any converter returned the concrete pointer unguarded, `deps.Notifications != nil` would be TRUE
// in demo mode and the routes would register onto a service with no store — a panic on the first
// request, from a mode whose whole point is that it cannot touch real state.
//
// It is asserted rather than reasoned about because the bug is invisible at the call site: the code
// that breaks reads `Notifications: pushSvc`, which looks correct in every way.
func TestTheConvertersProduceAnHonestNilNotATypedOne(t *testing.T) {
	if r := notificationReader(nil); r != nil {
		t.Errorf("notificationReader(nil) is non-nil, so demo mode would register the routes")
	}
	if d := pushDeliverer(nil); d != nil {
		t.Errorf("pushDeliverer(nil) is non-nil, so the notifier would start with a dead deliverer")
	}
	if j := engineJobs(nil); j != nil {
		t.Errorf("engineJobs(nil) is non-nil, so a muxerless daemon would panic on its first tick")
	}
}

// AND THEY PASS A REAL ONE THROUGH. A guard that returned nil unconditionally would satisfy the test
// above and turn notifications off for every install.
func TestTheConvertersPassARealServiceThrough(t *testing.T) {
	s := &pushsvc.Service{}
	if notificationReader(s) == nil {
		t.Errorf("a real push service did not reach the router")
	}
	if pushDeliverer(s) == nil {
		t.Errorf("a real push service did not reach the notifier")
	}
	if engineJobs(&backup.Engine{}) == nil {
		t.Errorf("a real engine did not reach the notifier")
	}
}

// THE FORWARDER PASSES WHAT THE RUNNER ACTS ON AND DROPS THE REST. `job.updated` alone fires on
// every phase change of every job, so forwarding the whole bus would make the notifier this daemon's
// busiest consumer to reach exactly the same decisions.
func TestForwardEventsCarriesOnlyTheThreeEventsTheNotifierActsOn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := bus.New()
	out := make(chan wire.Envelope, 16)
	go forwardEvents(ctx, quietLog(), b, out)
	waitForSubscriber(t, b, out)

	b.PublishEvent(wire.EventDeviceDetached, nil) // dropped
	b.PublishEvent(wire.EventDeviceAttached, nil) // carried — the opportunity signal
	b.PublishEvent("job.log", nil)                // dropped
	b.PublishEvent(wire.EventJobUpdated, nil)     // carried
	b.PublishEvent(wire.EventDeviceUpdated, nil)  // carried

	got := drain(t, out, 3)
	want := []string{wire.EventDeviceAttached, wire.EventJobUpdated, wire.EventDeviceUpdated}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("forwarded %v, want %v", got, want)
		}
	}
	select {
	case extra := <-out:
		t.Errorf("an event the notifier does not act on was forwarded: %q", extra)
	case <-time.After(50 * time.Millisecond):
	}
}

// A DROP IS RECOVERED, NOT PRETENDED AWAY. The bus drops a subscriber that falls behind rather than
// blocking the publisher, so after a drop the notifier does not know what it missed — and
// `device.updated` is already defined to re-evaluate the whole fleet, which is exactly the recovery.
//
// Without this, an overflow would silence reminders until the hourly tick with nothing saying so.
func TestADroppedSubscriptionRecoversByReEvaluatingEverything(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := bus.New()
	// UNBUFFERED-ish on purpose: the forwarder's own send blocks while nobody reads `out`, so its
	// bus buffer fills and the next publish drops it. That is the real condition, provoked rather
	// than simulated.
	out := make(chan wire.Envelope)
	go forwardEvents(ctx, quietLog(), b, out)
	waitForSubscriber(t, b, out)

	for i := 0; i < notifierBuffer+64; i++ {
		b.PublishEvent(wire.EventDeviceAttached, nil)
	}

	// Read until the synthetic re-evaluation arrives. Everything before it is the backlog that was
	// already buffered when the drop happened, which is not wrong to deliver.
	deadline := time.After(5 * time.Second)
	for {
		select {
		case env := <-out:
			if env.Type == wire.EventDeviceUpdated {
				return // the recovery — a full re-evaluation, from a stream that only carried attaches
			}
		case <-deadline:
			t.Fatal("a dropped subscription never produced a re-evaluation; reminders would stop " +
				"until the hourly tick with nothing saying why")
		}
	}
}

// waitForSubscriber blocks until the forwarder's subscription is registered, then leaves the channel
// quiet. Publishing before it subscribed would test nothing — the bus fans out to CURRENT
// subscribers, so those events simply vanish, and the test would pass or fail on goroutine start-up
// timing rather than on behaviour.
func waitForSubscriber(t *testing.T, b *bus.Bus, out <-chan wire.Envelope) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		b.PublishEvent(wire.EventDeviceUpdated, nil)
		select {
		case <-out:
			// It is listening. Drain the probes so the assertions below start from silence.
			for {
				select {
				case <-out:
				case <-time.After(100 * time.Millisecond):
					return
				}
			}
		case <-time.After(20 * time.Millisecond):
		case <-deadline:
			t.Fatal("the forwarder never subscribed")
		}
	}
}

func drain(t *testing.T, out <-chan wire.Envelope, n int) []string {
	t.Helper()
	got := make([]string, 0, n)
	for len(got) < n {
		select {
		case env := <-out:
			got = append(got, env.Type)
		case <-time.After(2 * time.Second):
			t.Fatalf("only %d of %d events arrived: %v", len(got), n, got)
		}
	}
	return got
}

// A NOTIFIER THAT DID NOT START MUST SAY WHICH DEPENDENCY WAS MISSING. This is asserted rather than
// trusted because the failure it guards is a SILENCE: an install whose notifier never started looks
// exactly like one where nothing was due, from the only place a user can see — their phone. The
// staging deploy that could not answer "is it live?" is what added these lines.
func TestStartNotifierSaysWhyItDidNotStart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		devices notify.Devices
		jobs    notify.Jobs
		deliver notify.Deliverer
		want    string
	}{
		{"demo mode", stubDevices{}, stubJobs{}, nil, "no push service"},
		{"no registry", nil, stubJobs{}, stubDeliverer{}, "no device registry"},
		{"no muxer", stubDevices{}, nil, stubDeliverer{}, "no backup engine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			log := slog.New(slog.NewTextHandler(&buf, nil))

			startNotifier(ctx, log, bus.New(), nil, nil, tc.devices, tc.jobs, tc.deliver)

			got := buf.String()
			if !strings.Contains(got, tc.want) {
				t.Errorf("the log does not name the missing dependency %q: %s", tc.want, got)
			}
			if !strings.Contains(got, "no notification will be sent") {
				t.Errorf("the log does not say what the consequence is: %s", got)
			}
		})
	}
}

type stubDevices struct{}

func (stubDevices) Devices() []wire.Device { return nil }

type stubJobs struct{}

func (stubJobs) RunningFor(string) bool { return false }

type stubDeliverer struct{}

func (stubDeliverer) DeliverDecision(context.Context, notify.Decision) error { return nil }
