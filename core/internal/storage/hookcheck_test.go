package storage

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// qn.6e G8 — `Test helper`, against THE REAL quince-zfs-helper.
//
// THIS FIXTURE DOES NOT STUB THE HELPER, and that is the whole design of the gate. qn.6d's sharpest
// finding (quince#593) was a read that sent flags through a forced command, which DISCARDS them and
// exits 0 — and no gate could have caught it, because "the tests stub the transport, so they assert
// the argv SENT and the stub answers whatever the test chose: A MIRROR, NOT A PEER."
//
// So the script under test is the operator's actual one, read from the file quince itself embeds and
// run under /bin/sh. What gets stubbed is `zfs` itself — one layer below the thing whose behaviour
// is in question. That makes the forced-command semantics real: $SSH_ORIGINAL_COMMAND splitting,
// last-arg targeting, the `case` guards, and the exit-1 fall-through are all genuinely exercised.
//
// THE SCRIPT IS READ, NOT COPIED INTO testdata/. A second copy drifts, and this file is the artifact
// operators actually install, so an edit that breaks the helper turns this gate red.
//
// IT USED TO BE PARSED OUT OF `deploy/storage.md`, and that is what quince#818 piece C ended. The
// extractor pinned itself to the document's shape with two content markers and a `PARENT="…"` string
// replacement, each with its own loud failure, because a fence has no other handle. `go:embed`
// needed a real file anyway; the gate reading one is what that bought, and a prose edit can no
// longer take a gate down.

// helperSource reads the real helper. It refuses rather than skipping: a gate that quietly stops
// running is the failure mode this rung keeps naming.
func helperSource(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join("zfshelper", "quince-zfs-helper"))
	if err != nil {
		t.Fatalf("G8 CANNOT RUN: reading the helper: %v", err)
	}
	return string(src)
}

// hookHarness writes the real helper, a stub `zfs`, and an ssh-shaped shim, and returns a hook_cmd.
//
// THE SHIM IS WHAT ssh DOES, not a convenience: a forced command receives its arguments in
// $SSH_ORIGINAL_COMMAND, never as argv. quince runs `<hook_cmd> capacity`, real ssh turns that into
// SSH_ORIGINAL_COMMAND="capacity" on the far side, and the helper reads it. Reproducing that
// translation is the only way to drive the real script from a test.
//
// zfsBehaviour is the stub's script body: it decides what `zfs` does, and therefore lets one harness
// produce a working helper, an un-migrated one, or an unreachable host.
// RETURNS AN ARGV since quince#818 — `CheckHook` takes the composed transport as a []string rather
// than splitting a command line. One element here: the shim IS the whole transport.
func hookHarness(t *testing.T, parent, zfsBehaviour, helperSrc string) []string {
	t.Helper()
	dir := t.TempDir()

	helper := filepath.Join(dir, "quince-zfs-helper")
	// THE SCRIPT IS INSTALLED UNMODIFIED SINCE quince#985 — the fixture used to substitute a
	// `PARENT=` line, as an operator used to. The dataset now arrives as the forced command's own
	// argument, which the shim below supplies, so what is written here is byte-for-byte what quince
	// serves.
	write(t, helper, helperSrc)

	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(bin, "zfs"), "#!/bin/sh\n"+zfsBehaviour+"\n")

	// THE PARENT IS THE FORCED COMMAND'S ARGUMENT AND THE REQUEST IS $SSH_ORIGINAL_COMMAND — the two
	// arrive by different routes and that separation is the confinement (quince#985). sshd runs
	// `command="<helper> <parent>"` and puts the client's words in the environment variable; the
	// shim reproduces exactly that, so a bug that let the client name its own parent would show up
	// here rather than on somebody's pool.
	shim := filepath.Join(dir, "fake-ssh")
	write(t, shim, "#!/bin/sh\nPATH="+bin+":$PATH\nexport PATH\n"+
		"SSH_ORIGINAL_COMMAND=\"$*\"\nexport SSH_ORIGINAL_COMMAND\nexec /bin/sh "+helper+" '"+parent+"'\n")
	return []string{shim}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
}

