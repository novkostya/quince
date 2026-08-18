// Package notify decides WHAT to tell a person and WHEN. It sends nothing.
//
// The split is deliberate and it is what makes this package testable without a network, a browser or
// a phone: delivery is `core/internal/push` plus the subscription store, and the seam between them
// is `Decision` — a kind, a device, and the words. Everything here is a pure function of the
// device's state, the config and a clock.
//
// `qn.12`, spec D4 and D5.
package notify

import (
	"fmt"
	"time"

	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/wire"
)

// Kind is one of the five push kinds frozen in contracts §1.
type Kind string

const (
	// The reminder track's two ranks — see Evaluate. At most ONE of these is outstanding per device.
	KindBackupAvailable Kind = "backup_available"
	KindBackupOverdue   Kind = "backup_overdue"

	// The two failure kinds, split by WHO CAN FIX IT AND WHERE THEY MUST BE STANDING.
	// `action_required` is the phone; `backup_failed` is quince.
	KindActionRequired Kind = "action_required"
	KindBackupFailed   Kind = "backup_failed"

	KindBackupCompleted Kind = "backup_completed"
)

// Decision is one notification to send. It carries the finished words rather than a template id,
// because the words are a product decision and belong beside the routing that chose them.
type Decision struct {
	Kind  Kind
	UDID  string
	Title string
	Body  string
	// Navigate is always the device page — every kind deep-links there (contracts §1), so a
	// notification can never be a dead end.
	Navigate string
}

// KindForTerminal routes a finished job to a push kind, or reports that it sends nothing.
//
// TOTAL OVER `backup.AllErrorCodes()`, and there is a gate for it. A code routed to nothing sends
// nothing — no error, no log line, and no symptom except a person who was never told — so an
// eleventh code must fail a test rather than fail quietly.
//
// THE LINE IS WHO CAN FIX IT AND WHERE THEY MUST BE STANDING, which is quince#1124's addition of a
// fifth kind: the frozen four routed EVERY failure to `action_required`, which fits the ASSISTED
// model's own failures — passcode not entered, device locked, confirm not tapped — and tells a user
// to go and unlock their phone when the disk is full.
func KindForTerminal(state, errorCode string) (Kind, bool) {
	if state == backup.StateSucceeded {
		return KindBackupCompleted, true
	}
	switch errorCode {
	// THE PHONE IS THE FIX. Every one of these is cleared by picking the device up.
	//
	// `device_disconnected` → `action_required` is CANON'S CHOICE, not this table's: roadmap M8's
	// gate reads *"a mid-backup disconnect produces an `action_required` push and a one-tap retry
	// works"*, and a mid-backup Wi-Fi drop terminates as `connection_lost` / `device_disconnected`.
	case backup.ErrDeviceDisconnected, backup.ErrDeviceNotVisible,
		backup.ErrNotPaired, backup.ErrEncryptionRequired:
		return KindActionRequired, true

	// QUINCE IS THE FIX. Nothing about the phone is wrong and going to it would be going to the
	// wrong place.
	//
	// `interrupted` IS THE LEAST OBVIOUS ROW and it is this spec's call rather than canon's: it means
	// quince itself restarted (`Reconcile` flips crash-orphaned jobs to `connection_lost` with this
	// code). The remedy is one tap in quince, which is what `backup_failed` means here.
	case backup.ErrDiskLow, backup.ErrVerifyFailed, backup.ErrCommitFailed,
		backup.ErrBackupFailed, backup.ErrInterrupted:
		return KindBackupFailed, true

	// THE USER DID IT, AND THEY WERE HOLDING THE PHONE WHEN THEY DID. A notification here tells
	// somebody what they already know, one second after they chose it.
	case backup.ErrCancelled:
		return "", false
	}
	// AN UNROUTED CODE IS A BUG THIS FUNCTION CANNOT FIX, and it must not pretend otherwise: it
	// reports "nothing to send" and the gate over `AllErrorCodes` is what stops one existing.
	return "", false
}

