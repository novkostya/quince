package main

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
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
	// reconcile is the qn.6i runner, non-nil only when the scan was DEFERRED (serve). The CLIs get
	// nil because they ran the scan synchronously and have nothing left to report about.
	reconcile *storage.Runner
}

// buildLiveStack constructs the live subsystems (muxer supervision qn.2b, device registry qn.2,
// device ops qn.3, storage qn.5, backup engine qn.4a), starts their background goroutines under
// ctx, and runs startup reconciliation in the mandated order — **storage first, then job rows**
// (amendment 1: a commit that rolled forward is visible to the job reconciler) — BEFORE returning,
// so the caller serves / drives only a reconciled system. Shared by `serve` and `backup`.
func buildLiveStack(ctx context.Context, bootstrap config.Bootstrap, cfgSvc *config.Service,
	st *store.Store, eventBus *bus.Bus, log *slog.Logger, scan scanMode) (*liveStack, error) {
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

	// Storage subsystem (qn.5): resolve the backend, roll any half-done commit forward, and — for
	// the CLIs only — scan before returning (qn.6i D2).
	// The runner comes back from buildStorage rather than being built here: the storage-added trigger
	// lives in the config applier, which is registered inside that function (qn.6i PR 4). Nil for the
	// CLIs. CONSTRUCTED, NOT STARTED — the caller decides when the first pass may begin, which for
	// `serve` is once the router exists, so the scan and the bind proceed together.
	storageMgr, runner, err := buildStorage(ctx, bootstrap, cfgSvc, st, eventBus, log, scan)
	if err != nil {
		return nil, err
	}
	ls.versions = storageMgr
	ls.versionAdmin = storageMgr
	ls.storages = storageMgr
	ls.reconcile = runner

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
	// quince#654: which transport an `auto` request prefers when the device is on BOTH. This line is
	// what makes `backup.preferred_transport` a setting rather than a validated string — its
	// predecessor `backup.transport` had no equivalent anywhere, which was the whole defect.
	ecfg.PreferredTransport = cfgSvc.Current().Backup.PreferredTransport
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

	// THE SECOND CONSUMER OF THE CONFIG SEAM (qn.6g, quince#577), and the one that proves the
	// mechanism is GENERAL rather than a storage hook wearing a general name: a different package,
	// a different lock, a different shape of state.
	//
	// Both `backup:` settings anything reads — `require_encryption` (checked at preflight) and
	// `preferred_transport` (resolved before the job row exists). A job already past those points
	// keeps the answer it got; Engine.SetLiveConfig says why that is correct rather than incidental.
	cfgSvc.Subscribe("backup", func(old, next config.Config) []config.Warning {
		if old.Backup == next.Backup {
			return nil // an edit to some other section
		}
		eng.SetLiveConfig(next.Backup.RequireEncryption, next.Backup.PreferredTransport)
		log.Info("backup settings applied without a restart",
			"require_encryption", next.Backup.RequireEncryption,
			"preferred_transport", next.Backup.PreferredTransport)
		return nil
	})

	log.Info("backup engine ready")
	return ls, nil
}

// scanMode says whether the per-device reconciliation scan runs INSIDE the build (the admin CLIs
// and `quince backup`, which are short-lived processes that want a complete registry before they do
// anything) or is left to the runner (`serve`, where it must not delay the listener — quince#592).
//
// AN EXPLICIT PARAMETER RATHER THAN A DEFAULT, because both answers are correct for their caller and
// the wrong one is silent in both directions: a CLI that skipped the scan would report on a registry
// nobody repaired, and a `serve` that ran it would keep the ~48 s of connection-refused this rung
// exists to remove. A default would make one of those the thing you get by not thinking about it.
type scanMode int

const (
	scanSynchronous scanMode = iota // run the scan before returning — CLIs
	scanDeferred                    // leave it to the runner — serve
)