// A working helper on a parent with NO SNAPSHOTS YET — the first-run case, and the one a naive
// implementation reports as broken.
func TestCheckHookEmptyListIsSuccess(t *testing.T) {
	requireSh(t)
	const parent = "tank/backups"
	// `zfs list -t snapshot …` on a dataset with no snapshots: exit 0, NOTHING on stdout.
	// `zfs list -H -p -o used,available` answers two numbers.
	zfs := `case "$*" in
  *"-t snapshot"*) exit 0 ;;
  *available*)     echo "10	20" ; exit 0 ;;
  *)               exit 0 ;;
esac`
	hook := hookHarness(t, parent, zfs, helperSource(t))

	got := CheckHook(context.Background(), parent, hook)

	if got.Outcome != HookOK {
		t.Fatalf("outcome = %q, want %q — AN EMPTY SNAPSHOT LIST IS SUCCESS. A storage with no "+
			"backups yet has no @quince-* snapshots, so the correct freshly-installed case answers "+
			"exit 0 with empty stdout. reason=%q detail=%q",
			got.Outcome, HookOK, got.Reason, got.Detail)
	}
}

// The un-migrated helper: qn.6d added `capacity)` and an operator who installed before it has every
// other verb working. Reproduced by running a helper with that case arm REMOVED, so the fall-through
// refusal is the real one rather than a simulated exit code.
func TestCheckHookNotMigratedWhenCapacityIsMissing(t *testing.T) {
	requireSh(t)
	const parent = "tank/backups"
	src := helperSource(t)
	cut := strings.Index(src, "  capacity)")
	if cut < 0 {
		t.Fatalf("G8 CANNOT RUN: no `capacity)` arm in the published helper to remove")
	}
	end := strings.Index(src[cut:], "esac")
	if end < 0 {
		t.Fatalf("G8 CANNOT RUN: could not find the end of the case block")
	}
	preMigration := src[:cut] + src[cut+end:]

	zfs := `case "$*" in
  *"-t snapshot"*) echo "tank/backups/UDID@quince-1" ; exit 0 ;;
  *)               exit 0 ;;
esac`
	hook := hookHarness(t, parent, zfs, preMigration)

	got := CheckHook(context.Background(), parent, hook)

	if got.Outcome != HookNotMigrated {
		t.Fatalf("outcome = %q, want %q (reason %q, detail %q)",
			got.Outcome, HookNotMigrated, got.Reason, got.Detail)
	}
	if !strings.Contains(got.Reason, "capacity") {
		t.Errorf("the remedy must name the verb to add; got %q", got.Reason)
	}
}

// The typed parent does not match the helper's baked-in $PARENT. `capacity` still answers, because
// it takes no caller argument — which is exactly why it runs first.
func TestCheckHookParentMismatch(t *testing.T) {
	requireSh(t)
	zfs := `case "$*" in
  *"-t snapshot"*) exit 0 ;;
  *available*)     echo "10	20" ; exit 0 ;;
  *)               exit 0 ;;
esac`
	// The helper is configured for one dataset; the form asks about another.
	hook := hookHarness(t, "tank/backups", zfs, helperSource(t))

	got := CheckHook(context.Background(), "tank/somewhere-else", hook)

	if got.Outcome != HookParentMismatch {
		t.Fatalf("outcome = %q, want %q — `capacity` takes no argument so it still answers; only "+
			"`list <typed parent>` can see the disagreement. reason=%q detail=%q",
			got.Outcome, HookParentMismatch, got.Reason, got.Detail)
	}
}

func TestCheckHookUnreachable(t *testing.T) {
	got := CheckHook(context.Background(), "tank/backups", []string{"/nonexistent/ssh"})
	if got.Outcome != HookUnreachable {
		t.Fatalf("outcome = %q, want %q", got.Outcome, HookUnreachable)
	}

	if e := CheckHook(context.Background(), "tank/backups", nil); e.Outcome != HookUnreachable {
		t.Errorf("an empty hook_cmd = %q, want %q", e.Outcome, HookUnreachable)
	}
	// A dataset name that could not be safe in an argv is refused WITHOUT RUNNING ANYTHING.
	bad := CheckHook(context.Background(), "tank/backups; rm -rf /", []string{"/bin/true"})
	if bad.Outcome != HookUnreachable || !strings.Contains(bad.Reason, "did not run") {
		t.Errorf("an invalid dataset name must refuse before exec; got %q / %q", bad.Outcome, bad.Reason)
	}
}

