package notify

import (
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/wire"
)

// A REALISTIC DEVICE NAME, because the defect only appears with one. iOS device names are the
// owner's name plus the model by default, so they are long — this is the length that truncated on a
// real lock screen.
const longDeviceName = "someones-iphone-15-pro"

// THE TITLE IS THE STATE AND CARRIES NO DEVICE NAME (Operator-reported 2026-08-18, twice).
//
// First the titles read `<device> could not be backed up`, which truncated to
// *"<device-name> could not be ba…"* — the news cut, the known part kept. Then `Backup failed —
// <device>`, which fixed the order and kept an UNBOUNDED tail: iOS names a phone after its owner, so
// the title's length was still set by a string quince does not choose.
//
// A fixed title cannot truncate at all. Apple asks for *"brief titles that people can read at a
// glance, especially on Apple Watch"*, and this is what satisfies it.
func TestNoTitleCarriesTheDeviceName(t *testing.T) {
	for _, d := range everyDecision(t) {
		if strings.Contains(d.Title, longDeviceName) {
			t.Errorf("kind %q puts the device name in the title: %q — put it in the body", d.Kind, d.Title)
		}
	}
}

// AND THE BODY LEADS WITH IT, so the device is still the first thing read — in the field where
// Apple's budget is 150 characters and the first ~40 are the most consistently visible.
func TestEveryBodyLeadsWithTheDeviceName(t *testing.T) {
	for _, d := range everyDecision(t) {
		if !strings.HasPrefix(d.Body, longDeviceName) {
			t.Errorf("kind %q body does not name the device first: %q", d.Kind, d.Body)
		}
	}
}

// A TITLE IS SHORT ENOUGH TO READ AT A GLANCE. Apple's ceiling is 50 characters; these are states,
// not sentences, and iOS spends part of the line on `from <app name>` besides. 24 is what the
// longest of them needs, and a title that grows past it is a title that has started carrying data.
func TestTitlesAreShortEnoughForAWatch(t *testing.T) {
	for _, d := range everyDecision(t) {
		if n := len([]rune(d.Title)); n > 24 {
			t.Errorf("kind %q title is %d characters: %q — Apple asks for brief titles", d.Kind, n, d.Title)
		}
	}
}

// TITLE-STYLE CAPITALIZATION AND NO ENDING PUNCTUATION, in the HIG's words.
//
// Checked as "every word starts capitalized", which is what title style means for strings this
// short — none of them contain an article or preposition that title style would leave lowercase.
func TestTitlesUseTitleStyleCapitalization(t *testing.T) {
	for _, d := range everyDecision(t) {
		for _, word := range strings.Fields(d.Title) {
			r := []rune(word)[0]
			if unicode.IsLetter(r) && !unicode.IsUpper(r) {
				t.Errorf("kind %q title is not title-style: %q", d.Kind, d.Title)
				break
			}
		}
		if strings.HasSuffix(d.Title, ".") || strings.HasSuffix(d.Title, "!") {
			t.Errorf("kind %q title ends with punctuation: %q", d.Kind, d.Title)
		}
	}
}

// THE BODY STAYS SENTENCE CASE WITH A FULL STOP, which is the same guidance's other half —
// *"complete sentences, sentence case, and proper punctuation"*. The product's voice lives here.
func TestBodiesAreCompleteSentences(t *testing.T) {
	for _, d := range everyDecision(t) {
		if !strings.HasSuffix(d.Body, ".") {
			t.Errorf("kind %q body is not a complete sentence: %q", d.Kind, d.Body)
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
