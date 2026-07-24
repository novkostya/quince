# qn.6a — soak-ready UI (mobile + offline devices)

**Goal (provable at rung close).** A phone browser is a first-class quince client, and nothing the
soak needs to surface is invisible: failures, dead versions, offline devices, and the seeding wait
all render honestly. State honesty is the bar throughout — the UI never claims more than is proven.

> **STATUS: BUILDING.** Scope + both contract shapes are already ruled — (ch) (rung + why), (cj)
> (gate-11 findings routing), (ck) (drop `kind` from the card), (cv) (the two contract shapes:
> `missing: bool` on `wire.Version`, and a `seeding` job phase). This is the **last rung under the
> current process**; friction is recorded in `friction-notes.md` beside this spec, as evidence for
> the revamp. Decision-log letter claimed: **(da)**.

---

## Scope (each item carries its ruling letter)

1. **Mobile-first responsive/touch pass over the EXISTING IA** ((ch)) — NOT an IA redesign. The
   desktop-shaped pieces are the work: the fixed sidebar shell, the job-log pane, version lists,
   the pair/encryption dialogs, and the backup-history table. Target viewport ~390×844.
2. **Offline devices listed** ((ch)) — union live muxd presence with the distinct UDIDs already in
   the versions registry; persist the identity fetched at enrichment so offline rows carry a name.
   Same card shape, **disabled-but-explained** "Back up now", last-seen, version count. Versions
   stay browsable.
3. **Device labels in the backup list** ((ch)) — the dashboard "Recent backups" rows don't say
   which device a backup belongs to. Small, real.
4. **Gate-11 findings ((cj)):** #6 — a failed newest attempt gets a "needs attention · Retry" line
   on the device card (CORE: invisible failures make the soak worthless); #7 — client-side
   single-`is_latest`-per-device invariant fix in the versions store; #10-byte — honest byte
   labelling. Plus (ck): drop the `kind` label from the version card (kind stays internal/API).
5. **Dead versions rendered dead ((cv), contract ruled)** — add `missing: bool` to `wire.Version`.
   The UI renders a missing version explicitly dead: no size claim, no Unlock/browse, an
   "artifact gone — remove?" action wired to the existing `DELETE /api/versions/{id}`. Never omit.
6. **`seeding` job phase ((cv), contract ruled)** — between `preflight` and `backing_up`, emitted
   while storage Seed runs (near-instant on a resume — fine). UI narrates "Preparing — cloning from
   your last backup…". Demo JobControl scripts it so it is demoable + e2e-provable.