// THE REFUSAL NAMES THE MISTAKE, and this is the half that was unguarded.
//
// `Test helper` and `Show the helper script` sit inches apart on one form and validate the same
// field, so a mistype reaches whichever button the user presses first. Two different explanations
// for one error would be worse than one bad explanation — which is the reason both messages were
// changed together, and it is worth exactly nothing if only one of them is pinned.
//
// The httpapi side has `TestZFSHelperRefusalTellsThemItIsNotAPath`. Before this test, a reword here
// that dropped the path-vs-dataset distinction passed every gate in the repository — the divergence
// the change exists to prevent, arriving through the unguarded half (quince#909 review).
//
// SAME FACTS, DELIBERATELY NOT THE SAME STRING. Only one of the two can end "and quince did not run
// anything", so asserting the sentences match would be asserting something false. Asserting the
// FACTS is what actually holds the two together.
func TestCheckHookRefusalTellsThemItIsNotAPath(t *testing.T) {
	// The exact value a user carries down from the Path field above it on the same form.
	got := CheckHook(context.Background(), "/backups", []string{"/bin/true"})

	if got.Outcome != HookUnreachable {
		t.Fatalf("outcome = %q, want %q", got.Outcome, HookUnreachable)
	}
	for _, want := range []struct{ fact, why string }{
		{"no leading `/`", "the single thing wrong with the value they typed"},
		{"field above", "which field they took it from — both are on one screen"},
		{"rpool/quince", "an example, because the rule alone does not show the shape"},
		{"did not run", "that nothing was executed, which this message has always promised"},
	} {
		if !strings.Contains(got.Reason, want.fact) {
			t.Errorf("the refusal does not carry %q — %s.\ngot: %s", want.fact, want.why, got.Reason)
		}
	}
}

// THE REMEDY MUST NAME THE CAUSE THAT WAS MEASURED TO PRODUCE THIS (quince#799).
//
// It named the key, the forced command and the host, and omitted the HOST KEY — the one thing
// quince#796 measured actually producing `unreachable`, on a rig where all three of those were
// correct. `Detail` carries ssh's `Host key verification failed.` verbatim, so a reader who reads
// the raw output is fine; `Reason` is the checklist that exists to spare them that, and it sent
// them to re-check three things that were already right.
//
// ASSERTED PER CAUSE rather than as one substring of the sentence, so a reword that drops a cause
// fails here instead of passing on a lucky match. That is the guard the issue asked for on top of
// the clause: the enumeration now has ONE home, and this proves that home reaches the surface a
// user actually reads.
func TestUnreachableRemedyNamesEveryCause(t *testing.T) {
	got := CheckHook(context.Background(), "tank/backups", []string{"/nonexistent/ssh"})
	if got.Outcome != HookUnreachable {
		t.Fatalf("outcome = %q, want %q — this test is about the reachability remedy", got.Outcome, HookUnreachable)
	}
	for _, cause := range []string{"key", "forced command", "known_hosts", "host is up"} {
		if !strings.Contains(got.Reason, cause) {
			t.Errorf("the unreachable remedy does not name %q, so an operator is sent to re-check "+
				"everything except the cause. Reason was: %q", cause, got.Reason)
		}
	}
}

// THE FORCED COMMAND DISCARDS FLAGS, and this is quince#593's defect reproduced as a gate rather
// than described in a comment. It is what justifies the whole extract-the-real-script design: a
// stubbed helper would answer however the test chose and this could not be observed.
func TestTheRealHelperDiscardsCallerFlags(t *testing.T) {
	requireSh(t)
	const parent = "tank/backups"
	// The stub RECORDS what zfs was actually asked for. If the helper forwarded caller flags, the
	// recorded argv would contain them.
	rec := filepath.Join(t.TempDir(), "argv")
	zfs := `echo "$*" >> ` + rec + `
case "$*" in
  *"-t snapshot"*) exit 0 ;;
  *available*)     echo "10	20" ; exit 0 ;;
  *)               exit 0 ;;
esac`
	hook := hookHarness(t, parent, zfs, helperSource(t))

	// Ask `list` with flags a caller might hope get forwarded.
	cli := newZFSCLI(parent, hook)
	if _, err := cli.run(context.Background(), cli.argv("list", "-H", "-p", "-o", "used,available", parent)); err != nil {
		t.Fatalf("the helper refused a guarded list: %v", err)
	}

	b, err := os.ReadFile(rec)
	if err != nil {
		t.Fatalf("the stub zfs was never invoked: %v", err)
	}
	got := string(b)
	if !strings.Contains(got, "-t snapshot") {
		t.Fatalf("the helper did not run its OWN fixed `list` argv; zfs saw %q", got)
	}
	if strings.Contains(got, "used,available") {
		t.Fatalf("THE HELPER FORWARDED CALLER FLAGS — quince#593's defect is back. zfs saw %q", got)
	}
}

