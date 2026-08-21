package notify

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/wire"
)

func defaults() config.NotificationsConfig { return config.Default().Notifications }

func seenNow(t time.Time) wire.Transports {
	s := t.Format(time.RFC3339)
	return wire.Transports{WiFi: &s}
}

func device(name string, lastBackup *time.Time, now time.Time) wire.Device {
	// NotificationsEnabled TRUE, because that is what the registry serves for a device nobody has
	// muted — the wire zero value is `false`, so a fixture that omits it silences every test that
	// asserts a notification IS sent, for a reason no assertion mentions (quince#1270).
	d := wire.Device{UDID: "UDID-FIXTURE", Name: name, Transports: seenNow(now), NotificationsEnabled: true}
	if lastBackup != nil {
		d.LastBackup = &wire.LastBackup{At: lastBackup.Format(time.RFC3339), Status: "succeeded"}
	}
	return d
}

// qn.12 G1 — THE ROUTING IS TOTAL OVER THE ENGINE'S ERROR CODES.
//
// A code routed to nothing sends nothing: no error, no log line, and no symptom except a person who
// was never told. This asserts every code either produces a kind or is DELIBERATELY silent, and the
// deliberate set is spelled out here so adding to it is an edit somebody reviews rather than a
// default. `backup.AllErrorCodes()` is kept honest by its own test against the constant block, which
// is what makes this a live rule rather than a snapshot.
func TestEveryErrorCodeRoutesSomewhere(t *testing.T) {
	deliberatelySilent := map[string]string{
		backup.ErrCancelled: "the user did it, and they were holding the phone when they did",
	}
	codes := backup.AllErrorCodes()
	if len(codes) == 0 {
		t.Fatalf("AllErrorCodes() is empty; this gate would pass over nothing")
	}
	for _, code := range codes {
		kind, ok := KindForTerminal(backup.StateFailed, code)
		if reason, silent := deliberatelySilent[code]; silent {
			if ok {
				t.Errorf("%q now routes to %q but is listed as deliberately silent (%s) — "+
					"one of the two is wrong", code, kind, reason)
			}
			continue
		}
		if !ok {
			t.Errorf("error code %q routes to NO kind. A failure nobody is told about is worse "+
				"than a wrong notification: it has no symptom at all.", code)
		}
	}
}

// AND THE TWO FAILURE KINDS DIVIDE BY WHO CAN FIX IT. Routing storage-side failures to
// `action_required` is the defect quince#1124's fifth kind exists to close — it tells a user to go
// and unlock their phone when the disk is full.
func TestFailuresRouteByWhoCanFixThem(t *testing.T) {
	phone := []string{backup.ErrDeviceDisconnected, backup.ErrDeviceNotVisible,
		backup.ErrNotPaired, backup.ErrEncryptionRequired}
	quince := []string{backup.ErrDiskLow, backup.ErrVerifyFailed, backup.ErrCommitFailed,
		backup.ErrBackupFailed, backup.ErrInterrupted}

	for _, c := range phone {
		if k, _ := KindForTerminal(backup.StateFailed, c); k != KindActionRequired {
			t.Errorf("%q → %q, want action_required: the fix is on the phone", c, k)
		}
	}
	for _, c := range quince {
		if k, _ := KindForTerminal(backup.StateFailed, c); k != KindBackupFailed {
			t.Errorf("%q → %q, want backup_failed: nothing about the phone is wrong", c, k)
		}
	}
	// And a success is the fifth kind rather than a failure with an empty code.
	if k, ok := KindForTerminal(backup.StateSucceeded, ""); !ok || k != KindBackupCompleted {
		t.Errorf("a succeeded job → %q ok=%v, want backup_completed", k, ok)
	}
}

// A FAILURE MESSAGE MUST NOT SEND SOMEBODY TO THE WRONG PLACE. This is the whole value of the split,
// so it is asserted on the words and not only on the routing.
func TestAStorageFailureNeverTellsTheUserToTouchTheirPhone(t *testing.T) {
	dev := device("Kostya's iPhone", nil, time.Now())
	for _, code := range []string{backup.ErrDiskLow, backup.ErrVerifyFailed, backup.ErrCommitFailed} {
		d, ok := ForTerminal(dev, backup.StateFailed, code, defaults())
		if !ok {
			t.Fatalf("%q produced no decision", code)
		}
		for _, forbidden := range []string{"unlock", "Unlock", "nearby", "passcode"} {
			if strings.Contains(d.Body, forbidden) {
				t.Errorf("%q says %q — that is the phone's remedy for a storage problem:\n%s",
					code, forbidden, d.Body)
			}
		}
	}
}

