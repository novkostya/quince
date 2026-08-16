// Package demo is the in-memory provider behind `quince serve --demo` (stack D9): it emits
// fixture devices, a scripted backup job, and fixture versions so the UI track can build
// every screen against live data with no hardware. The same state backs the REST reads and
// the WS event stream, so a browser reload after live churn shows consistent data. Fixture
// data is deterministic and presentable — README/release screenshots come from here.
package demo

import (
	"context"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/wire"
)

// Provider holds the mutable demo world. It implements httpapi's DeviceReader/JobReader/
// JobControl/VersionReader interfaces structurally.
type Provider struct {
	mu       sync.RWMutex
	bus      *bus.Bus
	log      *slog.Logger
	baseCtx  context.Context // set by Run; scripted jobs stop when it is cancelled
	devices  map[string]wire.Device
	order    []string // device display order
	jobs     map[string]wire.Job
	jobLog   map[string][]string // per-job accumulated log lines (GET /api/jobs/{id}/log)
	running  map[string]*demoRun // in-flight scripted jobs by UDID (single-flight, qn.4b)
	versions map[string]wire.Version
	verOrder []string // version display order (newest first)
	// seededVersions are the fixture history, which the growth trim must never eat. Without it the
	// trim — which drops from the END of verOrder, by position rather than by date — would consume
	// the storage history seeded at the end of that slice, one version per committed backup, until
	// the storage counts drifted back toward the disagreement quince#624 fixed. The trim exists to
	// bound RUNTIME growth; the seed is not growth.
	seededVersions map[string]bool
	ops            map[string]wire.Op // pair/encryption ops (GET /api/ops/{id}; qn.3 DeviceOps)
	// opInflight is the per-UDID device-op single-flight slot (quince#465). Distinct from
	// `running`, the BACKUP slot — startGuardedOp says why they are deliberately not shared.
	opInflight map[string]string
}

// NewProvider builds a provider seeded with deterministic fixtures. It does NOT start the
// live timeline — call Run for that (golden tests use the static seed only).
func NewProvider(b *bus.Bus, log *slog.Logger) *Provider {
	p := &Provider{
		bus:      b,
		log:      log,
		devices:  map[string]wire.Device{},
		jobs:     map[string]wire.Job{},
		jobLog:   map[string][]string{},
		running:  map[string]*demoRun{},
		versions: map[string]wire.Version{},
		ops:      map[string]wire.Op{},

		seededVersions: map[string]bool{},

		opInflight: map[string]string{},
	}
	p.seed()
	// Everything present after the seed is fixture history and is exempt from the growth trim.
	for _, id := range p.verOrder {
		p.seededVersions[id] = true
	}
	return p
}

// Devices returns the fixture devices in display order.
func (p *Provider) Devices() []wire.Device {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]wire.Device, 0, len(p.order))
	for _, udid := range p.order {
		if d, ok := p.devices[udid]; ok {
			out = append(out, d)
		}
	}
	return out
}

// Device returns one device by UDID.
func (p *Provider) Device(udid string) (wire.Device, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	d, ok := p.devices[udid]
	return d, ok
}

// Jobs returns jobs (optionally filtered by udid), newest first. The fixture set is small,
// so the cursor is ignored and next_cursor is always "".
func (p *Provider) Jobs(udid, _ string, limit int) ([]wire.Job, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]wire.Job, 0, len(p.jobs))
	for _, j := range p.jobs {
		if udid == "" || j.UDID == udid {
			out = append(out, j)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt > out[j].StartedAt })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, ""
}

// Job returns one job by id.
func (p *Provider) Job(id string) (wire.Job, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	j, ok := p.jobs[id]
	return j, ok
}

// JobLog returns the full-so-far log text for a job (GET /api/jobs/{id}/log). A known job
// with no log yet returns ("", true); an unknown job returns ("", false) → 404.
func (p *Provider) JobLog(id string) (string, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if _, ok := p.jobs[id]; !ok {
		return "", false
	}
	lines := p.jobLog[id]
	if len(lines) == 0 {
		return "", true
	}
	return strings.Join(lines, "\n") + "\n", true
}

// Versions returns versions (optionally filtered by udid) in display order.
func (p *Provider) Versions(udid string) []wire.Version {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]wire.Version, 0, len(p.verOrder))
	for _, id := range p.verOrder {
		v, ok := p.versions[id]
		if !ok {
			continue
		}
		if udid == "" || v.UDID == udid {
			out = append(out, v)
		}
	}
	return out
}

// Delete removes a fixture version (satisfies httpapi.VersionAdmin so --demo exercises the
// destructive path). Returns 202 on success, 404 for an unknown id.
func (p *Provider) Delete(id string) (int, error) {
	p.mu.Lock()
	v, ok := p.versions[id]
	if !ok {
		p.mu.Unlock()
		return http.StatusNotFound, nil
	}
	delete(p.versions, id)
	for i, vid := range p.verOrder {
		if vid == id {
			p.verOrder = append(p.verOrder[:i], p.verOrder[i+1:]...)
			break
		}
	}
	p.mu.Unlock()
	p.bus.PublishEvent(wire.EventVersionDeleted, v)
	return http.StatusAccepted, nil
}

