package storage

import "sync"

// THE COMMIT LEASE (qn.6i D3, Operator ruling 2026-08-08 on quince#731).
//
// Two actors can drive one device's commit state on one storage: the backup engine, through
// CommitJob, and reconciliation, through ResumeCommit and the adopt path. Nothing prevented them
// from doing it at once — `reconcile.go` held no lock of any kind — and the ruling settled what to
// build: a lock or lease spanning the engine's commit path and reconciliation, rather than a proof
// that ResumeCommit is idempotent against a live commit. The rejected alternative would have been a
// property of TODAY's phase set, which qn.6h has just changed for zfs, so it would expire silently.
//
// THE KEY IS (storage, device), NOT storage. Two devices backing up to one disk commit through
// separate journals into separate trees, so a storage-wide lock would serialise work that never
// touches the same bytes — and under a scheduled reconcile it would mean one device's backup defers
// every other device's repair.
//
// THE RECONCILER ALWAYS LOSES, AND IT LOSES BY DEFERRING RATHER THAN WAITING. That is the ruling's
// second constraint in as many words: a held lock must not become a hang, and whichever side loses
// must degrade honestly rather than block a backup or a request. Reconciliation is idempotent and
// re-triggerable by construction — adopt-if-absent, mark-if-vanished, recompute — so a deferral
// costs one trigger. A blocked commit costs a multi-hour transfer. Hence TryClaim for the
// reconciler and Claim for the commit path: only one of them can wait, and it is the one that has
// nothing at stake.
//
// WHAT THE COMMIT PATH'S WAIT IS BOUNDED BY. Claim blocks only while a reconcile holds the SAME
// device on the SAME storage, and the reconciler holds it for one device's scan rather than for a
// whole pass. It is not silent: CommitJob logs when it waits.
type commitLease struct {
	mu sync.Mutex
}

// leaseFor returns the lease for one (storage, device), creating it on first use.
//
// Leases are never removed. The map is bounded by (declared storages × devices ever seen) — tens of
// entries on the largest install anyone has described — and removing one would need a reference
// count to be safe against a claimer that is between the lookup and the Lock. A few dozen empty
// mutexes is cheaper than that, and cannot be got wrong.
func (m *Manager) leaseFor(storageID, udid string) *commitLease {
	key := storageID + "\x00" + udid
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.leases == nil {
		m.leases = map[string]*commitLease{}
	}
	l, ok := m.leases[key]
	if !ok {
		l = &commitLease{}
		m.leases[key] = l
	}
	return l
}

// Claim takes the lease, waiting for it. Only the commit path may use this.
func (l *commitLease) Claim() { l.mu.Lock() }

// TryClaim takes the lease if it is free and reports whether it did. Only reconciliation may use
// this: a false answer is a deferral, never a retry loop.
func (l *commitLease) TryClaim() bool { return l.mu.TryLock() }

// Release drops the lease. Safe to call only by whoever claimed it.
func (l *commitLease) Release() { l.mu.Unlock() }
