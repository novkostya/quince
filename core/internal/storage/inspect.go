package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// Inspect reports what a candidate storage path IS, without changing it. It is the add-a-storage
// probe (qn.6e): a user types a path, and quince answers before anything is declared or written.
//
// IT IS A REPORT, NOT A RESOLUTION, AND THAT IS WHY IT IS NOT Select.
//
// Select CREATES and CONSTRUCTS: probeNamespace opens with os.MkdirAll (probe.go), and every
// return path builds a live Backend. Both are correct for a storage already declared in
// config.yml and wrong behind a form, where a mistyped path would be silently created and then
// reported healthy — a worse answer than "that path does not exist".
//
// The codebase already knows this from the other side. resolveSlot (cmd/quince/live.go) makes the
// probe LAZY so a refusal never reaches it, and says: "NOBODY CREATES A STORAGE ROOT. A declared
// path must already exist." (quince#415). Inspect is the same law reached from the form: it
// refuses a path that is not there rather than manufacturing a creation moment at it.
//
// It also NEVER MINTS A MARKER. Creation stays ResolveStorage's, at the creation moment, once —
// WriteStorageMarker has exactly one non-test caller (creation.go). Creating the directory, and
// minting identity for it, are both explicit later steps and never side effects of checking.
func Inspect(path string, opt InspectOptions) Report {
	opt = opt.withDefaults()

	r := Report{Path: path, ZFS: ZFSNone}

	if path == "" || !filepath.IsAbs(path) {
		r.Outcome = InspectInvalidPath
		r.Reason = fmt.Sprintf("%q is not an absolute path", path)
		return r
	}
	r.CleanPath = filepath.Clean(path)

	fi, err := os.Stat(r.CleanPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		r.Outcome = InspectMissing
		// The container sentence belongs HERE and nowhere else: a path missing inside the
		// container is overwhelmingly a disk that was never mounted in, and this message is the
		// only thing a user is reading at the moment it happens.
		r.Reason = fmt.Sprintf("nothing exists at %s — the path must already exist, and must be "+
			"visible INSIDE the container (a bind mount or volume); quince does not create it", r.CleanPath)
		return r
	case err != nil:
		r.Outcome = InspectUnreadable
		r.Reason = fmt.Sprintf("cannot read %s: %v", r.CleanPath, err)
		return r
	case !fi.IsDir():
		r.Outcome = InspectNotDir
		r.Reason = fmt.Sprintf("%s exists but is not a directory", r.CleanPath)
		return r
	}

	// Everything below this line is about a directory that exists. Facts first, verdict last —
	// the marker and the space are reported whatever the outcome turns out to be, so a form can
	// say "this IS storage X, and the path is read-only" rather than only the second half.
	r.ZFS = inspectZFS(r.CleanPath, opt)
	r.FreeBytes, r.TotalBytes, _ = FilesystemSpace(r.CleanPath)
	r.NonEmpty = dirHasEntries(r.CleanPath)

	marker, mErr := ReadStorageMarker(r.CleanPath)
	corrupt := errors.Is(mErr, ErrStorageMarkerCorrupt)
	if mErr == nil {
		m := marker
		r.Marker = &m
	}

	if !writableProbe(r.CleanPath) {
		r.Outcome = InspectUnwritable
		r.Reason = fmt.Sprintf("%s exists but quince cannot write to it", r.CleanPath)
		return r
	}

	if corrupt {
		// Its own outcome, never folded into "no marker here". A path whose marker failed its
		// checksum is a storage quince has seen and can no longer identify; adopting it as new
		// would mint a SECOND identity for one disk. Mirrors StorageMarker.Mismatch's rule that
		// "could not determine" and "changed" are different states.
		r.Outcome = InspectCorruptMarker
		r.Reason = fmt.Sprintf("%s holds a %s that failed its checksum — quince has seen this "+
			"storage but can no longer identify it", r.CleanPath, StorageMarkerName)
		return r
	}

	if r.Marker != nil {
		// ADOPT, and the namespace probes are deliberately NOT run.
		//
		// A storage's backend is recorded at its creation moment and is IMMUTABLE thereafter; a
		// later probe that disagrees is a remount, not a re-selection (storagemarker.go). So the
		// form offers no selector here, and there is nothing for a probe to contribute — running
		// one would produce a number the UI must then be trusted to ignore.
		//
		// This is also the replug story for free: the disk you forgot, plugged back in, re-added.
		r.Outcome = InspectAdopt
		r.Backend = r.Marker.Backend
		r.BackendReason = fmt.Sprintf("recorded in %s at this storage's creation moment (%s); a "+
			"storage's backend is immutable", StorageMarkerName, r.Marker.CreatedAt)
		r.Reason = fmt.Sprintf("%s is an existing quince storage", r.CleanPath)
		return r
	}

	r.Outcome = InspectNew
	r.Backend, r.BackendReason = recommendBackend(r.CleanPath, r.ZFS)
	r.Reason = fmt.Sprintf("%s is usable and holds no quince storage yet", r.CleanPath)
	return r
}

// recommendBackend answers what backend quince WOULD choose for a path with no marker.
//
// The zfs tier comes first and is the whole reason this function is not just probeNamespace:
// Select never probes for zfs at all — wantZFS is config intent (a parent dataset or a hook being
// set), so pointing today's auto-probe at a ZFS dataset answers reflink (2.2 block cloning) or
// hardlink and never says the word zfs. A recommendation that cannot name the one backend with
// extra options is not doing the job a recommendation exists for.
func recommendBackend(dir string, z ZFSSignal) (string, string) {
	if z == ZFSPath {
		return BackendZFS, fmt.Sprintf("%s is on a ZFS filesystem (statfs f_type)", dir)
	}
	return probeNamespace(dir)
}