func strptr(s string) *string   { return &s }
func f64ptr(f float64) *float64 { return &f }

// Storages serves the demo's fixture storage list (qn.6c story 5).
//
// TWO storages, one deliberately UNREACHABLE — the spec's fixture, and it is the interesting one:
// a demo where everything is plugged in cannot show the state the rung exists to model, and the
// selector in story 9 has to render a disabled option with a reason attached.
//
// `will_be_full` appears only when a udid is asked for, matching the ruled device-independence of
// the list. It is now COMPUTED per (storage, device) rather than asserted: this comment used to say
// "the demo answers `true` for the shuttle because no version of any device lives there", which was
// a description of a hardcoded literal and stopped being true the moment the shuttle had a history
// (quince#624).
func (p *Provider) Storages(udid string) []wire.Storage {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// COUNTS ARE DERIVED FROM THE VERSIONS, not written down beside them (quince#624). Every number
	// below is a fold over one fixture set, so the storage card, the storage page's per-device rows
	// and the device card cannot disagree — which they did, three ways, because the totals were
	// literals and nothing computed them.
	tally := p.tallyLocked()

	code, reason := "missing_medium", "the path is readable but carries no quince storage marker — "+
		"if this is a removable disk, it is not mounted"

	// Space (qn.6d gap A). The reachable storage carries capacity; the unreachable one carries NULL
	// capacity and POPULATED counts — the whole asymmetry those fields exist for, and the state G2
	// asserts. The capacity is still fabricated, because a demo has no filesystem to statfs; the
	// COUNTS no longer are, because it does have versions to count. An unreachable disk cannot be
	// measured, but the database that remembers what is on it is reachable either way.
	free, total := int64(1_200_000_000_000), int64(3_600_000_000_000)

	out := []wire.Storage{{
		ID: demoStorageInternal, Name: "internal", Path: "/backups", Backend: "reflink",
		Default: true, Reachable: true,
		FilesystemFreeBytes: &free, FilesystemTotalBytes: &total,
		BackupCount: tally.backups[demoStorageInternal],
		DeviceCount: len(tally.devices[demoStorageInternal]),
	}, {
		// A DEEP PATH, ON PURPOSE, AND IT IS A GATE INPUT RATHER THAN SET DRESSING (quince#1042).
		// `story12` and `story5` sweep for sideways scroll, and neither could see a storage card
		// overflow while both fixture paths were short enough to fit — a gate passing because the
		// fixture is convenient, which story12's own header names as worse than no assertion.
		//
		// MEASURED, THREE WAYS, because the length is the whole of why this works. On a build with
		// `min-w-0` removed from StorageCard:
		//
		//	74-char path   story12 RED — 320px by 139px, 375px by 84px, 390px by 69px, 430px by 29px
		//	               story5 RED too (the 390px dashboard)
		//	36-char path   BOTH GREEN — the card still fit, so the fixture proved nothing
		//
		// So a "long enough to look long" path does NOT arm this. Do not shorten it: anything near
		// the threshold disarms the gate silently, and the failure mode is a green suite over a
		// reachable defect. The margin at 320px is the point, not the exact string.
		//
		// A removable disk on an offsite rotation, mounted under its own label, is what an operator
		// actually has — which is why this raises the fixture's fidelity rather than contorting it.
		ID: demoStorageShuttle, Name: "shuttle", Path: "/mnt/usb/external-8tb-offsite-rotation/quince-backups-and-archives-2026-q3", Backend: "unknown",
		Default: false, Reachable: false,
		UnreachableCode: &code, UnreachableReason: &reason,
		BackupCount: tally.backups[demoStorageShuttle],
		DeviceCount: len(tally.devices[demoStorageShuttle]),
	}}
	if udid != "" {
		// `will_be_full` IS PER (STORAGE, DEVICE), which is the question the field actually asks:
		// "this device has nothing on THIS storage, so the first backup here transfers everything".
		//
		// It used to be a hardcoded `true` for the shuttle and, for internal, "does this device have
		// ANY version anywhere" — a device-wide test standing in for a per-storage one. With one
		// storage that was indistinguishable; with two it was simply the wrong question, and it
		// answered `false` for a device that had never written a byte to the storage being asked
		// about.
		internalFull := !tally.hasVersion[verKey{demoStorageInternal, udid}]
		shuttleFull := !tally.hasVersion[verKey{demoStorageShuttle, udid}]
		out[0].WillBeFull = &internalFull
		out[1].WillBeFull = &shuttleFull
	}
	return out
}

// defaultStorageID is where an omitted `storage_id` resolves, matching the daemon's own rule that
// the default is what an unspecified destination means.
func (p *Provider) defaultStorageID() string {
	for _, s := range p.Storages("") {
		if s.Default {
			return s.ID
		}
	}
	return demoStorageInternal
}

