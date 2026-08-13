package storage

import (
	"strings"
	"testing"
)

// THE PLACEHOLDER MUST EXIST IN THE EMBEDDED SCRIPT, and this test is the reason RenderZFSHelper can
// afford to be three lines.
//
// If somebody renames the `PARENT=` line in the helper, `strings.Replace` finds nothing and returns
// the script UNCHANGED — quince would then serve an operator a helper still pointing at
// `pool/path/to/iphone-backup`. They would install it, and every backup would go somewhere that is
// not theirs, or nowhere. Nothing about that failure looks wrong on screen: it is a valid script.
//
// So the coupling is asserted here rather than discovered there. This is a build-time guard on a
// runtime refusal — RenderZFSHelper also returns ErrHelperPlaceholder — because the cost of the two
// is not comparable: red CI costs a commit, a wrong PARENT costs somebody's backups.
func TestZFSHelperPlaceholderExists(t *testing.T) {
	if !strings.Contains(zfsHelperScript, zfsHelperPlaceholder) {
		t.Fatalf("the embedded helper no longer contains %q.\n"+
			"If the script's PARENT line changed shape, update zfsHelperPlaceholder to match — do "+
			"NOT delete this test. Without the substitution quince serves a helper pointing at the "+
			"placeholder dataset, which is a valid script that backs up to the wrong place.",
			zfsHelperPlaceholder)
	}
	if n := strings.Count(zfsHelperScript, zfsHelperPlaceholder); n != 1 {
		t.Fatalf("the placeholder appears %d times, want exactly 1 — RenderZFSHelper replaces one", n)
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

func TestRenderZFSHelperSubstitutesTheParent(t *testing.T) {
	got, err := RenderZFSHelper("tank/backups/iphone")
	if err != nil {
		t.Fatalf("RenderZFSHelper: %v", err)
	}
	if !strings.Contains(got, `PARENT="tank/backups/iphone"`) {
		t.Error("the operator's dataset is not in the rendered script")
	}
	if strings.Contains(got, "pool/path/to/iphone-backup") {
		t.Error("the placeholder survived — an operator would install a script pointing at it")
	}
	// The rest of the script is untouched: the guards are what make the helper a boundary.
	for _, want := range []string{"capacity)", "rollback)", `case "$op" in`, "exit 1"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendering dropped %q from the helper", want)
		}
	}
}

// A NAME THAT COULD BREAK OUT OF THE QUOTES IS REFUSED, NOT ESCAPED.
//
// This is the sharpest edge in this file: the value is interpolated into a double-quoted assignment
// in a script the operator runs AS ROOT ON THE STORAGE HOST. `tank"; rm -rf /; x="` would close the
// quote and the remainder would be code. Refusing is right rather than escaping — every legal ZFS
// dataset name already matches the pattern, so nothing valid is lost, and an escaping routine is a
// thing that can have a bug.
func TestRenderZFSHelperRefusesUnsafeNames(t *testing.T) {
	for _, bad := range []string{
		`tank"; rm -rf /; x="`,
		"tank/backups; rm -rf /",
		"tank/backups $(id)",
		"tank/backups`id`",
		"tank/backups\nPARENT=\"other\"",
		"",
		"/leading-slash",
	} {
		if _, err := RenderZFSHelper(bad); err == nil {
			t.Errorf("RenderZFSHelper(%q) was accepted — it must refuse before interpolating", bad)
		}
	}
}