// ForTerminal builds the whole decision for a finished job, or reports that nothing is sent.
func ForTerminal(dev wire.Device, state, errorCode string, cfg config.NotificationsConfig) (Decision, bool) {
	kind, ok := KindForTerminal(state, errorCode)
	if !ok || !Enabled(kind, cfg) {
		return Decision{}, false
	}
	name := deviceName(dev)
	d := Decision{Kind: kind, UDID: dev.UDID, Navigate: "/devices/" + dev.UDID}
	switch kind {
	case KindBackupCompleted:
		d.Title = "Backup Complete"
		d.Body = name + " is backed up and verified."
	case KindActionRequired:
		d.Title = "Action Needed"
		d.Body = name + " — " + actionRequiredBody(errorCode)
	case KindBackupFailed:
		d.Title = "Backup Failed"
		d.Body = name + " — " + backupFailedBody(errorCode)
	}
	return d, true
}

// NOTIFICATION COPY RULES, from Apple's HIG and from a lock screen (Operator-reported 2026-08-18).
//
// THE TITLE IS THE STATE. THE DEVICE NAME IS THE BODY'S FIRST WORDS. Apple asks for *"brief titles
// that people can read at a glance, especially on Apple Watch, where space is limited"* — and a
// device name is neither brief nor bounded: iOS names a phone after its owner by default, so the
// title's length is set by a string quince does not choose.
//
// Two earlier shapes were both wrong. `<device> could not be backed up` truncated to
// *"<device-name> could not be ba…"*, keeping the part the reader already knows and cutting the news.
// `Backup failed — <device>` fixed the ORDER and kept the unbounded tail, so a long name still
// pushed the title past the line. **A fixed title cannot truncate at all**, and the name is then in
// the body, where Apple's budget is 150 characters rather than 50.
//
// It also costs less to the suffix iOS adds: a Home Screen web app renders as `<title> from <app
// name>`, which is redundant and not removable — Declarative Web Push requires a title, so there is
// no "let the system show the app name" option here. A short title is what makes it cheap.
//
// TITLE-STYLE CAPITALIZATION AND NO ENDING PUNCTUATION, which is the HIG's wording and is followed
// here. Bodies stay sentence case with full stops, which is the same guidance's other half —
// *"complete sentences, sentence case, and proper punctuation"* — so the product's voice lives in
// the body where the sentences are.
//
// THE BODY NEVER OPENS WITH "quince". Apple: *"avoid including your app name … the system displays
// your app icon automatically"* — and it is worse than redundant, because the first ~40 characters
// are the ones most consistently visible. Naming quince mid-sentence as a place to go is fine.
//
// "OPEN QUINCE", NOT "TAP". These go to every subscribed device, and a Mac does not tap.

// actionRequiredBody says what to do on the PHONE, because that is what this kind means.
func actionRequiredBody(errorCode string) string {
	switch errorCode {
	case backup.ErrNotPaired:
		return "it is no longer paired. Connect it, and trust this computer again."
	case backup.ErrEncryptionRequired:
		return "backup encryption is off. Turn it on, then start the backup again."
	default:
		// `device_disconnected` and `device_not_visible`. Deliberately one sentence for both: from
		// the user's side they are the same act — the phone left the network — and quince cannot
		// tell them which without saying something it does not know.
		return "it went off the network. Unlock it and keep it nearby, then try again."
	}
}

// backupFailedBody says what is wrong with QUINCE's side, and never sends anyone to their phone.
func backupFailedBody(errorCode string) string {
	switch errorCode {
	case backup.ErrDiskLow:
		return "the backup storage is nearly full. Free some space, then try again."
	case backup.ErrVerifyFailed:
		return "the backup did not verify, so it was not kept. Open quince for the details."
	case backup.ErrCommitFailed:
		return "the backup could not be saved. The transferred data was kept — open quince for the details."
	case backup.ErrInterrupted:
		return "a restart interrupted the backup. Open quince to start it again."
	default:
		return "the backup did not finish. Open quince for the details."
	}
}