// verKey identifies the (storage, device) pair `will_be_full` is a property of.
type verKey struct{ storageID, udid string }

type storageTally struct {
	backups    map[string]int
	devices    map[string]map[string]bool
	hasVersion map[verKey]bool
}

// tallyLocked folds the version list into the per-storage numbers the wire carries. ONE pass, ONE
// source: the point of quince#624 is that these three answers cannot drift apart, and they cannot
// if they are computed together from the same slice.
//
// A MISSING VERSION IS COUNTED IN `backups` AND `devices`, AND EXCLUDED FROM `hasVersion`. Three
// questions, three answers — and the demo now gives the same three the daemon gives (quince#661,
// architect ruling 2026-08-04):
//
//	backups / devices  store.CountVersionsByStorage  INCLUDES missing — "a version whose artifact has
//	                   (no `missing` predicate)       vanished is still history the user should see"
//	                                                  (qn.6d rung-ruled decision 3)
//	hasVersion         Slot.hasVersionFor            EXCLUDES missing — "will the next backup be
//	                   (storages_api.go:173-175)      full" depends on a USABLE artifact
//
// THIS FUNCTION USED TO EXCLUDE MISSING FROM ALL THREE, which is the defect quince#661 is about. The
// demo fabricated nothing; it implemented a DIFFERENT RULE — so `story7`'s assertion that the header
// equals the sum of its per-device rows was green here and would have been red against the daemon,
// which counts missing in the header while the client filters it out of the rows.
//
// That is a sharper instance of qn.6d's *"a fixture that fabricates a value the live code never
// produces makes its gate a lie"*: a fixture that answers a different QUESTION is harder to see than
// one that invents a value, because every number it produces is individually plausible.
//
// An UNATTRIBUTED version (nil storage_id) is excluded too, and that is deliberate rather than
// incidental: it is a real state — the migration that added `storage_id` left older rows null on
// purpose rather than guessing — so a demo that silently folded them into some storage would model
// the one behaviour the real resolver refuses.
//
// Caller must hold at least a read lock.
func (p *Provider) tallyLocked() storageTally {
	t := storageTally{
		backups:    map[string]int{},
		devices:    map[string]map[string]bool{},
		hasVersion: map[verKey]bool{},
	}
	for _, id := range p.verOrder {
		v, ok := p.versions[id]
		if !ok || v.StorageID == nil || *v.StorageID == "" {
			continue
		}
		sid := *v.StorageID
		// History: counted whether or not the artifact survives.
		t.backups[sid]++
		if t.devices[sid] == nil {
			t.devices[sid] = map[string]bool{}
		}
		t.devices[sid][v.UDID] = true
		// Restorability: a missing artifact means the next backup transfers everything again, so it
		// must NOT suppress the full-transfer warning. This is the one filter that stays.
		if !v.Missing {
			t.hasVersion[verKey{sid, v.UDID}] = true
		}
	}
	return t
}

// Recheck re-probes one demo storage. The shuttle STAYS unreachable: a demo whose disk appears
// because you pressed a button would teach the operator that the button fixes things, when what it
// actually does is look again.
func (p *Provider) Recheck(name string) (wire.Storage, bool) {
	for _, s := range p.Storages("") {
		if s.Name == name {
			return s, true
		}
	}
	return wire.Storage{}, false
}

// Stable fixture ids, so the e2e selector can address a storage by id rather than by position.
const (
	demoStorageInternal = "01JSTORAGEDEMOINTERNAL00"
	demoStorageShuttle  = "01JSTORAGEDEMOSHUTTLE000"
)

// JobsOn reports which demo jobs are bound to a storage, so the demo answers the liveness refusal
// the daemon does (qn.6g, quince#577 — a forget is refused while a backup runs on that storage).
//
// The demo binds a job's storage the same way the live path does: `Job.storage_id` is set when the
// job is created and the row survives until it terminates. So "running" here is exactly the states
// the live Manager's binding map represents — a job that has not reached a terminal state.
func (p *Provider) JobsOn(storageID string) []string {
	if storageID == "" {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	var out []string
	for _, j := range p.jobs {
		if j.StorageID == nil || *j.StorageID != storageID || terminalJobState(j.State) {
			continue
		}
		out = append(out, j.ID)
	}
	// SORTED, because map iteration order is random and this id lands in a user-facing 422. Unsorted
	// it would name a different job run to run — the same nondeterminism the live Manager's JobsOn
	// sorts away, for the same reason.
	sort.Strings(out)
	return out
}

// terminalJobState is the demo's copy of "this job is over". The wire enum is
// `queued … succeeded/failed/cancelled/connection_lost` (wire/objects.go:56); the last four end a
// job, everything before them means it is still bound to its storage.
func terminalJobState(state string) bool {
	switch state {
	case "succeeded", "failed", "cancelled", "connection_lost":
		return true
	}
	return false
}
