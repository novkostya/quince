package storage

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// Options are the resolved config inputs the storage subsystem needs (mirrors config.Storage*,
// passed as plain fields so this package does not import config).
type Options struct {
	Backend    string // auto | zfs | reflink | hardlink | copy
	Backups    string // this storage's root (qn.6c; was QUINCE_BACKUPS)
	AppVersion string
	ZFSParent  string // storage.zfs.parent_dataset
	ZFSHookCmd string // storage.zfs.hook_cmd — the only zfs transport (quince#697)
	ZFSSeed    string // auto | reflink | copy (in-container seed strategy; hardlink never used — amendment A)
}

// Select resolves the effective backend (stack D5 auto-selection): explicit zfs intent
// (storage.backend: zfs, or auto with a parent dataset/hook configured) → zfs; an explicit
// namespace backend → that; else probe the real /backups filesystem — FICLONE clone-SHARING →
// reflink, link()+inode identity → hardlink, else copy. The choice + reason is returned for
// onboarding and logged (never silent — a copy fallback is a surfaced degraded mode).
func Select(baseCtx context.Context, opts Options, log *slog.Logger) (Backend, string, string) {
	if WantZFS(opts.Backend, opts.ZFSParent, opts.ZFSHookCmd) {
		cli := newZFSCLI(opts.ZFSParent, opts.ZFSHookCmd)
		reason := "storage.zfs configured (parent dataset / hook set)"
		if opts.Backend == BackendZFS {
			reason = "storage.backend: zfs"
		}
		log.Info("storage backend selected", "backend", BackendZFS, "reason", reason)
		return newZFSBackend(baseCtx, cli, opts.Backups, orAuto(opts.ZFSSeed), opts.AppVersion, log), BackendZFS, reason
	}

	switch opts.Backend {
	case BackendReflink, BackendHardlink, BackendCopy:
		strategy := strategyFor(opts.Backend)
		reason := "storage.backend: " + opts.Backend + " (explicit)"
		log.Info("storage backend selected", "backend", opts.Backend, "reason", reason)
		return newNamespaceBackend(opts.Backend, strategy, opts.Backups, opts.AppVersion, log), opts.Backend, reason
	}

	// auto: probe the real /backups filesystem.
	name, reason, degraded := probeNamespaceDetail(opts.Backups)
	switch {
	case name == BackendCopy:
		// A degraded mode — surface it loudly (hard rule: no silent caps/fallbacks).
		//
		// THE PATH IS AN ATTRIBUTE, not text (quince#514). This sentence hardcoded `/backups` and is
		// the highest-stakes of the five instances: it is the surfaced degraded mode, announcing a
		// 2x transient space cost — and it announced it against a path that, with storage plural,
		// may be a different and perfectly healthy disk. Found by the architect; the other four
		// were inside probeNamespace and this one is not, so a fix scoped to that function would
		// have left it standing.
		log.Warn("storage backend selected: copy — this path supports neither reflink nor hardlinks; "+
			"versioning will use full copies (transient 2x space)",
			"storage_root", opts.Backups, "reason", reason)
	case degraded:
		// reflink, chosen on a filesystem that cannot report whether the clone actually shares
		// blocks. The strategy still works and the clone is still independent; what is unproven
		// is the SPACE saving, which is the reason D5 picks this backend at all (quince#747). A
		// space claim nobody can check is exactly the "no silent caps or fallbacks" case.
		log.Warn("storage backend selected: reflink — but this filesystem cannot report extent "+
			"sharing, so the space saving is UNVERIFIED on this path",
			"storage_root", opts.Backups, "reason", reason)
	default:
		log.Info("storage backend selected", "backend", name, "storage_root", opts.Backups, "reason", reason)
	}
	return newNamespaceBackend(name, strategyFor(name), opts.Backups, opts.AppVersion, log), name, reason
}

// probeNamespace tests a storage's OWN root for reflink SHARING, then hardlink+inode identity,
// else copy. It is probeNamespaceDetail without the degraded flag, for the callers that only
// need the backend and its reason.
func probeNamespace(backups string) (string, string) {
	name, reason, _ := probeNamespaceDetail(backups)
	return name, reason
}

// probeNamespaceDetail is probeNamespace plus whether the choice is a DEGRADED one that the
// caller must surface loudly rather than log at info.
//
// IT ASKS ABOUT SHARING, NOT INDEPENDENCE (quince#747). The reflink reason used to read "FICLONE
// independence probe passed", which was an honest name for a probe that answered the wrong
// question: a full copy passes an independence test identically to a clone. The two reflink
// reasons below are now distinguishable on purpose — one says the sharing was observed, the
// other says this filesystem cannot be asked — because they are different states and an
// operator reading a storage card is entitled to know which one they are in.
//
// EVERY REASON NAMES THE PATH IT PROBED (quince#514). All four hardcoded the literal `/backups`
// while taking the real root as a parameter — true while there was exactly one storage and always
// `/backups`, and wrong the moment quince#473 made storage plural. Measured on the staging stand:
// a second storage at `/backups-usb` reported *"probe passed on /backups"*, naming a DIFFERENT,
// healthy disk two lines above it in the same startup log.
//
// A reason is a state-honesty artifact, not a debug string: it is what tells an operator why their
// storage is on the backend it is on, and this rung made "which storage" the load-bearing question.
func probeNamespaceDetail(backups string) (string, string, bool) {
	if err := os.MkdirAll(backups, 0o755); err != nil {
		return BackendCopy, fmt.Sprintf("cannot create probe dir at %s: %v", backups, err), true
	}
	switch res, detail := clonetree.ReflinkProbeDetail(backups); res {
	case clonetree.ReflinkSharing:
		return BackendReflink, fmt.Sprintf("FICLONE clone-sharing probe passed on %s: %s", backups, detail), false
	case clonetree.ReflinkSharingUnverifiable:
		return BackendReflink, fmt.Sprintf("FICLONE probe passed on %s but SHARING IS UNVERIFIED: %s", backups, detail), true
	}
	if hardlinkProbe(backups) {
		return BackendHardlink, fmt.Sprintf("reflink unsupported; link()+inode-identity probe passed on %s", backups), false
	}
	return BackendCopy, fmt.Sprintf("neither reflink nor hardlinks supported on %s", backups), true
}

// hardlinkProbe creates a file, hardlinks it, and confirms both names share one inode.
func hardlinkProbe(dir string) bool {
	src := filepath.Join(dir, ".quince-hardlink-src")
	dst := filepath.Join(dir, ".quince-hardlink-dst")
	defer func() { _ = os.Remove(src); _ = os.Remove(dst) }()
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		return false
	}
	_ = os.Remove(dst)
	if err := os.Link(src, dst); err != nil {
		return false
	}
	fs, err1 := os.Stat(src)
	fd, err2 := os.Stat(dst)
	if err1 != nil || err2 != nil {
		return false
	}
	return os.SameFile(fs, fd)
}

func strategyFor(name string) clonetree.Strategy {
	switch name {
	case BackendReflink:
		return clonetree.Reflink
	case BackendHardlink:
		return clonetree.Hardlink
	default:
		return clonetree.Copy
	}
}

func orAuto(s string) string {
	if s == "" {
		return "auto"
	}
	return s
}
