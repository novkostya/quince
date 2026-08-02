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
	storages     httpapi.StorageReader
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
	ls.storages = storageMgr

	// qn.6c: the engine's A3 free-space preflight probes the same root the storage subsystem
	// committed to, which is now the DEFAULT declared storage rather than the retired
	// QUINCE_BACKUPS. Read from the same place buildStorage read it so the two cannot drift —
	// a preflight that measures free space on a different filesystem than the one being written
	// to is a check that passes for the wrong reason.
	engineBackupsRoot := defaultStorageRoot(cfgSvc.Current().Storage)

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
	entries := declaredStorages(scfg)
	if len(entries) == 0 {
		// Unreachable past config.CheckStorages, which refuses to serve on an absent or empty list.
		// Asserted rather than assumed: the ONE surviving hard refusal is "no storages declared"
		// (quince#435), so if it ever gets here the guard upstream has stopped working.
		return nil, fmt.Errorf("no storages declared — config.CheckStorages should have refused first")
	}

	// EVERY DECLARED STORAGE IS RESOLVED, AND ONE THAT CANNOT BE OPENED IS LISTED RATHER THAN FATAL
	// (Operator ruling 2026-08-01, quince#435).
	//
	// This used to resolve exactly the default and return an error on any resolution that was not
	// OK, which exited the process. Right when one storage could be declared; wrong for several —
	// one unplugged disk would refuse to start a daemon whose other storages are fine. The ruling's
	// argument is that refusing makes the page that would EXPLAIN the problem unreachable, so the
	// user gets a dead daemon and a log line instead of a screen naming the disk to plug in.
	//
	// The one hard refusal that survives is the empty list above: that is a CONFIGURATION error
	// nothing at runtime fixes, where an absent disk is a state the user fixes by plugging it in.
	slots := make([]storage.Slot, 0, len(entries))
	for _, e := range entries {
		slots = append(slots, resolveSlot(ctx, e, st, log))
	}

	// THE INTERIM SURFACE, UNTIL 5c PUTS IT ON THE WIRE (quince#378).
	//
	// Between this change and `GET /api/storages` there is a window where quince serves with a
	// storage missing and nothing on the wire mentions it — a degraded mode not surfaced, which is
	// worse than the refusal this replaces, because that at least named the disk. A log line is a
	// poor UI and a real one, and it is worth keeping permanently after 5c: a daemon that starts
	// without a declared disk should say so in its startup output whether or not anyone is looking
	// at a browser.
	for _, s := range slots {
		if !s.Usable() {
			log.Warn("storage UNREACHABLE — serving without it; backups to it will be refused",
				"storage", s.Name, "path", s.Root, "code", s.UnreachableCode,
				"reason", s.UnreachableReason)
		}
	}

	storageMgr := storage.NewManager(slots, st, st, eventBus, id.New, log)

	// Attribution happens INSIDE Reconcile now, per storage, from what Scan found (quince#439).
	// A loud comment about statement order stood here until then, protecting the sweep that had
	// to run first. That call is DELETED, not merely moved, so there is no ordering left to
	// protect — the hazard is structurally gone rather than unlikely.
	if err := storageMgr.Reconcile(ctx); err != nil {
		log.Error("storage: startup reconciliation failed", "error", err)
	}

	// THE RE-PROBE BEHIND POST /api/storages/{id}/recheck (quince#435: reachability may change
	// without a restart; the storage LIST still needs one). It closes over the same resolver the
	// startup loop used, so a recheck and a restart cannot disagree about what a storage is.
	//
	// It re-resolves by NAME, which is the identity the config carries and the one that survives
	// a replug — the storage_id is what the marker says, and on an unreachable storage there is
	// no marker to have said anything yet.
	storageMgr.SetRefresher(func(name string) (storage.Slot, bool) {
		for _, e := range declaredStorages(cfgSvc.Current().Storage) {
			if e.Name == name {
				return resolveSlot(ctx, e, st, log), true
			}
		}
		return storage.Slot{}, false
	})

	reportUnattributed(st, log)

	for _, s := range slots {
		log.Info("storage ready", "storage", s.Name, "path", s.Root, "backend", s.BackendName,
			"storage_id", s.StorageID, "reachable", s.Reachable)
	}
	return storageMgr, nil
}

