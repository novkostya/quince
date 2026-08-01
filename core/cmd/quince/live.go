package main

import (
	"context"
	"fmt"
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
	st *store.Store, eventBus *bus.Bus, log *slog.Logger) (*liveStack, error) {
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
	storageMgr, err := buildStorage(ctx, bootstrap, cfgSvc, st, eventBus, log)
	if err != nil {
		return nil, err
	}
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
	return ls, nil
}

// buildStorage resolves the qn.5 backend and returns a reconciled *storage.Manager. It is the
// storage half of buildLiveStack, factored out so the read-only admin CLIs (`versions verify`,
// `device repair-working-copy`) can operate on a truthful, reconciled registry WITHOUT starting the
// muxer supervisor / device registry / enrichment goroutines the full stack spins up. Reconcile runs
// before returning (same as serve) so adopted/missing versions are reflected.
func buildStorage(ctx context.Context, _ config.Bootstrap, cfgSvc *config.Service,
	st *store.Store, eventBus *bus.Bus, log *slog.Logger) (*storage.Manager, error) {
	scfg := cfgSvc.Current().Storage
	root, name := defaultStorage(scfg)

	// THE GUARD RUNS BEFORE ANYTHING TOUCHES THE PATH (quince#415).
	//
	// This used to call storage.Select first and hand ResolveStorage the already-probed backend.
	// That defeated the guard, because Select → probeNamespace does `os.MkdirAll(backups)`: a
	// declared path that did not exist was CREATED by the probe, so by the time ResolveStorage
	// looked, the path was reachable, markerless and unknown — a textbook creation moment, at a
	// path the user had typo'd. quince invented a directory beside the real root and sent backups
	// there. The check was downstream of the thing that made it pass.
	//
	// So the probe is now LAZY: it is a closure ResolveStorage calls only on the paths where a
	// backend is actually needed (comparing against an existing marker, or freezing one at a
	// genuine creation). A refusal never reaches it, which is what makes "the guard is first" a
	// property of the code rather than of the order two statements happen to be written in.
	//
	// NOBODY CREATES A STORAGE ROOT. A declared path must already exist — the refusal text has
	// said so since story 1 ("The path must already exist and hold your backups if you have any")
	// and this is what makes that true rather than aspirational. probeNamespace's MkdirAll
	// survives, but with this ordering it can only ever run against a directory that is already
	// there, so it cannot conjure a storage.
	var (
		stBackend   storage.Backend
		backendName string
		reason      string
		probed      bool
	)
	probe := func(string) string {
		if !probed {
			stBackend, backendName, reason = storage.Select(ctx, storage.Options{
				Backend: scfg.Backend, Backups: root, AppVersion: version.String(),
				ZFSParent: scfg.ZFS.ParentDataset, ZFSMode: scfg.ZFS.Mode,
				ZFSHookCmd: scfg.ZFS.HookCmd, ZFSSeed: scfg.ZFS.Seed,
			}, log)
			probed = true
		}
		return backendName
	}

	state, err := storage.ResolveStorage(name, root, probe,
		func(n string) (storage.KnownStorage, error) {
			row, ok, err := st.GetStorage(n)
			if err != nil || !ok || row.StorageID == nil {
				return storage.KnownStorage{}, err
			}
			return storage.KnownStorage{Known: true, StorageID: *row.StorageID}, nil
		},
		time.Now, version.String(), id.New)
	if err != nil {
		return nil, fmt.Errorf("storage %q: %w", name, err)
	}
	if !state.Resolution.OK() {
		// REFUSE. A quince that serves while its storage is not what it thinks it is can write
		// backups to the wrong filesystem, and the missing-medium case looks exactly like an empty
		// directory. The Reason carries observation, consequence and remedy (preflight's idiom).
		return nil, fmt.Errorf("%s (%s)", state.Reason, state.Resolution)
	}
	// Every resolution that says OK has been through the probe — comparing against a marker, or
	// freezing one at creation. Asserted rather than assumed: a nil backend here would be a nil
	// dereference several calls later, in code that would look unrelated to this ordering.
	if stBackend == nil {
		return nil, fmt.Errorf("storage %q resolved %s without a backend — the lazy probe was not "+
			"reached on a path that requires it (quince#415's ordering is wrong)", name, state.Resolution)
	}
	if state.Resolution == storage.ResolutionCreated {
		// LOUD and user-visible, deliberately: this is the one path where quince decides a place
		// is a new storage, and the residual it cannot rule out is a first declaration whose
		// medium was absent. A user must be able to contradict it.
		log.Warn("storage CREATED — quince had not seen this storage before and has claimed it",
			"storage", name, "path", root, "backend", state.Backend, "storage_id", state.StorageID,
			"note", "if this path should already hold backups, stop and check the medium is mounted")
	}
	if !state.Verified {
		log.Warn("storage opened UNVERIFIED — nothing confirmed the medium matches its marker",
			"storage", name, "path", root, "backend", state.Backend, "reason", state.Reason)
	}

	now := time.Now().UTC()
	created := now
	if state.Resolution == storage.ResolutionCreated {
		if err := st.UpsertStorage(store.StorageRow{
			Name: name, StorageID: &state.StorageID, Backend: &state.Backend,
			Path: root, CreatedAt: &created, SeenAt: now,
		}); err != nil {
			return nil, fmt.Errorf("storage %q: recording it: %w", name, err)
		}
	} else if err := st.UpsertStorage(store.StorageRow{
		Name: name, StorageID: &state.StorageID, Backend: &state.Backend, Path: root, SeenAt: now,
	}); err != nil {
		return nil, fmt.Errorf("storage %q: recording it: %w", name, err)
	}

	storageMgr := storage.NewManager(stBackend, backendName, st, st, eventBus, root,
		storage.RetentionPolicy{
			KeepRecent: scfg.Retention.KeepRecent,
			KeepDaily:  scfg.Retention.KeepDaily,
			KeepWeekly: scfg.Retention.KeepWeekly,
		}, id.New, log)
	if err := storageMgr.Reconcile(ctx); err != nil {
		log.Error("storage: startup reconciliation failed", "error", err)
	}

	// Attribute versions that predate qn.6c. Fills only NULLs, so it never rewrites where a
	// committed backup is recorded as living; running it every startup is therefore safe.
	attributeVersions(st, state.StorageID, log)

	log.Info("storage subsystem ready", "storage", name, "path", root, "backend", backendName,
		"reason", reason, "storage_id", state.StorageID, "resolution", state.Resolution,
		"verified", state.Verified)
	return storageMgr, nil
}

