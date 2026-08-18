package notify

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/wire"
)

type fakeDevices struct{ list []wire.Device }

func (f *fakeDevices) Devices() []wire.Device { return f.list }

type fakeJobs struct{ running map[string]bool }

func (f *fakeJobs) RunningFor(udid string) bool { return f.running[udid] }

type fakeReminders struct {
	at      map[string]time.Time
	cleared []string
	setErr  error
}

func (f *fakeReminders) PushReminder(udid string) (time.Time, bool, error) {
	t, ok := f.at[udid]
	return t, ok, nil
}
func (f *fakeReminders) SetPushReminder(udid string, at time.Time) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.at == nil {
		f.at = map[string]time.Time{}
	}
	f.at[udid] = at
	return nil
}
func (f *fakeReminders) ClearPushReminder(udid string) error {
	f.cleared = append(f.cleared, udid)
	delete(f.at, udid)
	return nil
}

type fakeDeliverer struct {
	sent []Decision
	err  error
}

func (f *fakeDeliverer) DeliverDecision(_ context.Context, d Decision) error {
	f.sent = append(f.sent, d)
	return f.err
}

func runner(t *testing.T, devs []wire.Device, now time.Time) (*Runner, *fakeDeliverer, *fakeReminders) {
	t.Helper()
	del := &fakeDeliverer{}
	rem := &fakeReminders{at: map[string]time.Time{}}
	return &Runner{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Devices:   &fakeDevices{list: devs},
		Jobs:      &fakeJobs{running: map[string]bool{}},
		Reminders: rem,
		Deliver:   del,
		Config:    func() config.NotificationsConfig { return config.Default().Notifications },
		Now:       func() time.Time { return now },
	}, del, rem
}

func staleDevice(udid string, days int, now time.Time) wire.Device {
	seen := now.Format(time.RFC3339)
	at := now.Add(-time.Duration(days) * 24 * time.Hour).Format(time.RFC3339)
	return wire.Device{
		UDID: udid, Name: "iPhone",
		Transports: wire.Transports{WiFi: &seen},
		LastBackup: &wire.LastBackup{At: at, Status: "succeeded"},
	}
}

// A DEVICE APPEARING ON THE NETWORK IS THE OPPORTUNITY SIGNAL. This is the whole rung in one test:
// quince notices, decides, and tells somebody — driven from visibility it already has rather than
// from an artifact anyone must install.
func TestADeviceEventProducesAReminder(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r, del, rem := runner(t, []wire.Device{staleDevice("U1", 5, now)}, now)

	r.handle(context.Background(), wire.Envelope{Type: wire.EventDeviceUpdated})

	if len(del.sent) != 1 || del.sent[0].Kind != KindBackupAvailable {
		t.Fatalf("no reminder from a device event: %+v", del.sent)
	}
	// AND THE TRACK IS RECORDED, which is what makes the cooldown mean anything.
	if _, ok, _ := rem.PushReminder("U1"); !ok {
		t.Errorf("the reminder was sent and not recorded; the next evaluation would send again")
	}
}

// THE COOLDOWN HOLDS ACROSS TRIGGERS. A device event arriving a minute after a reminder must not
// produce a second one — which is what a phone reconnecting repeatedly on a flaky network does.
func TestASecondEventInsideTheCooldownSendsNothing(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r, del, rem := runner(t, []wire.Device{staleDevice("U1", 5, now)}, now)
	rem.at["U1"] = now.Add(-time.Minute)

	r.handle(context.Background(), wire.Envelope{Type: wire.EventDeviceUpdated})
	if len(del.sent) != 0 {
		t.Errorf("a reminder went out inside the cooldown: %+v", del.sent)
	}
}

// THE TRACK IS RECORDED EVEN WHEN DELIVERY FAILED, and this is the least obvious decision in the
// runner. Recording only on success retries every evaluation against a push service that is down —
// a notification storm the moment it recovers. A missed reminder costs one cycle; the storm costs
// the user's trust in notifications entirely.
func TestAFailedDeliveryStillRecordsTheTrack(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r, del, rem := runner(t, []wire.Device{staleDevice("U1", 5, now)}, now)
	del.err = context.DeadlineExceeded

	r.evaluateAll(context.Background())

	if len(del.sent) != 1 {
		t.Fatalf("nothing was attempted: %+v", del.sent)
	}
	if _, ok, _ := rem.PushReminder("U1"); !ok {
		t.Errorf("a failed delivery left the track empty — every evaluation would retry")
	}
}

// A SUCCESSFUL BACKUP CLEARS THE TRACK. Without it a device that was reminded, backed up, and went
// stale again would wait out the remainder of a cooldown belonging to a lapse that is over.
func TestASuccessfulBackupClearsTheReminderTrack(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r, _, rem := runner(t, []wire.Device{staleDevice("U1", 5, now)}, now)
	rem.at["U1"] = now.Add(-2 * time.Hour)

	r.handle(context.Background(), wire.Envelope{
		Type: wire.EventJobUpdated,
		Data: wire.Job{UDID: "U1", State: backup.StateSucceeded},
	})

	if len(rem.cleared) != 1 || rem.cleared[0] != "U1" {
		t.Errorf("a successful backup did not clear the track: %+v", rem.cleared)
	}
}