// resolveSlot resolves ONE declared storage into a Slot, reachable or not.
//
// It never returns an error: every failure mode is a state on the Slot, per quince#435. The caller
// gets a storage it can list and refuse jobs for, rather than a reason to exit.
// scfg is GONE from this signature, and its absence is the flattening in one line (quince#473):
// an entry used to need the global block beside it to resolve a backend, and now carries
// everything that decides one.
func resolveSlot(ctx context.Context, e config.StorageEntry,
	st *store.Store, log *slog.Logger) (slot storage.Slot) {
	// RETENTION IS STAMPED ON EVERY RETURN PATH, via a named return, and that is deliberate rather
	// than tidy (quince#473). This function has six exits and five of them are UNREACHABLE states —
	// and an unreachable storage's policy still matters, because versions already attributed to it
	// are prunable the moment it comes back. Setting it at each `return storage.Slot{...}` would be
	// five places to forget it in a function whose whole shape is "every failure is a state".
	defer func() { slot.Retention = retentionOf(e) }()

	// THE GUARD RUNS BEFORE ANYTHING TOUCHES THE PATH (quince#415).
	//
	// This used to call storage.Select first and hand ResolveStorage the already-probed backend.
	// That defeated the guard, because Select → probeNamespace does `os.MkdirAll(backups)`: a
	// declared path that did not exist was CREATED by the probe, so by the time ResolveStorage
	// looked, the path was reachable, markerless and unknown — a textbook creation moment, at a
	// path the user had typo'd.
	//
	// So the probe is LAZY: a closure ResolveStorage calls only on the paths where a backend is
	// actually needed. A refusal never reaches it, which is what makes "the guard is first" a
	// property of the code rather than of the order two statements happen to be written in.
	//
	// NOBODY CREATES A STORAGE ROOT. A declared path must already exist.
	var (
		stBackend   storage.Backend
		backendName string
		probed      bool
	)
	probe := func(string) string {
		if !probed {
			ez := e.ZFS
			stBackend, backendName, _ = storage.Select(ctx, storage.Options{
				Backend: e.Backend, Backups: e.Path, AppVersion: version.String(),
				ZFSParent: ez.ParentDataset, ZFSMode: ez.Mode,
				ZFSHookCmd: ez.HookCmd, ZFSSeed: ez.Seed,
			}, log)
			probed = true
		}
		return backendName
	}

	state, err := storage.ResolveStorage(e.Name, e.Path, probe,
		func(n string) (storage.KnownStorage, error) {
			row, ok, err := st.GetStorage(n)
			if err != nil || !ok || row.StorageID == nil {
				return storage.KnownStorage{}, err
			}
			return storage.KnownStorage{Known: true, StorageID: *row.StorageID}, nil
		},
		time.Now, version.String(), id.New)
	if err != nil {
		return storage.Slot{
			Name: e.Name, Root: e.Path, Reachable: false,
			UnreachableCode: "path_unreachable", UnreachableReason: err.Error(),
		}
	}
	if !state.Resolution.OK() {
		// A STATE, NOT AN EXIT. The Reason carries observation, consequence and remedy (preflight's
		// idiom), and it is the only thing that can tell a user WHICH disk — so it is kept verbatim
		// rather than replaced with a category.
		return storage.Slot{
			StorageID: state.StorageID, Name: e.Name, Root: e.Path, Reachable: false,
			UnreachableCode: string(state.Resolution), UnreachableReason: state.Reason,
		}
	}
	if stBackend == nil {
		// Every resolution that says OK has been through the probe. A nil backend here would be a
		// nil dereference several calls later, in code that would look unrelated to this ordering.
		return storage.Slot{
			StorageID: state.StorageID, Name: e.Name, Root: e.Path, Reachable: false,
			UnreachableCode: "backend_mismatch",
			UnreachableReason: fmt.Sprintf("resolved %s without a backend — the lazy probe was not "+
				"reached on a path that requires it (quince#415's ordering is wrong)", state.Resolution),
		}
	}

	if state.Resolution == storage.ResolutionCreated {
		// LOUD and user-visible, deliberately: this is the one path where quince decides a place is
		// a new storage, and the residual it cannot rule out is a first declaration whose medium was
		// absent. A user must be able to contradict it.
		log.Warn("storage CREATED — quince had not seen this storage before and has claimed it",
			"storage", e.Name, "path", e.Path, "backend", state.Backend, "storage_id", state.StorageID,
			"note", "if this path should already hold backups, stop and check the medium is mounted")
	}
	if !state.Verified {
		log.Warn("storage opened UNVERIFIED — nothing confirmed the medium matches its marker",
			"storage", e.Name, "path", e.Path, "backend", state.Backend, "reason", state.Reason)
	}

	now := time.Now().UTC()
	row := store.StorageRow{
		Name: e.Name, StorageID: &state.StorageID, Backend: &state.Backend, Path: e.Path, SeenAt: now,
	}
	if state.Resolution == storage.ResolutionCreated {
		created := now
		row.CreatedAt = &created
	}
	if err := st.UpsertStorage(row); err != nil {
		// Recording it failed, so quince cannot vouch for this storage's identity next startup.
		// Listing it unreachable is the honest answer; writing to a storage whose row did not
		// persist is how an identity silently forks.
		log.Error("storage: recording it failed", "storage", e.Name, "error", err)
		return storage.Slot{
			StorageID: state.StorageID, Name: e.Name, Root: e.Path, Reachable: false,
			UnreachableCode:   "path_unreachable",
			UnreachableReason: "quince could not record this storage's identity: " + err.Error(),
		}
	}

	return storage.Slot{
		StorageID: state.StorageID, Name: e.Name, Root: e.Path,
		Backend: stBackend, BackendName: backendName, Reachable: true,
	}
}