// Enabled reports whether a kind's per-category switch is on (spec D9).
//
// A SWITCH IS NOT A SILENT CAP. Turning a category off is the user's own instruction, which is what
// makes suppression here honest where a suppression they did not ask for would not be — and the
// status surface reports `category_off` as its own state rather than saying nothing arrived.
func Enabled(kind Kind, cfg config.NotificationsConfig) bool {
	switch kind {
	case KindBackupAvailable:
		return cfg.BackupAvailable
	case KindBackupOverdue:
		return cfg.BackupOverdue
	case KindActionRequired:
		return cfg.ActionRequired
	case KindBackupFailed:
		return cfg.BackupFailed
	case KindBackupCompleted:
		return cfg.BackupCompleted
	}
	return false
}

// Reminder is a device's place on the reminder track — the only state this package keeps.
type Reminder struct {
	// LastSentAt is when this device was last reminded, at EITHER rank. Zero means never.
	LastSentAt time.Time
}

// Evaluate decides whether a device is owed a reminder right now, and at which rank.
//
// ONE TRACK, TWO RANKS — spec D5, and this is the structural answer to quince#1124's item 3.
// `backup_available` and `backup_overdue` describe the same lapse at two severities, so they are one
// reminder with a rank rather than two reminders that would both fire. **The cooldown belongs to the
// track**: escalating from rank 1 to rank 2 does not reset it and cannot produce a second push for
// one lapse, which is the double-notification that gets notifications turned off in week one.
func Evaluate(dev wire.Device, r Reminder, cfg config.NotificationsConfig, jobRunning bool, now time.Time) (Decision, bool) {
	// VISIBLE, because a reminder for a phone that is not on the network asks for something that
	// cannot be done. Presence is muxd-event-driven (design §3); either transport counts.
	if dev.Transports.USB == nil && dev.Transports.WiFi == nil {
		return Decision{}, false
	}
	if jobRunning {
		return Decision{}, false
	}
	if !r.LastSentAt.IsZero() &&
		now.Sub(r.LastSentAt) < time.Duration(cfg.ReminderCooldownHours)*time.Hour {
		return Decision{}, false
	}

	name := deviceName(dev)
	nav := "/devices/" + dev.UDID

	// A DEVICE THAT HAS NEVER BEEN BACKED UP IS INVITED, NEVER REPROACHED. Its age is unbounded, so
	// the naive rule would greet a phone paired ninety seconds ago with "overdue".
	if dev.LastBackup == nil {
		if !Enabled(KindBackupAvailable, cfg) {
			return Decision{}, false
		}
		return Decision{
			Kind: KindBackupAvailable, UDID: dev.UDID, Navigate: nav,
			Title: "Never Backed Up",
			Body:  name + " has never been backed up. It is on the network now — open quince to start.",
		}, true
	}

	at, err := time.Parse(time.RFC3339, dev.LastBackup.At)
	if err != nil {
		// AN UNPARSEABLE TIMESTAMP IS NOT AN INVITATION TO GUESS. Treating it as "very old" would
		// notify on every tick for a device whose data quince cannot read.
		return Decision{}, false
	}
	age := now.Sub(at)
	if age < time.Duration(cfg.StalenessDays)*24*time.Hour {
		return Decision{}, false
	}

	days := int(age.Hours() / 24)
	if age >= time.Duration(cfg.OverdueDays)*24*time.Hour {
		if !Enabled(KindBackupOverdue, cfg) {
			return Decision{}, false
		}
		return Decision{
			Kind: KindBackupOverdue, UDID: dev.UDID, Navigate: nav,
			Title: "Backup Overdue",
			Body:  name + " has not been backed up in " + plural(days, "day") + ". It is on the network now — open quince to start.",
		}, true
	}
	if !Enabled(KindBackupAvailable, cfg) {
		return Decision{}, false
	}
	return Decision{
		Kind: KindBackupAvailable, UDID: dev.UDID, Navigate: nav,
		Title: "Backup Due",
		Body:  name + " was last backed up " + plural(days, "day") + " ago. It is on the network now — open quince to start.",
	}, true
}

// deviceName is what a person calls the device, falling back to something they can still act on.
//
// A UDID IS NEVER SHOWN. It is Operator-private by canon's privacy rule and it is meaningless to a
// reader besides; a notification naming one would be both a leak and useless.
func deviceName(dev wire.Device) string {
	if dev.Name != "" {
		return dev.Name
	}
	if dev.Model != "" {
		return dev.Model
	}
	return "Your device"
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
