# qn.5 — storage backends + startup reconciliation

**Goal.** A structurally-verified `idevicebackup2` backup tree commits into an immutable,
versioned store on any of four backends (`zfs` snapshot-native / `reflink` / `hardlink` /
`copy`, auto-selected by probe), such that the live namespace always presents a consistent
last-verified `latest/` for whole-tree offsite sync, every committed version is
listed/deletable via the API, and a crash at any commit phase reconciles to a defined state
on restart — **all provable on fixture trees + a manually-produced backup, with no backup
engine** (the "provable at rung close" rule; qn.4 supplies the engine after this).

**Status: CLOSED (CI-proven) — lab gate 12 RE-HOMED to qn.4a** (decisions log (bd), (bl), (bm)).
Cleared the pre-build spec-review gate (architect go, three amendments + five rulings, all
folded in). `make gates` + `make image` + `make gates-ui-e2e` green; the 11 CI stories pass; the
mirror-ladder ruling ((bi)/(bk)) is folded in ((bl)). Landed on `main` in four commits
(`285c40b`..`3ce5bb1`). **What is proven: the whole storage subsystem in CI** — the four
backends, auto-probe, journaled commit + `quince-version.json` markers, the startup-reconciliation
kill-matrix, adopted-version discovery, encryption-branched structural `Verify`, retention,
`RepairWorkingCopy`, the versions registry + `DELETE`, and the D5a anchored-filter contract; plus
the real-zfs commit + encrypted `Verify` were exercised on hardware during the gate-12
investigation (decisions (bf)→(bk)). **What is NOT yet proven on hardware, re-homed to qn.4a
(post-qn.4, a NAMED owner — not a silent defer, per the qn.2b→qn.7 precedent):** the host-side
`mirror` verb on the real rpool, iMazing-opens of a committed version, syncoid mid-write, and the
12c destructive hardlink-safety matrix. qn.4a is the first rung that runs real backups end-to-end
on a real backend, so it exercises qn.5's storage `Commit` on real traffic — the natural home for
these legs. The as-built details are in **Rung-ruled decisions** below.

- **Ruling 1 (CI-vs-lab split, confirmed).** fake-`zfs` in CI + real on the host is the line —
  do **not** attempt a file-backed pool (an unprivileged Alpine LXC running nested OCI can't
  honestly provide kernel-module access; it would break dev/CI gate-parity and test a topology
  nobody runs). The things a fake can't prove — real snapshot semantics, `.zfs` mounts,
  FICLONE-from-snapshot, `rslave` propagation — are exactly what gate 12 proves on the real
  topology. A GH-only supplementary real-pool workflow is a *future proposal*, not this rung.