// A FAILURE NOTIFIES AND DOES *NOT* TOUCH THE TRACK. It is an event downstream of something the user
// did; recording it would silence the next genuine reminder for a day.
func TestAFailureNotifiesWithoutConsumingTheCooldown(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r, del, rem := runner(t, []wire.Device{staleDevice("U1", 5, now)}, now)

	r.handle(context.Background(), wire.Envelope{
		Type: wire.EventJobUpdated,
		Data: wire.Job{UDID: "U1", State: backup.StateFailed,
			Error: &wire.JobError{Code: backup.ErrDiskLow}},
	})

	if len(del.sent) != 1 || del.sent[0].Kind != KindBackupFailed {
		t.Fatalf("a disk-full failure did not produce backup_failed: %+v", del.sent)
	}
	if _, ok, _ := rem.PushReminder("U1"); ok {
		t.Errorf("a failure consumed the reminder cooldown")
	}
}

// A RUNNING JOB IS NOT SOMETHING TO REMIND ABOUT — the thing being asked for is happening.
func TestNoReminderWhileABackupIsRunning(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r, del, _ := runner(t, []wire.Device{staleDevice("U1", 5, now)}, now)
	r.Jobs = &fakeJobs{running: map[string]bool{"U1": true}}

	r.evaluateAll(context.Background())
	if len(del.sent) != 0 {
		t.Errorf("reminded about a device that is backing up right now: %+v", del.sent)
	}
}

// AN UNREADABLE EVENT PAYLOAD IS LOGGED, NOT GUESSED AT. A silent decode failure here presents as
// "notifications stopped working" with nothing anywhere saying why.
func TestAnUnreadableJobEventSendsNothingAndDoesNotPanic(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r, del, _ := runner(t, []wire.Device{staleDevice("U1", 5, now)}, now)

	r.handle(context.Background(), wire.Envelope{Type: wire.EventJobUpdated, Data: "not a job"})
	if len(del.sent) != 0 {
		t.Errorf("an unreadable payload produced a notification: %+v", del.sent)
	}
}

// A NON-TERMINAL JOB SAYS NOTHING. `job.updated` fires on every phase change, and a notification per
// phase is the noise that gets notifications turned off in an afternoon.
func TestAJobInProgressSendsNothing(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r, del, _ := runner(t, []wire.Device{staleDevice("U1", 5, now)}, now)

	for _, state := range []string{"queued", "preflight", "backing_up", "verifying", "committing"} {
		r.handle(context.Background(), wire.Envelope{
			Type: wire.EventJobUpdated,
			Data: wire.Job{UDID: "U1", State: state},
		})
	}
	if len(del.sent) != 0 {
		t.Errorf("in-progress phases produced %d notifications: %+v", len(del.sent), del.sent)
	}
}

// THE LOOP EXITS ON CONTEXT CANCEL. A runner that outlives its daemon holds a store handle open and
// keeps notifying after shutdown has begun.
func TestRunExitsWhenTheContextIsCancelled(t *testing.T) {
	now := time.Now()
	r, _, _ := runner(t, nil, now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx, make(chan wire.Envelope)); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("Run did not return after its context was cancelled")
	}
}

// `device.attached` IS THE OPPORTUNITY SIGNAL, AND THE RUNNER LISTENED FOR THE WRONG EVENT (found by
// wiring the runner into the daemon, quince#1124).
//
// The registry publishes `device.attached` when a phone appears on the network, and `device.updated`
// only when enrichment CHANGED something or a backup was announced. A phone that reconnects to Wi-Fi
// with the same name, pairing and encryption setting — a phone's daily behaviour — emits the first
// and never the second, so the whole assisted model silently degraded to the hourly tick.
//
// This is the defect's own regression test: it fails against the runner as merged.
func TestAnAttachedDeviceProducesAReminder(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r, del, _ := runner(t, []wire.Device{staleDevice("U1", 5, now)}, now)

	r.handle(context.Background(), wire.Envelope{Type: wire.EventDeviceAttached})

	if len(del.sent) != 1 || del.sent[0].Kind != KindBackupAvailable {
		t.Fatalf("a phone appearing on the network produced no reminder: %+v", del.sent)
	}
}

// THE COOLDOWN DOES NOT CARE WHICH DEVICE EVENT WOKE IT. An attach and an update are two names for
// the same chance to ask, so a phone that emits both — which happens whenever enrichment does change
// something — must still produce exactly one notification.
func TestAnAttachFollowedByAnUpdateSendsOnce(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	r, del, _ := runner(t, []wire.Device{staleDevice("U1", 5, now)}, now)

	r.handle(context.Background(), wire.Envelope{Type: wire.EventDeviceAttached})
	r.handle(context.Background(), wire.Envelope{Type: wire.EventDeviceUpdated})

	if len(del.sent) != 1 {
		t.Errorf("one phone appearing produced %d notifications: %+v", len(del.sent), del.sent)
	}
}
