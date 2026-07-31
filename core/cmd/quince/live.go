package main

import (
	"context"
	"log/slog"
	"time"

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

	// qn.6c: the engine's A3 free-space preflight probes the same root the storage subsystem
	// committed to, which is now the DEFAULT declared storage rather than the retired
	// QUINCE_BACKUPS. Read from the same place buildStorage read it so the two cannot drift —
	// a preflight that measures free space on a different filesystem than the one being written
	// to is a check that passes for the wrong reason.
	engineBackupsRoot, _ := defaultStorage(cfgSvc.Current().Storage)

	// Device.last_backup derives from the committed versions (qn.4c finding (v)): the version
	// registry is the source of truth for "has this device been backed up", so a device shows its
	// real last backup immediately after a restart — including versions adopted from a restored
	// dataset, which no job row would ever explain.
	reg.SetLastBackupSource(storageMgr.LastBackup)

	// Offline devices (qn.6a): a powered-off device that has backups stays listed. The registry
	// unions live presence with the UDIDs that have committed versions, and persists the identity
	// fetched at enrichment so an offline row shows a name + last-seen after a restart.
	reg.SetKnownUDIDs(storageMgr.KnownUDIDs)
	reg.SetPersist(func(udid string, idn device.Identity, lastSeen string) {
		if err := st.UpsertDeviceIdentity(store.DeviceIdentityRow{
			UDID: udid, Name: idn.Name, Model: idn.Model, IOSVersion: idn.IOSVersion,
			Paired: idn.Paired, BackupEncryption: idn.BackupEncryption, WifiSync: idn.WifiSync,
			LastSeen: lastSeen, UpdatedAt: time.Now().UTC(),
		}); err != nil {
			log.Warn("device identity persist failed", "udid", udid, "error", err)
		}
	})
	if rows, err := st.ListDeviceIdentities(); err != nil {
		log.Warn("device identity load failed", "error", err)
	} else {
		persisted := make([]device.PersistedIdentity, 0, len(rows))
		for _, row := range rows {
			persisted = append(persisted, device.PersistedIdentity{
				UDID: row.UDID, LastSeen: row.LastSeen,
				Identity: device.Identity{
					Name: row.Name, Model: row.Model, IOSVersion: row.IOSVersion,
					Paired: row.Paired, BackupEncryption: row.BackupEncryption, WifiSync: row.WifiSync,
				},
			})
		}
		reg.LoadPersisted(persisted)
	}

	// Backup engine (qn.4a): drives idevicebackup2 through the state machine into storage. Its
	// job-row reconciliation runs AFTER storage's (order matters — amendment 1).
	ecfg := backup.DefaultConfig()
	ecfg.RequireEncryption = cfgSvc.Current().Backup.RequireEncryption
	eng := backup.New(backup.Options{
		BaseCtx: ctx, Store: st, Storage: storageMgr, VersionQ: storageMgr, Devices: reg,
		Prober: opsMgr, Announcer: reg,
		Bus: eventBus, Log: log, Config: ecfg, Backups: engineBackupsRoot, NewID: id.New,
		Tool: backup.ToolConfig{
			Bin: "idevicebackup2", UsbmuxdSocket: dcfg.UsbmuxdSocket, NetmuxdAddr: dcfg.NetmuxdAddr,
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
func buildStorage(ctx context.Context, _ config.Bootstrap, cfgSvc *config.Service,
	st *store.Store, eventBus *bus.Bus, log *slog.Logger) *storage.Manager {
	scfg := cfgSvc.Current().Storage
	root, name := defaultStorage(scfg)
	stBackend, backendName, reason := storage.Select(ctx, storage.Options{
		Backend: scfg.Backend, Backups: root, AppVersion: version.String(),
		ZFSParent: scfg.ZFS.ParentDataset, ZFSMode: scfg.ZFS.Mode,
		ZFSHookCmd: scfg.ZFS.HookCmd, ZFSSeed: scfg.ZFS.Seed,
	}, log)
	storageMgr := storage.NewManager(stBackend, backendName, st, st, eventBus, root,
		storage.RetentionPolicy{
			KeepRecent: scfg.Retention.KeepRecent,
			KeepDaily:  scfg.Retention.KeepDaily,
			KeepWeekly: scfg.Retention.KeepWeekly,
		}, id.New, log)
	if err := storageMgr.Reconcile(ctx); err != nil {
		log.Error("storage: startup reconciliation failed", "error", err)
	}
	log.Info("storage subsystem ready", "storage", name, "path", root, "backend", backendName, "reason", reason)
	return storageMgr
}

// defaultStorage returns the root and name of the storage a backup goes to when none is named.
//
// qn.6c STORY 1 ONLY: the root now comes from `storage.storages` instead of the retired
// QUINCE_BACKUPS, and quince still holds exactly ONE backend. Holding one per storage is story 3
// and a separate PR on purpose — this change moves where the root comes FROM, that one changes how
// MANY there are, and bundling them would put a signature ripple across ~32 test call sites into
// the same diff as the retirement.
//
// Callers reach here only past config.CheckStorages, which refuses to serve on an absent or empty
// list, so there is nothing to guess through and no fallback: the empty return is what an
// unreachable-by-construction case should look like. storage.Select then surfaces an unusable root
// loudly rather than inventing one.
func defaultStorage(scfg config.StorageConfig) (root, name string) {
	if scfg.Storages == nil {
		return "", ""
	}
	entries := *scfg.Storages
	for _, s := range entries {
		if s.Default {
			return s.Path, s.Name
		}
	}
	if len(entries) > 0 {
		return entries[0].Path, entries[0].Name
	}
	return "", ""
}