// EVERY DECISION DEEP-LINKS TO THE DEVICE PAGE (contracts §1) AND NEVER NAMES A UDID.
//
// The second half is the privacy rule: a UDID is Operator-private and is meaningless to a reader, so
// a notification carrying one would be both a leak and useless. The NAVIGATE target contains it by
// necessity; the words must not.
func TestDecisionsDeepLinkAndNeverShowAUDID(t *testing.T) {
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	cases := []Decision{}
	if d, ok := ForTerminal(device("iPhone", nil, now), backup.StateFailed, backup.ErrDiskLow, defaults()); ok {
		cases = append(cases, d)
	}
	if d, ok := Evaluate(device("iPhone", &old, now), Reminder{}, defaults(), false, now); ok {
		cases = append(cases, d)
	}
	if d, ok := Evaluate(device("iPhone", nil, now), Reminder{}, defaults(), false, now); ok {
		cases = append(cases, d)
	}
	if len(cases) != 3 {
		t.Fatalf("expected three decisions to check, got %d", len(cases))
	}
	for _, d := range cases {
		if d.Navigate != "/devices/UDID-FIXTURE" {
			t.Errorf("%s does not deep-link to the device page: %q", d.Kind, d.Navigate)
		}
		if strings.Contains(d.Title, "UDID-FIXTURE") || strings.Contains(d.Body, "UDID-FIXTURE") {
			t.Errorf("%s puts the UDID in the words:\n%s\n%s", d.Kind, d.Title, d.Body)
		}
	}
}

// qn.12 G3 — ONE REMINDER PER LAPSE, DRIVEN BY A CLOCK WE CONTROL.
//
// The cooldown belongs to the TRACK, not to either kind on it. This is the structural fix for
// quince#1124's item 3: escalating from available to overdue must not produce a second push for one
// lapse, which is the double-notification that gets notifications turned off in week one.
func TestTheReminderTrackSendsOnePushPerLapse(t *testing.T) {
	cfg := defaults() // staleness 3, cooldown 24h, overdue 14
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	old := now.Add(-5 * 24 * time.Hour) // stale, not yet overdue
	dev := device("iPhone", &old, now)

	first, ok := Evaluate(dev, Reminder{}, cfg, false, now)
	if !ok || first.Kind != KindBackupAvailable {
		t.Fatalf("a stale, visible, idle device produced %q ok=%v, want backup_available", first.Kind, ok)
	}

	// INSIDE THE COOLDOWN: nothing, at either rank.
	sent := Reminder{LastSentAt: now}
	if _, ok := Evaluate(dev, sent, cfg, false, now.Add(23*time.Hour)); ok {
		t.Errorf("a second reminder went out inside the cooldown")
	}

	// THE ESCALATION. The device crosses overdue_days; the cooldown has elapsed, so exactly one push
	// goes out and it is the NEW rank — not a repeat of the old one, and not both.
	later := now.Add(20 * 24 * time.Hour)
	dev = device("iPhone", &old, later)
	esc, ok := Evaluate(dev, Reminder{LastSentAt: later.Add(-25 * time.Hour)}, cfg, false, later)
	if !ok {
		t.Fatalf("no reminder after the cooldown elapsed and the device went overdue")
	}
	if esc.Kind != KindBackupOverdue {
		t.Errorf("escalation produced %q, want backup_overdue", esc.Kind)
	}

	// AND THE ESCALATION ITSELF DOES NOT RESET THE TRACK: immediately after, still nothing.
	if _, ok := Evaluate(dev, Reminder{LastSentAt: later}, cfg, false, later.Add(time.Hour)); ok {
		t.Errorf("the rank change started a fresh cooldown — one lapse, two pushes")
	}
}

// A NEVER-BACKED-UP DEVICE IS INVITED, NOT REPROACHED. Its age is unbounded, so the naive rule would
// greet a phone paired ninety seconds ago with "overdue".
func TestANeverBackedUpDeviceIsInvited(t *testing.T) {
	now := time.Now()
	d, ok := Evaluate(device("iPhone", nil, now), Reminder{}, defaults(), false, now)
	if !ok {
		t.Fatalf("a visible, never-backed-up device produced no reminder")
	}
	if d.Kind != KindBackupAvailable {
		t.Errorf("kind = %q, want backup_available — the first backup is an invitation", d.Kind)
	}
	if strings.Contains(strings.ToLower(d.Title+d.Body), "overdue") {
		t.Errorf("a device with no backups is called overdue:\n%s\n%s", d.Title, d.Body)
	}
}

