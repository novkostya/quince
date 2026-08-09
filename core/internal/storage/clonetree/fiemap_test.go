package clonetree

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE REGRESSION FOR quince#747 — hermetic, and it must stay that way.
//
// The defect is that a FICLONE which SUCCEEDS WITHOUT SHARING was accepted as a reflink. No
// filesystem available to this project produces that artifact on demand, so the branch is tested
// where it is decided: reflinkVerdict, which is pure for exactly this reason. Every row below runs
// in CI, on any filesystem, forever.
func TestReflinkVerdict(t *testing.T) {
	why := errors.New("no FIEMAP here")
	for _, c := range []struct {
		name        string
		sharing     Sharing
		independent bool
		want        ReflinkResult
	}{
		// THE DEFECT. Before quince#747 this was indistinguishable from the row below it, and
		// selected the reflink backend — after which every version is a full physical copy and
		// nothing says so.
		{"a clone that shares nothing is a full copy", SharingUnshared, true, ReflinkUnsupported},
		{"a clone that shares its extents is a reflink", SharingShared, true, ReflinkSharing},
		{"sharing unobservable, clone independent", SharingUnknown, true, ReflinkSharingUnverifiable},
		// Independence still rules out a hardlink, and it outranks an unobservable answer.
		{"not independent, sharing unobservable", SharingUnknown, false, ReflinkUnsupported},
		{"not independent, sharing observed", SharingShared, false, ReflinkUnsupported},
		// Unshared decides on its own: a full copy is not made acceptable by being independent,
		// which is the confusion the old probe was built on.
		{"not independent and unshared", SharingUnshared, false, ReflinkUnsupported},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, detail := reflinkVerdict(c.sharing, why, c.independent)
			if got != c.want {
				t.Fatalf("verdict = %d, want %d (%s)", got, c.want, detail)
			}
			if detail == "" {
				t.Fatal("verdict carries no reason — the reason is what an operator reads on the storage card")
			}
		})
	}
}

// The verdict's reason must never claim sharing it did not observe. quince#747's closing note:
// "a probe that claimed sharing while testing independence would be worse than the present
// state, which is at least honestly named."
func TestUnverifiableVerdictDoesNotClaimSharing(t *testing.T) {
	_, detail := reflinkVerdict(SharingUnknown, errors.New("no FIEMAP here"), true)
	if !strings.Contains(detail, "does not report extent sharing") {
		t.Fatalf("the unverifiable reason must say sharing was not observed, got: %s", detail)
	}
	if strings.Contains(detail, "FIEMAP_EXTENT_SHARED") {
		t.Fatalf("the unverifiable reason claims an observation it did not make: %s", detail)
	}
}