// attributeVersions fills storage_id on versions that have none, and reports what remains.
//
// `null` means NOT YET ATTRIBUTED and is TRANSITIONAL (contracts §2). This is what makes it
// transitional in fact rather than only in the documentation — and the leftover count is logged
// rather than swallowed, because a nullable-with-meaning field whose meaning is "temporary" decays
// into a permanent unknown unless something keeps saying otherwise.
//
// Single-storage this rung: every unattributed version belongs to the one declared storage. When a
// device can have versions on several, attribution stops being a whole-table sweep and becomes a
// per-(device, storage) question — which is why this is deliberately a small, replaceable function.
func attributeVersions(st *store.Store, storageID string, log *slog.Logger) {
	udids, err := st.UDIDsWithVersions()
	if err != nil {
		log.Error("storage: could not list devices for version attribution", "error", err)
		return
	}
	var total int64
	for _, u := range udids {
		n, err := st.AttributeVersions(u, storageID)
		if err != nil {
			log.Error("storage: attributing versions failed", "udid", u, "error", err)
			return
		}
		total += n
	}
	remaining, err := st.CountUnattributedVersions()
	if err != nil {
		log.Error("storage: could not count unattributed versions", "error", err)
		return
	}
	if total > 0 {
		log.Info("storage: attributed versions to their storage", "count", total, "storage_id", storageID)
	}
	if remaining > 0 {
		// Not an error here — but it must never be silent, because this is the state that is
		// supposed to disappear.
		log.Warn("storage: versions still carry no storage_id", "count", remaining)
	}
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
