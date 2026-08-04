package config

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// qn.6g PR 2 — THE SEAM. Its claim is narrow on purpose: the mechanism exists and fires EXACTLY
// ONCE per successful write. There is no consumer yet, so nothing here asserts that any setting
// takes effect — that is PRs 4 and 5, and claiming it now would be the thing this rung exists to
// stop.

func testService(t *testing.T) *Service {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	svc := NewService(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	// A valid document to write over: `storage:` is the one key with no default, so a Replace of
	// Default() would be refused by CheckStorages before any applier could run.
	svc.cfg = validConfig()
	return svc
}

// validConfig is a document Replace will accept. It goes through `withStorages`, i.e. through
// ResolveStorages, because per-entry defaults are applied at PARSE rather than pre-filled into
// Default() (quince#473) — a hand-built entry has an empty `zfs.mode` and `zfs.seed` and is refused
// by Validate. Worth the two lines: the first version of this file built the slice directly and
// every test in it failed on that rather than on the mechanism.
func validConfig() Config {
	return withStorages(StorageEntry{Name: "local", Path: "/backups", Default: true, Backend: "auto"})
}

func TestAnApplierRunsExactlyOncePerWrite(t *testing.T) {
	svc := testService(t)
	var calls int
	svc.Subscribe("counter", func(_, _ Config) []Warning { calls++; return nil })

	for i := 0; i < 3; i++ {
		if errs, _, err := svc.Replace(validConfig()); err != nil || len(errs) > 0 {
			t.Fatalf("replace %d: errs=%v err=%v", i, errs, err)
		}
	}
	if calls != 3 {
		t.Errorf("applier ran %d time(s) over 3 writes, want 3", calls)
	}
}

// A REFUSED WRITE MUST NOT NOTIFY. Nothing was written, so telling subsystems there is new
// configuration would hand them a document that is not on disk and never will be — the mirror of
// the reason appliers run AFTER the write.
func TestARefusedWriteDoesNotNotify(t *testing.T) {
	svc := testService(t)
	var calls int
	svc.Subscribe("counter", func(_, _ Config) []Warning { calls++; return nil })

	// Invalid: fails Validate before anything is written.
	bad := validConfig()
	bad.Backup.PreferredTransport = "carrier-pigeon"
	if errs, _, _ := svc.Replace(bad); len(errs) == 0 {
		t.Fatal("setup is wrong: that document should not validate")
	}
	// Refused by CheckStorages rather than Validate — the other refusal path, and it is past
	// Validate, so it is the one that could plausibly have written something first.
	empty := validConfig()
	none := []StorageEntry{}
	empty.Storage = &none
	if errs, _, _ := svc.Replace(empty); len(errs) == 0 {
		t.Fatal("setup is wrong: an empty storage list should be refused")
	}

	if calls != 0 {
		t.Errorf("appliers ran %d time(s) on refused writes, want 0", calls)
	}
}

// THE APPLIER SEES THE OLD AND THE NEW, and the old is what was live until this write. Asserted
// because the obvious implementation — reading s.cfg after the swap — makes `old` and `next` the
// same value, and every consumer that decides "have I anything to do?" by comparing them would then
// silently never act.
func TestAnApplierSeesTheOldAndTheNewConfig(t *testing.T) {
	svc := testService(t)
	var gotOld, gotNext string
	svc.Subscribe("observer", func(old, next Config) []Warning {
		gotOld, gotNext = old.Backup.PreferredTransport, next.Backup.PreferredTransport
		return nil
	})

	first := validConfig()
	first.Backup.PreferredTransport = "usb"
	if _, _, err := svc.Replace(first); err != nil {
		t.Fatal(err)
	}
	second := validConfig()
	second.Backup.PreferredTransport = "wifi"
	if _, _, err := svc.Replace(second); err != nil {
		t.Fatal(err)
	}

	if gotOld != "usb" || gotNext != "wifi" {
		t.Errorf("applier saw old=%q next=%q, want usb/wifi — an applier that cannot tell what "+
			"changed cannot decide whether it has work", gotOld, gotNext)
	}
}

// THE SPEC'S OPEN QUESTION 2, SETTLED. Applier warnings are returned per-call and NEVER stored in
// Service.warnings.
//
// `warnings` describes THE FILE AS PARSED and is cleared on every valid write. An apply warning
// describes the gap between the file and the running process — a different fact with a different
// lifetime — so storing it there would have an unrelated later save wipe it while its cause
// persisted. `ForgetRestartWarning` already made exactly this split, for exactly this reason.
func TestApplierWarningsAreReturnedAndNeverStored(t *testing.T) {
	svc := testService(t)
	svc.Subscribe("grumpy", func(_, _ Config) []Warning {
		return []Warning{{Path: "storage", Message: "could not re-open the disk"}}
	})

	_, warns, err := svc.Replace(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0].Message, "could not re-open") {
		t.Fatalf("Replace returned %+v, want the applier's warning", warns)
	}

	// NOT in the snapshot. A GET after this write must not carry it.
	if _, stored, _ := svc.Snapshot(); len(stored) != 0 {
		t.Errorf("an applier warning was stored in Service.warnings (%+v) — it would then be wiped "+
			"by the next unrelated save while its cause persisted", stored)
	}
}

