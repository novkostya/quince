package main

import (
	"context"
	"log/slog"
	"path/filepath"

	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/device"
	"github.com/novkostya/quince/core/internal/deviceops"
	"github.com/novkostya/quince/core/internal/httpapi"
	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/muxd"
	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/version"
)

// liveStack is the non-demo subsystem set: everything the HTTP server and the `backup` CLI drive.
type liveStack struct {
	devices      httpapi.DeviceReader
	jobs         httpapi.JobReader
	jobControl   httpapi.JobControl
	versions     httpapi.VersionReader
	versionAdmin httpapi.VersionAdmin
	muxer        httpapi.MuxerControl
	ops          httpapi.DeviceOps
	engine       *backup.Engine
}

// buildLiveStack constructs the live subsystems (muxer supervision qn.2b, device registry qn.2,
// device ops qn.3, storage qn.5, backup engine qn.4a), starts their background goroutines under
// ctx, and runs startup reconciliation in the mandated order — **storage first, then job rows**
// (amendment 1: a commit that rolled forward is visible to the job reconciler) — BEFORE returning,
// so the caller serves / drives only a reconciled system. Shared by `serve` and `backup`.
func buildLiveStack(ctx context.Context, bootstrap config.Bootstrap, cfgSvc *config.Service,
	st *store.Store, eventBus *bus.Bus, log *slog.Logger) *liveStack {
	dcfg := cfgSvc.Current().Devices
	ls := &liveStack{muxer: httpapi.UnmanagedMuxer{}}

	// Managed muxers (SIMPLE profile: usbmuxd for USB + netmuxd for Wi-Fi, qn.2b/qn.4c) or
	// external (HARDENED / manage_muxer: false — dialed only, still reported in /api/health).
	group := buildMuxerGroup(dcfg, log)
	go group.Run(ctx)
	ls.muxer = muxerHealth{group}

	// Live device tracking (qn.2): one muxd client per configured muxer socket feeds the registry.
	reg := device.NewRegistry(eventBus, log)
	for _, addr := range []string{dcfg.UsbmuxdSocket, dcfg.NetmuxdAddr} {
		if addr == "" {
			continue
		}
		client := muxd.NewClient(addr, log)
		sink := reg.Sink(addr)
		go client.Run(ctx, sink)
	}
	ls.devices = reg
	log.Info("device registry watching muxers", "usbmuxd", dcfg.UsbmuxdSocket, "netmuxd", dcfg.NetmuxdAddr)

	// Device ops (qn.3): pair/validate/info + encryption; enrichment overlays lockdown identity.
	tools := deviceops.NewTools(dcfg.UsbmuxdSocket, dcfg.NetmuxdAddr, log)
	lockdown := deviceops.NewLockdownStore(bootstrap.Data, lockdownSystemDir, log)
	lockdown.Restore()
	opsMgr := deviceops.NewManager(ctx, tools, reg, eventBus, st, log)
	opsMgr.SetLockdown(lockdown)
	ls.ops = opsMgr
	go deviceops.NewEnrichDriver(tools, reg, eventBus, log).Run(ctx)
	log.Info("device ops ready (pair/encryption/enrichment)")

	// Storage subsystem (qn.5): resolve the backend + reconcile before anything serves.
	storageMgr := buildStorage(ctx, bootstrap, cfgSvc, st, eventBus, log)
	ls.versions = storageMgr
	ls.versionAdmin = storageMgr

	// Device.last_backup derives from the committed versions (qn.4c finding (v)): the version
	// registry is the source of truth for "has this device been backed up", so a device shows its
	// real last backup immediately after a restart — including versions adopted from a restored
	// dataset, which no job row would ever explain.
	reg.SetLastBackupSource(storageMgr.LastBackup)

	// Backup engine (qn.4a): drives idevicebackup2 through the state machine into storage. Its
	// job-row reconciliation runs AFTER storage's (order matters — amendment 1).
	ecfg := backup.DefaultConfig()
	ecfg.RequireEncryption = cfgSvc.Current().Backup.RequireEncryption
	eng := backup.New(backup.Options{
		BaseCtx: ctx, Store: st, Storage: storageMgr, VersionQ: storageMgr, Devices: reg,
		Prober: opsMgr, Announcer: reg,
		Bus: eventBus, Log: log, Config: ecfg, Backups: bootstrap.Backups, NewID: id.New,
		Tool: backup.ToolConfig{
			Bin: "idevicebackup2", UsbmuxdSocket: dcfg.UsbmuxdSocket, NetmuxdAddr: dcfg.NetmuxdAddr,
			TargetRoot: filepath.Join(bootstrap.Cache, "backup-targets"),
		},
	})
	if err := eng.Reconcile(); err != nil {
		log.Error("backup: startup job reconciliation failed", "error", err)
	}
	ls.jobs = eng
	ls.jobControl = eng
	ls.engine = eng
	log.Info("backup engine ready")
	return ls
}

// buildStorage resolves the qn.5 backend and returns a reconciled *storage.Manager. It is the
// storage half of buildLiveStack, factored out so the read-only admin CLIs (`versions verify`,
// `device repair-working-copy`) can operate on a truthful, reconciled registry WITHOUT starting the
// muxer supervisor / device registry / enrichment goroutines the full stack spins up. Reconcile runs
// before returning (same as serve) so adopted/missing versions are reflected.
func buildStorage(ctx context.Context, bootstrap config.Bootstrap, cfgSvc *config.Service,
	st *store.Store, eventBus *bus.Bus, log *slog.Logger) *storage.Manager {
	scfg := cfgSvc.Current().Storage
	stBackend, backendName, reason := storage.Select(ctx, storage.Options{
		Backend: scfg.Backend, Backups: bootstrap.Backups, AppVersion: version.String(),
		ZFSParent: scfg.ZFS.ParentDataset, ZFSMode: scfg.ZFS.Mode,
		ZFSHookCmd: scfg.ZFS.HookCmd, ZFSMirror: scfg.ZFS.Mirror,
	}, log)
	storageMgr := storage.NewManager(stBackend, backendName, st, st, eventBus, bootstrap.Backups,
		storage.RetentionPolicy{
			KeepRecent: scfg.Retention.KeepRecent,
			KeepDaily:  scfg.Retention.KeepDaily,
			KeepWeekly: scfg.Retention.KeepWeekly,
		}, id.New, log)
	if err := storageMgr.Reconcile(ctx); err != nil {
		log.Error("storage: startup reconciliation failed", "error", err)
	}
	log.Info("storage subsystem ready", "backend", backendName, "reason", reason)
	return storageMgr
}
