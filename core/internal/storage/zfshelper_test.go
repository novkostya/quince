package storage

import (
	"strings"
	"testing"
)

// THE SCRIPT MUST STILL READ ITS PARENT FROM THE FORCED COMMAND, and this test is what makes the
// promise on the `authorized_keys` line true.
//
// quince tells the operator that `command="/usr/local/sbin/quince-zfs-helper <dataset>"` is what
// confines the helper. If somebody changes the script to take its parent from anywhere else — an
// environment variable, a config file, a `PARENT=` assignment again — that line keeps being served,
// keeps looking right, and confines nothing. Nothing about that failure looks wrong on screen.
//
// IT REPLACES A RUNTIME REFUSAL WITH A BUILD-TIME ONE, deliberately. While the script was rendered
// per install there was a substitution that could silently no-op, so `RenderZFSHelper` had to be able
// to fail and the endpoint had to be able to answer `500`. A static file removes that path entirely:
// there is nothing to substitute, so the only way to get this wrong is to commit it.
func TestHelperTakesItsParentFromTheForcedCommand(t *testing.T) {
	if !helperReadsParentFromForcedCommand() {
		t.Fatalf("the embedded helper no longer contains %q.\n"+
			"If the script's PARENT line changed shape, update zfsHelperParentLine to match — do NOT "+
			"delete this test. The authorized_keys line quince serves promises that the dataset in "+
			"command=\"…\" is what bounds the helper, and this line is what keeps that promise.",
			zfsHelperParentLine)
	}
	// IT MUST BE READ BEFORE $SSH_ORIGINAL_COMMAND IS SPLIT. `set -- $SSH_ORIGINAL_COMMAND` replaces
	// the positional parameters, so a `PARENT="${1:-}"` below it would read the CLIENT'S first word
	// as the parent — the client naming its own confinement, which is no confinement at all.
	parent := strings.Index(zfsHelperScript, zfsHelperParentLine)
	split := strings.Index(zfsHelperScript, "set -- $SSH_ORIGINAL_COMMAND")
	if split < 0 {
		t.Fatal("the helper no longer splits $SSH_ORIGINAL_COMMAND — this test cannot check the order")
	}
	if parent > split {
		t.Fatal("THE PARENT IS READ AFTER THE CLIENT'S REQUEST IS SPLIT INTO $@, so the client " +
			"supplies its own $PARENT. The operator's forced command must be read first.")
	}
}

// A helper installed with no dataset in its forced command REFUSES rather than guessing.
//
// This is the failure mode quince#985 creates: the parent used to be inside the file, where it could
// not be left out. Now it is one word in an `authorized_keys` line an operator types, so omitting it
// is a new way to get this wrong — and an empty `$PARENT` would make `case "$target" in "$PARENT"/*`
// match `/*`, which is a guard that no longer guards.
func TestHelperRefusesWithNoParentInTheForcedCommand(t *testing.T) {
	if !strings.Contains(zfsHelperScript, `[ -n "$PARENT" ]`) {
		t.Error("the helper does not refuse an empty $PARENT — an unset forced-command argument " +
			"would leave every `case` guard matching a bare `/*`")
	}
}

// The embedded bytes are the ones the G8 suite executes. Asserted so that "quince serves the script
// the gate proves" stays true rather than being a claim in a comment.
func TestZFSHelperEmbedMatchesTheFileTheGateRuns(t *testing.T) {
	if zfsHelperScript != helperSource(t) {
		t.Fatal("the embedded helper differs from the file hookcheck_test.go executes — " +
			"the script quince serves is no longer the script the suite proves")
	}
}

// THE SERVED SCRIPT IS THE EMBEDDED SCRIPT, BYTE FOR BYTE — nothing installation-specific in it.
//
// That is the property piece 3 rests on (serving it over plain HTTP, and offering it as readable
// text on the page): a file that carries no operator's dataset carries nothing to leak, and two
// installs of the same version can be compared by hash.
func TestZFSHelperScriptIsStatic(t *testing.T) {
	got := ZFSHelperScript()
	if got != zfsHelperScript {
		t.Fatal("ZFSHelperScript() does not return the embedded bytes")
	}
	if strings.Contains(got, "pool/path/to/iphone-backup") {
		t.Error("the script still carries the old placeholder dataset — under quince#985 the parent " +
			"comes from the forced command, so nothing in the file names a dataset at all")
	}
	// The guards are what make the helper a boundary; a static script keeps every one of them.
	for _, want := range []string{"capacity)", "rollback)", `case "$op" in`, "exit 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("the served helper is missing %q", want)
		}
	}
}

// THE HEADER SAYS "EVERYTHING QUINCE IS ALLOWED TO DO", SO EVERY ARM HAS TO BE IN IT (quince#1008).
//
// It listed four of six: `list` and `capacity` were missing. The rewrite that introduced the
// sentence argued for itself on exactly this ground — that the header "now states what the arms
// below permit, so the sentence is checkable against the code under it" — and it was short by two.
//
// SO THE CHECK IS MECHANICAL NOW, because a totality claim maintained by hand is one verb behind
// from the moment somebody adds a verb. `rollback` and `capacity` were both added after this file
// was written, and both are how the header got out of date the last two times.
//
// IT READS THE ARMS OUT OF THE `case`, not out of a list here. A second list would be a third place
// to forget, which is the defect one size up.
func TestHelperHeaderNamesEveryArm(t *testing.T) {
	header, _, found := strings.Cut(zfsHelperScript, "\nset -eu")
	if !found {
		t.Fatal("the helper no longer opens with a comment block followed by `set -eu` — this test " +
			"cannot tell the header from the code")
	}

	// Each arm is `  <verb>) ` at the start of a line inside the case block. Taken from the file so
	// a new verb is covered the day it lands rather than the day somebody remembers this test.
	var arms []string
	for _, line := range strings.Split(zfsHelperScript, "\n") {
		verb, rest, ok := strings.Cut(strings.TrimSpace(line), ")")
		if !ok || verb == "" || strings.ContainsAny(verb, " \t\"$#(|") || !strings.HasPrefix(rest, " ") {
			continue
		}
		arms = append(arms, verb)
	}
	if len(arms) < 6 {
		t.Fatalf("found %d case arms (%v) — the parse no longer matches the script's shape, so this "+
			"test would pass by finding nothing", len(arms), arms)
	}

	// THE VERB ITSELF NEED NOT APPEAR — the header is prose for an operator, not a symbol table, and
	// "read snapshot lists and free space" is better copy than "list, capacity". So each arm is
	// matched against the words that stand for it, and a new arm with no entry here FAILS: that is
	// the moment somebody has to decide what the header should say about it.
	stands := map[string][]string{
		"create":   {"create child datasets"},
		"snapshot": {"take, destroy or roll back"},
		"destroy":  {"take, destroy or roll back"},
		"rollback": {"roll back"},
		"list":     {"snapshot lists"},
		"capacity": {"free space"},
	}
	for _, arm := range arms {
		phrases, known := stands[arm]
		if !known {
			t.Errorf("the `%s)` arm is not described in the header, and this test does not know what "+
				"it should say. Add the arm to the header sentence and a phrase for it here — the "+
				"header claims to be EVERYTHING quince may do on that host.", arm)
			continue
		}
		for _, p := range phrases {
			if !strings.Contains(header, p) {
				t.Errorf("the header does not say %q, which is what stands for the `%s)` arm.\n"+
					"header:\n%s", p, arm, header)
			}
		}
	}
}