// Registration order is application order, and every applier runs even when an earlier one warns.
// An applier is not a filter: one subsystem failing to take a setting says nothing about another.
func TestEveryApplierRunsInOrderEvenWhenOneWarns(t *testing.T) {
	svc := testService(t)
	var order []string
	svc.Subscribe("first", func(_, _ Config) []Warning {
		order = append(order, "first")
		return []Warning{{Path: "a", Message: "nope"}}
	})
	svc.Subscribe("second", func(_, _ Config) []Warning {
		order = append(order, "second")
		return nil
	})

	_, warns, err := svc.Replace(validConfig())
	if err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Errorf("ran %v, want [first second]", order)
	}
	if len(warns) != 1 {
		t.Errorf("got %d warning(s), want 1", len(warns))
	}
}

// A PANICKING APPLIER MUST NOT TAKE THE WRITE WITH IT. The file is on disk and the snapshot is
// swapped before any applier runs, so unwinding would report a failed save that had succeeded —
// and would leave the file and the UI disagreeing about what is stored.
func TestAPanickingApplierDoesNotFailTheWrite(t *testing.T) {
	svc := testService(t)
	svc.Subscribe("exploder", func(_, _ Config) []Warning { panic("boom") })
	var ranAfter bool
	svc.Subscribe("after", func(_, _ Config) []Warning { ranAfter = true; return nil })

	next := validConfig()
	next.Backup.PreferredTransport = "wifi"
	errs, warns, err := svc.Replace(next)
	if err != nil || len(errs) > 0 {
		t.Fatalf("a panicking applier failed the write: errs=%v err=%v", errs, err)
	}
	if !ranAfter {
		t.Error("a panic in one applier stopped the next one from running")
	}
	if len(warns) == 0 {
		t.Error("a panicking applier produced no warning — the failure would be silent, which is " +
			"the whole thing this rung is closing")
	}
	// THE WRITE STANDS. Asserted on the file, not on the snapshot: the snapshot could be right
	// while the document on disk was not.
	raw, rerr := os.ReadFile(svc.path)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !strings.Contains(string(raw), "preferred_transport: wifi") {
		t.Errorf("the write did not reach the file:\n%s", raw)
	}
}

// NO LOCK IS HELD WHILE AN APPLIER RUNS. A real applier reaches back into the service — a storage
// applier re-resolves `Current().Storage` — and holding `mu` across that is an immediate deadlock.
// This is the cheapest possible reproduction of it, and it hangs rather than fails if it regresses,
// which is why it is worth having explicitly rather than trusting the comment.
func TestAnApplierMayCallBackIntoTheService(t *testing.T) {
	svc := testService(t)
	var seen string
	svc.Subscribe("reentrant", func(_, _ Config) []Warning {
		seen = svc.Current().Backup.PreferredTransport // would deadlock under a held write lock
		return nil
	})

	next := validConfig()
	next.Backup.PreferredTransport = "wifi"
	if _, _, err := svc.Replace(next); err != nil {
		t.Fatal(err)
	}
	if seen != "wifi" {
		t.Errorf("an applier calling Current() saw %q, want wifi — it must observe the NEW snapshot, "+
			"since the swap happens before appliers run", seen)
	}
}

// ForgetStorage goes through Replace, so it notifies too — asserted rather than assumed, because it
// is the second write path and the one a storage applier most needs to hear about.
func TestForgetStorageNotifiesAppliers(t *testing.T) {
	svc := testService(t)
	two := withStorages(
		StorageEntry{Name: "local", Path: "/backups", Default: true, Backend: "auto"},
		StorageEntry{Name: "shuttle", Path: "/mnt/shuttle", Backend: "auto"},
	)
	if _, _, err := svc.Replace(two); err != nil {
		t.Fatal(err)
	}

	var calls int
	svc.Subscribe("counter", func(_, _ Config) []Warning {
		calls++
		return []Warning{{Path: "storage", Message: "noted"}}
	})

	outcome, errs, warns, err := svc.ForgetStorage("shuttle")
	if err != nil || outcome != ForgetDone || len(errs) > 0 {
		t.Fatalf("forget: outcome=%v errs=%v err=%v", outcome, errs, err)
	}
	if calls != 1 {
		t.Errorf("appliers ran %d time(s) on a forget, want 1", calls)
	}
	if len(warns) != 1 {
		t.Errorf("forget returned %d applier warning(s), want 1 — they must reach the response the "+
			"same way the restart notice does", len(warns))
	}
}

