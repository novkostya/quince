package storage

import (
	"fmt"
	"sort"
)

// A JOB'S STORAGE IS BOUND AT START AND RESOLVED FROM THE jobID EVERY WRITE-PATH METHOD ALREADY
// CARRIES (qn.6c story 6a).
//
// The alternative was threading a storage id through Seed, PrepareWork, SeedWork, VerifyWork,
// CommitJob and Discard — six signatures and ~30 call sites — to carry a fact that does not change
// for the life of a job. Binding it once is smaller AND better modelled: which storage a backup
// writes to is a property OF THE JOB, decided when it starts and constant thereafter. A parameter
// would let two calls within one job disagree; a binding cannot.
//
// This slice changes no behaviour. Nothing binds yet, so every job still resolves to the default —
// which is what `POST /api/jobs {storage_id}` will change in 6b. The seam exists first so that the
// slice adding the choice is about the choice.

// BindJobStorage records which storage a job writes to, for the life of that job.
//
// It REFUSES rather than falling back. A job bound to a storage that is not declared, or not
// currently usable, must not quietly become a job against the default: that would write a backup to
// a disk the user did not choose, which is the most expensive form of "no silent fallbacks" this
// rung has.
//
// IT CARRIES THE UDID AS OF qn.6i, AND THAT IS THE WHOLE OF WHY THE SIGNATURE MOVED. The binding is
// what tells reconciliation whether a commit path is live, and "a job is running on this storage" is
// too coarse a question to guard on: it would defer a whole disk's repair pass for one device's
// backup. With the udid the guard is per (storage, device), which is the granularity the hazard
// actually has — two devices on one disk commit independently.
func (m *Manager) BindJobStorage(jobID, udid, storageID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, s := range m.slots {
		if s.StorageID != storageID {
			continue
		}
		if !s.Usable() {
			return fmt.Errorf("storage %q is not reachable (%s): %s",
				s.Name, s.UnreachableCode, s.UnreachableReason)
		}
		if m.jobStorage == nil {
			m.jobStorage = map[string]jobBinding{}
		}
		m.jobStorage[jobID] = jobBinding{storageID: storageID, udid: udid}
		return nil
	}
	return fmt.Errorf("no storage with id %q is declared", storageID)
}

// jobBinding is what a job holds for its whole life: the storage it writes to, and the device it
// writes for. Both are constant from BindJobStorage to UnbindJob.
type jobBinding struct {
	storageID string
	udid      string
}

// UnbindJob drops a finished job's binding, so the map does not grow for the life of the process.
func (m *Manager) UnbindJob(jobID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.jobStorage, jobID)
}

// jobSlot is the storage THIS JOB writes to, and it enforces the invariant that makes serving with
// a disk missing honest: A STORAGE WHOSE RESOLUTION DID NOT SUCCEED NEVER ACCEPTS A JOB (Operator
// ruling 2026-08-01, quince#435).
//
// An UNBOUND job resolves to the default. That is correct today because nothing binds — a backup
// cannot name a storage until 6b — so the default IS every job's storage. It is a property of
// `POST /api/jobs` rather than of this function, which is why it is stated here rather than
// defended at each caller.
//
// When 6b lands, the binding is what makes the choice real, and this comment is what tells the next
// reader that an unbound job is a job from before the choice existed rather than a job whose
// storage was lost.
func (m *Manager) jobSlot(jobID string) (Slot, error) {
	m.mu.RLock()
	b, bound := m.jobStorage[jobID]
	want := b.storageID
	slots := make([]Slot, len(m.slots))
	copy(slots, m.slots)
	m.mu.RUnlock()

	// GUARDED (qn.6g). Note this function already copies the list under the lock and then works on
	// the copy — which is the right shape and is why the ONLY thing missing was the length check:
	// once the list can shrink, the copy can be empty.
	//
	// A bound job does not need slots[0] at all, so the guard is deliberately AFTER the bound lookup
	// would have run... except it cannot be, because `s` is assigned first. Hence the explicit
	// ordering below: an unbound job on an empty list is the only case that has nowhere to go.
	if len(slots) == 0 {
		if bound {
			return Slot{}, fmt.Errorf(
				"this job was started against storage %q, and no storage is declared any more", want)
		}
		return Slot{}, fmt.Errorf("no storage is declared, so this job has nowhere to write")
	}
	s := slots[0]
	if bound {
		found := false
		for _, c := range slots {
			if c.StorageID == want {
				s, found = c, true
				break
			}
		}
		if !found {
			// The binding named a storage that is no longer declared. Refusing beats writing to the
			// default: the job was started against a specific disk and silently retargeting it is
			// how a backup lands somewhere nobody chose.
			return Slot{}, fmt.Errorf(
				"this job was started against storage %q, which is no longer declared — "+
					"refusing rather than writing it to a different disk", want)
		}
	}
	if !s.Usable() {
		return Slot{}, fmt.Errorf(
			"storage %q is not reachable (%s): %s", s.Name, s.UnreachableCode, s.UnreachableReason)
	}
	return s, nil
}