// quince#984 — `capacity` is confined by a GUARD, not by having nothing to confine.
//
// Every other verb checks its target against `$PARENT`. `capacity` did not, because it had no
// target: it ignored whatever the caller sent and read `$PARENT`. The answer was right and the
// reason was wrong — measured on a rig, a client sending `capacity <someone-else's-dataset>` got its
// OWN parent's figures back — so the rule an auditor reads off the file ("every verb checks its
// target") was false for one arm, and would stay false the moment someone taught that arm to honour
// an argument.
//
// Driven through the ssh shim rather than through `zfsCLI`, because quince never sends this: the
// caller here is an operator at a terminal, or a client with the key and an idea. There is no Go
// surface to go through.
func TestTheRealHelperRefusesCapacityWithAnArgument(t *testing.T) {
	requireSh(t)
	const parent = "tank/backups"

	// Returns what `zfs` was asked for (empty if it was never reached), the helper's stderr, and the
	// exit error. The stub answers a plausible capacity so a refusal cannot be confused with a
	// broken fixture.
	run := func(t *testing.T, args ...string) (zfsSaw, stderr string, err error) {
		t.Helper()
		rec := filepath.Join(t.TempDir(), "argv")
		hook := hookHarness(t, parent, `echo "$*" >> `+rec+"\necho \"10\t20\"\nexit 0", helperSource(t))
		cmd := exec.Command(hook[0], args...) //nolint:gosec // the shim is written by this test
		var errb bytes.Buffer
		cmd.Stderr = &errb
		err = cmd.Run()
		b, readErr := os.ReadFile(rec)
		if readErr != nil {
			return "", errb.String(), err
		}
		return string(b), errb.String(), err
	}

	// The guard must not cost the verb its actual job — which is also what `CheckHook` fires first.
	t.Run("the bare verb still answers", func(t *testing.T) {
		saw, stderr, err := run(t, "capacity")
		if err != nil {
			t.Fatalf("the helper refused a bare `capacity`: %v\nstderr: %s", err, stderr)
		}
		if !strings.Contains(saw, "used,available") || !strings.Contains(saw, parent) {
			t.Errorf("zfs saw %q, want a used,available read of %q", strings.TrimSpace(saw), parent)
		}
	})

	// The case the issue was filed on: a client asking about a dataset that is not its own used to be
	// answered — correctly, by accident — instead of refused.
	t.Run("SOMEBODY ELSE'S dataset is refused, not silently answered for ours", func(t *testing.T) {
		saw, stderr, err := run(t, "capacity", "rpool/somebody-else")
		if err == nil {
			t.Fatalf("the helper ACCEPTED `capacity <foreign dataset>`; it must refuse rather than "+
				"answer for $PARENT. zfs saw %q", strings.TrimSpace(saw))
		}
		if saw != "" {
			t.Errorf("zfs was invoked for a refused call: %q", strings.TrimSpace(saw))
		}
		// The refusal has to say WHICH rule bit. "refused: capacity rpool/somebody-else" reads as a
		// confinement failure and would send an operator to check their $PARENT line.
		if !strings.Contains(stderr, "capacity takes no argument") {
			t.Errorf("the refusal does not name the rule; stderr = %q", stderr)
		}
	})

	// $PARENT itself is still an argument. Accepting it would make the guard depend on the value
	// rather than on the shape, which is the coincidence being removed.
	t.Run("even OUR OWN dataset is refused", func(t *testing.T) {
		saw, _, err := run(t, "capacity", parent)
		if err == nil {
			t.Errorf("the helper accepted `capacity $PARENT` — the verb takes no argument at all. "+
				"zfs saw %q", strings.TrimSpace(saw))
		}
	})
}

