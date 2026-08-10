//go:build lab

// The gate-12c seed harness — NOT compiled or run in CI (build tag `lab`).
//
// quince#518 asked whether the hardlink tier could ever be real, and gate 12c is what answered it.
// A hardlink seed aliases `working/<udid>` to the committed `latest/`; the question was whether an
// `idevicebackup2` write reaches THROUGH that alias. It does not — the tool unlinks before it
// creates — so the seed is no longer downgraded to copy, and this harness is what measured that.
//
// IT IS KEPT RATHER THAN DELETED WITH THE DOWNGRADE, because the safety property is a fact about
// the WRITER rather than about quince: it can regress without a line of this repository changing.
// Re-run this whenever `LIBIMOBILEDEVICE_REF` moves, and re-run the diff that goes with it — seed a
// tree, take a manifest of the committed one, run a real incremental, and check the committed
// manifest is unchanged.
//
// It seeds DST from SRC with a caller-chosen strategy, using clonetree itself rather than `cp -al`,
// so what is under test is the code that ships rather than an approximation of it.
//
//	go test -c -tags lab -o /tmp/seed ./internal/storage/clonetree
//	QUINCE_LAB_SEED_SRC=<committed latest/> QUINCE_LAB_SEED_DST=<fresh working/> \
//	  QUINCE_LAB_SEED_STRATEGY=hardlink /tmp/seed -test.run TestLabSeed -test.v
//
// It hardcodes NO infrastructure (privacy rule): every path comes from the environment. It REFUSES
// a non-empty destination, because seeding onto an existing tree measures nothing.
package clonetree_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

func TestLabSeed(t *testing.T) {
	src := os.Getenv("QUINCE_LAB_SEED_SRC")
	dst := os.Getenv("QUINCE_LAB_SEED_DST")
	if src == "" || dst == "" {
		t.Skip("set QUINCE_LAB_SEED_SRC and QUINCE_LAB_SEED_DST")
	}
	var strategy clonetree.Strategy
	switch s := os.Getenv("QUINCE_LAB_SEED_STRATEGY"); s {
	case "hardlink":
		strategy = clonetree.Hardlink
	case "reflink":
		strategy = clonetree.Reflink
	case "copy", "":
		strategy = clonetree.Copy
	default:
		t.Fatalf("QUINCE_LAB_SEED_STRATEGY=%q — want hardlink|reflink|copy", s)
	}

	// A NON-EMPTY DESTINATION MEASURES NOTHING: the seed would land on top of a tree whose
	// provenance nobody knows, and every later assertion about sharing would be about that tree.
	if entries, err := os.ReadDir(dst); err == nil && len(entries) > 0 {
		t.Fatalf("%s is not empty (%d entries) — seed into a fresh directory", dst, len(entries))
	}

	if err := clonetree.Clone(dst, src, strategy); err != nil {
		t.Fatalf("seed %s -> %s with %s: %v", src, dst, strategy, err)
	}

	// Report what the seed actually produced: how many destination files SHARE an inode with their
	// source (the alias gate 12c is about) and how many were copied. Since MutatesInPlace was retired
	// (quince#518) a hardlink seed should report copied=0 — a non-zero count means some class is
	// escaping the link path and paying copy cost, which is the defect that list used to cause.
	var linked, copied, missing int
	err := filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil || !fi.Mode().IsRegular() {
			return nil //nolint:nilerr // unreadable entries are counted by the destination walk
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		di, err := os.Stat(filepath.Join(dst, rel))
		if err != nil {
			missing++
			return nil
		}
		if os.SameFile(fi, di) {
			linked++
		} else {
			copied++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", src, err)
	}
	t.Logf("seeded %s with strategy=%s\n  aliased (same inode): %d\n  copied (independent): %d\n  missing: %d",
		dst, strategy, linked, copied, missing)
	if strategy == clonetree.Hardlink && linked == 0 {
		t.Fatal("a hardlink seed produced no aliased files — nothing for gate 12c to examine")
	}
	fmt.Printf("SEED_RESULT strategy=%s aliased=%d copied=%d missing=%d\n", strategy, linked, copied, missing)
}
