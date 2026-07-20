package backup

import (
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// The liveness sampler judges a running backup by ACTIVITY, not output silence (design §4): the
// lab proved multi-minute output silence is normal while the tree still churns. Each tick it takes
// a cheap fingerprint of the work tree (top-level entry count, dir mtime, Manifest.db size/mtime)
// plus whether any output arrived, and stages active → silent_but_connected → suspected_stall →
// (timeout kill). waiting_for_passcode PAUSES the clock (the user may take minutes). It also runs
// the A3 free-space watch: a disk_low warning when the target fs drops below the floor — surfaced,
// never a silent kill.
type sampler struct {
	cfg       Config
	treeDir   string
	freeSpace func(string) (uint64, error)

	lastFP     treeFingerprint
	lastChange time.Time
	started    bool // seen the first sign of life? no idle accrues before then (startup grace)
	warnedLow  bool
}

type treeFingerprint struct {
	count         int
	dirMtime      int64
	manifestSize  int64
	manifestMtime int64
}

type diskLowInfo struct{ free uint64 }

func newSampler(cfg Config, treeDir string, freeSpace func(string) (uint64, error), now time.Time) *sampler {
	return &sampler{cfg: cfg, treeDir: treeDir, freeSpace: freeSpace, lastChange: now,
		lastFP: cheapFingerprint(treeDir)}
}

// sample examines the tree since the last tick. paused = waiting_for_passcode (clock frozen);
// outputSince = output arrived since the last tick. It returns the staged liveness, whether the
// zero-activity timeout was reached (→ kill), and a disk-low note the first time the floor is hit.
func (s *sampler) sample(now time.Time, paused, outputSince bool) (liveness string, killTimeout bool, low *diskLowInfo) {
	fp := cheapFingerprint(s.treeDir)

	if s.freeSpace != nil && !s.warnedLow {
		if free, err := s.freeSpace(s.treeDir); err == nil && free < s.cfg.DiskLowFreeBytes {
			s.warnedLow = true
			low = &diskLowInfo{free: free}
		}
	}

	active := outputSince || fp != s.lastFP
	// Grace: accrue no idle before the FIRST sign of life (a re-exec / process startup can take
	// longer than a short timeout), or while paused for the passcode, or whenever active.
	if !s.started || paused || active {
		if active {
			s.started = true
		}
		s.lastChange = now
		s.lastFP = fp
		return LivenessActive, false, low
	}

	idle := now.Sub(s.lastChange)
	switch {
	case idle >= s.cfg.LivenessTimeout:
		return LivenessSuspectedStall, true, low
	case idle >= s.cfg.LivenessTimeout/2:
		return LivenessSuspectedStall, false, low
	case idle >= s.cfg.LivenessTimeout/6:
		return LivenessSilentConnected, false, low
	default:
		return LivenessActive, false, low
	}
}

// cheapFingerprint is a few stats, never a full tree walk (perf budget): it changes when
// idevicebackup2 adds/removes a top-level entry or updates Manifest.db — enough to tell a
// churning tree (still alive) from a frozen one (dead transport).
func cheapFingerprint(dir string) treeFingerprint {
	var fp treeFingerprint
	if fi, err := os.Stat(dir); err == nil {
		fp.dirMtime = fi.ModTime().UnixNano()
	}
	if entries, err := os.ReadDir(dir); err == nil {
		fp.count = len(entries)
	}
	if fi, err := os.Stat(filepath.Join(dir, "Manifest.db")); err == nil {
		fp.manifestSize = fi.Size()
		fp.manifestMtime = fi.ModTime().UnixNano()
	}
	return fp
}

// statfsFree returns the bytes available to an unprivileged writer on path's filesystem. Used by
// the A3 free-space watch (preflight + sampler). unix.Statfs works on both linux and darwin.
func statfsFree(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