// quince#985 — A HELPER WHOSE FORCED COMMAND NAMES NO DATASET REFUSES, AND SAYS WHAT TO WRITE.
//
// This is the failure mode the change introduces, so it is the one that has to be measured. The
// parent used to be inside the file, where it could not be left out; it is now one word in an
// `authorized_keys` line an operator types, and omitting it is the obvious new mistake. An empty
// `$PARENT` would leave every `case "$target" in "$PARENT"/*` matching a bare `/*` — a guard that
// still looks like a guard.
//
// It presents to the user as `unreachable`, whose remedy names the forced command, and the helper's
// own sentence arrives in `HookCheck.Detail` — so the diagnosis is on the screen even though the
// outcome is the generic one.
func TestTheRealHelperRefusesWithNoParentInTheForcedCommand(t *testing.T) {
	requireSh(t)
	dir := t.TempDir()
	helper := filepath.Join(dir, "quince-zfs-helper")
	write(t, helper, helperSource(t))
	rec := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(bin, "zfs"), "#!/bin/sh\necho \"$*\" >> "+rec+"\nexit 0\n")

	// The forced command with its argument LEFT OFF — everything else exactly as sshd delivers it.
	cmd := exec.Command("/bin/sh", helper) //nolint:gosec // the helper is written by this test
	cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"),
		"SSH_ORIGINAL_COMMAND=capacity")
	var errb bytes.Buffer
	cmd.Stderr = &errb
	err := cmd.Run()

	if err == nil {
		t.Fatal("the helper ran with no dataset in its forced command — it must refuse rather than " +
			"operate against an empty $PARENT")
	}
	if _, statErr := os.Stat(rec); statErr == nil {
		t.Error("zfs was invoked despite there being no parent to confine the call to")
	}
	// THE REMEDY IS THE LINE THEY HAVE TO FIX, not a restatement of the problem. An operator seeing
	// this in `Test helper`'s detail has to know that the thing to edit is authorized_keys.
	for _, want := range []string{"no parent dataset", "authorized_keys", "command="} {
		if !strings.Contains(errb.String(), want) {
			t.Errorf("the refusal does not carry %q; stderr = %q", want, errb.String())
		}
	}
}

func requireSh(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Fatalf("G8 CANNOT RUN: no /bin/sh — this gate refuses rather than skipping")
	}
}

// qn.6h gate G11 — the ABANDON verb, against the operator's REAL script text.
//
// rollback is the one guarded arm whose guard failing destroys committed versions: -r/-R delete
// snapshots NEWER than the target, and those are versions. Every other arm is either harmless when
// mis-guarded (a `list` leaks a listing) or predates this rung.
//
// quince's own refusal is pinned in zfscli_rollback_test.go. This pins the OTHER half, which the
// Rollback doc comment claims — "the helper enforces it independently … guarded on both sides".
// That was measured once on a real pool, 2026-08-08; the helper is a file operators hand-edit, and
// quince#593 is the record of flag forwarding being reintroduced by exactly such an edit.
func TestTheRealHelperBoundsRollback(t *testing.T) {
	requireSh(t)
	const parent = "tank/backups"
	const udid = "AAAABBBBCCCC"
	snap := parent + "/" + udid + "@quince-2026-08-08-0000"

	run := func(t *testing.T, args ...string) (string, error) {
		t.Helper()
		rec := filepath.Join(t.TempDir(), "argv")
		hook := hookHarness(t, parent, `echo "$*" >> `+rec+"\nexit 0", helperSource(t))
		cli := newZFSCLI(parent, hook)
		_, err := cli.run(context.Background(), cli.argv(args[0], args[1:]...))
		b, readErr := os.ReadFile(rec)
		if readErr != nil {
			return "", err // zfs was never invoked — the helper refused before exec'ing
		}
		return string(b), err
	}

	t.Run("a guarded target reaches zfs as rollback + target and nothing else", func(t *testing.T) {
		got, err := run(t, "rollback", snap)
		if err != nil {
			t.Fatalf("the helper refused a guarded rollback: %v", err)
		}
		if want := "rollback " + snap; strings.TrimSpace(got) != want {
			t.Errorf("zfs saw %q, want exactly %q", strings.TrimSpace(got), want)
		}
	})

	t.Run("-r is DISCARDED by the parse", func(t *testing.T) {
		got, err := run(t, "rollback", "-r", snap)
		// zfs MUST have been invoked. If the helper refused outright, `got` is empty and the flag
		// assertion below passes for the wrong reason — a gate that reads identically whether it
		// bit or was never reached, which is the failure this rung has met twice already.
		if err != nil || got == "" {
			t.Fatalf("the helper never exec'd zfs (err=%v) — this subtest proves nothing unless the "+
				"FLAGGED call reaches zfs stripped", err)
		}
		if strings.Contains(got, "-r") {
			t.Fatalf("THE HELPER FORWARDED -r — that flag destroys snapshots NEWER than the target, "+
				"which are committed versions. zfs saw %q", got)
		}
		if want := "rollback " + snap; strings.TrimSpace(got) != want {
			t.Errorf("zfs saw %q, want exactly %q", strings.TrimSpace(got), want)
		}
	})

	t.Run("a FOREIGN snapshot is refused", func(t *testing.T) {
		foreign := parent + "/" + udid + "@zfs-auto-snap_frequent-2026-08-08-0345"
		got, err := run(t, "rollback", foreign)
		if err == nil {
			t.Error("the helper accepted a non-@quince-* target — those are not quince's to roll back to")
		}
		if got != "" {
			t.Errorf("zfs was invoked for a refused target: %q", got)
		}
	})

	t.Run("a target outside PARENT is refused", func(t *testing.T) {
		got, err := run(t, "rollback", "rpool/somebody-else@quince-2026-08-08-0000")
		if err == nil {
			t.Error("the helper accepted a target outside $PARENT")
		}
		if got != "" {
			t.Errorf("zfs was invoked for a refused target: %q", got)
		}
	})

	t.Run("a DATASET, with no @, is refused", func(t *testing.T) {
		got, err := run(t, "rollback", parent+"/"+udid)
		if err == nil {
			t.Error("the helper accepted a dataset rather than a snapshot")
		}
		if got != "" {
			t.Errorf("zfs was invoked for a refused target: %q", got)
		}
	})
}

