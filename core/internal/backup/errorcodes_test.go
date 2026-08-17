package backup

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// `AllErrorCodes` MUST MATCH THE CONSTANT BLOCK, and this test is what keeps it true.
//
// The list exists so `qn.12`'s notifier can be total over the codes — every one routed to a push
// kind, so a failure always tells somebody. That gate is only as good as this one: if an eleventh
// code can be added without appearing in `AllErrorCodes`, the notifier stays "total" over a list
// that no longer describes reality, and the new code silently notifies nobody.
//
// IT READS THIS PACKAGE'S OWN SOURCE, because Go cannot enumerate a constant block at runtime and
// the alternative — a second hand-written list in the test — would drift in exactly the same way.
func TestAllErrorCodesMatchesTheConstantBlock(t *testing.T) {
	src, err := os.ReadFile("backup.go")
	if err != nil {
		t.Fatalf("read backup.go: %v", err)
	}
	// Scope the scan to the declared block, so an unrelated `Err… = "…"` elsewhere in the file
	// cannot make this fail for the wrong reason.
	const marker = "// Error codes surfaced on Job.error"
	i := strings.Index(string(src), marker)
	if i < 0 {
		t.Fatalf("the error-code block's comment has moved; this test locates the block by it")
	}
	rest := string(src)[i:]
	end := strings.Index(rest, "\n)")
	if end < 0 {
		t.Fatalf("could not find the end of the error-code const block")
	}
	block := rest[:end]

	re := regexp.MustCompile(`Err[A-Za-z]+\s*=\s*"([a-z_]+)"`)
	var declared []string
	for _, m := range re.FindAllStringSubmatch(block, -1) {
		declared = append(declared, m[1])
	}
	if len(declared) == 0 {
		t.Fatalf("found no codes in the block; the scan is broken, not the list")
	}

	listed := map[string]bool{}
	for _, c := range AllErrorCodes() {
		listed[c] = true
	}
	for _, c := range declared {
		if !listed[c] {
			t.Errorf("error code %q is declared but missing from AllErrorCodes() — every consumer "+
				"that claims to be total over the codes silently is not, for this one", c)
		}
	}
	if len(AllErrorCodes()) != len(declared) {
		t.Errorf("AllErrorCodes() has %d entries and the block declares %d — a stale entry is as "+
			"wrong as a missing one, because a consumer will route a code that cannot occur",
			len(AllErrorCodes()), len(declared))
	}
}
