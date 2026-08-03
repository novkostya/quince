package demo

import "github.com/novkostya/quince/core/internal/config"

// StorageEntries is the demo's storage list in CONFIG form — the same two storages Storages()
// serves on GET /api/storages, written as the declarations a real operator would put in
// config.yml.
//
// WHY IT EXISTS: `--demo` never touched `config.storage`, because in demo mode `storages` is the
// provider and no storage.Manager is built from config. So the config document sat with
// `storage: null` — which `GET /api/config` returns and `config.Service.Replace` REFUSES, since a
// save must satisfy the storage requirement. A visitor who opened Settings and pressed Save got a
// 422 without having changed anything, on the surface quince#444 calls the reason a demo beats
// screenshots (quince#574).
//
// Operator ruling 2026-08-03: the demo DECLARES the storages it serves, and validation stays
// identical in both modes. The two alternatives were declined for reasons worth keeping:
// seeding one throwaway entry would make Settings show one storage while the cards show two, and
// teaching Replace about demo mode would let a visitor save a zero-storage config and see success
// where a real operator gets a 422 — a demo that validates differently is a screenshot with extra
// steps.
//
// IT IS SAFE PRECISELY BECAUSE NOTHING READS IT. In demo mode no subsystem consumes
// config.storage, so writing these entries cannot change behaviour; it only makes the document
// quince serves one that quince accepts back.
//
// Kept beside Storages() rather than derived from it: the two shapes are genuinely different —
// wire.Storage carries resolved, observed state (reachability, capacity, counts) where a config
// entry carries a declaration — and collapsing them would mean inventing a config field for
// "unreachable", which is not a thing anyone declares. TestDemoStoragesAgreeWithConfigEntries is
// what keeps them honest instead, and drift between them is exactly the incoherence the ruling
// rejected the one-entry option to avoid.
// RESOLVED BEFORE RETURN, and that is load-bearing rather than tidy. `Resolved()` normally runs at
// YAML PARSE, so every entry reaching Validate has its zfs mode/seed and retention filled in — and
// these entries are built in Go and never parsed, so without this they arrive half-filled and
// Validate refuses `storage[0].zfs.mode` regardless of backend. Found by seedDemoStorages refusing
// to start rather than by reading the schema, which is the argument for it being fatal.
func StorageEntries() []config.StorageEntry {
	return *config.ResolveStorages(&[]config.StorageEntry{
		// The reachable one, and the demo's default. `reflink` matches what the provider reports.
		{Name: "internal", Path: "/backups", Backend: "reflink", Default: true},

		// The unreachable one. `auto` rather than the `unknown` the provider reports, because the
		// two words answer different questions and only one of them is declarable: `unknown` is
		// what the RESOLVER says when it cannot probe a medium that is not there, and it is not a
		// legal config backend (auto | zfs | reflink | hardlink | copy). `auto` is what an operator
		// writes for a disk they have not characterised, so the pair reads exactly as production
		// does — declared `auto`, reported `unknown` while it is away.
		//
		// NOT `zfs`: that would oblige the demo to invent a whole zfs block (mode, seed) it has no
		// business having an opinion about.
		{Name: "shuttle", Path: "/mnt/shuttle", Backend: "auto"},
	})
}
