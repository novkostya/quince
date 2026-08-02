package storage

// Slot is one storage the Manager speaks for: its identity, its root, and the Backend bound to
// that root.
//
// qn.6c story 3. The Manager holds a SET of these rather than one backend and one root, because
// every per-storage operation must resolve WHICH storage before it touches a path — and the four
// reads that used to take `m.backups` were the places that silently could not.
//
// STORY 5b: a Slot no longer always describes a storage that RESOLVED. An unreachable one is
// listed rather than refused, so a Slot can describe a storage quince knows about and cannot
// currently open.
type Slot struct {
	// StorageID is the UUID from quince-storage.json. "" means unattributed — the pre-qn.6c
	// world, where there is exactly one storage and no marker has been written yet.
	StorageID   string
	Name        string // the config entry's name; stable across replug
	Root        string // this storage's backups root
	Backend     Backend
	BackendName string

	// Retention is THIS storage's keep policy (qn.6c, quince#473). It moved onto the slot when
	// `storage:` flattened and the global `storage.retention:` block stopped existing — a list has
	// nowhere to put a global.
	//
	// Prune groups a device's versions by storage and applies each one's policy, so `keep_recent:
	// 10` means ten on THAT disk. A single policy across storages would have made a second disk
	// silently change what the first one keeps.
	Retention RetentionPolicy

	// Reachable is false for a storage quince could not open at startup — the disk is out, or the
	// path is readable but is not the backup medium. That is a STATE, not a refusal to serve
	// (Operator ruling 2026-08-01, quince#435).
	//
	// BACKEND IS NIL WHEN THIS IS FALSE, and that is the ruling's structural consequence rather
	// than an oversight: a backend is chosen by probing a filesystem, and a storage that could not
	// be reached was never probed. Every path that touches Backend must establish reachability
	// first — which is what `Usable` is for.
	Reachable bool

	// UnreachableCode is the machine-readable cause, "" when Reachable: the Resolution that failed
	// — `path_unreachable`, `missing_medium`, `backend_mismatch`. It is separate from the prose
	// because a client cannot branch on a sentence and a user cannot read an enum (contracts §2,
	// ruled 2026-08-01). The wire shape lands in 5c; this is the internal half.
	UnreachableCode string

	// UnreachableReason is the daemon's own sentence, carrying what only it knows — which path,
	// which marker. "" when Reachable.
	UnreachableReason string
}

// Usable reports whether this storage may be written to.
//
// THE INVARIANT THAT MAKES SERVING SAFE (quince#435, and it is not optional): a storage whose
// resolution did not succeed NEVER accepts a job. Serving with a disk missing is honest only while
// nothing can write to a storage quince could not verify — `missing_medium` is the case that proves
// it, because a readable path with no marker is exactly where a write would land on the wrong
// filesystem.
//
// It checks Backend too rather than trusting Reachable alone: the two are set together, and a
// predicate guarding a nil dereference should not depend on that staying true.
func (s Slot) Usable() bool { return s.Reachable && s.Backend != nil }
