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