// The filesystem-dependent half. It needs a FIEMAP-capable filesystem and SKIPS where there is
// none — which includes both places this project's gates run (the toolchain container's /tmp is
// overlayfs; the runner box is ZFS). The proof on real filesystems is TestLabFilesystemMatrix on
// the lab rig's btrfs and XFS tiers; this is here so a CI runner that does have FIEMAP uses it.
//
// The defect was that ReflinkProbe accepted a full copy: it wrote 8 bytes, cloned, rewrote the
// clone and checked the source was intact — a test two INDEPENDENT files pass identically. This
// test builds exactly what a FICLONE that succeeded without sharing would leave behind (a plain
// byte copy) and asserts BOTH halves at once:
//
//   - the old criterion (independence) still passes on it — so the old probe really could not
//     tell this from a clone;
//   - the new criterion (extent sharing) rejects it.
//
// It skips only where the filesystem cannot answer at all, which is stated rather than silently
// passed.
func TestAPlainCopyPassesIndependenceAndFailsSharing(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	data := probePattern(reflinkProbeSize)
	if err := os.WriteFile(src, data, 0o600); err != nil {
		t.Fatalf("seed src: %v", err)
	}
	// A USERSPACE byte copy, deliberately NOT clonetree's own copyFile.
	//
	// copyFile is io.Copy between two *os.File, and Go's ReadFrom for that pair issues
	// copy_file_range(2) — which on btrfs and XFS SERVER-SIDE REFLINKS. Measured on the lab rig:
	// this test written against copyFile fails on both reflink tiers, reporting the "copy" as
	// shared, because it genuinely is one. The fixture has to be a real full copy or it is not
	// the artifact the defect is about.
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("write dst: %v", err)
	}

	sharing, why := extentSharing(dst)
	if sharing == SharingUnknown {
		t.Skipf("this filesystem does not report extent sharing (%v) — the sharing assertion runs "+
			"on the lab rig's btrfs/XFS tiers, and on any FIEMAP-capable CI filesystem", why)
	}
	if sharing != SharingUnshared {
		t.Fatalf("a plain copy reports sharing = %s, want unshared", sharing)
	}

	// The old probe's whole test, on the same pair — it passes, which is the defect.
	if err := os.WriteFile(dst, []byte("BBBBBBBB"), 0o600); err != nil {
		t.Fatalf("rewrite dst: %v", err)
	}
	got, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("re-read src: %v", err)
	}
	if len(got) != reflinkProbeSize {
		t.Fatal("rewriting the copy changed the source — the fixture is not what this test thinks it is")
	}
}

// A real reflink must be reported SHARED wherever the filesystem answers at all. The mirror of
// the test above: together they are the discrimination the old probe could not make.
func TestAReflinkIsReportedShared(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, probePattern(reflinkProbeSize), 0o600); err != nil {
		t.Fatalf("seed src: %v", err)
	}
	if err := reflinkFile(dst, src); err != nil {
		t.Skipf("FICLONE unsupported on the test filesystem: %v", err)
	}
	sharing, why := extentSharing(dst)
	switch sharing {
	case SharingShared:
	case SharingUnknown:
		t.Skipf("FICLONE works here but the filesystem does not report sharing (%v) — this is the "+
			"ReflinkSharingUnverifiable state, measured on ZFS", why)
	default:
		t.Fatalf("a FICLONE clone reports sharing = %s, want shared", sharing)
	}
}

// The probe file must be big enough that no filesystem stores it inline: btrfs inlines a small
// file into its metadata, and an inline extent carries no sharing flag even for a real clone.
// Guarded as a constant assertion because the failure it prevents is a FALSE NEGATIVE — reflink
// silently downgraded to hardlink on btrfs — which no green gate would show.
func TestProbeSizeIsAboveAnyInlineThreshold(t *testing.T) {
	if reflinkProbeSize < 64<<10 {
		t.Fatalf("reflinkProbeSize = %d — too small; btrfs max_inline defaults to 2048 and the "+
			"margin exists so no tail-packing filesystem can inline the probe file", reflinkProbeSize)
	}
}

// The probe pattern must be neither a hole nor a single repeated byte: a sparse or compressing
// filesystem would map no extents for either, and the probe would answer "unknown" everywhere.
func TestProbePatternIsNotTrivial(t *testing.T) {
	b := probePattern(4096)
	if len(b) != 4096 {
		t.Fatalf("probePattern returned %d bytes, want 4096", len(b))
	}
	seen := map[byte]bool{}
	for _, c := range b {
		seen[c] = true
	}
	if len(seen) < 64 {
		t.Fatalf("probe pattern uses only %d distinct byte values — too compressible to be sure of getting real extents", len(seen))
	}
	// Deterministic: the same probe must behave the same on every run.
	if string(probePattern(4096)) != string(b) {
		t.Fatal("probePattern is not deterministic")
	}
}

func TestSharingString(t *testing.T) {
	for s, want := range map[Sharing]string{SharingShared: "shared", SharingUnshared: "unshared", SharingUnknown: "unknown", Sharing(99): "unknown"} {
		if got := s.String(); got != want {
			t.Errorf("Sharing(%d).String() = %q, want %q", s, got, want)
		}
	}
}