// declaredStorages returns every declared storage, DEFAULT FIRST.
//
// Order is the contract the Manager reads: `slots[0]` is the storage a backup goes to when none is
// named. It replaced `defaultStorage`, which returned only the default's root and name — a shape
// that could not express "and the others".
//
// Callers reach here only past config.CheckStorages, which refuses to serve on an absent or empty
// list, so there is nothing to guess through and no fallback.
func declaredStorages(scfg *[]config.StorageEntry) []config.StorageEntry {
	if scfg == nil {
		return nil
	}
	entries := *scfg
	out := make([]config.StorageEntry, 0, len(entries))
	for _, s := range entries {
		if s.Default {
			out = append(out, s)
		}
	}
	for _, s := range entries {
		if !s.Default {
			out = append(out, s)
		}
	}
	return out
}

// reportUnattributed says how many versions still carry a NULL storage_id, after reconciliation
// has had its chance to fill them.
//
// This is what survives of the `attributeVersions` sweep quince#439 deleted. The FILLING moved into
// reconciliation, where `Scan` has just walked a root and "which storage" is observed rather than
// guessed; the COUNTING stayed here, because it answers a different question and the nullability
// ruling made it mandatory: `null` means "not yet attributed" and is TRANSITIONAL, and a
// nullable-with-meaning field whose meaning is "temporary" decays into a permanent unknown unless
// something asserts otherwise.
//
// A non-zero count is NOT an error. It is the honest state for a version whose artifact is gone, or
// sits on a disk nobody declared — quince does not know where it is, and says so rather than
// picking. It must never be silent, though, because this is the state that is supposed to shrink.
func reportUnattributed(st *store.Store, log *slog.Logger) {
	remaining, err := st.CountUnattributedVersions()
	if err != nil {
		log.Error("storage: could not count unattributed versions", "error", err)
		return
	}
	if remaining > 0 {
		log.Warn("storage: versions still carry no storage_id — their artifacts were not found "+
			"under any reachable declared storage", "count", remaining)
	}
}

// defaultStorageRoot is the root of the storage a backup goes to when none is named.
//
// The engine's A3 free-space preflight measures THIS filesystem, so it must read the default the
// same way buildStorage orders its slots — a preflight that measures a different filesystem than
// the one being written to is a check that passes for the wrong reason. Empty when nothing is
// declared, which config.CheckStorages has already refused.
func defaultStorageRoot(scfg *[]config.StorageEntry) string {
	if e := declaredStorages(scfg); len(e) > 0 {
		return e[0].Path
	}
	return ""
}

// retentionOf is one storage's keep policy. Entries come from Parse, which fills an absent
// `retention:` with the code defaults, so the nil branch is a guard against a hand-built entry
// rather than a config path — and it returns the same defaults rather than a zero policy, because
// a zero policy means "keep nothing" and would silently delete a user's history.
func retentionOf(e config.StorageEntry) storage.RetentionPolicy {
	r := config.DefaultRetention()
	if e.Retention != nil {
		r = *e.Retention
	}
	return storage.RetentionPolicy{
		KeepRecent: r.KeepRecent, KeepDaily: r.KeepDaily, KeepWeekly: r.KeepWeekly,
	}
}
