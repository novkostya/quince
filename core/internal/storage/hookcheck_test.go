package storage

import (
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
// So the script under test is the operator's actual one, extracted from deploy/storage.md and run
// under /bin/sh. What gets stubbed is `zfs` itself — one layer below the thing whose behaviour is in
// question. That makes the forced-command semantics real: $SSH_ORIGINAL_COMMAND splitting, last-arg
// targeting, the `case` guards, and the exit-1 fall-through are all genuinely exercised.
//
// THE SCRIPT IS EXTRACTED, NOT COPIED INTO testdata/. A second copy drifts, and the doc is the
// artifact operators actually install — so a doc edit that breaks the helper turns this gate red.
// Extraction FAILS LOUDLY rather than skipping (see extractHelper); a gate that quietly stops
// running is the failure mode this rung keeps naming.

// extractHelper pulls the `quince-zfs-helper` script out of deploy/storage.md.
//
// It refuses rather than skipping when it cannot find it. The cost of extraction is exactly this
// coupling to the document's shape, and the mitigation is that the failure is a red build with a
// sentence saying what to look at — never a silent pass. Rung-local and reversible to a testdata/
// copy if it proves brittle (qn.6e Gates).
func extractHelper(t *testing.T) string {
	t.Helper()
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "storage.md"))
	if err != nil {
		t.Fatalf("G8 CANNOT RUN: reading deploy/storage.md: %v", err)
	}
	// The helper is the fenced block that starts with a shebang and defines PARENT — pinned on two
	// markers rather than on a fence index, so an inserted example above it does not silently
	// select the wrong block.
	blocks := strings.Split(string(doc), "```")
	for _, b := range blocks {
		body := strings.TrimPrefix(strings.TrimPrefix(b, "sh"), "\n")
		if strings.HasPrefix(body, "#!/bin/sh") && strings.Contains(body, "quince-zfs-helper") &&
			strings.Contains(body, "capacity)") {
			return body
		}
	}
	t.Fatalf("G8 CANNOT RUN: no quince-zfs-helper script found in deploy/storage.md. " +
		"If the doc's shape changed, fix this extractor — do NOT let the gate stop running.")
	return ""
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
func hookHarness(t *testing.T, parent, zfsBehaviour, helperSrc string) string {
	t.Helper()
	dir := t.TempDir()

	helper := filepath.Join(dir, "quince-zfs-helper")
	// The published script carries a placeholder PARENT; an operator edits it, and so do we.
	src := strings.Replace(helperSrc, `PARENT="pool/path/to/iphone-backup"`, `PARENT="`+parent+`"`, 1)
	if !strings.Contains(src, `PARENT="`+parent+`"`) {
		t.Fatalf("G8 CANNOT RUN: the PARENT assignment in deploy/storage.md changed shape; " +
			"the extractor found the script but could not point it at a test dataset")
	}
	write(t, helper, src)

	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(bin, "zfs"), "#!/bin/sh\n"+zfsBehaviour+"\n")

	shim := filepath.Join(dir, "fake-ssh")
	write(t, shim, "#!/bin/sh\nPATH="+bin+":$PATH\nexport PATH\n"+
		"SSH_ORIGINAL_COMMAND=\"$*\"\nexport SSH_ORIGINAL_COMMAND\nexec /bin/sh "+helper+"\n")
	return shim
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
	hook := hookHarness(t, parent, zfs, extractHelper(t))

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
	src := extractHelper(t)
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
	hook := hookHarness(t, "tank/backups", zfs, extractHelper(t))

	got := CheckHook(context.Background(), "tank/somewhere-else", hook)

	if got.Outcome != HookParentMismatch {
		t.Fatalf("outcome = %q, want %q — `capacity` takes no argument so it still answers; only "+
			"`list <typed parent>` can see the disagreement. reason=%q detail=%q",
			got.Outcome, HookParentMismatch, got.Reason, got.Detail)
	}
}

func TestCheckHookUnreachable(t *testing.T) {
	got := CheckHook(context.Background(), "tank/backups", "/nonexistent/ssh")
	if got.Outcome != HookUnreachable {
		t.Fatalf("outcome = %q, want %q", got.Outcome, HookUnreachable)
	}

	if e := CheckHook(context.Background(), "tank/backups", ""); e.Outcome != HookUnreachable {
		t.Errorf("an empty hook_cmd = %q, want %q", e.Outcome, HookUnreachable)
	}
	// A dataset name that could not be safe in an argv is refused WITHOUT RUNNING ANYTHING.
	bad := CheckHook(context.Background(), "tank/backups; rm -rf /", "/bin/true")
	if bad.Outcome != HookUnreachable || !strings.Contains(bad.Reason, "did not run") {
		t.Errorf("an invalid dataset name must refuse before exec; got %q / %q", bad.Outcome, bad.Reason)
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
	hook := hookHarness(t, parent, zfs, extractHelper(t))

	// Ask `list` with flags a caller might hope get forwarded.
	cli := newZFSCLI(parent, "hook", hook, "")
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
		hook := hookHarness(t, parent, `echo "$*" >> `+rec+"\nexit 0", extractHelper(t))
		cli := newZFSCLI(parent, "hook", hook, "")
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