// ResolveChoice maps a requested storage id to the CONCRETE one a job will use, or an HTTP status
// and a reason (contracts §1 POST /api/jobs, ruled 2026-07-31).
//
//	""          → the DEFAULT storage. Keeps every request that does not care working unchanged,
//	              which is what makes the field additive.
//	unknown     → 404, matching unknown-device.
//	unreachable → 409, NOT 422. It is a state conflict the user can act on — plug the disk in —
//	              rather than a malformed request, the same reading POST /api/devices/{udid}/pair
//	              already uses for "not present on USB". A 202-then-queue is explicitly refused:
//	              queuing fights the assisted model.
//
// The DEFAULT being unreachable is refused with a reason NAMING it, never redirected to whichever
// storage happens to be reachable — a fallback there would write a backup to a disk the user did
// not choose (Operator ruling 2026-08-01).
func (m *Manager) ResolveChoice(storageID string) (concrete string, status int, reason string) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if storageID == "" {
		// GUARDED (qn.6g, quince#577): the list can be REPLACED while the process runs, so
		// "non-empty at construction" no longer implies non-empty here. A 409 rather than a panic,
		// and with its own wording — an absent declaration and an unreachable disk are two distinct
		// conditions, and a message that covers both is false half the time it appears.
		if len(m.slots) == 0 {
			return "", 409, "no storage is declared, so there is nowhere to back up to — declare one in config.yml"
		}
		s := m.slots[0]
		if !s.Usable() {
			return "", 409, fmt.Sprintf(
				"the default storage %q is not reachable (%s): %s — choose another storage or "+
					"reconnect this one", s.Name, s.UnreachableCode, s.UnreachableReason)
		}
		return s.StorageID, 0, ""
	}
	for _, s := range m.slots {
		if s.StorageID != storageID {
			continue
		}
		if !s.Usable() {
			return "", 409, fmt.Sprintf("storage %q is not reachable (%s): %s",
				s.Name, s.UnreachableCode, s.UnreachableReason)
		}
		return s.StorageID, 0, ""
	}
	return "", 404, "no storage with that id is declared"
}

// JobsOn returns the ids of jobs currently bound to a storage, SORTED — see the sort's own note.
//
// THE LIVENESS QUESTION `DELETE /api/config/storage/{name}` ASKS BEFORE IT WRITES (qn.6g, Operator
// ruling 2026-08-06, option (b) on quince#577): a forget is refused with a 422 while a backup is
// running on that storage.
//
// WHY REFUSING BEATS LETTING THE JOB DIE. All six write phases resolve through `jobSlot`, and three
// of them decide it — `VerifyWork`, `CommitJob`, `Discard`. A forget landing BETWEEN verify passing
// and commit completing leaves `CommitJob` unable to resolve its slot, and restart-time recovery
// fails identically because the storage is no longer declared. `Discard` needs the slot too, so even
// the cleanup path is gone. Canon: *"once verify has passed and the immutable artifact exists,
// recovery completes the remaining commit phases — it never unwinds them, because a commit failure
// must not destroy a multi-hour Wi-Fi transfer."*
//
// AND WHY NOT "CANCEL IT FOR YOU". Cancellation is ASYNCHRONOUS: `JobControl` requests it, the job
// goroutine observes it, and `UnbindJob` drops the binding only when the job finishes. Cancel-then-
// forget would have to wait for every affected job to reach a terminal state inside an HTTP handler,
// with a timeout — and the timeout path lands the forget while a job is still mid-phase, which is
// the defect the ruling exists to prevent. A mechanism whose failure mode is the bug it fixes is the
// wrong mechanism.
//
// RESIDUAL, STATED RATHER THAN SOLVED: a job can bind between this call and the write. The window is
// the width of an HTTP handler and the failure is the pre-existing one, so it is accepted — but it
// is not zero, and the way to close it is to make the check and the write share the lock, never to
// add a retry.
func (m *Manager) JobsOn(storageID string) []string {
	if storageID == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for jobID, b := range m.jobStorage {
		if b.storageID == storageID {
			out = append(out, jobID)
		}
	}
	sort.Strings(out) // deterministic: the message names a job, and a set iteration would vary it
	return out
}

// JobsOnDevice is JobsOn narrowed to ONE device, and it is the question reconciliation asks before
// it touches a (storage, device) pair (qn.6i D3).
//
// THE NARROWING IS THE POINT, not an optimisation. `JobsOn` answers "is this disk busy", which would
// defer the repair of every device on a storage because one of them is backing up — and under a
// SCHEDULED reconcile that is not a rare coincidence, it is most evenings. The hazard is per device:
// two devices commit to one disk through separate journals, separate trees and separate rows.
//
// An EMPTY udid matches nothing and returns nil, deliberately: every caller here has a udid in hand,
// so an empty one is a bug rather than a wildcard, and answering "no jobs" is the safe direction for
// a *reconcile* guard to be wrong in only if it is never reached — which is why the reconciler also
// holds the lease. Belt and braces, and the braces are the lease.
func (m *Manager) JobsOnDevice(storageID, udid string) []string {
	if udid == "" {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []string
	for jobID, b := range m.jobStorage {
		if b.storageID == storageID && b.udid == udid {
			out = append(out, jobID)
		}
	}
	sort.Strings(out)
	return out
}

// jobIsBound reports whether a jobID names a job that is LIVE right now.
//
// It is what tells a commit journal left by a CRASH from one a running job is currently driving —
// the distinction quince#731's blocker 1 turns on. The journal carries the JobID that wrote it
// (`journal.go:51`), so no new bookkeeping is needed to ask.
func (m *Manager) jobIsBound(jobID string) bool {
	if jobID == "" {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.jobStorage[jobID]
	return ok
}
