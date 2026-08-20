package deviceops

import (
	"log/slog"
	"os"
)

// PAIRING RECORDS BELONG TO THE MUXER, NOT TO QUINCE (qn.6r D1, ruling A on quince#1309).
//
// libimobiledevice stores no pairing records: `userpref_read/save/delete_pair_record` are each a
// message to the muxer, with no filesystem fallback (measured at 1.4.0, `common/userpref.c`).
// `/var/lib/lockdown` is the DAEMON's store — usbmuxd's, or netmuxd's `--plist-storage`. Before
// qn.6p quince supervised the muxer in its own container, so the daemon's store WAS quince's, and
// the distinction cost nothing; the split moved it and this file did not follow.
//
// So quince neither persists nor restores records. What stood here — a whole-dir copy between
// /var/lib/lockdown and a persistent dir under $QUINCE_DATA, plus the same-file guard quince#1310
// added to it — implemented quince as the custodian of records it does not own, and retires with
// the role (qn.6r D2). The guard is not being reverted: after this change there is no copy for it
// to defend.
//
// WHAT SURVIVES IS ONE PROBE, AND ONLY UNTIL qn.6r's NEXT SLICE. `Writable` asks whether quince
// can write to its own container-local /var/lib/lockdown, which is not the question that decides
// whether a pairing can be recorded — that is the muxer's store, and quince mounts it nowhere.
// D3 rules that no safe pre-check for it exists, so this is deleted rather than repointed once
// the muxer-side check lands. Kept here for one slice so this one carries a single claim.

// LockdownStore answers whether quince could write into the libimobiledevice system dir.
type LockdownStore struct {
	sysDir string // e.g. /var/lib/lockdown
	log    *slog.Logger
}

// NewLockdownStore returns a store over sysDir, where libimobiledevice looks for the daemon's
// records. It takes no data dir: nothing under $QUINCE_DATA holds pairing records any more.
func NewLockdownStore(sysDir string, log *slog.Logger) *LockdownStore {
	return &LockdownStore{sysDir: sysDir, log: log}
}

// Writable reports whether quince can write a pairing record, and why not when it cannot
// (qn.6p D7, Operator 2026-08-16: quince should detect a read-only lockdown dir "and mention it in
// UI instead of offering Pair button, like springback does").
//
// A WRITE PROBE, NOT A MOUNT-FLAG READ, and the choice is deliberate. `:ro` is visible through
// statfs's ST_RDONLY, but it is one of three ways this fails and the other two set no flag: a
// permissions problem, and a full filesystem. The question the UI needs answered is *can quince
// write a pairing record here*, so the check is to write one — an empty file, removed immediately,
// in a directory quince already owns. Nothing is read, so no record's content is touched (design
// §6: these are private-key-grade secrets).
//
// IT ASKS THE WRONG FILESYSTEM, AND THAT IS qn.6r D3's TO FIX, NOT THIS SLICE'S. The directory it
// probes is container-local now, so it answers *writable* about a path no muxer reads.
func (l *LockdownStore) Writable() (bool, string) {
	if err := os.MkdirAll(l.sysDir, 0o700); err != nil {
		return false, l.sysDir + " cannot be created: " + err.Error()
	}
	f, err := os.CreateTemp(l.sysDir, ".quince-pairwrite-*")
	if err != nil {
		return false, l.sysDir + " is not writable: " + err.Error()
	}
	name := f.Name()
	_ = f.Close()
	if err := os.Remove(name); err != nil {
		// Written but not removable is still writable for pairing's purposes; say so rather than
		// failing the check, and leave a trace so the stray file is explicable.
		l.log.Warn("lockdown: write probe left a file behind", "path", name, "error", err)
	}
	return true, ""
}
