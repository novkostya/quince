//go:build linux

package clonetree

import (
	"fmt"
	"os"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Sharing is what a filesystem answers when asked whether a file's blocks are shared with
// another file — which is the property the reflink backend is CHOSEN for, and the property the
// old ReflinkProbe never asked about (quince#747).
//
// It is a tri-state rather than a bool because "the filesystem cannot tell you" is a real and
// common third answer — ZFS implements no FIEMAP at all — and collapsing it into either bool is
// a lie in one direction or the other.
type Sharing int

const (
	// SharingUnknown — the filesystem did not answer: no FIEMAP support, or it mapped no real
	// extents (inline/tail-packed data, delayed allocation, a hole).
	SharingUnknown Sharing = iota
	// SharingShared — every mapped extent carries FIEMAP_EXTENT_SHARED.
	SharingShared
	// SharingUnshared — extents were mapped and at least one is this file's alone. A full copy.
	SharingUnshared
)

func (s Sharing) String() string {
	switch s {
	case SharingShared:
		return "shared"
	case SharingUnshared:
		return "unshared"
	default:
		return "unknown"
	}
}

// FS_IOC_FIEMAP and the flags it uses. `golang.org/x/sys/unix` v0.47.0 defines NEITHER the ioctl
// number nor the structs (checked in the module cache, not remembered), so they are written out
// here against <linux/fiemap.h>.
//
// fsIocFiemap is _IOWR('f', 11, struct fiemap): dir=3, size=32 (the header struct), type=0x66,
// nr=11 → 0xC020660B.
const (
	fsIocFiemap = 0xC020660B

	// fiemapFlagSync makes the kernel sync the file before mapping. Without it a freshly
	// written file is still in delayed allocation and maps NOTHING, which would make the probe
	// answer SharingUnknown on every filesystem including the ones that work.
	fiemapFlagSync = 0x0001

	// The extent flags, transcribed from <linux/fiemap.h>. TestFiemapConstantsMatchTheHeader
	// carries the header excerpt they were transcribed from, so a wrong one is checkable by eye
	// against a source that is not this file.
	//
	// fiemapExtentDataInline read 0x0008 until the architect caught it on review — 0x0008 is
	// ENCODED and DATA_INLINE is 0x0200, and the mistake was wrong in both directions at once.
	// The inline guard this file's comment is written for was INERT: an inline extent fell
	// through to the SHARED test, carried no SHARED flag, and returned "unshared" — the exact
	// false negative the guard exists to prevent, masked only by reflinkProbeSize. And compressed
	// btrfs, which sets ENCODED on every extent, was silently excluded from measurement.
	fiemapExtentLast       = 0x0001
	fiemapExtentUnknown    = 0x0002
	fiemapExtentDelalloc   = 0x0004
	fiemapExtentEncoded    = 0x0008
	fiemapExtentDataInline = 0x0200
	fiemapExtentDataTail   = 0x0400
	fiemapExtentShared     = 0x2000
)

// notABlockMapping is the set of flags meaning "nothing can be concluded about sharing from this
// extent". It is named so that what is IN it is reviewable, because the interesting decision is
// what is OUT.
//
// ENCODED IS DELIBERATELY EXCLUDED, and that is measured rather than reasoned. ENCODED means the
// physical offset cannot be used to read the data — on btrfs it marks a compressed extent — and it
// says nothing about sharing, which the kernel reports independently. Measured on the lab rig with
// btrfs remounted `compress=zstd`: a `cp --reflink=always` clone comes back `encoded,shared`, and a
// plain copy of the same file comes back `encoded` alone. Excluding ENCODED would turn a correct,
// observable answer into SharingUnknown and warn that the space saving is UNVERIFIED on one of the
// commonest btrfs configurations there is.
//
// DATA_INLINE and DATA_TAIL are both tail-packing — the bytes live in metadata, or share a block
// with another file — so there is no extent that could be shared and the flag is the whole answer.
const notABlockMapping = fiemapExtentUnknown | fiemapExtentDelalloc |
	fiemapExtentDataInline | fiemapExtentDataTail

// fiemapHeader mirrors `struct fiemap` — 32 bytes, 8-aligned.
type fiemapHeader struct {
	start         uint64
	length        uint64
	flags         uint32
	mappedExtents uint32
	extentCount   uint32
	reserved      uint32
}

// fiemapExtent mirrors `struct fiemap_extent` — 56 bytes, 8-aligned.
type fiemapExtent struct {
	logical    uint64
	physical   uint64
	length     uint64
	reserved64 [2]uint64
	flags      uint32
	reserved   [3]uint32
}

// fiemapExtentSlots is how many extents one ioctl asks for. The probe file is written in a
// single stream so a handful is the realistic count; the answer only needs enough extents to be
// representative, and the LAST-flag check below reports when it was not.
const fiemapExtentSlots = 32

type fiemapQuery struct {
	hdr     fiemapHeader
	extents [fiemapExtentSlots]fiemapExtent
}

// extentSharing asks the filesystem whether path's mapped extents are shared with another file.
//
// The returned error is INFORMATIONAL, never fatal: a filesystem that refuses FS_IOC_FIEMAP is
// answering SharingUnknown, which is a legitimate answer, and the error carries why so a caller
// can put it in a reason string. Only SharingUnknown is ever returned with a non-nil error.
func extentSharing(path string) (Sharing, error) {
	f, err := os.Open(path)
	if err != nil {
		return SharingUnknown, err
	}
	defer func() { _ = f.Close() }()

	q := fiemapQuery{hdr: fiemapHeader{
		start:       0,
		length:      ^uint64(0),
		flags:       fiemapFlagSync,
		extentCount: fiemapExtentSlots,
	}}
	_, _, errno := unix.Syscall(unix.SYS_IOCTL, f.Fd(), fsIocFiemap, uintptr(unsafe.Pointer(&q)))
	runtime.KeepAlive(f)
	if errno != 0 {
		return SharingUnknown, fmt.Errorf("ioctl FS_IOC_FIEMAP on %s: %w", path, errno)
	}

	n := q.hdr.mappedExtents
	if n == 0 {
		return SharingUnknown, fmt.Errorf("%s: the filesystem mapped no extents", path)
	}
	if n > fiemapExtentSlots {
		n = fiemapExtentSlots
	}
	sawLast := false
	for i := uint32(0); i < n; i++ {
		fl := q.extents[i].flags
		// Nothing can be concluded about sharing from these — see notABlockMapping for what is in
		// the set and, more importantly, what is out. btrfs INLINES a small file into its
		// metadata and reports exactly this: measured on the lab rig, an 8-byte file (the size
		// the old probe used) comes back `inline` with no `shared` flag even when it was
		// genuinely cloned.
		if fl&notABlockMapping != 0 {
			return SharingUnknown, fmt.Errorf("%s: extent %d is not a block mapping (flags 0x%x)", path, i, fl)
		}
		if fl&fiemapExtentShared == 0 {
			return SharingUnshared, nil
		}
		if fl&fiemapExtentLast != 0 {
			sawLast = true
		}
	}
	if !sawLast {
		// Every extent we saw was shared, but we did not see the one flagged LAST — so this is a
		// prefix, and reporting a whole-file property from a prefix would be worse than saying so.
		// Worded without asserting a count: the ordinary cause is more extents than slots, but a
		// filesystem returning fewer than the slots without ever setting LAST lands here too, and
		// the old message claimed "more than 32 extents" while n was under 32.
		return SharingUnknown, fmt.Errorf("%s: the extent list ended without FIEMAP_EXTENT_LAST (%d of at most %d slots); sharing observed only on what was returned", path, n, fiemapExtentSlots)
	}
	return SharingShared, nil
}