// buildStorage resolves the qn.5 backend and returns a reconciled *storage.Manager. It is the
// storage half of buildLiveStack, factored out so the read-only admin CLIs (`versions verify`,
// `device repair-working-copy`) can operate on a truthful, reconciled registry WITHOUT starting the
// muxer supervisor / device registry / enrichment goroutines the full stack spins up. Reconcile runs
// before returning (same as serve) so adopted/missing versions are reflected.
func buildStorage(ctx context.Context, _ config.Bootstrap, cfgSvc *config.Service,
	st *store.Store, eventBus *bus.Bus, log *slog.Logger, scan scanMode) (*storage.Manager, *storage.Runner, error) {
	scfg := cfgSvc.Current().Storage
	entries := declaredStorages(scfg)
	// ZERO STORAGES IS NOW A LEGITIMATE STARTUP STATE, and this is where that stops being a
	// contradiction. This returned an error whose comment read "unreachable past
	// config.CheckStorages … if it ever gets here the guard upstream has stopped working" — true
	// while the daemon refused to start on an empty list, and false since qn.6e's ruling (option
	// (a), 2026-08-07): a first run has no `storage:` key at all, and quince serves anyway so the
	// storage can be added from the UI.
	//
	// A Manager with NO SLOTS is safe rather than merely tolerated, and that was proven before this
	// rung needed it. qn.6g's empty-list guards make defaultSlot, BackendName, storageIDPtr,
	// policyFor, Storages, RecheckStorage and ResolveChoice all answer honestly on zero —
	// ResolveChoice 409s, which is exactly right for a job that names nowhere to go — and
	// movinglist_test.go gates every one of them.
	//
	// THE MODE ENDS WITHOUT A RESTART. ApplyStorages refuses an EMPTY list and keeps what it has,
	// but empty→non-empty is an ordinary add, so the applier brings the first storage live the
	// moment it is declared. Nothing here is re-run.
	if len(entries) == 0 {
		log.Warn("storage subsystem starting with NO STORAGES — setup only until one is added")
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

	// Attribution happens INSIDE the scan now, per storage, from what Scan found (quince#439).
	// A loud comment about statement order stood here until then, protecting the sweep that had
	// to run first. That call is DELETED, not merely moved, so there is no ordering left to
	// protect — the hazard is structurally gone rather than unlikely.
	//
	// THE PASS IS SPLIT AS OF qn.6i, AND WHICH HALF RUNS HERE IS THE RUNG'S CENTRAL DECISION (D2).
	//
	// ROLL-FORWARD ALWAYS RUNS HERE, SYNCHRONOUSLY, WHATEVER THE MODE. `Engine.Reconcile` — forty
	// lines up in buildLiveStack — decides `succeeded` vs `connection_lost` for crash-orphaned job
	// rows by asking `VersionForJob`, so a job judged BEFORE its commit is rolled forward is written
	// to the database as *interrupted by a restart* for a backup that actually finished. That is the
	// sharpest state-honesty defect this rung could have introduced, and it would have been
	// introduced BY the fix. It costs a few syscalls: roll-forward is O(1) in tree size on both
	// backends (see RollForwardAll).
	//
	// THE SCAN is what took the 36-48 seconds, and it is the half that moves. In `serve` it goes to
	// the runner and this function returns without it; the admin CLIs keep the whole synchronous
	// pass, because `versions verify` needs a complete registry and no listener is waiting on it.
	if err := storageMgr.RollForwardAll(ctx); err != nil {
		log.Error("storage: startup roll-forward failed", "error", err)
	}
	if scan == scanSynchronous {
		if _, err := storageMgr.ReconcileScan(ctx); err != nil {
			log.Error("storage: startup reconciliation scan failed", "error", err)
		}
	}

	// THE RUNNER IS BUILT HERE, NOT BY THE CALLER, BECAUSE THE APPLIER BELOW NEEDS IT.
	//
	// It was created in `buildLiveStack` while it had one trigger; the storage-added trigger lives in
	// the config subscriber registered further down, inside this function. Assigning it to a captured
	// variable after `Subscribe` would work by closure and read as a nil-deref waiting to happen — the
	// applier can only fire after a config write, which cannot happen before this function returns,
	// and "cannot happen yet" is exactly the reasoning that stops being true when somebody adds a
	// caller.
	//
	// NIL FOR THE CLIs, and every use of it is nil-guarded: a short-lived process that already ran its
	// scan synchronously has nothing to trigger, and a runner nobody starts would swallow triggers
	// rather than run them.
	var runner *storage.Runner
	if scan == scanDeferred {
		runner = storage.NewRunner(storageMgr, log)
		runner.SetInterval(reconcileInterval(cfgSvc.Current()))
		// THE THIRD CONSUMER OF THE CONFIG SEAM (qn.6g). `reconcile.interval_minutes` is LIVE: the
		// runner re-reads it when it schedules the next wait, and `SetInterval` wakes the scheduler so
		// a change applies to the CURRENT wait rather than after the old one elapses. Turning six hours
		// down to fifteen minutes and waiting up to six hours for that to bite would be live in name
		// only.
		cfgSvc.Subscribe("reconcile", func(old, next config.Config) []config.Warning {
			if old.Reconcile == next.Reconcile {
				return nil // an edit to some other section
			}
			runner.SetInterval(reconcileInterval(next))
			log.Info("reconcile schedule applied without a restart",
				"interval_minutes", next.Reconcile.IntervalMinutes,
				"enabled", next.Reconcile.IntervalMinutes > 0)
			return nil
		})
		// A JOB ENDING RE-TRIGGERS A PASS, so a device deferred behind a backup comes back when that
		// backup ends rather than when something unrelated next asks. `Engine.release` calls
		// `UnbindJob` on EVERY ending — success, failure, cancel, shutdown — so the cancel case is
		// covered by construction rather than by a second call site somebody must remember.
		runner.TriggerOnJobEnd(storageMgr)
	}

	// THE RE-PROBE BEHIND POST /api/storages/{name}/recheck (quince#435: reachability may change
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

	// STORAGE IS THE FIRST CONSUMER OF THE CONFIG SEAM (qn.6g, quince#577). A `storage:` edit — the
	// list, a path, a backend, a zfs block, or retention — now takes effect without a restart.
	//
	// IT REBUILDS THE LIST THE WAY STARTUP DOES: `declaredStorages` + `resolveSlot`, the same two
	// calls forty lines above, so a live apply and a restart cannot disagree about what a storage IS.
	// That is the property `SetRefresher`'s closure already argues for, applied to the whole list
	// rather than to one entry. `resolveSlot` is safe to re-run — it creates nothing (see its own
	// "NOBODY CREATES A STORAGE ROOT"), so re-resolving is idempotent and touches no tree.
	//
	// RETENTION RIDES HERE rather than in its own applier, and that is a dependency rather than a
	// preference: retention lives on `Slot.Retention` and `policyFor` reads it off the slot list, so
	// `ApplyStorages` is the only path by which a retention edit can reach `Prune`.
	cfgSvc.Subscribe("storage", func(old, next config.Config) []config.Warning {
		before := declaredStorages(old.Storage)
		after := declaredStorages(next.Storage)
		if sameStorageDeclaration(before, after) {
			return nil // an edit to some other section; nothing here to do
		}

		rebuilt := make([]storage.Slot, 0, len(after))
		for _, e := range after {
			rebuilt = append(rebuilt, resolveSlot(ctx, e, st, log))
		}

		var warns []config.Warning
		for _, w := range storageMgr.ApplyStorages(rebuilt) {
			warns = append(warns, config.Warning{Path: "storage", Message: w})
		}

		// THE SCAN IS STILL THE PART THAT IS EASY TO FORGET — a newly declared disk may ALREADY HOLD
		// committed backups, and without a scan they stay invisible until a restart. What changed in
		// qn.6i is that the request no longer WAITS for it (quince#715): this ran `Reconcile` inline,
		// inside the HTTP handler, holding `writeMu` across a ~48-second walk, so the button hung and
		// the next config write — a Forget, say — queued behind it.
		//
		// Only when something was ADDED: a forget needs no scan, and triggering on every unrelated
		// storage edit would walk every declared tree for nothing.
		//
		// THE WARNING THIS REPLACED IS DELETED RATHER THAN REWORDED, and that is the honest move.
		// It said the scan had failed "so they may not be listed until quince restarts" — a claim
		// about an OUTCOME this handler can no longer observe, because the pass has not run yet when
		// the response is written. Rewording it into a promise about a future pass would be a claim
		// with no observation behind it, which is the failure `state honesty` names. What replaces it
		// is a state a client can actually read: `reconciling` on `GET /api/health`, plus the log.
		//
		// NIL RUNNER IS NOT REACHABLE FROM HERE and is guarded anyway: the applier is registered in
		// every mode, and only `serve` has both a runner and a live config surface. The CLIs ran their
		// scan synchronously and never serve `PUT /api/config`.
		if addedStorage(before, after) && runner != nil {
			runner.Trigger("storage added")
		}
		return warns
	})

	reportUnattributed(st, log)

	for _, s := range slots {
		log.Info("storage ready", "storage", s.Name, "path", s.Root, "backend", s.BackendName,
			"storage_id", s.StorageID, "reachable", s.Reachable)
	}
	return storageMgr, runner, nil
}

// sameStorageDeclaration reports whether two resolved declarations are the same storages, in the
// same order, with the same per-entry settings.
//
// ORDER MATTERS and is not a detail: position IS the default (`slots[0]`), so a reorder with
// identical members is a real change — the user made a different disk the default. Comparing sets
// would miss exactly that.
//
// It exists so an edit to `backup:` or `ui:` does not re-resolve every storage. Re-resolution is
// idempotent but not free: it stats every declared root and may probe a backend.
func sameStorageDeclaration(a, b []config.StorageEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !reflect.DeepEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// addedStorage reports whether b names a storage a did not. BY NAME, which is the identity the
// config carries and the one `resolveSlot` re-resolves by — a path change on an existing entry is an
// edit rather than an addition, and it re-resolves through ApplyStorages either way.
func addedStorage(a, b []config.StorageEntry) bool {
	known := make(map[string]bool, len(a))
	for _, e := range a {
		known[e.Name] = true
	}
	for _, e := range b {
		if !known[e.Name] {
			return true
		}
	}
	return false
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
		// THE ID SURVIVES THE ERROR (quince#652). ResolveStorage returns its state alongside the
		// error, and the DB lookup is now the first thing it does — so that state carries the known
		// storage_id even when reading the marker failed outright.
		//
		// THIS is the path the reported case actually took, which quince#652 attributed to the
		// !reachable branch: an unplugged USB whose MOUNTPOINT still exists is a readable directory,
		// so reachable() passes and the failure surfaces from the marker read as
		// `open …/quince-storage.json: input/output error` — which is this err, verbatim, and is
		// what the Operator saw on the page. Fixing only the !reachable branch would have left the
		// reported symptom exactly as it was.
		return storage.Slot{
			StorageID: state.StorageID,
			Name:      e.Name, Root: e.Path, Reachable: false,
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

// reconcileInterval turns the config's integer minutes into a duration, with <= 0 meaning DISABLED
// (qn.6i). One place, so the schedule and any future reader cannot disagree about what 0 means.
func reconcileInterval(c config.Config) time.Duration {
	if c.Reconcile.IntervalMinutes <= 0 {
		return 0
	}
	return time.Duration(c.Reconcile.IntervalMinutes) * time.Minute
}
