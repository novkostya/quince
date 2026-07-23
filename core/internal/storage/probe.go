package storage

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/novkostya/quince/core/internal/storage/clonetree"
)

// Options are the resolved config inputs the storage subsystem needs (mirrors config.Storage*,
// passed as plain fields so this package does not import config).
type Options struct {
	Backend    string // auto | zfs | reflink | hardlink | copy
	Backups    string // QUINCE_BACKUPS root
	AppVersion string
	ZFSParent  string // storage.zfs.parent_dataset
	ZFSMode    string // exec | hook
	ZFSHookCmd string
	ZFSSeed    string // auto | reflink | copy (in-container seed strategy; hardlink never used — amendment A)
}

// Select resolves the effective backend (stack D5 auto-selection): explicit zfs intent
// (storage.backend: zfs, or auto with a parent dataset/hook configured) → zfs; an explicit
// namespace backend → that; else probe the real /backups filesystem — FICLONE-independence →
// reflink, link()+inode identity → hardlink, else copy. The choice + reason is returned for
// onboarding and logged (never silent — a copy fallback is a surfaced degraded mode).
func Select(baseCtx context.Context, opts Options, log *slog.Logger) (Backend, string, string) {
	wantZFS := opts.Backend == BackendZFS ||
		(opts.Backend == "auto" && (opts.ZFSParent != "" || opts.ZFSHookCmd != ""))
	if wantZFS {
		cli := newZFSCLI(opts.ZFSParent, opts.ZFSMode, opts.ZFSHookCmd, "")
		reason := "storage.zfs configured (parent dataset / hook set)"
		if opts.Backend == BackendZFS {
			reason = "storage.backend: zfs"
		}
		log.Info("storage backend selected", "backend", BackendZFS, "reason", reason, "mode", opts.ZFSMode)
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
	name, reason := probeNamespace(opts.Backups)
	if name == BackendCopy {
		// A degraded mode — surface it loudly (hard rule: no silent caps/fallbacks).
		log.Warn("storage backend selected: copy — /backups supports neither reflink nor hardlinks; "+
			"versioning will use full copies (transient 2x space)", "reason", reason)
	} else {
		log.Info("storage backend selected", "backend", name, "reason", reason)
	}
	return newNamespaceBackend(name, strategyFor(name), opts.Backups, opts.AppVersion, log), name, reason
}

// probeNamespace tests /backups for reflink independence, then hardlink+inode identity, else copy.
func probeNamespace(backups string) (string, string) {
	if err := os.MkdirAll(backups, 0o755); err != nil {
		return BackendCopy, "cannot create /backups probe dir: " + err.Error()
	}
	if clonetree.ReflinkProbe(backups) {
		return BackendReflink, "FICLONE independence probe passed on /backups"
	}
	if hardlinkProbe(backups) {
		return BackendHardlink, "reflink unsupported; link()+inode-identity probe passed on /backups"
	}
	return BackendCopy, "neither reflink nor hardlinks supported on /backups"
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

// seedStrategy returns the strategy SAFE for seeding working/<udid> from latest/ (qn.5b
// amendment A, decisions (co)). Reflink (independent CoW) and copy are safe; a HARDLINK seed
// would alias working/<udid> to the committed latest/, so an in-place idevicebackup2 write to any
// file class not yet on clonetree.MutatesInPlace would corrupt the committed version through the
// alias — the very completeness the deferred gate 12c proves. Until then the hardlink tier stays
// disabled-to-copy for the seed too, so it downgrades to copy (a surfaced degraded mode; the
// caller logs it). reflink/copy pass through unchanged.
func seedStrategy(s clonetree.Strategy) clonetree.Strategy {
	if s == clonetree.Hardlink {
		return clonetree.Copy
	}
	return s
}

func orAuto(s string) string {
	if s == "" {
		return "auto"
	}
	return s
}