7. **The log-blob `SplitFunc` fix** ((cj) #3) — one fix clears the mangled log pane, the stale byte
   counter (the parser reading the *oldest* frame in a `\r`-joined blob), and log bloat. Stays
   small (a custom `bufio.SplitFunc` splitting on `\r`/`\n` + a test) → in scope.

**NOT in scope:** storage onboarding (qn.6, beside P1/P1b); the Synology/alpha prerequisites (DSM
spike, gate 12c); the seed-latency *mechanisms* (parked, evidence-gated — (cx)/(cz)); "Wake up"
push (post-qn.12 spike); any IA redesign; anything the multi-storage epic (cl) owns.

---

## Contract changes (the ONLY two; shapes ruled in (cv))

Landed with the build, following the qn.5b Reset precedent. Both are **non-breaking additions**
(a field, and an enum value).

### C1 — `missing: bool` on `wire.Version` (contracts §2)

```diff
   "structure_verified_at": "..." | null,   // set at commit (structural verification)
   "content_verified_at": "..." | null,     // set by verify_canary on a later unlock
-  "logical_bytes": 42400000000, "physical_bytes": 3400000000   // best-effort
+  "logical_bytes": 42400000000, "physical_bytes": 3400000000,  // best-effort
+  "missing": false
+  // qn.6a (cr)(a)/(cv): true = the registry row survives but its on-disk artifact is GONE
+  // (reconciliation could not find the snapshot/dir — roll-forward keeps the row, never drops
+  // it). store.VersionRow.Missing already exists and is honoured by LastBackup/recomputeLatest/
+  // Delete/Verify; this crosses it to the wire so the UI can render the version explicitly DEAD
+  // (no size claim, no Unlock, an "artifact gone — remove?" action on DELETE /api/versions/{id})
+  // instead of asserting a backup that does not exist. Omitting the row would silently shrink
+  // history — masking exactly the drift a soak exists to surface.
```

`Manager.toWire` maps `r.Missing → v.Missing`; `LastBackup`/retention already skip missing rows.

### C2 — `seeding` added to the `Job.state` enum (contracts §2)

```diff
-  "state": "queued" | "waiting_for_device" | "preflight" | "backing_up" | "verifying"
-         | "committing" | "succeeded" | "failed" | "cancelled" | "connection_lost",
+  "state": "queued" | "waiting_for_device" | "preflight" | "seeding" | "backing_up" | "verifying"
+         | "committing" | "succeeded" | "failed" | "cancelled" | "connection_lost",
+  // qn.6a (cu) opt 1/(cv): `seeding` is emitted between `preflight` and `backing_up` while
+  // storage Seed reflink/hardlink-clones latest/ → working/<udid> (O(files); ~23 s on a 34 GB
+  // iPhone, near-instant on a resume). The UI narrates "Preparing — cloning from your last
+  // backup…" instead of dead air before the on-device passcode prompt. progress.phase mirrors it.
```

**Why a state, not just a `progress.phase` value (rung-local ruling).** The ruling calls it a "job
phase … (contracts phase-enum addition)". In this contract the only *enumerated* phase list is
`Job.state`; `progress.phase` is an open example string, not an enum. The engine already models
each lifecycle stage (`preflight`/`backing_up`/`verifying`/`committing`) as a **state** mirrored
into `progress.phase`, and the dashboard card + details panel key their primary label off
`job.state` (`humanJobState`). Only a state makes the ruled "Preparing — cloning…" the headline; a
bare phase would leave the headline reading "Preflight" during a 23 s clone. So `seeding` is a new
state, with `progress.phase` mirroring it — consistent with every existing stage. It is a running
(non-terminal) state (`isRunning` includes it).

---

## Design

### Offline devices (item 2) — the largest piece; deliberately minimal ((ch) challenge 3)

The device registry today returns only devices with a **live** transport; a device with no
transports is dropped, so quince forgets a device the moment it is unplugged. Minimal union, no new
subsystem:

- **Offline set = the distinct UDIDs in the versions registry that are not currently live.** A
  device is remembered because it has backups — not because it was ever plugged in. `storage.Manager`
  gains `KnownUDIDs() []string` (`SELECT DISTINCT udid FROM versions`); the registry gets it via a
  `SetKnownUDIDs(fn)` hook (same shape as the existing `SetLastBackupSource`).
- **Persisted identity so offline rows carry a name.** Today `Registry.identity` is in-memory only
  (lost on restart). Add a small SQLite table `device_identity` (migration `0004`) — `udid` PK plus
  `name/model/ios_version/paired/backup_encryption/last_seen/updated_at`. `Enrich` upserts it (and
  records `last_seen` from the merged device when the device is present); startup loads the rows
  into `Registry.identity` + a `Registry.offlineSeen` map so an offline row renders its known name
  and a real "last seen" immediately after a restart. This is the "small table, not a new
  subsystem" the roadmap sanctioned; it also naturally covers `last_seen` for the offline card.
- **`Registry.Devices()` returns the union** — live devices first (in `order`), then offline shells
  (identity + `last_backup` overlaid, no transports, `last_seen` from the persisted value), sorted
  by last-seen desc. `Device(udid)` returns an offline shell for a non-live known UDID too, so a
  deep-link to a powered-off device's details page works.
- **Live online→offline transition.** When the last transport of a device that HAS versions
  detaches, the registry emits `device.updated` (the offline shell) *after* the normal
  `device.detached`, so a card seen live turns into an offline card without a page refresh rather
  than vanishing. (A device with no versions still just leaves, as today.)
- **Version count** is derived **client-side** from the versions store the UI already loads whole
  (`GET /api/versions` with no udid) — no backend count, no new field.

**Card behaviour (Operator-specified, (ch)).** An offline device keeps the same card shape with a
**disabled** "Back up now" carrying a reason on hover/tap ("Connect the device to back it up"),
never a dead button (the qn.4b pattern, the (bq) lesson). The card's control order becomes: active
job → progress; else not-present → disabled "Back up now" + reason; else paired → live "Back up
now"; else (present, unpaired) → Pair. Last-seen + version count show on every card.

### #6 failed-newest-attempt line (item 4)

`last_backup` correctly means last *success* ((bz)); a failed newest attempt must surface elsewhere
or failures go invisible — worthless for a soak ((cj), CORE). On the device card, find the device's
newest job by `started_at`; if it is `failed`/`connection_lost` and no job is currently running,
render a "needs attention · Retry" companion line (NOT a mutation of `last_backup`). Retry reuses
`useBackup.start("auto", newestFailed.id)` (inherits the intent chain).

### #7 single-`is_latest` (item 4)

The versions store keeps a demoted version's `is_latest=true` until reload → two "latest" badges.
Mirror the server invariant in `useVersionsStore.upsert`: upserting a version with `is_latest=true`
demotes every other version of the same UDID in the store. Pure UI.

### #10-byte honest labelling (item 4) + the SplitFunc (item 7)

idevicebackup2's `(X/Y)` size pair is the **current file's** transfer, not the backup total; the
trustworthy overall signal is `percent` ("NN% Finished"). Two coupled fixes:

- **SplitFunc.** Progress bars redraw with `\r` and no `\n`, so `bufio.ScanLines` buffers many
  frames into one giant "line": the log pane shows a mangled blob, and `reBytes.FindStringSubmatch`
  matches the **oldest** frame in it (stale bytes). A custom `bufio.SplitFunc` that treats `\r` and
  `\n` (and `\r\n`) as line terminators makes each frame its own token → latest bytes, clean pane,
  less bloat. Small + a table test.
- **Honest labelling (UI).** `JobProgressFull` stops presenting `bytes_done / bytes_total` as a
  backup total. Overall `percent` + `files_received` lead; the byte pair, when present, is labelled
  as the **current file**. No contract change — the field names/types are unchanged; only the
  documented meaning (current-transfer bytes) is pinned in the `wire` comment and the UI stops
  implying a whole-backup denominator. The demo emits current-file-style pairs so demo and real
  agree.

### `seeding` phase in the engine (item 6)

Split `preflight` into checks-only, then a `seeding` step:

```
transition → preflight ;  preflightChecks(...)                 // presence/pair/encryption/disk; no Seed
transition → seeding   ;  workDir, err := storage.Seed(...)    // the clone; phase mirrors state
transition → backing_up;  supervise(...)
```

A seed failure terminates `failed` with `seed work area: …` (nothing committed; the qn.5b Finding B
sentinel guard handles any partial on the next attempt — no discard here, matching today). The demo
scripts a `seeding` step in both the ambient loop and the on-demand `scriptBackup`.

---

## Stories / gates

**CI (mobile-proven where the claim is UI):**

1. Offline device: a powered-off device (versions present, no transports) appears with its
   persisted name, last-seen, version count, and a **disabled** "Back up now" whose reason is
   reachable — registry union test (Go) + a mobile e2e assertion at 390×844.
2. Dead version: a `missing` version renders explicitly dead (no size, no Unlock, a Remove action)
   and is never omitted — `VersionList` unit test + wire/golden.
3. `seeding` phase: a scripted backup passes through `seeding` with the "Preparing — cloning…"
   narration before `backing_up` — engine test (real) + demo + mobile e2e.
4. #6: a device whose newest attempt failed shows "needs attention · Retry" on the card while
   `last_backup` still shows the older success — DeviceCard test.
5. #7: applying a `version.created(is_latest)` demotes the prior latest in the store immediately —
   versions-store test.
6. SplitFunc: a `\r`-joined progress blob yields per-frame tokens with the **latest** bytes — parser
   split test.
7. Mobile pass: the whole "Back up now → seeding → progress → cancel → retry" flow is drivable at
   390×844 with no horizontal scroll and no sub-target control — a mobile e2e project/story.

**Rung gate (Operator, from the roadmap).** The Operator drives a complete backup **from the iPhone
browser** — start, watch live progress (incl. the seeding narration), cancel, retry — without
pinch-zooming or meeting an unusable control; a powered-off device appears with last-seen, version
count, and a disabled-with-reason "Back up now"; the backup list names its device; a dead version
shows dead. Then the soak begins.

**Proof obligation:** `make gates` + `make image` + e2e green in `quince-dev`; UI claims shown at a
mobile viewport (~390×844), not just desktop.