// writableProbe answers whether quince can write here, WITHOUT deciding a backend.
//
// Separate from the namespace probes because the adopt and refuse branches both need the answer
// and neither wants a backend. Mode bits are deliberately not consulted: under ACLs, and under the
// uid mapping of an unprivileged user namespace, they disagree with what a write actually does —
// and what a write actually does is the question.
func writableProbe(dir string) bool {
	f, err := os.CreateTemp(dir, ".quince-inspect-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// dirHasEntries reports whether anything is in dir besides quince's own storage marker.
//
// A non-empty path is REPORTED AND ALLOWED, never refused — settled here as rung-local (qn.6e
// spec, open question 2). Refusing would break the case that most needs to work: a path holding
// real backups from before storage markers existed (pre-qn.6c) has no marker and is not empty, and
// it is exactly the path an upgrading operator types. The form warns; quince does not decide for
// them that someone else's data means the disk is unusable.
func dirHasEntries(dir string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if e.Name() != StorageMarkerName && !strings.HasPrefix(e.Name(), ".quince-") {
			return true
		}
	}
	return false
}

// inspectZFS answers the three-tier zfs question. See ZFSSignal for what each tier may be used to
// say — and, more importantly, for what ZFSNone may NOT be used to say.
func inspectZFS(dir string, opt InspectOptions) ZFSSignal {
	if t, err := opt.FSType(dir); err == nil && t == zfsSuperMagic {
		return ZFSPath
	}
	if opt.KernelHasZFS() {
		return ZFSHost
	}
	return ZFSNone
}

// zfsSuperMagic is statfs(2)'s f_type for an OpenZFS filesystem.
//
// MEASURED, NOT RECALLED (qn.6e, canon's "interface facts are looked up live"). Inside the shipped
// runtime image, on a bind-mounted host directory whose filesystem is ZFS, f_type reads 0x2fc12fc1
// — while the image's own overlay root reads 0x794c7630. So the signal survives a bind mount into
// an unprivileged container and discriminates.
//
// IT IS DEFINED HERE BECAUSE golang.org/x/sys/unix DOES NOT EXPORT IT, and that is not an
// oversight in x/sys: it enumerates the magics in the kernel's own magic.h, and OpenZFS is
// out-of-tree. Verified against the pinned v0.47.0 — 0x2fc12fc1 appears nowhere in the package.
//
// The obvious grep finds ONE ZFS-shaped constant in the package and it is not this one:
// ZOSZFS_SUPER_MAGIC = 0x5A4653, IBM z/OS zFS, an unrelated filesystem on an unrelated operating
// system. REACHING FOR IT DOES NOT COMPILE — it lives in zerrors_zos_s390x.go behind
// `//go:build zos && s390x`, so on linux the identifier does not exist. The build tag is the reason
// the wrong reach is safe, which is worth more here than the warning: what a reader needs to know
// is that the compiler stops them, not that they must be careful.
//
// The measurement method is corroborated by that same package: the four filesystems used to
// validate it match its constants exactly — OVERLAYFS_SUPER_MAGIC 0x794c7630, TMPFS_MAGIC
// 0x1021994, PROC_SUPER_MAGIC 0x9fa0, SYSFS_MAGIC 0x62656572.
const zfsSuperMagic = 0x2fc12fc1

// procFilesystems is where tier 2 comes from: the running kernel's list of known filesystems. In
// hook mode the container holds no zfs userland at all, so this is the ONLY in-container signal
// that the host can do zfs — see ZFSNone for why that matters more than it looks.
const procFilesystems = "/proc/filesystems"

func statfsType(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Type), nil //nolint:unconvert // Type is int64 on amd64, int32 on some arches
}

func kernelHasZFS() bool {
	b, err := os.ReadFile(procFilesystems)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		// Fields are "nodev\tzfs" or "\text4"; match the name field exactly rather than
		// substring-matching the line, so a filesystem merely CONTAINING "zfs" cannot answer yes.
		f := strings.Fields(line)
		if len(f) > 0 && f[len(f)-1] == "zfs" {
			return true
		}
	}
	return false
}

// WantZFS reports whether a storage declaration is zfs INTENT.
//
// zfs is never probed for: it is chosen because the config says so, explicitly or by configuring a
// parent dataset or a hook. Inspect's tier 1 is the first thing in this codebase that detects zfs
// rather than being told about it, and it deliberately does not change this predicate — a storage
// on a ZFS path with no parent dataset configured still is not a zfs storage, because the backend
// has nowhere to create its per-device datasets.
//
// FACTORED HERE SO THE ADD PATH DOES NOT MAKE A THIRD COPY. It is duplicated in
// config/storagereq.go's isZFS, semantically rather than literally — that copy spells the backend
// "zfs" where this one spells it BackendZFS, so a grep for either spelling finds only one of them.
// The two packages deliberately do not import each other (see Options), so collapsing them needs a
// layering decision this function does not make.
func WantZFS(backend, parentDataset string, hookConfigured bool) bool {
	return backend == BackendZFS ||
		(backend == "auto" && (parentDataset != "" || hookConfigured))
}
