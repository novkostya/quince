package config

import (
	"os"
	"strings"
	"testing"
)

// quince#852 — AN ADD MUST NOT DESTROY A CONFIG THAT COULD NOT BE READ.
//
// The bug these pin was MEASURED on a real container (2026-08-12), not reasoned: with a `config.yml`
// carrying the retired `storage.zfs.mode: exec`, `POST /api/config/storage` returned 200 and left
// the file holding only the new entry. The zfs storage, its parent dataset and its hook command were
// gone, there was no undo, and `warnings` came back `[]` so every surface then reported health.
//
// The cause is one line of ordinary code doing exactly what it says: `AddStorage` splices into
// `Current()`, and on a discarded config `Current()` is `Default()` — the operator's declaration was
// never in the document being written. Operator ruling 2026-08-12: refuse the write.

// A config the loader DISCARDS. `mode: exec` was retired by quince#793 and is refused by name, which
// makes it the cheapest true instance of this state and the one quince#818 is about to make common.
const discardedConfig = `storage:
  - name: one
    path: /backups-a
    default: true
    backend: zfs
    zfs:
      parent_dataset: tank/one
      mode: exec
      hook_cmd: ssh h
`

// THE REGRESSION, ASSERTED ON THE BYTES ON DISK rather than on the return value — because the return
// value was already a perfectly cheerful 200 when this was broken. What the operator lost was the
// file, so the file is what the test reads.
func TestAddStorageRefusesWhileTheConfigOnDiskWasDiscarded(t *testing.T) {
	svc, path := serviceOver(t, discardedConfig)

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	outcome, errs, _, err := svc.AddStorage(newEntry(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != AddRefused {
		t.Fatalf("outcome = %v, want AddRefused — a splice over a discarded config replaces the file", outcome)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %v, want exactly one refusal", errs)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("THE FILE WAS REWRITTEN by a refused add.\nbefore:\n%s\nafter:\n%s", before, after)
	}
	// The operator's declaration is still in it, named specifically: "unchanged" above would also
	// pass if nothing were ever written for an unrelated reason, and this is what was actually lost.
	for _, want := range []string{"parent_dataset: tank/one", "hook_cmd: ssh h"} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("the declaration lost %q:\n%s", want, after)
		}
	}
}

// THE REFUSAL MUST NAME THE LINE AND A REMEDY. `qn.6g` ruled that a remedy the user cannot follow is
// the same defect as a silent failure, so "config invalid" on its own would be this bug in a politer
// form. The restart is part of the remedy because there is no reload path (quince#727) — an operator
// who edits the file and presses the button again would meet the same refusal and learn nothing.
func TestTheDiscardedConfigRefusalNamesTheOffendingLineAndTheRemedy(t *testing.T) {
	svc, path := serviceOver(t, discardedConfig)

	_, errs, _, err := svc.AddStorage(newEntry(nil))
	if err != nil || len(errs) != 1 {
		t.Fatalf("outcome errs = %v, err = %v", errs, err)
	}

	if errs[0].Path != "storage[0].zfs.mode" {
		t.Fatalf("refusal Path = %q, want the offending config path", errs[0].Path)
	}
	msg := errs[0].Message
	for _, want := range []string{
		"storage[0].zfs.mode", // where
		"must be one of [hook]",
		path,      // which file
		"restart", // and what to do about it
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal does not mention %q — a refusal the operator cannot act on is the defect it replaces.\ngot: %s", want, msg)
		}
	}
}

// THE GUARD IS NOT OVER-BROAD, and this is the half that keeps it honest. A file that PARSED with
// warnings is a different state: `cfg.Storage` survives the load, so a splice over it loses nothing
// and refusing would decline a safe write on a config quince is perfectly happy to run.
//
// An unknown key is the reachable instance — contracts §6 makes a key the app does not know a
// warning and never an error.
func TestAddStorageIsAllowedWhenTheConfigMerelyCarriesWarnings(t *testing.T) {
	svc, path := serviceOver(t, `storage:
  - name: one
    path: /backups-a
    default: true
totally_unknown_key: 1
`)

	outcome, errs, _, err := svc.AddStorage(newEntry(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != AddDone {
		t.Fatalf("outcome = %v (errs %v), want AddDone — a parsed config with warnings is not a discarded one", outcome, errs)
	}

	// And the sibling really did survive, which is the property that made refusing unnecessary.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/backups-a", "/backups-b"} {
		if !strings.Contains(string(after), want) {
			t.Fatalf("expected %q in the written file:\n%s", want, after)
		}
	}
}

// REPAIRING THE CONFIG CLEARS THE GUARD, in the same process. Without this the refusal would be
// permanent for the lifetime of the daemon — a guard that cannot be cleared by fixing the very thing
// it guards against, which would turn one bug into a worse one.
func TestASuccessfulReplaceClearsTheDiscardRecord(t *testing.T) {
	svc, _ := serviceOver(t, discardedConfig)

	if outcome, _, _, _ := svc.AddStorage(newEntry(nil)); outcome != AddRefused {
		t.Fatalf("precondition failed: the add was not refused")
	}

	// The repair path — a full-document replace, which is what `PUT /api/config` performs. It is
	// deliberately NOT refused by this guard: it is how an operator without shell access fixes the
	// file, and refusing it would leave them with no route back.
	fixed := Default()
	fixed.Storage = &[]StorageEntry{{Name: "one", Path: "/backups-a", Default: true, Backend: "copy"}}
	if errs, _, err := svc.Replace(fixed); err != nil || len(errs) > 0 {
		t.Fatalf("the repair replace was refused: errs = %v, err = %v", errs, err)
	}

	outcome, errs, _, err := svc.AddStorage(newEntry(nil))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome != AddDone {
		t.Fatalf("outcome = %v (errs %v), want AddDone — the config was repaired, so the guard must be clear", outcome, errs)
	}
}
