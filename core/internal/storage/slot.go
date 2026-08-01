package storage

// Slot is one storage the Manager speaks for: its identity, its root, and the Backend bound to
// that root. `buildStorage` resolves these before handing them over, so a Slot always describes a
// storage that passed ResolveStorage.
//
// qn.6c story 3. The Manager holds a SET of these rather than one backend and one root, because
// every per-storage operation must resolve WHICH storage before it touches a path — and the four
// reads that used to take `m.backups` were the places that silently could not.
type Slot struct {
	// StorageID is the UUID from quince-storage.json. "" means unattributed — the pre-qn.6c
	// world, where there is exactly one storage and no marker has been written yet.
	StorageID   string
	Name        string // the config entry's name; stable across replug
	Root        string // this storage's backups root
	Backend     Backend
	BackendName string
}