- **Ruling 2 (`RepairWorkingCopy` split, confirmed).** Backend op here (design §4's "implemented
  in qn.5"), CLI surface in qn.4 — both canon lines satisfied. Now **proven** (amendment A2).
- **Ruling 3 (`Verify` split, confirmed + sharpener).** The tree-inspection half lives in
  storage because **adoption and reconciliation verify trees that have no process exit code to
  consult** — that is the reason the division exists, not a convenience (folded into the design).
- **Ruling 4 (`VersionAdmin`, confirmed).** Implementation of a frozen shape via the established
  consumer-interface pattern — not a contract change; implemented `DELETE` error codes get
  recorded in contracts §1 at close (the qn.3 pattern the Rule check already promises).
- **Ruling 5 (acceptance tree).** The qn.3-kept paired staging container — exactly what it was
  kept standing for; **no fresh capture**. Its device has encryption **ON**, so the manual tree
  is **encrypted** — which is what surfaced amendment A1.

The three amendments, folded in below:
- **A1 (substantive; canon already fixed — `44c55b9`, decisions log (bc)).** design §4's
  structural-verification checklist silently assumed an *unencrypted* `Manifest.db`; encrypted
  backups (product default + gate 12's actual input) encrypt the manifest itself since iOS 10.2,
  so passwordless open-and-sample is impossible. `Verify` now **branches on
  `Manifest.plist.IsEncrypted`** (design bullet + story 9 + Fixtures updated; an encrypted
  fixture variant added). Without this CI would pass and the lab gate fail — the wrong-order
  discovery.
- **A2.** `RepairWorkingCopy` was implemented-but-unproven → **new story 11** exercises it
  (zfs rebuild from last-good snapshot; namespace reseed from `latest/`).
- **A3.** `Prune`'s **trigger** is named in the design + rung-ruled decisions (post-commit
  and/or explicit call; **no scheduler this rung**).

This rung is M3's foundation: the roadmap swap ((ar)) put qn.5 *before* qn.4 precisely because
the engine's `succeeded` state requires `Commit()`, which is storage's to provide. It
implements stack **D5** (the two version models) + **D5a** (the offsite-sync contract) and
design **§5** (storage backend semantics), and it fills in the `VersionReader` seam qn.1
stubbed with `Empty` and the `version.created`/`version.deleted` events qn.1 froze.

## Boundary

**In scope:**

- `core/internal/storage` (**new package**) — the `Backend` interface (design §5's operation
  list: `Provision / Seed / Commit / Discard / List / Delete / Prune / Verify` + dirty-working
  reporting + `RepairWorkingCopy`), the **four backends**, the **auto-selection probe**, the
  **journaled commit** phase model with on-disk `quince-version.json` markers, the **startup
  reconciliation** subsystem (every phase × crash point → a defined repair), **adopted-version
  discovery**, and **structural verification** (design §4's passwordless checklist).
- `core/internal/storage/clonetree` (**new package**) — the one shared tree-clone package
  implementing the `reflink` (FICLONE ioctl), `hardlink` (`os.Link`), and `copy` strategies,
  used by all three consumers (namespace `Seed`, namespace version promotion, and the zfs
  `latest/` mirror). The `hardlink` strategy consults the **destructive safety matrix** and
  copies (never links) any in-place-mutating file class.
- `core/internal/store` — a `versions` table **migration** + a **versions registry** (insert /
  list-by-udid / get / delete / mark-missing / adopt) — the real `VersionReader`. Reuses the
  existing generic `audit` table for version-delete rows (`AuditSink`, already built qn.3).
- `core/internal/httpapi` — route **`DELETE /api/versions/{id}` → 202** (the frozen
  confirmed-destructive action) with an audit row + `version.deleted`; wire the real
  `VersionReader` into the already-routed `GET /api/versions`. No new consumer interface shapes
  beyond a `VersionAdmin` (delete) primitive, same pattern as `DeviceOps`/`MuxerControl`.
- `core/cmd/quince` — non-demo wiring: construct the storage subsystem from `storage.*` config,
  **run startup reconciliation before the server starts serving**, expose the registry as
  `VersionReader` + the delete primitive, emit `version.*` events.
- `core/internal/demo` — the demo `Provider` already serves `Version` fixtures (golden-tested);
  keep them conformant and add a demo delete path so the version list is exercisable in `--demo`
  without a backend.
- `deploy/` — **joins the boundary** (the qn.2b "deploy is in scope when the gate needs it"
  lesson) for exactly two artifacts the storage contract owns: (1) the **constrained-command
  `quince-zfs-helper`** reference for the hook mode (forced-command allowing only
  `snapshot`/`destroy`/`list` on `@quince-*` + `create` of children under the one configured
  parent — **dataset destroy is never in the key**), and (2) the **exact anchored rclone filter
  block** (D5a) shipped in the deploy docs. Both are generic (no Operator infra — privacy gate).

**Out of scope (explicit):**

- **Backup engine / job state machine / `idevicebackup2` supervisor** → **qn.4**. qn.5 never
  runs a backup; the test harness / Operator produces a tree and qn.5 commits it. The `Job`
  states `verifying`/`committing`/`succeeded` are qn.4's to *drive*; qn.5 supplies the backend
  operations they call and proves them stand alone. `httpapi.Jobs` stays `Empty`.
- **Vault / unlock / browse / content verification** → **qn.8**. qn.5 sets `browse_root` and
  `structure_verified_at` but never decrypts; `content_verified_at` stays null (set on a later
  unlock) — a display field, **not** a qn.5 gate dependency (state honesty: two verification
  levels shown honestly).
- **The headless CLI** (`quince versions list/verify/path --latest`, `quince device
  repair-working-copy`) → **qn.4** (roadmap). qn.5 implements the *backend operations* those
  commands will call (incl. `RepairWorkingCopy`, which design §4 says is "implemented in qn.5")
  and exercises them via tests — but ships **no** `cmd` CLI surface. (Rung-local boundary call,
  flagged in the Rule check.)
- **Version-list / retention UI** → qn.4 (history) + qn.6 (polish) + qn.12 (retention policy
  UI). qn.5 serves the API only; the demo already renders a version list from fixtures.
- **Post-commit offsite hook** (push-style rclone) → parked (roadmap "Later"). qn.5 proves the
  *pull-style whole-tree* contract only (D5a).
- **Contracts are consumed, not changed.** `Version`, `version.created`/`version.deleted`,
  `GET /api/versions`, `DELETE /api/versions/{id}`, and the `storage.*` config keys are already
  frozen (contracts §1/§2/§3/§6). qn.5 implements them and treats any build-vs-contract
  mismatch as a **gap** (protocol), not a silent divergence.

## Design

Canon this rung implements (linked, not repeated): stack **D5** (two version models —
snapshot-native zfs vs namespace-versioned reflink/hardlink/copy), **D5a** (whole-tree
offsite-sync contract, anchored filters), **D3** (verified-commit-or-discard, journaled
phases, reconciliation first-class); design **§4** (verifying/committing/discard, roll-forward,
repair-working-copy), **§5** (the backend interface + layouts + reconciliation matrix +
retention), **§6** (subprocess hygiene, committed versions read-only), **§10** (observability);
contracts **§1** (versions endpoints), **§2** (`Version` object), **§3** (`version.*` events),
**§6** (`storage.*` config); decisions log **(h)(k)(l)(m)(ar)**.

Decisions this rung settles (rung-local unless a `PROPOSED (gap)` block says otherwise):

- **The `Backend` Go interface.** One interface, four implementations, chosen by the probe.
  Operations are idempotent and log their real commands (design §5). `Commit` returns a
  `VersionRef` the registry rows from; `Discard` reports the dirty-working state on zfs.
- **`clonetree` — one package, three leaf strategies, one walk.** FICLONE via
  `golang.org/x/sys/unix` (`unix.IoctlFileClone` / the `FICLONE` ioctl — API **looked up live**
  at build, never shelled to `cp --reflink`; the ioctl passes through container bind mounts to
  the real fs, which is the only layer that must support it). `hardlink` via `os.Link` guarded
  by the safety matrix (§ Fixtures); `copy` via streamed `io.Copy` preserving mode/mtime.
  Symlinks/xattrs/sparse handling stated per strategy. Used identically by namespace `Seed`,
  namespace promotion, and the zfs `latest/` mirror.
- **Auto-selection probe** (`storage.backend: auto`): explicit zfs intent
  (`storage.zfs.parent_dataset` or a `hook_cmd` set) → `zfs`; else probe the **real `/backups`
  filesystem** at runtime — FICLONE a test file and verify block independence → `reflink`;
  `link()` + inode-identity test → `hardlink`; else `copy`. Deterministic, **logged**, and the
  chosen backend + why is a plain-language string for onboarding (design §9). Selecting `copy`
  (a degraded mode) is **surfaced**, never silent (hard rule).
- **Journaled commit + on-disk markers.** Each committed version carries a
  `quince-version.json` (version id, job id, created_at, structural-verify result, app version)
  **written before promotion**. Phase sequences (design §5):
  - namespace: `prepared → previous_archived → latest_promoted → registry_committed`
    (rename pair: `latest/` → `versions/<prev-ts>/`, then `work/<job>/` → `latest/`).
  - zfs: `prepared → snapshot_created → latest_rebuilt → registry_committed`
    (`latest/` rebuilt from the new snapshot's `.zfs` path — an immutable source even during a
    long clone).
  Phases persist to the job journal AND the disk markers so reconciliation is deterministic.
- **Startup reconciliation is first-class** (not cleanup). Disk is the source of truth; every
  half-state → a defined repair, following the **roll-forward principle**: once structural
  verification passed and the immutable artifact exists (the snapshot, or the promoted dir),
  reconciliation *completes* the remaining phases, never unwinds them — the only exception is an
  artifact whose marker is missing or fails its hash. The full matrix (design §5): half-rotated
  `latest`/`versions` → finish the rename by phase; version on disk/snapshot without a DB row →
  **adopt** (protected from retention); DB row without its dir/snapshot → **mark `missing`,
  never silently drop**; snapshot created but `latest/` stale → rebuild + swap; lost registry
  write → re-register from `quince-version.json`; orphaned `work/` → swept **only after**
  reconciliation completes; stale tmp → removed.
- **zfs exec vs hook.** `mode: exec` runs `zfs` argv directly (delegated privileges); `mode:
  hook` runs the configured `hook_cmd` (the forced-command SSH). Both go through the qn.3
  **subprocess hygiene** (argv arrays never shell, own process group via `setpgid`, ctx-killed;
  UDIDs **and** dataset names validated against strict patterns before reaching argv). **Dataset
  destroy is never issued** — quince prints the exact host command for a human (design §5).
  Child-dataset visibility is **probed** empirically (the `rbind,rslave` propagation path with
  a printed `pct set` fallback); the single-dataset fallback mode is documented.
- **Structural verification** (`Verify`, design §4/D3, passwordless, automatic). `Status.plist`
  parses with `SnapshotState == finished` AND `Manifest.plist`/`Info.plist` parse, then the DB
  checks **branch on `Manifest.plist.IsEncrypted`** (amendment A1 / decisions (bc) — the
  original checklist silently assumed an unencrypted manifest, impossible on the product default
  since iOS 10.2):
  - **encrypted** (the default, and gate 12's real input): `Manifest.db` exists + has
    non-trivial size + does **NOT** carry the plaintext SQLite magic (an "encrypted" manifest
    that opens as plain SQLite is a red flag) + blob-shard sanity (the two-hex-char dirs exist
    and are non-empty on a full backup). The per-record blob-resolution sample is **deferred to
    the content level** — qn.8's unlock owns it for encrypted versions.
  - **unencrypted**: the full checklist — `Manifest.db` opens read-only with the required tables
    AND a deterministic sample of Manifest records resolves to existing blob files.
  Sets `structure_verified_at`. The exit-code + `Backup Successful` output checks belong to
  qn.4's supervisor; qn.5's `Verify` is the **tree-inspection half**, and it lives in storage
  **because adoption and reconciliation verify trees that have no process exit code to
  consult** (ruling 3) — callable standalone, by reconciliation, and by adoption.
- **Version identity + naming.** Version id = ULID (`internal/id`); zfs snapshot name
  `@quince-<id>-<ts>` (contracts §2 example). `job_id` null ⇒ adopted. `is_latest` tracked in
  the registry; `logical_bytes`/`physical_bytes` best-effort (cached at commit, never a hot-path
  fs walk — perf budget).
- **Retention (`Prune`)** is backend-uniform policy (keep_recent + keep_daily + keep_weekly),
  acting on **quince-created versions only** (adopted versions are protected until the user says
  otherwise); deletion always requires confirmed UI action or explicit policy opt-in.
  **Trigger (A3, rung-ruled): `Prune` runs after a successful `Commit` (post-commit) and on an
  explicit call — there is NO scheduler in this rung** (a timed/cron retention sweep, if ever
  wanted, is a later rung); each pruned version audits + emits `version.deleted`.
- **`RepairWorkingCopy`** (design §4 escape hatch, "implemented in qn.5"): zfs rebuilds
  `working/` from the last good snapshot's `.zfs` path; namespace backends reseed `work/` from
  `latest/`. Backend op only — the `quince device repair-working-copy` CLI is qn.4.

If a live check contradicts canon (e.g. FICLONE unavailable where design assumes it, or the
`.zfs` snapshot mount refuses clones with no working fallback), that is a **gap** —
`PROPOSED (gap)` in the affected canon doc + an open question + stop, never a silent workaround.

### Interface facts to verify live (evidence — looked up, never remembered; decisions (al))

Recorded in Rung-ruled decisions once checked against the shipped tools/libs, code built to
match:

1. **FICLONE ioctl — RESOLVED (gate 12 + the five-round investigation, decisions (bf)→(bk)).**
   `golang.org/x/sys/unix` v0.47.0 exposes `unix.IoctlFileClone(destFd, srcFd int) error`
   (verified). Block cloning **works on the pool** — but proven ONLY at the pool level
   (`bcloneused`/`bclonesaved` +≈file-size, ALLOC flat); dataset-level `used`/`du` bills BRT
   clones full-size like dedup (the accounting trap that misled the first read). AND FICLONE
   returns **`EPERM` inside the unprivileged user-namespace** (LXC + the OCI container in it),
   measured in the exact production mount shape — so in-container reflink is unavailable in the
   secure topology; CI (unprivileged nerdctl + tmpfs) cannot prove reflink, the host/hook path
   proves it. This drives the mirror ladder (below).
2. **FICLONE-from-snapshot = `EXDEV` at EVERY layer — RESOLVED (definitive, direct-tested).** A
   snapshot is a separate superblock; cross-superblock FICLONE is refused by the kernel, no mount
   option changes it. So **all sharing strategies clone from `working/` under the per-UDID job
   lock, never from the `.zfs` mount** (working/ == the snapshot's content under the lock).
3. **`zfs` command surface** (`snapshot`/`create`/`list -t snapshot`/`destroy`) exit/text
   semantics for the exec path and the fake-CLI transcripts.
4. **rclone filter anchoring** — confirm a leading-`/` anchored `--filter "- /…/working/**"`
   excludes only the transfer-root path and an unanchored `**/working/**` would over-match
   (the D5a hazard), on the pinned rclone used for the gate.
5. **runtime image** — does the hook mode need an `ssh` client in the image, and is `zfs`
   present for exec mode? (Pins/packages looked up at build; a degraded selection is surfaced.)

## Stories

Each independently checkable. CI stories use fixture trees + a fake `zfs` CLI (the qn.3/qn.2b
`GO_WANT_HELPER_PROCESS` discipline) + a local rclone target — **no hardware in CI**.

1. **Auto-selection probe.** On a `reflink`-capable `/backups` the probe picks `reflink`; force
   each of `zfs`/`hardlink`/`copy` via config and the probe honors it; a `copy`-only fs selects
   `copy` and **logs the degraded choice** + emits the plain-language onboarding string. (unit +
   probe test against temp dirs.)
2. **clonetree.** reflink clones are byte-identical and **block-independent** (mutating the
   clone leaves the source untouched); hardlink links share an inode **except** for the
   matrix's in-place-mutating classes, which are copied; copy is a faithful independent tree.
   (unit; the reflink assertion is skipped-with-a-log where the fs lacks FICLONE — no silent
   pass.)
3. **Namespace commit** (reflink/hardlink/copy, table-driven). `Seed` populates `work/<job>/`
   from `latest/`; the harness writes a verified tree; `Verify` passes; `Commit` runs the
   journaled rotation → the version is listed, `latest/` is the newest, the previous version is
   at `versions/<ts>/`, `work/<job>/` is gone, and `quince-version.json` is present + hash-valid.
4. **zfs commit** (fake-`zfs` in CI). `Seed` is a no-op; `Verify` passes; commit runs
   `snapshot_created → latest_rebuilt` (from `.zfs`) `→ registry_committed`; the version IS the
   snapshot; `Discard` leaves a dirty `working/` and reports "working copy dirty, last good =
   <ts>" (no unwind).
5. **Startup reconciliation matrix.** Kill (simulated) at every commit phase — seed, verify,
   `prepared`, `previous_archived`/`snapshot_created`, `latest_promoted`/`latest_rebuilt`,
   `registry_committed` — on fixture trees; on restart each recovers to the **defined** state
   (roll-forward completes; the immutable artifact is never destroyed; orphaned `work/` swept
   only after). One test per (backend × phase) cell.
6. **Adopted-version discovery.** A tree/snapshot with a valid `quince-version.json` but no DB
   row → adopted (`job_id: null`), listed, **retention-protected**; a DB row whose dir/snapshot
   is gone → marked `missing`, **never dropped**; a marker failing its hash → not adopted,
   surfaced.
7. **Versions registry + API.** `GET /api/versions?udid=` returns the registry rows (< 100 ms,
   no fs walk on the hot path); `DELETE /api/versions/{id}` → 202, writes an audit row (event +
   version id + outcome, **no secret**), deletes the artifact, emits `version.deleted`; a commit
   emits `version.created`. Golden-tested against contracts §2 (regenerate via `make gen-golden`).
8. **Prune retention.** With `keep_recent/daily/weekly` set, `Prune` deletes only the correct
   quince-created versions, **never** an adopted one, and never without the policy/confirmation
   path; each deletion audits + emits `version.deleted`.
9. **Structural verification, both variants** (amendment A1). *Unencrypted* fixture: `Verify`
   passes the full checklist and stamps `structure_verified_at`; torn `Status.plist`,
   unparseable manifest, or a Manifest record pointing at a missing blob → **fails actionably**.
   *Encrypted* fixture (`Manifest.plist.IsEncrypted=true` + a `Manifest.db` blob that is
   non-trivial and **not** plaintext-SQLite-magic + populated blob shards): `Verify` passes on
   size/magic/shard sanity **without** opening the DB; an encrypted-flagged manifest that IS
   plaintext SQLite, or empty shards, → fails (the red-flag path). No false "verified" either way.
10. **Offsite-sync contract (D5a), automated.** A local rclone target syncs the whole
    `/backups` tree while the harness churns a `work/`/`working/` area; with the **anchored**
    filter block the upload contains a complete valid `latest/` + immutable versions and **never**
    `working/`/`work/`/`versions/`; an unanchored filter is shown (in a negative test) to
    over-match content dirs — proving why the block must be anchored.
11. **Repair working copy** (amendment A2 — `RepairWorkingCopy`, prove-by-running). zfs (fake-
    `zfs`): with a dirty `working/` and a last-good snapshot, `RepairWorkingCopy` rebuilds
    `working/` from that snapshot's `.zfs` path (committed versions untouched). Namespace: it
    reseeds `work/` from `latest/`. A repair with no last-good version fails honestly (nothing
    to rebuild from), never silently leaving a half-state.

**Lab gate (manual, hardware/host — RE-HOMED to qn.4a, decisions (bm); legs preserved verbatim
below for that session to inherit — a named owner, not a silent defer):** the gate-12
investigation (decisions (bf)→(bk)) already proved the real-zfs commit + encrypted `Verify` +
the reflink/EPERM/EXDEV facts on hardware; the remaining legs (host-side `mirror` verb, iMazing,
syncoid, the 12c destructive matrix) attach to qn.4a's first real-backup hardware session.

12. **Real zfs on the lab host + iMazing + syncoid + the destructive matrix.** On the lab
    deployment (the qn.3-kept paired staging container + the PVE host's real rpool, hook mode):
    - **(a) Provision + commit.** Create a child dataset via the constrained hook; confirm live
      `rbind,rslave` propagation (or the printed `pct set` fallback); commit a
      **manually-produced `idevicebackup2` tree** — **encrypted**, because the kept device's
      encryption is ON (ruling 5), so this exercises A1's encrypted `Verify` branch on real data
      — into a `@quince-*` snapshot; `latest/` rebuilt via **reflink-from-snapshot** (or the
      logged fallback). **iMazing opens the committed version.**
    - **(b) Replication mid-write.** A `syncoid` pass **during** a manual write replicates every
      committed version intact (dirty `working/` + all `@quince-*` restore points + a consistent
      `latest/`).
    - **(c) The destructive hardlink-safety matrix** (stack D5, wherever hardlinks are used — the
      `hardlink` backend + the zfs mirror's hardlink fallback; reflink builds exempt): byte- and
      metadata-identity of the previous version across full→incremental, big-file change,
      SQLite `-wal`/`-shm` companions, deletions, renames, interrupted backup + the next
      incremental, iOS upgrade, and encryption-settings change (truncate/chmod/xattr traps
      included). Any in-place-mutating class is **proven copied, not linked**.
    - Record outcomes + timings. **Every bug found becomes a scrubbed replay fixture before it
      is fixed** (hard rule).

## Gates

- `make gates` (go + vault + ui) + `make image` + `make gates-ui-e2e` green in `quince-dev`.
- **Wire `-cover` into `gates-go`** (the program doc's "when first needed" — this is that
  moment; one line) and **declare coverage** in the rung report: `go test -cover` summary for
  `internal/storage` + `clonetree` + the touched packages, plus a known-untested list with
  reasons.
- Stories 1–11 as Go tests (fixtures + fake-`zfs` + local rclone target); the reconciliation
  matrix (story 5) and the offsite contract (story 10) are the headline CI gates.
- `deploy/` artifacts (the `quince-zfs-helper` forced-command reference + the anchored filter
  block) are syntax-/lint-checked where cheap (the qn.2b pattern) and proven live by gate 12.
- Lab gate 12 recorded in the rung report + progress decisions log — the "provable at rung
  close" rule (CI proves the backends/reconciliation/API/offsite contract; gate 12 proves the
  real-zfs commit + iMazing-opens + syncoid + the destructive matrix end-to-end).

## Fixtures

- **Synthetic backup trees** — a minimal but structurally-valid MobileBackup2 layout
  (`Status.plist` with `SnapshotState=finished`, `Manifest.plist`, `Info.plist`, a `Manifest.db`
  with the required tables + a handful of referenced blob files), in **both an unencrypted and an
  encrypted variant** (amendment A1: encrypted = `Manifest.plist.IsEncrypted=true` + a
  `Manifest.db` blob that is non-trivial and carries **no** plaintext SQLite magic + populated
  two-hex-char blob shards), plus deliberately-broken variants for story 9 (torn status,
  unparseable manifest, missing blob, and an encrypted-flagged-but-plaintext-SQLite manifest).
  Small; committed.
- **Fake `zfs` CLI** (`GO_WANT_HELPER_PROCESS`) scripting `snapshot`/`create`/`list -t
  snapshot`/`destroy` + a `.zfs/snapshot/` layout, and an injectable-failure mode for the kill
  matrix.
- **Local rclone target** for story 10 (a `local:` remote into a temp dir; the pinned rclone
  looked up live).
- **Privacy (hard rule):** all fixtures use **synthetic UDIDs and synthetic device names**; no
  real backup data, no Operator paths/hosts in the committed `deploy/` artifacts (the helper +
  filter block are generic); `make privacy-check` before every commit; every lab bug → a
  scrubbed replay fixture before the fix.

## Rule check (mandatory — written before building; program spec shape + decisions log (as))

Every hard rule / canon boundary this rung touches *or comes near*, one compliance line each.

- **Never mutate a committed version / storage invariants** (hard rule — this rung's central
  rule). Re-proven per backend: the writer only ever touches the mutable area (`work/<job>` on
  namespace, `working/` on zfs); `latest/` is **never** written by the writer and changes only by
  journaled atomic swap; zfs versions are quince's own post-verify snapshots and browse never
  reads the head; startup reconciliation is part of the subsystem, not cleanup. **Stories 3–6 +
  gate 12 exercise it directly. Complies.**
- **No silent caps or fallbacks** (hard rule). The `copy` degraded selection, a dirty `working/`,
  a `missing` version, a reflink→hardlink→copy mirror fallback, and an unavailable FICLONE are
  each **surfaced** (log + onboarding string / status), never a quiet no-op. **Complies.**
- **Subprocesses: argv arrays, own process group, supervised, killed on end** (hard rule; design
  §6). The zfs exec/hook path reuses the qn.3 hygiene (argv slices, `setpgid`, ctx-kill); UDIDs
  **and dataset names** are validated against strict patterns before argv; **dataset destroy is
  never issued** (printed for a human). **Near-miss flagged:** the hook runs an
  Operator-configured `hook_cmd` — quince validates the *arguments* it appends (snapshot/dataset
  names) but does not sanitize the operator's own forced-command string (that is the SSH key's
  job by design). **Complies.**
- **State honesty** (hard rule). A version exists only after `Verify` + `Commit`;
  `structure_verified_at` is set at commit, `content_verified_at` stays null until a qn.8
  unlock (both shown honestly, two levels); adopted vs `missing` vs dirty-working are reported as
  they are, never guessed. **Complies.**
- **Config tidiness — D12** (hard rule). qn.5 adds **no** config key — `storage.backend`,
  `storage.zfs.{parent_dataset,mode,hook_cmd,mirror}`, and `storage.retention.*` all already
  exist in schema v0 (contracts §6). **Near-miss flagged:** nothing to add → trivially
  compliant; no UI-only state, no new env var, no secret in the file (the SSH key path is a path,
  not a secret). **Complies.**
- **A rung's goal is provable at rung close** (hard rule). The goal needs **no engine**: fixture
  trees + a manually-produced `idevicebackup2` tree drive `Seed`/`Verify`/`Commit` directly, and
  gate 12 proves the real-zfs path + iMazing + syncoid on **already-landed** infrastructure (the
  qn.3-kept container + the PVE rpool) — a dependency on landed rungs, not future ones. Content
  verification is honestly deferred to qn.8 as a *display field*, not a gate. **Complies.**
- **Perf budgets** (hard rule: version list API < 100 ms). `GET /api/versions` reads the registry
  (indexed by udid), never walks the fs; `logical/physical_bytes` are cached at commit
  (best-effort), not computed per request. **Complies.**
- **Privacy is a commit-time gate** (hard rule). Synthetic UDIDs + device names in all fixtures;
  the committed `deploy/` helper + rclone filter block are **generic** (no hosts/IPs/datasets/
  paths of the Operator — those stay in `local/environment.md`); commit messages describe *what*,
  never *where*; `make privacy-check` before every commit; a lab dump is rewritten or stays
  Operator-local. **Complies.**
- **Version pins / interface facts are looked up, never remembered** (hard rule). The FICLONE
  ioctl API, the `.zfs` clone behavior, the `zfs` command surface, rclone filter anchoring,
  syncoid semantics, and any new image package (ssh/zfs) are verified **live** at build and
  recorded as evidence; a contradiction with canon is a **gap**, not a workaround. **Complies.**
- **Every bug found on the lab box becomes a replay fixture before it's fixed** (hard rule).
  Gate 12's destructive matrix is designed to surface exactly such bugs; each gets a scrubbed
  fixture (a broken-tree variant or a fake-`zfs` transcript) before the fix. **Complies.**
- **Docs are part of the diff** (hard rule). design §5 / stack D5·D5a / contracts §1/§2/§3/§6
  already describe this rung — qn.5 **implements to them and verifies the match** (mismatch = a
  gap). Any implemented `DELETE /api/versions/{id}` error code is recorded in contracts §1 (the
  qn.3 pattern); the dashboard + decisions log are updated at rung end. **Complies.**
- **Contract / boundary discipline** (program loop step 2). qn.5 owns its slice of `core/` +
  `store` + `httpapi` + `deploy`; it **routes already-frozen** surfaces (`Version`, `version.*`,
  the versions endpoints) — implementation, not a contract change. **Near-miss flagged:** wiring
  the real `VersionReader` + adding the `DELETE` route + a `VersionAdmin` consumer primitive is
  first-time implementation of frozen shapes, not a contract-change rung; touching qn.4's engine
  or qn.8's vault would be a STOP. **Complies.**

## Rung-ruled decisions (settled during the build; *rung-ruled* canon)

- **Interface fact — FICLONE ioctl.** `golang.org/x/sys/unix` v0.47.0 exposes
  `unix.IoctlFileClone(destFd, srcFd int) error` (verified live in the pinned module); `clonetree`
  uses it, never `cp --reflink`. `x/sys` promoted from indirect to a direct dep.
- **Package split.** `internal/storage` (backends, journaled commit, reconciliation, retention,
  `Verify`, the `Manager` subsystem + `VersionReader`/`Delete`) + `internal/storage/clonetree`
  (the one FICLONE/hardlink/copy tree cloner) + a `versions` table & registry in `internal/store`.
  `Backend` is a same-package interface (two impls: `namespaceBackend`, `zfsBackend`).
- **Journaled phases = an on-disk `.quince-commit.json` per device** written before each rename
  (namespace: prepared→previous_archived→latest_promoted) / snapshot step (zfs:
  prepared→snapshot_created→latest_rebuilt), cleared on success; reconciliation reads it and
  rolls forward. Registry recovery is separate: a marker present on disk with no row → adopted
  (job_id null), a row with no artifact → `missing` (never dropped) — recomputed at every startup.
- **Marker = self-checksummed `quince-version.json`** (sha256 over the body with checksum
  cleared; no companion file). **`WriteMarker` replaces (remove-then-write), never truncates** —
  found during the build: on the hardlink backend a seeded `work/` shares inodes with committed
  `latest/`, so an in-place truncate of the marker would rewrite a committed version's identity.
  (The clonetree hardlink strategy already copies `MutatesInPlace` classes — dbs/`-wal`/`-shm`/
  top-level plists — for the same reason; the marker is quince's own such file.)
- **zfs `exec` vs `hook`** both go through the qn.3 subprocess hygiene (argv arrays, `setpgid`,
  ctx-kill); dataset/snapshot names are pattern-validated before argv; **dataset destroy is never
  issued**. Interface facts (`.zfs` layout, real `zfs` command surface, FICLONE-from-snapshot,
  `rslave` propagation) are proven on the host in lab gate 12; CI drives an injectable `zfsCLI.run`
  fake that records argv and simulates snapshots as directory copies.
- **CI-vs-lab for reflink + rclone (ruling 1 + rclone absent in the toolchain image).** The
  toolchain-go container's `/tmp` is tmpfs (no FICLONE) and ships no `rclone`, so: the `reflink`
  content proof is **skipped-with-a-log** in CI (copy/hardlink fully proven) and runs for-real in
  gate 12; the D5a offsite contract is proven in CI by `storage.PathExcluded` + a faithful Go
  sync-simulation of rclone's anchored-filter semantics (incl. the negative over-match test), with
  the real `rclone` binary run in gate 12. `storage.AnchoredFilterRules` is the single source for
  the filter block shipped in `deploy/storage.md`.
- **Mirror ladder — folds the (bi)/(bj)/(bk) ruling (stack D5).** The zfs `latest/` mirror
  ALWAYS clones from `working/` (never `.zfs` — EXDEV at every layer), via the risk-dominance
  ladder: **(i) hook configured → the constrained `mirror` verb rebuilds `latest/` HOST-side**
  (`cp -a --reflink=always` + atomic swap; touches only the derived `latest/`, never snapshots),
  reporting a host-side SHARED/COPIED verdict; **(ii) hookless → in-container reflink from
  `working/`**; **(iii) `EPERM`/unsupported → hardlink-under-matrix** (gate 12c); **(iv) → copy**,
  cost surfaced. A successful reflink self-selects (a non-sharing reflink is functionally a copy);
  the **one measurement selection edge** (measured-not-sharing → hardlink) is taken only with a
  usable channel — in-container has none yet (statfs `f_bavail` is a documented follow-up), so an
  in-container reflink is reported **UNVERIFIED per (bk)**, never a silent zero-space claim, and
  the risky downgrade is not taken. Every mode + honest claim is surfaced (`MirrorReport`, logged;
  `LastMirror()` for `/api/health`). CI proves the fallthrough (reflink-unavailable → hardlink →
  copy) + the hook-verb argv (fake hook, verdict parsed); the reflink-shares + host-side-hook
  paths are proven on the host/lab (gate 12). **Hardlink's tier is validated by gate 12c
  (pending on hardware) — flagged.**
- **Prune trigger (A3)** = post-commit + explicit call, no scheduler this rung.
- **`deploy/storage.md`** ships the constrained `quince-zfs-helper` forced-command reference
  (now incl. the **`mirror` verb**), and the anchored rclone filter block (both generic — no
  Operator infra).
