package storage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// EVERY PROBE REASON NAMES THE PATH IT PROBED (quince#514).
//
// All four strings in `probeNamespace` hardcoded the literal `/backups` while the function took
// the real root as a parameter. That was TRUE while there was exactly one storage and it was
// always `/backups`; quince#473 made storage plural and it became a claim about the wrong disk.
// Measured on the staging stand: a second storage at `/backups-usb` reported *"probe passed on
// /backups"*, naming a different and healthy disk two lines above it in the same startup log.
//
// THE ROOT HERE IS `t.TempDir()`, AND THAT IS THE POINT rather than convenience. A fixture at
// `/backups-usb` — the path the hardware run actually used — has `/backups` as a PREFIX, so a
// reason still saying `/backups` would be a substring of it and a careless assertion could pass
// either way round. A temp dir shares no prefix with the old literal, so the assertion can only
// pass if the path was genuinely substituted.
func TestProbeReasonNamesTheRootItProbed(t *testing.T) {
	root := t.TempDir()

	backend, reason := probeNamespace(root)
	if backend == "" {
		t.Fatal("probe returned no backend")
	}
	if !strings.Contains(reason, root) {
		t.Errorf("reason does not name the root it probed:\n  root   = %s\n  reason = %s", root, reason)
	}
	// The old literal must be GONE, not merely accompanied — a reason carrying both paths is the
	// same defect with more words.
	if strings.Contains(reason, "/backups") && !strings.Contains(root, "/backups") {
		t.Errorf("reason still carries the hardcoded /backups: %s", reason)
	}
}

// The FAILURE branch is the one where naming the wrong directory costs most: it is what an
// operator debugs a permissions problem with.
//
// IT MAKES MkdirAll FAIL WITH A FILE IN THE PATH, not with permissions — the suite runs as root in
// the toolchain container, and root ignores mode bits, so `chmod 0500` on a parent left MkdirAll
// succeeding and the test asserting nothing. A non-directory path component returns ENOTDIR for
// every uid.
func TestProbeReasonNamesTheRootWhenItCannotCreateIt(t *testing.T) {
	parent := t.TempDir()
	blocker := filepath.Join(parent, "a-file-not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}
	root := filepath.Join(blocker, "storage-root")

	backend, reason := probeNamespace(root)
	if backend != BackendCopy {
		t.Errorf("backend = %q, want copy when the root cannot be created", backend)
	}
	if !strings.Contains(reason, root) {
		t.Errorf("the MkdirAll failure does not name the path:\n  root   = %s\n  reason = %s", root, reason)
	}
}

// THE BEHAVIOURAL TEST ABOVE COVERS ONE BRANCH — whichever the test filesystem happens to take —
// and that is not enough, which I found by mutation: reverting the FICLONE string to its hardcoded
// form left the suite GREEN, because the temp dir is not reflink-capable and that branch never
// runs. Four of the five instances were unasserted.
//
// So this reads the source. A source assertion is normally a poor substitute for a behavioural
// one, and here it is the right tool: the defect IS a literal in a string, the branches are
// selected by the filesystem rather than by the caller, and no fixture can make one runner take
// all three. It is also exactly the check that would have caught the original — all five instances
// at once, in the file they live in.
func TestNoProbeReasonHardcodesAPath(t *testing.T) {
	src, err := os.ReadFile("probe.go")
	if err != nil {
		t.Fatalf("read probe.go: %v", err)
	}
	for i, line := range strings.Split(string(src), "\n") {
		// Only string LITERALS matter. A comment may name `/backups` — several do, describing the
		// history — and the fix is about what the daemon SAYS, not what the source discusses.
		code := line
		if c := strings.Index(code, "//"); c >= 0 {
			code = code[:c]
		}
		if !strings.Contains(code, "\"") {
			continue
		}
		if strings.Contains(code, "/backups") {
			t.Errorf("probe.go:%d hardcodes a path in a string literal — a reason must name the root "+
				"it was passed (quince#514):\n  %s", i+1, strings.TrimSpace(line))
		}
	}
}