// THE THREE SUPPRESSIONS, each for its own reason, asserted separately so one cannot silently cover
// for another.
func TestNoReminderWhenItWouldAskForTheImpossible(t *testing.T) {
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	cfg := defaults()

	// Not on the network: a reminder asks for something that cannot be done.
	away := device("iPhone", &old, now)
	away.Transports = wire.Transports{}
	if _, ok := Evaluate(away, Reminder{}, cfg, false, now); ok {
		t.Errorf("reminded about a device that is not on the network")
	}
	// A job is already running: the thing being asked for is happening.
	if _, ok := Evaluate(device("iPhone", &old, now), Reminder{}, cfg, true, now); ok {
		t.Errorf("reminded while a backup was already running")
	}
	// Fresh: nothing is owed.
	fresh := now.Add(-2 * time.Hour)
	if _, ok := Evaluate(device("iPhone", &fresh, now), Reminder{}, cfg, false, now); ok {
		t.Errorf("reminded about a device backed up two hours ago")
	}
}

// AN UNPARSEABLE TIMESTAMP IS NOT AN INVITATION TO GUESS. Treating it as "very old" would notify on
// every evaluation, forever, about a device whose data quince cannot read.
func TestAnUnreadableLastBackupProducesNothing(t *testing.T) {
	now := time.Now()
	dev := device("iPhone", nil, now)
	dev.LastBackup = &wire.LastBackup{At: "not a timestamp", Status: "succeeded"}
	if _, ok := Evaluate(dev, Reminder{}, defaults(), false, now); ok {
		t.Errorf("a device with an unreadable last-backup time was reminded about")
	}
}

// A CATEGORY THE USER TURNED OFF SUPPRESSES ITS OWN KIND AND NOTHING ELSE.
func TestACategorySwitchSuppressesOnlyItsOwnKind(t *testing.T) {
	now := time.Now()
	old := now.Add(-30 * 24 * time.Hour)
	cfg := defaults()
	cfg.BackupOverdue = false

	if _, ok := Evaluate(device("iPhone", &old, now), Reminder{}, cfg, false, now); ok {
		t.Errorf("backup_overdue was off and a reminder still went out")
	}
	// The other kinds are untouched.
	if _, ok := ForTerminal(device("iPhone", nil, now), backup.StateFailed, backup.ErrDiskLow, cfg); !ok {
		t.Errorf("turning off backup_overdue also suppressed backup_failed")
	}
	// And backup_completed is OFF by default, which is the ruling's one non-obvious default.
	if _, ok := ForTerminal(device("iPhone", nil, now), backup.StateSucceeded, "", defaults()); ok {
		t.Errorf("a successful backup notified by default; the ruling says backup_completed is OFF")
	}
}

// --- the per-device notifications switch, quince#1270, MOVED BY qn.13 slice 10b ---

// THE DEVICE SWITCH IS NO LONGER ASKED IN THIS PACKAGE, and these tests record where it went.
//
// Operator ruling, 2026-08-21: with two principals there is no single answer to *should this go
// out*. The admin muting a device says nothing about its scoped holder — and deciding here produced
// NO decision at all, so the send-path filter never ran and the holder received nothing. The
// suppression moved to `pushsvc`, per subscription owner.
//
// WHAT WAS LOST AND IS DECLARED RATHER THAN QUIETLY DROPPED: precedence used to be testable in ONE
// place, because both gates sat here. It is now AND across two layers — category at the decision,
// device at the send — and no single test sees both. `TestCategoryPrecedenceSurvivesTheMove` below
// covers this half; `pushsvc.TestAMutedOwnerReceivesNothing` covers the other.

// THE CATEGORY GATE STAYS HERE, because it is quince-wide and has no owner. Half of the old
// precedence table, and the half this package can still answer.
func TestCategoryPrecedenceSurvivesTheMove(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	last := now.Add(-40 * 24 * time.Hour) // well past overdue_days
	for _, tc := range []struct {
		name       string
		categoryOn bool
		want       bool
	}{
		{"category on", true, true},
		{"category off", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaults()
			cfg.BackupOverdue = tc.categoryOn
			_, got := Evaluate(device("phone", &last, now), Reminder{}, cfg, false, now)
			if got != tc.want {
				t.Fatalf("Evaluate sent=%v, want %v (category=%v)", got, tc.want, tc.categoryOn)
			}
		})
	}
}