// THE `ok` REASON MUST NOT CLAIM WHAT THE CHECK DID NOT DO (Operator, 2026-08-13).
//
// It read "quince can snapshot here", which is an INFERENCE from two READ-ONLY verbs. `CheckHook`
// runs `capacity` and `list` and never attempts `snapshot` — deliberately, because a form must not
// create anything on the operator's pool to answer a question. A helper whose `snapshot)` arm is
// missing, mistyped or refused by zfs permissions passes both verbs and would have passed that
// sentence, then failed at commit after a multi-hour transfer. That is the exact failure this button
// exists to move earlier.
//
// Asserted as a BAN plus the facts that replace it, so a future reword cannot quietly reintroduce a
// capability claim while still reading well.
func TestCheckHookOKClaimsOnlyWhatItMeasured(t *testing.T) {
	requireSh(t)
	const parent = "tank/backups"
	zfs := `case "$*" in
  *"-t snapshot"*) exit 0 ;;
  *available*)     echo "10	20" ; exit 0 ;;
  *)               exit 0 ;;
esac`
	got := CheckHook(context.Background(), parent, hookHarness(t, parent, zfs, helperSource(t)))
	if got.Outcome != HookOK {
		t.Fatalf("outcome = %q, want %q", got.Outcome, HookOK)
	}

	// NOTHING ABOUT WRITING. `snapshot` is the verb that was not run.
	for _, banned := range []string{"can snapshot", "will snapshot", "snapshot here"} {
		if strings.Contains(got.Reason, banned) {
			t.Errorf("the ok reason claims %q, which the read-only checks did not prove.\ngot: %s",
				banned, got.Reason)
		}
	}
	// AND IT NAMES THE THREE THINGS IT DID PROVE, each a separate way to be wrong and each just
	// typed by the operator.
	for _, want := range []struct{ fact, why string }{
		{"key", "that it reached the host at all"},
		{"forced command", "that the helper ran rather than a shell"},
		{"parent dataset", "that the helper is pinned to the dataset in the form"},
	} {
		if !strings.Contains(got.Reason, want.fact) {
			t.Errorf("the ok reason does not name %q — %s.\ngot: %s", want.fact, want.why, got.Reason)
		}
	}

	// THE TWO CLAUSES ARE SEPARATED, AND THE DAEMON IS WHAT SEPARATES THEM (Operator, 2026-08-14).
	// What was proved and what was NOT are different statements; run together they read as one long
	// line whose second half is the one a reader skips — which is precisely the half this sentence
	// exists for. The form renders the reason with `whitespace-pre-line`, so the break is decided
	// here, and asserting it here is what stops a later reword from quietly closing it up.
	before, after, found := strings.Cut(got.Reason, "\n")
	if !found {
		t.Fatalf("the ok reason runs its two clauses together with no break.\ngot: %s", got.Reason)
	}
	if !strings.Contains(after, "not tested") {
		t.Errorf("the break does not separate what was proved from what was not.\nbefore: %s\nafter: %s",
			before, after)
	}
}
