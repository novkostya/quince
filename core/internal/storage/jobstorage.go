package storage

import "fmt"

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
func (m *Manager) BindJobStorage(jobID, storageID string) error {
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
			m.jobStorage = map[string]string{}
		}
		m.jobStorage[jobID] = storageID
		return nil
	}
	return fmt.Errorf("no storage with id %q is declared", storageID)
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
	want, bound := m.jobStorage[jobID]
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