// A MUTED DEVICE NOW PRODUCES A DECISION, which is the change and the thing most likely to look
// like a regression to a reader who knew the old behaviour.
//
// Nothing is delivered — `pushsvc` drops it for every owner who muted the device — but the decision
// exists, so a scoped holder who did NOT mute it still receives.
func TestAMutedDeviceStillProducesADecision(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	drawer := device("drawer-iphone", nil, now)
	drawer.NotificationsEnabled = false // the admin's mute, and it no longer gates here

	d, send := Evaluate(drawer, Reminder{}, defaults(), false, now)
	if !send {
		t.Fatal("a muted device produced no decision — the admin's mute is still gating the " +
			"decision point, which denies a scoped holder reminders about their own phone")
	}
	if d.Kind != KindBackupAvailable {
		t.Fatalf("kind %q, want %q", d.Kind, KindBackupAvailable)
	}
}

// THE COOLDOWN IS SPENT ON A MUTED DEVICE, which is the cost of the move. Asserted through the
// RUNNER rather than restated at `Evaluate`, because the track advance happens in `send` and a test
// that only checks `send == true` proves the same thing as the test above it while carrying a name
// that claims the half it does not reach (quince#1409 review, finding 6).
//
// `push_reminders` advances when a decision is produced, so a device muted by every principal still
// consumes its track. Nothing is delivered — the send loop drops it per owner — so this is a wasted
// evaluation rather than a wrong notification. A reader wondering why the ledger moved finds it here.
type stubDevices struct{ list []wire.Device }

func (s stubDevices) Devices() []wire.Device { return s.list }

type stubJobs struct{ running bool }

func (s stubJobs) RunningFor(string) bool { return s.running }

type stubReminders struct {
	at      time.Time
	setUDID string
	setAt   time.Time
	sets    int
}

func (s *stubReminders) PushReminder(string) (time.Time, bool, error) {
	return s.at, !s.at.IsZero(), nil
}

func (s *stubReminders) SetPushReminder(udid string, at time.Time) error {
	s.sets, s.setUDID, s.setAt = s.sets+1, udid, at
	return nil
}

func (s *stubReminders) ClearPushReminder(string) error { return nil }

type stubDeliverer struct{ delivered int }

func (s *stubDeliverer) DeliverDecision(context.Context, Decision) error {
	s.delivered++
	return nil
}

func TestAMutedDeviceStillAdvancesTheReminderTrack(t *testing.T) {
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	muted := device("drawer-iphone", nil, now)
	muted.NotificationsEnabled = false

	rem := &stubReminders{}
	del := &stubDeliverer{}
	r := &Runner{
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Devices:   stubDevices{list: []wire.Device{muted}},
		Jobs:      stubJobs{},
		Reminders: rem,
		Deliver:   del,
		Config:    func() config.NotificationsConfig { return defaults() },
		Now:       func() time.Time { return now },
	}
	r.evaluateAll(context.Background())

	if rem.sets != 1 {
		t.Fatalf("SetPushReminder called %d times, want 1 — a muted device must still spend its track", rem.sets)
	}
	if rem.setUDID != muted.UDID {
		t.Fatalf("the track advanced for %q, want %q", rem.setUDID, muted.UDID)
	}
	if !rem.setAt.Equal(now) {
		t.Fatalf("the track recorded %v, want %v", rem.setAt, now)
	}
	// THE CONTROL. An unmuted device must behave identically here — the mute is no longer read at
	// this layer at all, so if these two ever differ the gate has crept back to the decision point.
	unmuted := device("kitchen-ipad", nil, now)
	unmuted.NotificationsEnabled = true
	rem2, del2 := &stubReminders{}, &stubDeliverer{}
	r.Devices, r.Reminders, r.Deliver = stubDevices{list: []wire.Device{unmuted}}, rem2, del2
	r.evaluateAll(context.Background())

	if rem2.sets != rem.sets || del2.delivered != del.delivered {
		t.Fatalf("muted and unmuted differ at this layer: muted sets=%d delivered=%d, unmuted sets=%d delivered=%d",
			rem.sets, del.delivered, rem2.sets, del2.delivered)
	}
}
