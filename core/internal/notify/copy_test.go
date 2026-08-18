package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/wire"
)

// A REALISTIC DEVICE NAME, because the defect only appears with one. iOS device names are the
// owner's name plus the model by default, so they are long — this is the length that truncated on a
// real lock screen.
const longDeviceName = "someones-iphone-15-pro"

// EVERY TITLE MUST LEAD WITH THE STATE, NOT THE DEVICE NAME (Operator-reported 2026-08-18).
//
// The lock screen showed *"<device-name> could not be ba…"*: the name survived and the news was
// cut off. The name is what the reader already knows; the state is why the notification exists.
//
// ASSERTED OVER EVERY DECISION THIS PACKAGE CAN PRODUCE, not over a list of strings. The bug was in
// all six titles at once — it was a habit, not a typo — so the test has to be the shape that catches
// the seventh.
func TestNoTitleLeadsWithTheDeviceName(t *testing.T) {
	for _, d := range everyDecision(t) {
		if strings.HasPrefix(d.Title, longDeviceName) {
			t.Errorf("kind %q leads with the device name: %q — truncation would cut the state",
				d.Kind, d.Title)
		}
	}
}

// APPLE ASKS FOR UNDER 50 CHARACTERS, and iOS spends some of that itself: a Home Screen web app's
// notification renders as `<title> from <app name>`, so the app name shares the line.
func TestTitlesFitALockScreen(t *testing.T) {
	for _, d := range everyDecision(t) {
		if len([]rune(d.Title)) > 50 {
			t.Errorf("kind %q title is %d characters, over Apple's 50: %q",
				d.Kind, len([]rune(d.Title)), d.Title)
		}
	}
}

// THE BODY MUST NOT OPEN WITH THE APP NAME. Apple: the system already shows the app icon — and the
// opening words are the ones that survive truncation, so spending them on branding is the same
// mistake as leading with the device name.
func TestNoBodyOpensWithTheAppName(t *testing.T) {
	for _, d := range everyDecision(t) {
		if strings.HasPrefix(strings.ToLower(d.Body), "quince") {
			t.Errorf("kind %q body opens with the app name: %q", d.Kind, d.Body)
		}
		if len([]rune(d.Body)) > 150 {
			t.Errorf("kind %q body is %d characters, over Apple's 150: %q",
				d.Kind, len([]rune(d.Body)), d.Body)
		}
	}
}

// "TAP" IS WRONG ON A MAC, and these go to every subscribed device. It is one word, and it is the
// difference between an instruction and a puzzle for whoever is reading it on a laptop.
func TestNoCopyTellsALaptopUserToTap(t *testing.T) {
	for _, d := range everyDecision(t) {
		if strings.Contains(strings.ToLower(d.Title+" "+d.Body), "tap") {
			t.Errorf("kind %q says \"tap\", which a Mac cannot do: %q / %q", d.Kind, d.Title, d.Body)
		}
	}
}

// A TITLE ENDS WITHOUT PUNCTUATION (HIG), and an empty one is dropped by the user agent with nothing
// recorded anywhere — which is why `push.MarshalPayload` refuses it and why this checks here too.
func TestTitlesAreWellFormed(t *testing.T) {
	for _, d := range everyDecision(t) {
		if strings.TrimSpace(d.Title) == "" {
			t.Errorf("kind %q has an empty title; the user agent would drop the whole message", d.Kind)
		}
		if strings.HasSuffix(d.Title, ".") {
			t.Errorf("kind %q title ends with a full stop: %q", d.Kind, d.Title)
		}
	}
}

// everyDecision produces one Decision per kind per error code — the whole copy surface this package
// can put on a lock screen.
func everyDecision(t *testing.T) []Decision {
	t.Helper()
	cfg := config.Default().Notifications
	now := time.Date(2026, 8, 18, 12, 0, 0, 0, time.UTC)
	var out []Decision

	// The three terminal kinds, across every error code the engine defines — `AllErrorCodes` is what
	// makes this total rather than a list somebody has to remember to extend.
	for _, state := range []string{backup.StateSucceeded, backup.StateFailed, backup.StateConnectionLost} {
		for _, code := range append(backup.AllErrorCodes(), "") {
			on := cfg
			on.BackupCompleted = true
			if d, ok := ForTerminal(namedDevice(now, 5), state, code, on); ok {
				out = append(out, d)
			}
		}
	}

	// The reminder ranks: never backed up, ordinarily stale, and overdue.
	never := namedDevice(now, 0)
	never.LastBackup = nil
	for _, dev := range []wire.Device{never, namedDevice(now, 5), namedDevice(now, 40)} {
		if d, ok := Evaluate(dev, Reminder{}, cfg, false, now); ok {
			out = append(out, d)
		}
	}

	if len(out) == 0 {
		t.Fatal("no decisions were produced, so this suite asserts nothing")
	}
	return out
}

func namedDevice(now time.Time, staleDays int) wire.Device {
	seen := now.Format(time.RFC3339)
	at := now.Add(-time.Duration(staleDays) * 24 * time.Hour).Format(time.RFC3339)
	return wire.Device{
		UDID: "UDID-FIXTURE", Name: longDeviceName,
		Transports: wire.Transports{WiFi: &seen},
		LastBackup: &wire.LastBackup{At: at, Status: "succeeded"},
	}
}