// Concurrent writes must not race the applier list. Registration is wiring-time only, so this is
// about the READ side under -race rather than about concurrent Subscribe.
func TestConcurrentWritesDoNotRaceTheApplierList(t *testing.T) {
	svc := testService(t)
	var mu sync.Mutex
	var calls int
	svc.Subscribe("counter", func(_, _ Config) []Warning {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = svc.Replace(validConfig())
		}()
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if calls != 8 {
		t.Errorf("applier ran %d time(s) over 8 concurrent writes, want 8", calls)
	}
}

// THE LAST APPLIER CALL CARRIES THE CONFIG THAT IS ACTUALLY LIVE (quince#665 review).
//
// The test above fires eight writes of the SAME document, so it proves the list read is race-free
// and that each write notifies once — and ordering is unobservable by construction, because every
// write carries identical content. This one makes the writes DISTINGUISHABLE, which is the whole
// point: without writeMu serialising the write path, which AtomicWrite lands last, which snapshot
// swap lands last and which notify arrives last are three independent orderings, so a subsystem
// could be left on a config that neither the file nor the snapshot holds — and left there, because
// nothing re-notifies.
// DETERMINISTIC, NOT A RACE OF EIGHT GOROUTINES, and the first version of this test was the latter
// — it passed with writeMu removed. Worth stating: the hazard is a LOGICAL ordering race, not a data
// race, so `-race` cannot see it and identical timing may simply never interleave. A test that
// hopes to catch it is a test that reports green for the wrong reason.
//
// So the interleaving is FORCED. The first applier call blocks on a channel; while it is blocked a
// second Replace runs. Without writeMu that second write proceeds — swapping the snapshot and
// notifying — and the released first call then records ITS config as the applier's last word, which
// is now stale. With writeMu the second Replace cannot start until the first has finished
// notifying, so the last word is the live one.
func TestTheLastApplierCallMatchesTheLiveConfig(t *testing.T) {
	svc := testService(t)

	var mu sync.Mutex
	var lastSeen string
	release := make(chan struct{})
	entered := make(chan struct{})
	var seq atomic.Int32

	svc.Subscribe("recorder", func(_, next Config) []Warning {
		// The FIRST call blocks; every later call passes straight through.
		//
		// NOT sync.Once — that was the first version and it made this test useless. `Once.Do`
		// BLOCKS concurrent callers until the first Do returns, so it serialised the two applier
		// calls itself and the test passed with writeMu removed. The harness's own primitive was
		// supplying the ordering the test existed to detect the absence of.
		if seq.Add(1) == 1 {
			close(entered)
			<-release
		}
		mu.Lock()
		defer mu.Unlock()
		lastSeen = next.Backup.PreferredTransport
		return nil
	})

	first := validConfig()
	first.Backup.PreferredTransport = "usb"
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = svc.Replace(first)
	}()
	<-entered // the first applier is now inside, holding the write open

	second := validConfig()
	second.Backup.PreferredTransport = "wifi"
	secondDone := make(chan struct{})
	go func() {
		defer close(secondDone)
		_, _, _ = svc.Replace(second)
	}()

	// Give the second write every chance to overtake. Under writeMu it is parked on the mutex;
	// without it, it has completed by now — snapshot swapped, applier notified.
	time.Sleep(50 * time.Millisecond)
	close(release)
	<-done
	<-secondDone

	mu.Lock()
	got := lastSeen
	mu.Unlock()
	if live := svc.Current().Backup.PreferredTransport; got != live {
		t.Errorf("the last applier call saw %q while the live config is %q — a subsystem is left on "+
			"a configuration nothing holds, and nothing will re-notify it", got, live)
	}

	// AND THE FILE AGREES. The snapshot and the file are swapped and written at different moments,
	// so checking only the snapshot would miss a write path that serialised one and not the other.
	raw, err := os.ReadFile(svc.path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "preferred_transport: "+got) {
		t.Errorf("the file does not carry %q, which the last applier was told is live:\n%s", got, raw)
	}
}

// Two concurrent forgets must not lose one. Forget is a READ-MODIFY-WRITE — read the list, splice,
// write — so without the whole of it under writeMu both callers read the same list and the second
// write restores the entry the first removed, with both getting a 200.
func TestConcurrentForgetsBothTakeEffect(t *testing.T) {
	svc := testService(t)
	three := withStorages(
		StorageEntry{Name: "local", Path: "/backups", Default: true, Backend: "auto"},
		StorageEntry{Name: "shuttle", Path: "/mnt/shuttle", Backend: "auto"},
		StorageEntry{Name: "attic", Path: "/mnt/attic", Backend: "auto"},
	)
	if _, _, err := svc.Replace(three); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for _, name := range []string{"shuttle", "attic"} {
		wg.Add(1)
		go func(n string) {
			defer wg.Done()
			_, _, _, _ = svc.ForgetStorage(n)
		}(name)
	}
	wg.Wait()

	live := svc.Current()
	if live.Storage == nil {
		t.Fatal("the storage list vanished entirely")
	}
	if n := len(*live.Storage); n != 1 {
		var names []string
		for _, e := range *live.Storage {
			names = append(names, e.Name)
		}
		t.Errorf("%d storage(s) remain (%v), want 1 — a concurrent forget was silently undone",
			n, names)
	}
}
