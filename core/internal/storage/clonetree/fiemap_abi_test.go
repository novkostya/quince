//go:build linux

package clonetree

import (
	"testing"
	"unsafe"
)

// THE ONE CLASS OF BUG IN fiemap_linux.go THAT NO AMOUNT OF FILESYSTEM TESTING REACHES.
//
// quince#787's review found `fiemapExtentDataInline = 0x0008`, which is ENCODED. Six real
// filesystem tiers passed anyway: the btrfs tier is `compress=none` so ENCODED never appeared, and
// the 1 MiB probe file is far too large to be inlined so DATA_INLINE never appeared either. The two
// configurations that would have exposed either half were, respectively, configured out and sized
// out. `reflinkVerdict` is pure and takes a Sharing already decided, so it structurally cannot
// catch it. Nothing else in the package referenced a single one of these constants.
//
// So this test exists to be read against a source that is not this package. The header block below
// is `/usr/include/linux/fiemap.h`, quoted verbatim from a Debian 13 box (kernel 6.12), and the
// table asserts our transcription of it. It is deliberately a second copy of the numbers: the value
// is that a reader can diff two independent transcriptions, which is exactly the check that failed.
//
//	#define FIEMAP_FLAG_SYNC		0x00000001 /* sync file data before map */
//	#define FIEMAP_EXTENT_LAST		0x00000001 /* Last extent in file. */
//	#define FIEMAP_EXTENT_UNKNOWN		0x00000002 /* Data location unknown. */
//	#define FIEMAP_EXTENT_DELALLOC		0x00000004 /* Location still pending. */
//	#define FIEMAP_EXTENT_ENCODED		0x00000008 /* Data can not be read while fs is unmounted */
//	#define FIEMAP_EXTENT_DATA_ENCRYPTED	0x00000080 /* Data is encrypted by fs. */
//	#define FIEMAP_EXTENT_NOT_ALIGNED	0x00000100 /* Extent offsets may not be block aligned. */
//	#define FIEMAP_EXTENT_DATA_INLINE	0x00000200 /* Data mixed with metadata. */
//	#define FIEMAP_EXTENT_DATA_TAIL		0x00000400 /* Multiple files in block. */
//	#define FIEMAP_EXTENT_UNWRITTEN		0x00000800 /* Space allocated, but no data */
//	#define FIEMAP_EXTENT_MERGED		0x00001000 /* File does not natively support extents */
//	#define FIEMAP_EXTENT_SHARED		0x00002000 /* Space shared with other files. */
func TestFiemapConstantsMatchTheHeader(t *testing.T) {
	for _, c := range []struct {
		name string
		got  uint32
		want uint32
	}{
		{"FIEMAP_FLAG_SYNC", fiemapFlagSync, 0x00000001},
		{"FIEMAP_EXTENT_LAST", fiemapExtentLast, 0x00000001},
		{"FIEMAP_EXTENT_UNKNOWN", fiemapExtentUnknown, 0x00000002},
		{"FIEMAP_EXTENT_DELALLOC", fiemapExtentDelalloc, 0x00000004},
		{"FIEMAP_EXTENT_ENCODED", fiemapExtentEncoded, 0x00000008},
		{"FIEMAP_EXTENT_DATA_INLINE", fiemapExtentDataInline, 0x00000200},
		{"FIEMAP_EXTENT_DATA_TAIL", fiemapExtentDataTail, 0x00000400},
		{"FIEMAP_EXTENT_SHARED", fiemapExtentShared, 0x00002000},
	} {
		if c.got != c.want {
			t.Errorf("%s = 0x%04x, the header says 0x%04x", c.name, c.got, c.want)
		}
	}
}

// The membership decision, asserted rather than left in a comment — because the bug was that a flag
// the comment discussed was not the flag the code masked, and the comment read correctly either way.
func TestNotABlockMappingHoldsTheIntendedSet(t *testing.T) {
	if notABlockMapping&fiemapExtentEncoded != 0 {
		t.Error("ENCODED is masked out. It marks a compressed extent on btrfs and says nothing " +
			"about sharing — measured on the lab rig, a reflink there is `encoded,shared` and a " +
			"copy is `encoded` alone. Excluding it warns UNVERIFIED on a filesystem that answers.")
	}
	if notABlockMapping&fiemapExtentShared != 0 {
		t.Error("SHARED is masked out — the probe would then never observe the property it exists for")
	}
	if notABlockMapping&fiemapExtentLast != 0 {
		t.Error("LAST is masked out — every single-extent file would report unknown")
	}
	for _, w := range []struct {
		name string
		flag uint32
	}{
		{"UNKNOWN", fiemapExtentUnknown},
		{"DELALLOC", fiemapExtentDelalloc},
		{"DATA_INLINE", fiemapExtentDataInline},
		{"DATA_TAIL", fiemapExtentDataTail},
	} {
		if notABlockMapping&w.flag == 0 {
			t.Errorf("%s is NOT masked out — an extent carrying it is not a block mapping, so a "+
				"missing SHARED flag on it would be read as 'this is a full copy'", w.name)
		}
	}
}

// The ioctl number and both struct layouts, measured against the same header on the same box:
//
//	sizeof(struct fiemap)        = 32     offsetof fm_flags/fm_mapped_extents/fm_extent_count = 16/20/24
//	sizeof(struct fiemap_extent) = 56     offsetof fe_flags = 40
//	FS_IOC_FIEMAP                = 0xC020660B
//
// Go's layout has to agree byte for byte or the kernel writes into the wrong fields, and it does so
// silently — the ioctl returns 0 and the extent count is read out of whatever landed at offset 20.
func TestFiemapStructLayoutMatchesTheKernelABI(t *testing.T) {
	var h fiemapHeader
	var e fiemapExtent
	var q fiemapQuery
	for _, c := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"sizeof(struct fiemap)", unsafe.Sizeof(h), 32},
		{"offsetof(fiemap, fm_flags)", unsafe.Offsetof(h.flags), 16},
		{"offsetof(fiemap, fm_mapped_extents)", unsafe.Offsetof(h.mappedExtents), 20},
		{"offsetof(fiemap, fm_extent_count)", unsafe.Offsetof(h.extentCount), 24},
		{"sizeof(struct fiemap_extent)", unsafe.Sizeof(e), 56},
		{"offsetof(fiemap_extent, fe_flags)", unsafe.Offsetof(e.flags), 40},
		// fm_extents[0] must begin exactly where the header ends — the kernel writes the array
		// straight after the 32-byte header, with no padding of its own.
		{"offsetof(fiemapQuery, extents)", unsafe.Offsetof(q.extents), 32},
		{"FS_IOC_FIEMAP", uintptr(fsIocFiemap), 0xC020660B},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d (0x%X), the kernel ABI says %d (0x%X)", c.name, c.got, c.got, c.want, c.want)
		}
	}
}
