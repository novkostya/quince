package main

import (
	"testing"

	"github.com/novkostya/quince/core/internal/config"
)

// THE `default: true` FLAG DECIDES WHICH STORAGE IS `slots[0]`, NOT THE POSITION IN `config.yml`
// (Operator ruling 2026-08-11, quince#722).
//
// WHAT THIS PINS IS ALREADY TRUE — and that is the reason to write it, not a reason not to.
// `declaredStorages` has hoisted the flagged entry since `ce81f53` (`qn.6c`, the rung that made
// storage plural), and until this file NOTHING ASSERTED IT: `grep declaredStorages
// core/cmd/quince/*_test.go` returned nothing. The function that decides which disk a backup lands
// on when none is named had no test of the property it exists for, so three separate documents
// drifted to the exact opposite of the code with every gate green — contracts §6's live table,
// `sameStorageDeclaration`'s reasoning, and `storage.NewManager`'s own doc comment, all corrected
// in this diff.
//
// IT IS THE SEAM, NOT THE HELPER, THAT MATTERS. Every slot-building path reaches the ordering
// through `declaredStorages` — startup, the live-apply rebuild, and the applier's re-resolve — so
// this is the one place the rule can be stated once. A test against a built `storage.Manager`
// would pass with the hoist removed, because the Manager takes whatever list it is handed and
// correctly treats `slots[0]` as the default; the false thing would be one layer up.
func TestTheDefaultFlagDecidesTheOrderAndNotThePositionInTheFile(t *testing.T) {
	// THE FLAG SITS ON THE SECOND OF THREE, which is the arrangement the product itself cannot
	// produce — `add.go` refuses a newcomer that claims `default`, and `declared.go` marks the
	// incumbent when a second storage is added, so position 0 and the flag stay locked together
	// through every UI path. It is reachable by ONE route, the hand-edit `config.yml` supports and
	// this issue was filed about: move `default: true` and leave the order alone.
	entries := []config.StorageEntry{
		{Name: "first-in-file", Path: "/mnt/first"},
		{Name: "flagged-default", Path: "/mnt/flagged", Default: true},
		{Name: "third", Path: "/mnt/third"},
	}

	got := declaredStorages(&entries)
	if len(got) != len(entries) {
		t.Fatalf("declaredStorages returned %d entries, want %d — the hoist must reorder, never drop",
			len(got), len(entries))
	}
	if got[0].Name != "flagged-default" {
		t.Errorf("slots[0] is %q, want \"flagged-default\" — the flag decides which storage a backup "+
			"that names none resolves to, and it is on the SECOND entry here", got[0].Name)
	}

	// THE SURVIVORS KEEP THEIR RELATIVE ORDER, and that is worth asserting rather than assuming.
	// Nothing downstream depends on it today — only `slots[0]` carries meaning — but a hoist that
	// shuffled the rest would make the storage list on Home reorder itself for no reason a user
	// could name, and the two-pass loop makes it free to promise.
	for i, want := range []string{"flagged-default", "first-in-file", "third"} {
		if got[i].Name != want {
			t.Errorf("slots[%d] is %q, want %q — the default is hoisted and the rest keep file order",
				i, got[i].Name, want)
		}
	}
}

// A BARE REORDER THAT MOVES NO FLAG CHANGES NOTHING ABOUT THE DEFAULT.
//
// This is the half `sameStorageDeclaration`'s comment got backwards. It justified comparing
// declarations position-wise on the premise that *"position IS the default (`slots[0]`), so a
// reorder with identical members is a real change — the user made a different disk the default"*.
// The behaviour it produces is harmless either way — re-resolution is idempotent, only wasteful —
// but a load-bearing comment that states the opposite of the truth is how the next reader reasons
// their way to a wrong change, which is what this asserts against.
func TestReorderingWithoutMovingTheFlagLeavesTheDefaultAlone(t *testing.T) {
	flaggedFirst := []config.StorageEntry{
		{Name: "usb", Path: "/mnt/usb", Default: true},
		{Name: "nas", Path: "/mnt/nas"},
	}
	flaggedSecond := []config.StorageEntry{
		{Name: "nas", Path: "/mnt/nas"},
		{Name: "usb", Path: "/mnt/usb", Default: true},
	}

	a := declaredStorages(&flaggedFirst)
	b := declaredStorages(&flaggedSecond)
	if a[0].Name != b[0].Name {
		t.Errorf("swapping the two lines changed the default from %q to %q — it must not: the flag "+
			"did not move, and the flag is what decides", a[0].Name, b[0].Name)
	}
	if a[0].Name != "usb" {
		t.Errorf("the default is %q, want \"usb\" — the entry carrying `default: true`", a[0].Name)
	}
}
