# quince — stack decisions

> Every load-bearing tech choice, with the reasoning and the alternatives that lost.
> Implementing agents follow these as settled law; reopening one requires the Operator
> saying so in chat, and the change lands here first.
>
> Grounding: every decision below assumes a protocol path that was **measured, not hoped
> for**. Before any of it was written, a hands-on feasibility pass
> (`../local/chatgpt-original-idea-chat.md`) drove a full encrypted USB backup (143k files)
> and a Wi-Fi incremental via netmuxd (3.6 GB, ~9 min, `Backup Successful`) in the target
> environment — Alpine LXC on Proxmox, libimobiledevice 1.4.0 — and iMazing opened the
> result. Those facts are load-bearing; decisions resting on them say so where they rest.

---

## D1. Core daemon: Go

**Decision.** The long-running service — device tracking, backup jobs, storage backends,
HTTP/WebSocket API, UI hosting — is a single Go binary (`quince`).

**Why.**
- *Deployment reality:* open-source target includes weak NAS boxes (Synology). A static
  musl-friendly binary idles at ~30 MB RSS, cross-compiles to amd64/arm64 trivially, and
  produces a tiny container. Python service stacks cost more RAM at idle and drag a
  runtime into every image layer.
- *Robustness bias:* explicit error handling, static types, `go test -race`, and a flat
  concurrency model (goroutines supervising subprocesses, fanning out WS events) fit a
  daemon whose whole job is "never wedge, never lie about state". It is also the language
  this project's human reviewers already work in, and review familiarity counts when
  correctness is the entire product.
- *LLM authorship:* the project will be written by Claude (Opus). Go's small surface and
  enforced explicitness produce reviewable diffs; the race detector and `vet` catch what
  reviews miss.
- The riskiest integration — MobileBackup2 — is subprocess-shaped regardless of language
  (see D3), so Go sacrifices nothing there.

**Alternatives.** *All-Python (FastAPI)*: fastest start, native access to decryption
libs, Operator has some Python — but weakest on idle footprint, packaging, and
"discourages shitty code" for a 24/7 daemon. Rejected. *Splitting the decryption layer
into a second-language sidecar*, to reuse a mature ecosystem there: rejected too, and D4
is why — it drags a whole second runtime into every image layer for a format small enough
to implement first-party in Go.

## D2. Device layer: speak the muxd protocol natively; drive Apple ops via CLI subprocesses

**Decision.** **`netmuxd` (v0.4+) is the single muxer daemon for BOTH transports** —
the Operator identified, and its README confirms, that netmuxd outgrew its name: it
now talks to USB devices directly via `nusb` with *"no dependency on a separate
usbmuxd daemon"*, alongside its original mDNS Wi-Fi discovery. The core's muxd client
connects to its socket in `Listen` mode → attach/detach events push to the UI in real
time, with per-transport presence read from the muxer protocol's `ConnectionType`.
The client is written against *N* muxer sockets (config: `devices.*`), so alternate
topologies (classic `usbmuxd` for USB alongside netmuxd, or the hardened profile's
external socket) are configuration, not code changes. Pairing, device info, and
backups execute the proven libimobiledevice CLIs (`idevicepair`, `ideviceinfo`,
`idevicebackup2`) as argv subprocesses (never shell strings), pointed at the muxer via
`USBMUXD_SOCKET_ADDRESS`.

**Why.** Listen mode is what makes "iPhone appears → UI updates instantly" real — no
polling. One daemon for both transports collapses the merge complexity. The protocol is
small, documented, and has a reference Go implementation to crib from (go-ios, MIT).
The CLIs are exactly what succeeded in the lab; wrapping them keeps us on the paved
road and keeps pairing records (`/var/lib/lockdown`) compatible with the whole
libimobiledevice toolchain. go-ios itself is automation-focused (no MobileBackup2), so
it is a protocol reference, not a dependency.

**Honest risk + fallback (ruled 2026-07-19).** netmuxd's USB path is *young* (added in
the v0.4 line; v0.4.3 released 2026-07-14) versus usbmuxd's decades of hardening — and
USB is our reliability anchor. This is not hypothetical: **a backup-breaking failure in netmuxd's USB path is on
record from the feasibility lab (2026-07-13)**, exact line preserved in the lab log:

```
[2026-07-13T18:30:10Z WARN  netmuxd::usb::mux] dev=0 CONTROL ERROR: asyncReadComplete,
message was too large (65536 bytes, max = 65535)
```

— exactly one byte over a **u16** boundary in the USB mux read path: the signature of
a `0xFFFF` length-field/buffer guard meeting a `0x10000`-byte message, plausibly a
one-line fix. Any real backup trips it immediately (backup messages exceed 64 KiB at
once). As of 2026-07-19 nothing matching it is in netmuxd's issue tracker. Notably,
**v0.4.3 shipped the day after the observation** with the note "Fixes iTunes on the
Apple mux" — possibly this very bug, unconfirmed. Consequences:
- **Until netmuxd-USB is proven clean on real hardware, the configured default for USB
  is the two-daemon topology**: `usbmuxd` serves USB, netmuxd serves Wi-Fi. The daemon
  is `apk add usbmuxd` on **Alpine ≥ 3.24 community ONLY** (verified per-branch against
  the APKINDEX files, 2026-07-19: absent in 3.21–3.23) — hence the runtime base is
  Alpine 3.24 (`versions.env`). The qn.0 "no package on 3.21" finding was CORRECT; an
  intermediate architect claim of all-branch availability was the faulty one (apk's
  `--repository` flag *appends* to configured repos — it answered from the host's own
  3.24). Lesson: package-existence claims are verified against the branch's APKINDEX or
  in a clean container of that branch, never with `apk search --repository` on a
  configured host. Single-muxer netmuxd is the goal state, one config flip away.
- The netmuxd-USB audition on the pinned v0.4.3 **with real backup traffic** (which
  crosses the 64 KiB message boundary immediately — the same workload that failed on
  2026-07-13) now runs in **qn.7** (originally qn.2's lab gate, re-homed via qn.2b —
  decisions log (aw): it pairs with qn.7's netmuxd co-supervision, and `idevicepair
  unpair` destroys the lab pairing record, so it belongs in the dedicated hardening
  session; procedure preserved verbatim in the qn.2b spec, gate 8): clean → flip the
  default to single-muxer and credit the v0.4.3 mux fix; reproduces → file the upstream
  issue with the exact log line, and optionally carry a patch in our pinned source build
  (the same pattern as the qn.7 libimobiledevice timeout patch). Until then the
  two-daemon default stands — proven on hardware by qn.2b's supervised-usbmuxd gate.
- qn.4/qn.5 re-prove sustained full USB backup through whichever topology is default
  before it carries real data. Note the protocol floor
either way: initial pairing and enabling Wi-Fi sync require a USB *connection* — a
fresh device can never be adopted over Wi-Fi — so USB must work by the qn.6 gate
("fresh user pairs via UI"); netmuxd-first/Wi-Fi-first sequencing inside qn.2/qn.3 is
encouraged (the Operator's device is pre-paired; records in the lab CT).

**Verified netmuxd invocation (qn.4c, run against the shipped pinned v0.4.3 — not remembered).**
quince supervises netmuxd as
`netmuxd --host <h> --port <p> --socket-path <private> --disable-usb`:

- `--host/--port` from `devices.netmuxd_addr`, making the configured address authoritative (the
  same discipline as usbmuxd's `-S`).
- **`--socket-path` is a safety flag, not a preference.** netmuxd deletes and rebinds whatever
  unix socket that names, and its default is `/var/run/usbmuxd`. Reproduced in the built image:
  with usbmuxd running and serving, starting netmuxd on the default path logged `Deleting old
  Unix socket` and took it over — usbmuxd stayed alive with its socket inode gone, i.e. a
  **silent USB blackout**. quince gives netmuxd a private path and refuses to supervise it at all
  if that path would equal `devices.usbmuxd_socket`. (The feasibility lab hit the same class of
  accident by running `netmuxd --version`, which is not a flag, so it started normally.)
- `--disable-unix` was the alternative way to avoid the collision and was **rejected**: it puts
  netmuxd in "host mode", where it depends on another unix-mode daemon being alive — coupling
  Wi-Fi health to USB health, backwards for two transports that should fail independently.
- `--disable-usb` keeps this decision's two-daemon split real: without it both daemons claim the
  same USB device. It is the single flag the single-muxer flip removes after qn.7's audition.
- **The pair-record store is the MUXER's, and quince mounts it nowhere.**
  `userpref_read/save/delete_pair_record` are each a message to the muxer with no filesystem
  fallback (libimobiledevice 1.4.0, `common/userpref.c`), so `/var/lib/lockdown` is the daemon's
  directory. netmuxd both reads it and **writes** it — `SavePairRecord` is implemented (pinned
  `ac8da97`) — so the muxer must be able to write there, or a pairing cannot be recorded.
  `deploy/compose.yml` passes `--plist-storage` explicitly and keeps that store outside quince's
  data directory. `RUST_LOG=info` is injected when unset, since netmuxd is silent below `error`.
- **Wi-Fi discovery is mDNS-only**, so a supervised netmuxd is necessary but not sufficient: the
  container must be able to receive multicast from the LAN (`deploy/compose.yml`).

**Consequence — AND THE MUXER HALF OF IT IS RETIRED IN v0.1 (qn.6p).** The container ships
**neither** daemon: the operator runs the muxer and quince dials it, so a daemon in the image is one
nothing could start (`devices.manage_muxer: true` is refused). `NETMUXD_REF` and `RUST_IMAGE` left
`versions.env` with the build stage that consumed them. **Everything this decision RULES is untouched** — usbmuxd
stays the USB anchor, netmuxd's argv stays verified — it is parked in `muxsup` rather than shipped,
and the netmuxd-USB audition (quince#326) is still what would change it.

**One trap the removal exposed, and it belongs beside the patches.** `apk add usbmuxd` pulls
`libimobiledevice`, which **replaces the patched `/usr/lib/libimobiledevice-1.0.so.6` — a
1,136,400-byte regular file — with a symlink into the stock package** (measured 2026-08-16).
Whoever restores the in-container profile must add it *above* the `COPY`, or `0001` disappears and
the symptom is a premature receive error on a slow passcode: the exact bug it fixes, arriving with
nothing to connect it to a Dockerfile line.

The container ships **`libimobiledevice` built from source at a pinned tag with in-tree
patches** (qn.6b, `LIBIMOBILEDEVICE_REF` in `versions.env`; `deploy/patches/libimobiledevice/`):
`0001` raises the 30 s default service receive timeouts to **15 min**
(`src/property_list_service.c` + `src/service.c` — upstream issue #1413, reproduced in the
lab (ct)) so a slow passcode / large-device transfer / transient Wi-Fi flap does not trip a
premature receive error; `0002` adds `idevicebackup2 --gate <path>`, a pause after the Backup
request (passcode already fired) and before the message loop, so quince seeds `working/` in
parallel and the on-device passcode prompt shows in ~1–2 s (candidate C, the seed-latency fix).
This lands in **qn.6b** (pulled out of qn.7 (de)), **before** the first public release, since
Wi-Fi is a primary transport (D13). **The raised timeout is the default for EVERY libimobiledevice
binary**, so quince bounds its non-backup device ops (pair/validate/enrich) with a Go-side deadline
(qn.6b amendment A) — the 15-min patience is intentional only on the backup receive path, where the
liveness sampler (design §4) is the backstop, held **longer** than the tool timeout so a sampler kill
never cuts a flap the tool was riding out.

## D3. Backup engine: never-mutate-committed state machine around `idevicebackup2`

**Decision.** A backup job is a state machine
(`queued → waiting_for_device → preflight → backing_up → verifying → committing →
succeeded`, with `failed / cancelled / connection_lost` terminals) built on one
invariant: **a committed version is immutable — `idevicebackup2` only ever writes into
the backend's working area.** Verify is two-layered: *structural verification*
(automatic, passwordless — exit code AND `Backup Successful` in output AND
`Status.plist` sane AND `Manifest.db` opens read-only with required tables AND sampled
Manifest records point at existing blobs) runs on every job; *content verification*
(decrypt a canary file) runs on the user's next unlock and is recorded as
`content_verified_at`. The UI shows the levels honestly ("backup completed · structure
verified · decryption verified 3 days ago"). Commit follows a **journaled phase model**
(`prepared → version_promoted → latest_swapped → registry_committed`) with on-disk
markers, and **startup reconciliation is a first-class subsystem**: after any crash the
disk is the source of truth and every half-state has a defined repair (design §5).

**Why.** Verified-commit-or-discard turns a fragile protocol into a transaction, and
admitting that commit itself is multiple filesystem operations (rename + pointer swap +
registry write) is what makes crash recovery designable instead of accidental.

**Lab grounding.** MobileBackup2 over Wi-Fi drops sessions, cannot resume within a
file, and reports progress erratically (multi-minute silence is normal) — hence the
activity-sampler liveness with staged stall states (design §4), and never "resuming" a
torn session: recovery is always a fresh job, user-initiated per the assisted model
(D13 — no automatic retries exist). Never run two jobs for one UDID.

## D4. Vault: encrypted-backup reading behind a language-neutral seam

**Decision.** All encrypted-backup reading lives behind a Go `vault.Vault` interface —
the swappable seam. It takes the backup password, unlocks the keybag, and answers list /
stat / decrypt-file / domain queries; keys live only for the duration of an unlocked
session and are gone at lock or timeout. There is no batch indexer: reading is lazy and
session-scoped (D8).

**Built on quince's own Go libraries, as ordinary pinned module dependencies** — Operator
ruling, 2026-07-30; a module dependency, not vendored. Decryption is
[`ios-backup-crypt`](https://github.com/novkostya/ios-backup-crypt): keybag, PBKDF2,
AES-CBC/KW, `Manifest.db`, file streaming. Domain parsing is
[`ios-backup-parser`](https://github.com/novkostya/ios-backup-parser): typed records per
domain, plus a capability report naming what a given backup's schema *cannot* provide —
which is "no silent caps or fallbacks" one layer down, and the degraded-mode surface the
UI needs. Both are MIT, live in their own public repos, and are written for quince rather
than adopted. **This costs zero new third-party surface:** their direct dependencies
(`howett.net/plist`, `modernc.org/sqlite`) are already in `core/go.mod` at identical
versions. Re-verify that when the rung starts.

**Why first-party Go.** One language and one runtime in the image, on a product whose
deployment target is a weak NAS (D1). The format is small and frozen — backup encryption
has been stable since iOS 10.2 — and every primitive has a mature Go counterpart, so a
first-party implementation is tractable rather than heroic. Owning it also makes the
fixture-backup generator free: it falls out of the decryption library's own
encrypt/builder side, which is what lets the conformance suite run in CI rather than only
on lab hardware.

**The seam is designed for replacement.** The core never knows which implementation it
holds. The conformance suite — golden requests/responses against fixture backups — is the
contract's executable form, and **any implementation must pass it before it can ship.**
That is a gate, not a preference.

**MEASURED — the process model is IN-PROCESS on the evidence, and the number is here rather
than in a report** ([quince#270](https://github.com/novkostya/quince/issues/270) §6; qn.8
spec D10). The deciding measurement was always *peak RSS reading the largest realistic
backup*, and it has been taken against synthetic manifests on a session box by
`core/cmd/rss-spike`:

| phase | input | peak RSS |
| --- | --- | --- |
| unlock | 1 000 → 50 000 rows | 7.2 → 10.9 MiB |
| full walk | 1 000 → 50 000 rows | 12.4 → 19.6 MiB |
| stream one file | 1 MiB → 128 MiB | 7.8 → 7.9 MiB |

**The stream row is the one that settles the design question.** A 128× increase in file size
moves peak RSS by nothing at all, so `DecryptFile` genuinely streams — the property the
whole in-process case rests on. The manifest curves plateau rather than scale: 50× the rows
for 1.5× the memory, with the residual consistent with the SQLite index itself rather than
with per-row retention.

Against qn.8 D10.3's confirmed threshold: **(a)** under 256 MB — passes with more than an
order of magnitude to spare; **(b)** no growth beyond a flat streaming constant — passes.
**(c)**, memory returning to baseline after `lock`, is **not measured here and cannot be**:
a process that exits has no post-lock RSS. It is owed to G7, in-process, on the
implementation slice.

**So the sidecar is not built, and the reasons it might be are unchanged rather than
refuted.** Crash isolation and an rlimitable ceiling are still real things a child process
buys; what the number removes is the *memory* argument for paying for them now. The seam is
the interface either way (`contracts.md` §4), so an RPC implementation remains a drop-in
whenever something else earns it.

**THE FIRST VERSION OF THIS MEASUREMENT WAS WRONG IN THE DIRECTION THAT WOULD HAVE DECIDED
IT THE OTHER WAY, and that is worth more than the figures.** It built each fixture inside
the process it was measuring. `fixture` reads the whole assembled `Manifest.db` into memory
and holds plaintext and ciphertext at once, so the readings rose 13.6 → 124.9 MiB across
1 000 → 50 000 rows and a 128 MiB file appeared to cost 777.9 MiB. Every one of those
numbers was a true reading of the wrong thing, nothing looked broken, and **a rising curve
is exactly what clause (b) fails on** — the instrument would have bought a sidecar. The
harness now builds in a separate process and measures only the reader.
**Partly built, and not yet validated on real data.** `core/internal/vault/` now holds the
`vault.Vault` interface and the session registry (qn.8 slice 5); **nothing implements the
interface yet**, which is the slice that reads a backup. The decryption library's
correctness today rests on known-answer vectors, a synthetic round trip under `-race`, and a
differential against an independent reference implementation — **not** on a real device
backup. The rung gate
(*unlock a real version, browse domains, download a file, lock*) is what closes that, and
it is operator-local work. `CLAUDE.md`'s *"hardware-proven over both transports"* is a
claim about the backup engine; the vault does not inherit it.

## D5. Storage: two genuinely different version models — ZFS snapshot-native, or namespace-versioned

**Decision.** A `VersionBackend` implements `Seed / Commit / Discard / List / Delete /
Prune / Verify`, chosen by capability probe (`storage.backend: auto` overridable) — but
the backends do NOT share one layout, because ZFS and plain filesystems version
differently (Operator ruling: no hardlink games under ZFS):

1. **`zfs` — snapshot-native, one dataset per device** (Operator ruling: snapshot
   streams must be independent — versioning device A must never snapshot device B — and
   the per-device snapshot list is the version list). Layout inside each child dataset
   `<parent>/<udid>`:
   > **qn.5b SUPERSEDES the commit-time-mirror model below (built 2026-07-24, decisions
   > (cg)/(co)).** `latest/` is no longer a mirror *rebuilt from the snapshot at commit*;
   > it **IS the backup**. A **per-job `working/<udid>`** is seeded from `latest/` at job
   > start, verified, then **atomically exchanged** into `latest/` (`renameat2(RENAME_
   > EXCHANGE)`, in-container, no privilege), and the snapshot captures `latest/` = the
   > version (`browse_root` → `…/latest`). Between backups the dataset holds **only
   > `latest/`**. The reflink MOVED from commit (rebuild) to job-start (seed): the hook's
   > `mirror` verb became a **`seed` verb**, and the in-container ladder is **reflink →
   > copy — never hardlink** for the seed (it would alias the committed `latest/`, gate
   > 12c). The reflink measurement facts below (pool clones fine / dataset accounting lies
   > / userns blocks FICLONE) still hold — they now govern the *seed*, not a mirror. The
   > detail below is retained as the reflink saga's record; the live model is the qn.5b spec.
   - `working/` — the MobileBackup2 working copy for a job, seeded from `latest/` at job
     start (Apple-designed, most-tested write path); honestly dirty mid-job; **never** a
     sync source; kept dirty on FAILURE so a retry resumes, removed on success (exchanged
     into `latest/`). (Named `working/`, not `current/`, by Operator ruling: the name must
     scream "possibly dirty" — `working`/`latest` reads correctly, `current`/`latest` does not.)
   - `latest/` — historically (pre-qn.5b) a consistent mirror of the last *verified* backup,
     rebuilt at commit and swapped atomically. **The snapshot is the canonical version; `latest/`
     is its materialized view**: built from the just-created snapshot's `.zfs` path (an
     immutable source even during a long clone/copy fallback), by the first mirror
     strategy that actually works — reflink → hardlink (safety-matrix-gated) → copy.
     **RESOLVED 2026-07-20 after a three-round investigation (decisions log
     (bf)→(bg)→(bh)→(bi)) — the zfs mirror uses a strategy ladder, because three
     distinct facts were established:** (1) *the pool clones fine* (host-side:
     `bclonesaved` +≈file-size, ALLOC flat); (2) *dataset-level accounting lies* —
     ZFS bills BRT clones like dedup (full size per reference in `used`/`du`), so
     sharing is verified ONLY at pool level (`zpool get bcloneused,bclonesaved`, or
     the `avail` delta reachable via the hook's `list` verb). **The SNAPSHOT columns
     lie the same way, with a signature that looks like catastrophe ((dl),
     2026-07-24):** under qn.5b's reclone-per-generation lifecycle, every snapshot
     older than the newest bills ~the FULL tree as "unique" `USED` (the next seed's
     exchange replaced every block *pointer*, so the old snapshot is the sole
     referent of its pointers — while the physical blocks stay BRT-shared), and the
     newest snapshot bills ~0 (the head still shares its pointers). A 34 GB device
     with N generations therefore *lists* ~N×34 GB in `USEDSNAP` while physically
     paying roughly one tree + real deltas. Verified live: a 177 GB listing resolved
     to `bclonesaved 122G` + `bcloneused 37G` ≈ ~55 GB actual. Never diagnose from
     `zfs list -o space`; always the pool ledger. **The one place this cost becomes
     REAL: `zfs send` does not preserve block cloning** — a syncoid/replication
     target rematerializes every `@quince-*` snapshot at full billed size, so
     snapshot RETENTION is a real capacity knob on any replica (not on the origin
     pool), and pruning old generations is the lever. **A genuine `cp` on seed
     would NOT help the replica ((do)):** send size is driven by block BIRTHS,
     not physical sharing — both reflink and real copy mint an entirely newborn
     tree each generation, so incremental sends are ~full-size either way;
     dropping reflink would only make the ORIGIN pay full price too (reflink is
     pure origin-side win, send-neutral). Replica-side levers, in order:
     retention/selective replication; `dedup=on` on the RECEIVING dataset only
     (fast-dedup — recv rematerializes, the DDT collapses it back to ~deltas,
     cost quarantined to the replica); or a content-addressed offsite channel
     (restic/borg) instead of send. The D5a PRIMARY channel (rclone of `latest/`,
     file-level, mtime-preserved by `cp -a`) is delta-efficient and unaffected.
     The mutate-in-place lifecycle that WOULD make sends delta-sized is **RULED** —
     Operator, 2026-08-04, quince#591; design §5, end of section — and **NOT BUILT**.
     It sat here as the epic-(cl) `zfs-native` candidate and *"a post-freeze redesign,
     not a tweak"*; the freeze lifted 2026-07-30 and the candidate is now the ruling,
     so this clause defers nothing and must not be read as parking it; (3) *the unprivileged
     user-namespace blocks FICLONE outright (`EPERM`)* — measured in the exact
     production shape (rpool child rbind'd into an unprivileged CT, and the OCI
     container inside it), so in-container reflink is unavailable in the
     recommended secure topology. **The mirror ladder (ruled (bi)):**
     (i) hook configured → a new constrained **`mirror` verb** runs the rebuild
     host-side, where FICLONE works (`cp -a --reflink=always` from `working/` under
     the job lock into a temp dir + atomic swap; constrained to children of the
     configured parent; touches only the DERIVED `latest/` — never snapshots — so
     even a buggy verb cannot damage canonical versions); (ii) no hook →
     in-container reflink attempt (covers privileged/bare-metal topologies; fails
     fast with `EPERM` in unprivileged ones, self-selecting down the ladder);
     (iii) hardlink — **gate 12c PASSED on hardware 2026-08-10 (quince#518)**, so this
     rung is live rather than matrix-pending, and it shares every regular file;
     (iv) copy — always correct,
     cost SURFACED (backend-selection string, commit log, health;
     ~full-backup-size write amplification per commit stated plainly), never
     silent. EXDEV-from-snapshot holds at every layer (kernel: separate
     superblock), so ALL sharing strategies clone from `working/` under the job
     lock, never from the `.zfs` mount. **Probe semantics (refined 2026-07-20, (bj), corrected (bk) — both
     Operator-challenged): the sharing measurement governs REPORTING plus exactly
     ONE selection edge.** Reflink outranks hardlink *when both share* because
     clones are independent while hardlinks alias (in-place mutation of `working/`
     would silently corrupt a hardlinked `latest/` — the matrix-gated risk), so
     the ladder orders by RISK dominance, not space. The one edge: a
     **measured-not-sharing** reflink falls through to hardlink-under-matrix
     (downgrade-for-space is allowed; upgrading into aliasing risk blindly is
     not). Absent any measurement channel, reflink wins on the risk asymmetry —
     its worst case is copy COST (reported "unverified"), hardlink's worst case
     is silent `latest/` corruption. The measurement otherwise decides what
     quince honestly *claims* ("zero-space verified" / "sharing unverifiable in
     this topology — budget full-copy cost" / "copy"). Measurement channels, best available:
     **`FIEMAP_EXTENT_SHARED` on the test clone** (`FS_IOC_FIEMAP`, quince#747 — exact,
     per-file, unaffected by concurrent writers, and the one the NAMESPACE probe uses; btrfs
     and XFS both answer it, ZFS answers `EOPNOTSUPP`, measured) → hook `list` avail-delta →
     syscall-only `statfs(2)`
     `f_bavail` delta around an incompressible test clone (works in any container;
     sync-and-settle for txg accounting lag) → none usable ⇒ report UNVERIFIED,
     never claim zero-space. This mirror exists for file-level offsite sync (D5a)
     — which is unchanged: pointing rclone at `.zfs` paths instead was considered
     and rejected (with `snapdir=hidden` rclone never sees them; with
     `snapdir=visible` it would walk EVERY snapshot at full size).
   A version IS a `zfs snapshot <parent>/<udid>@quince-<YYYY-MM-DDTHH-MM>-<ULID>`, taken **only after
   structural verification passes** on the child dataset root, which since qn.6h IS the backup tree —
   quince writes into it in place, so the snapshot captures the head = the version. Browsed read-only
   at `.zfs/snapshot/<snap>/`, **the snapshot root with no trailing component** (qn.6h D7);
   pre-qn.6h snapshots hold their content at `<snap>/latest/`, are not browsable, and are skipped
   with a log line. There is no seed and no `working/`; `rollback` discards a dirty head and is for
   **abandon only** — a failed job KEEPS the head so a retry resumes without re-transferring;
   retention = destroying our own snapshots. **Only quince-created snapshots count** — host auto-snapshot tooling is
   never relied on, created, or classified. Host-side ops go through a
   constrained hook (forced-command SSH key allowing only: `snapshot`/`destroy`/`list`
   scoped to `@quince-*` snapshots, plus `create` of child datasets under the one
   configured parent; **dataset destroy is never in the key** — quince prints the exact
   host command for a human). Container-visibility caveat: a child dataset created after
   an LXC starts appears as an empty stub inside a plain bind mount (mount propagation).
   **Recommended PVE setup**: a raw `lxc.mount.entry: … none rbind,rslave,…` instead of
   a plain `mpX` — with rslave propagation, datasets created on the host appear in the
   running container live, no restart (the same file already carries the USB entries;
   the nested-OCI hop needs `bind: {propagation: rslave}` in compose — included in the
   examples). Provisioning still probes visibility empirically and, when propagation
   isn't available, prints the exact `pct set -mpN` + restart instructions. If child
   datasets are impossible in a setup, a documented single-dataset fallback mode exists
   (dataset-wide snapshots namespaced per device, with the space-accounting entanglement
   stated honestly).
2. **`reflink` — namespace-versioned via CoW file clones** (FICLONE): same layout as
   `hardlink` below, but clones are **fully independent files**, so the in-place-
   mutation hazard (and its destructive test matrix) does not exist here at all.
   Supported by Btrfs (Synology), XFS, bcachefs — and by OpenZFS 2.2+ itself, which
   makes `reflink` the graceful mode for ZFS **without** a host hook: full versioning
   inside the dataset, zero host coupling. **The smart default wherever the probe
   passes.**
3. **`hardlink` — namespace-versioned** for filesystems with neither reflink nor
   snapshots (ext4 NAS): guarded by the destructive safety matrix below.
4. **`copy`** — like `hardlink` but seeds by full copy (transient 2× space, retention
   defaults to latest-only) for filesystems without hardlinks.

For all three namespace backends (`reflink`/`hardlink`/`copy`), **`latest/` is a real
directory, never a symlink** (external-review point, accepted — symlink behavior under
rclone depends on flags and would make the offsite contract fragile): the newest
verified backup *lives* at `latest/`; commit rotates by rename pair — `latest/` →
`versions/<prev-ts>/`, then `work/<job>/` → `latest/` — journaled, same filesystem,
crash-repairable. `work/<job>/` is seeded from `latest/` (reflink or hardlink clone, or
copy).

**Auto-selection** (`storage.backend: auto`): explicit zfs intent in config
(`storage.zfs.parent_dataset`/hook set) → `zfs`; otherwise probe the actual `/backups`
filesystem at runtime — FICLONE a test file and verify the clone SHARES its extents (and is
independent of its source) → `reflink`; else `link()` + inode identity → `hardlink`; else
`copy`. Deterministic, logged, explained in
plain language during onboarding. All cloning happens in-process via the FICLONE ioctl
(`golang.org/x/sys/unix`), never by shelling out to `cp --reflink` — busybox userlands
are irrelevant, and the ioctl passes through container bind mounts to the real
filesystem, which is the only layer that must support it (host OpenZFS needs block
cloning enabled — probed, with a plain-language onboarding message when absent).

**D5a. The offsite-sync contract (the requirement that drove this storage design).** The
offsite model is **file-level sync of the whole storage tree** (rclone → B2 class tools,
one cron job over e.g. `/rpool/userdata` covering quince and everything else), which walks
live mounted filesystems and uploads whatever is there. The rule:

> **The live namespace always presents a consistent last-verified backup per device;
> working areas are excluded by one static filter rule.**

- `zfs`: **rclone can no longer include a live tree on this backend, and that is the ruling rather
  than a gap** (Operator, 2026-08-04, quince#591; qn.6h D1, **built**). The old rule included
  `<udid>/latest/` and rested on a parenthesis — *a backup running concurrently in `working/` cannot
  perturb it* — and under in-place writes there is no `working/` to run concurrently *in*: the
  backup writes into the dataset root, so the head is torn mid-backup and a walk can capture a
  half-transferred tree. **The rule above — *the live namespace always presents a consistent
  last-verified backup per device* — fails for `zfs` alone**; the namespace backends keep it,
  because they keep `working/` and the exchange. Offsite on zfs must read a snapshot mount, and
  quince must be excluded from a general whole-host rclone job and handled separately. **The
  Operator accepted this explicitly**: the tolerance requirement *"is probably not worth the
  complexity it brought to users."* **Building the snapshot-sourced path is NOT the ruling and is
  tracked on quince#735** — which also records that nothing else tracked it, so an accepted cost had
  no owner.

  ```
  --filter "- /iphone-backup/*/working/**"
  --filter "- /iphone-backup/*/work/**"
  --filter "- /iphone-backup/*/versions/**"
  ```

  ⚠ Filters MUST be anchored (leading `/` = transfer root). An unanchored
  `--exclude "**/working/**"` would also silently drop any same-named directory *inside
  the backup content* under `latest/` — a corrupted offsite copy with no error. The
  deploy docs ship the exact filter block; `versions/` is excluded because rclone has
  no reflink/hardlink awareness and would upload every version at full size — local
  history stays local, remote history comes from B2 bucket versioning or
  `--backup-dir`. **The filter block and this whole flow are the NAMESPACE backends'** —
  the operator's flow is literally `zfs snapshot -r … && rclone sync /rpool/userdata b2:…`,
  the snapshot for local restore points and `latest/` guaranteeing the upload is never torn.
  **On zfs the second half of that no longer holds** (qn.6h): there is no `latest/`, the head is
  torn mid-backup, and the `rclone sync` arm must be pointed at a snapshot mount or dropped.

  > **RESOLVED by qn.5b (built 2026-07-24; the gap was Operator-found 2026-07-22).** The
  > swap is now **atomic**: `latest/` changes only by a single `renameat2(RENAME_EXCHANGE)`
  > (verified on the real ZFS pool, decisions (co)), so it is never unoccupied and a walk
  > or `zfs snapshot` crossing a commit always sees a complete `latest/` — the remote copy
  > can no longer be deleted, the snapshot can no longer capture a missing `latest/`. The
  > old two-rename swap (`mv latest → latest.old; mv latest.new → latest`) is gone from
  > BOTH paths — the in-container Go path (`storage/zfs.go`) and the host hook. Along with
  > it: the backup is written into a **per-job `working/<udid>`** seeded from `latest/` at
  > job start (host-side reflink via the hook's new `seed` verb, or the in-container
  > reflink→copy ladder — **never hardlink**, gate 12c), verified, then EXCHANGED into
  > `latest/`; the snapshot captures `latest/` = the version (`browse_root` → `…/latest`),
  > and between backups the dataset holds **only `latest/`**. The privilege split held:
  > FICLONE (the seed) is host-side, the exchange is quince's in-container `renameat2` (no
  > privilege). `kind` (full/incremental) is now derived from the seed decision, not
  > `IsFullBackup`. D5's two version models collapse toward one. Full scope: qn.5b spec +
  > decisions log (cg)/(co).

  **SUPERSEDED FOR `zfs` BY qn.6h (built).** Everything in the block above still describes the
  namespace backends exactly. On zfs there is no `working/`, no seed, no exchange and no `latest/`:
  the dataset root is the tree, `browse_root` is `…/.zfs/snapshot/<snap>` with no trailing
  component, and between backups the dataset holds the tree itself. The qn.5b record is kept because
  it is what the namespace lifecycle still is, and because a reader meeting `<snap>/latest` in an
  old snapshot needs to know where it came from.

  Push-style alternative: the post-commit hook (parked) runs rclone
  right after each verified commit.
- `reflink`/`hardlink`/`copy`: `latest/` is a real immutable-between-commits directory —
  same include rule, same anchored filter block (minus `working/`).
- Snapshot-stream replication (syncoid) of zfs datasets is still safe at any instant, and it is now
  the ONLY safe whole-tree route on this backend: a mid-backup pass ships the dirty head *plus*
  every `quince-*` restore point, and each of those snapshots is a complete verified version. What
  it no longer ships is a consistent live tree — since qn.6h there is no `latest/`, so a consumer
  must read a snapshot rather than the dataset root.

Restore/browse never read `working/`. A torn `working/` normally needs no repair — the
lab showed MobileBackup2 continues from torn state, and every result re-passes full
structural verification — and qn.5b makes this first-class: on FAILURE the dirty
`working/<udid>` is **KEPT** so a retry RESUMES into it (no re-transfer). The explicit
escape hatch is **Reset** — `quince device reset-working <udid>` / `POST /api/devices/
{udid}/reset-working` (the landed `RepairWorkingCopy` op, now a **discard**: drop the
dirty `working/` so the next backup re-seeds clean from `latest/`, losing only the
partial, never a version). The UI reports "working copy kept dirty for retry; last good
version = <ts>" meanwhile. The `quince versions path --latest <udid>` CLI prints the
`latest/` path (or a specific version's path) for scripts that want a single-device source.

**Why.** The Operator's ruling makes the zfs backend *simpler and more robust* than the
previous hardlink+snapshot hybrid: the write path is exactly what Apple ships, versioning
rides on CoW instead of 143k directory entries per job, and the hardlink-safety
hypothesis (below) stops applying to ZFS at all. The crosscheck's alternative —
per-version/clone child datasets — is rejected: dynamically created datasets don't
propagate into an LXC/container bind mount (mount namespaces are private), and it turns
one host-side operation (snapshot) into fragile clone/promote/rename chains over a
constrained channel.

**Verified assumption (early destructive gate — wherever hardlinks are actually used:
the `hardlink` backend, and hardlink *fallbacks* of the reflink/mirror machinery).** The
hardlink scheme assumes MobileBackup2 replaces files rather than mutating them in place;
every reflink-built tree is exempt (independent files). qn.5
proves this with a **destructive lab matrix**, not one file: byte- and metadata-identity
of the previous version across full→incremental, big-file change, SQLite `-wal`/`-shm`
companions, deletions, renames, interrupted backup + the incremental after it, iOS
upgrade, and encryption-settings change (truncate/chmod/xattr traps included). Any
in-place-mutating file class is copied instead of linked. The matrix re-runs manually
after every libimobiledevice upgrade (release checklist).

App state (job history DB, caches, pairing records, logs) lives **outside** the backup
dataset in every model.

## D6. API: REST for commands, one WebSocket for events

**Decision.** JSON REST (`/api/...`) for CRUD and commands; a single `/api/ws` WebSocket
pushing typed events (device attach/detach, job state + progress, log chunks, snapshot and
index updates). No gRPC.

**Why.** The responsiveness requirement is server→browser push; WS covers it with zero
proxy friction and trivial browser support. gRPC-web needs a proxy layer and buys nothing
for a single-user LAN app. Protocol shapes are pinned in `contracts.md` so UI and core
tracks can build in parallel.

## D7. Frontend: maximally mainstream — React + Vite + TS, Tailwind, vendored shadcn-style components, Zustand

**Decision** (revised on Operator ruling: mainstream, highly maintainable, lightweight,
strong LLM fluency — no niche dependencies):
- React 19 + Vite + TypeScript.
- **Tailwind CSS v4** with the design tokens as CSS variables in the theme. The *idiom*
  (semantic tokens, light/dark one variable deep) is carried over from `mercury` — a
  private design system evaluated as a source of conventions — without the dependency
  that came with it: `@mercury-fx/ui` is not consumed.
- **Components vendored, shadcn/ui-style**: accessible primitives from Radix UI, styled
  copies living in our repo — we own the code, no component-library version churn, and
  it is the pattern current LLMs author most fluently. lucide icons.
- State: **Zustand** stores fed by one WebSocket bridge; **TanStack Query** for REST
  fetching/caching; TanStack Virtual for unbounded lists. Effector is dropped — good
  library, but niche enough to be an onboarding barrier for open-source contributors
  and a weaker LLM path than the boring mainstream trio.
- Built assets embedded in the Go binary (`go:embed`) — one artifact serves everything.

**Why.** Every piece is the most-trodden path in its slot (huge ecosystems, hiring/
contributor familiarity, deep LLM training coverage), the bundle stays light, and
nothing here can be abandoned upstream in a way that strands us — the components are
ours, and Tailwind/Radix/Zustand/TanStack are as durable as frontend deps get.

## D8. Persistence: SQLite app DB; backup reading is lazy and session-scoped

**Decision.** One app database (devices, jobs, versions registry, settings, sessions) via
`modernc.org/sqlite` (no cgo), WAL mode — this records what *quince did*, never mirrors
backup content. Backup content is read **lazily inside an unlocked session**: the vault
decrypts `Manifest.db` (and domain DBs like `sms.db` on first use) into session scratch,
queries run against those live copies, and lock/timeout wipes it all. No persistent index
of backup content — the backup dataset is external storage the user may prune, replicate,
or hand-edit, and a stored index *will* diverge from it (Operator-raised concern; agreed).

**The one exception: derived caches, fingerprint-validated.** Some artifacts are too
expensive to rebuild per session on NAS hardware (photo thumbnails above all; possibly
FTS shards for huge message stores). These may be cached in `/cache` under strict rules:
keyed by immutable version identity + `Manifest.db` hash; validated before every use;
silently dropped and rebuilt (or absent) on mismatch or missing source; wipeable at any
time with zero correctness impact. A cache is a lie-proof derivation or it doesn't exist.
Message search default: FTS built in session scratch on first search.

**Why.** Single-user scale; zero-ops; cgo-free keeps cross-compilation clean. Lazy-first
also removes the only feature that wanted a stored backup password (post-backup batch
indexing) — v1 keeps no secrets at rest, full stop.

## D9. Quality bar: comprehensive tests, race-clean, fixtures from real transcripts

**Decision.**
- Go: unit tests + `go test -race` everywhere; integration tests run the real state
  machine against a **fake `idevicebackup2`** (scripted binary replaying real stdout
  transcripts captured in the lab, including the pathological ones: 30 s stalls, `-4`
  disconnects, silent minutes) and a **fake muxd socket** (record/replay of the plist
  protocol).
- Vault: the golden conformance suite against fixture backups, which any implementation
  must pass before it ships (D4). The fixture *generator* — a tiny synthetic encrypted
  backup with a known password — comes from the decryption library's encrypt/builder
  side. Until the decrypt layer has real-data validation, it is additionally checked
  against a real backup on the Operator's lab box (a non-CI gate).
- Frontend: vitest for logic, Playwright against `quince serve --demo` (fixture data, no
  device) — the demo mode doubles as the public screenshot/demo story.
- Live E2E (real iPhone, real LXC): a documented manual checklist per release, not CI.

**Why.** The core's whole value is reliability; the lab transcripts are a free corpus of
real failure modes. Demo mode keeps UI development unblocked by hardware.

## D10. Delivery: GitHub Actions → multi-arch images + releases

**Decision.** CI on every PR: lint (golangci-lint, eslint/tsc) + all test
suites. On tag: goreleaser builds binaries, buildx builds `linux/amd64 + linux/arm64`
images pushed to `ghcr.io` (Docker Hub mirror optional later), GitHub Release with
changelog. During pre-public development the same image target pushes to the Operator's
LAN registry via `make image push REGISTRY=...` (registry/creds via env, never committed).
Base image: Alpine; ships **libimobiledevice built FROM SOURCE** at a pinned tag with quince's
in-tree patches, plus its runtime link deps and `openssh-client` for the zfs `hook` transport.
**One binary and no language runtime** — the daemon is static Go and the UI is embedded in it, so
the image carries Apple-protocol userland and nothing else executable.

**NO MUXER DAEMON, since qn.6p D1 (Operator, 2026-08-16).** The operator runs a muxer and quince
dials it, so `devices.manage_muxer: true` is refused at startup and a daemon in the image would be
one nothing could start. The image carries no `netmuxd` and therefore no Rust stage. The
patched-timeout `libimobiledevice` build is what ships, and it is a **source build rather than the
`-progs` package precisely so the patches survive**.

**The in-container profile is DESCOPED, not abandoned**, and `deploy/Dockerfile` carries the note on
what restoring it costs: **`apk add usbmuxd` replaces quince's patched shared library with a symlink
into the stock package, silently losing patch 0001.**

**Why.** Standard, boring, reproducible; multi-arch is what makes the Synology story real.

**PROPOSED (gap): does quince ship BINARIES at all, or only the image?** Raised by
[quince#724](https://github.com/novkostya/quince/issues/724) while building the release pipeline.
Open; nothing is built on a guess about it, and the pipeline publishes the image only.

*"goreleaser builds binaries"* above was written before the product's shape settled, and the shape
has moved twice since. A bare `quince` binary has no `idevicebackup2`, no `idevicepair` and no
`ideviceinfo` — so it cannot pair, cannot back up, and is not a working install. What it *can* do is
`quince config validate`, which D12 already asks scripts and CI to run.

**qn.6p narrows the question without settling it.** Dropping the muxer daemon, netmuxd and the Rust
stage shrank the gap between *the image* and *the binary* considerably. What remains in the gap is
the patched libimobiledevice — the one part a user cannot supply from their own distribution,
because the patches are quince's and unreleased upstream.

Three answers are defensible:

- **ship them anyway** — useful for `quince config validate` today, and for the future where the
  vault is Go rather than a sidecar;
- **drop the clause** — the unit of delivery is the image, and a binary that cannot take a backup is
  a support burden shaped like a product;
- **defer** — decide it when something needs it.

Which one is right is a **D10 amendment**, not a rung-local call, which is why it is here rather
than in a spec.

## D11. Language/toolchain versions & conventions

- Go: latest stable (1.24.x at writing), `golangci-lint` pinned config, no cgo.
- Node: 22 LTS, `pnpm`, workspace mirroring mercury conventions where sensible.
- Monorepo layout: `core/` (Go), `ui/` (React), `deploy/` (Dockerfile, compose examples),
  `docs/`. Two languages. The vault has no tree of its own: in-process or as a second
  binary, it is Go under `core/` either way, so D4's open process-model question does not
  move this line.
- Licenses: MIT for quince (`LICENSE`); all Apple-protocol heavy lifting stays in
  subprocesses. **The audit is done and lives in `CREDITS.md`** — enumerated from the built
  image rather than quoted, with the command that re-derives each list. *Invoked, not
  linked* is now **verified** (`CGO_ENABLED=0`, no `import "C"` in `core/`, no C source in
  the repo), and it was the smaller half of the question: the **image** distributes
  ~20 **patched** LGPL-2.1 libimobiledevice binaries plus the patched shared library, which
  is a source-availability obligation quince meets through `LIBIMOBILEDEVICE_REF`,
  `deploy/patches/libimobiledevice/` and the Dockerfile. It also ships unmodified GPL-2.0
  Alpine base packages (busybox, apk-tools, …), met by Alpine's published aports. Neither
  reaches quince's own code.

## D12. Operations UX: Plex-grade setup, OpenWrt-grade config

**Decision.** Getting started is copy-paste-compose → `compose up` → open the web UI —
first-run onboarding (set admin password, guided checks: backups dir writable, backend
probe explained, usbmuxd reachable) handles the rest in-app. Only deployment topology
lives in env (`QUINCE_DATA/CACHE/BACKUPS`, `QUINCE_LISTEN`); **every other setting
lives in one tidy, hand-editable file** (`/data/config.yml`) that the UI *edits* rather
than replaces as the source of truth:

- canonical key order, and **ONLY THE KEYS THE USER SET — no generated annotation at all**;
- atomic validated writes; manual edits picked up by file watch — an invalid edit never
  crashes the app (keep running on last-good, show a UI banner naming the bad key);
- deterministic regeneration on UI saves (user's own comments aren't preserved — the
  OpenWrt/PVE precedent, stated honestly);
- **no secrets in the config file, ever** (admin password hash lives in the app DB) — the
  file is safely diffable, shareable, and versionable;
- `quince config validate` for pre-flight in scripts/CI.

**RULED (was two bullets promising the opposite): the file contains ONLY WHAT WAS SET, and carries
NO GENERATED ANNOTATION AT ALL.** Operator, 2026-08-08, relayed on
[quince#728](https://github.com/novkostya/quince/issues/728). Raised by a Settings page that wrote
back every optional key at its default and called it the user's config.

**Generated doc-comments are DROPPED, not deferred.** There is no smaller annotation to stage
toward, and quince#727's staging item for it is moot rather than pending — do not pick it up as
outstanding work.

**What survives is the reason D12 exists.** *Tidy, hand-editable, diffable, no secrets, the UI edits
the file rather than replacing it* — all unchanged. The belief this drops is that a config file
is more legible for being complete. **A file that lists every key at its default is not
self-documenting, it is noise a reader has to filter**, and it makes a hand-edit harder to diff
rather than easier: the signal is what somebody chose, and defaults belong in `--help`, the UI and
this document.

**IMPLEMENTATION NOTE, because the obvious approach cannot deliver this and the second-obvious one
breaks a live behaviour.** `omitempty` drops **zero** values, not **default** ones — and
`ResolveStorages` fills non-zero defaults at parse (`core/internal/config/schema.go`: `backend:
auto`, `zfs.mode: hook`, `zfs.seed: auto`), with `Marshal` serialising the **resolved** document. So
`omitempty` tidies empty strings and leaves exactly the keys this ruling is about. *Only what was
set* is a fact about the **input document** that resolution has already destroyed, so it needs
declared-vs-resolved tracking, and that tracking has to survive the write path or every UI save
re-inflates the file. **And `omitempty` must never reach the `json:` tag on the same line**: a sparse
wire representation makes `GET /api/config` drop keys, the UI spreads a partial document, `PUT` sends
it, and the decoder zeroes every absent key — `devices.manage_muxer` becomes `false` and quince stops
supervising its muxers (quince#493's latent defect, made live by a tidying change).

**Why.** The litmus test is what makes an appliance stay installed: PVE and OpenWrt earn
loyalty because their config is transparent files you can read, edit, and diff, while a
GUI that buries state in an opaque store gets uninstalled within the hour. Plex is the
setup bar: nothing to learn before the UI is up. Both properties at once — GUI-first
onboarding, file-first truth.

**Staged delivery** (external-review point, accepted): the full subsystem is the
destination, not the qn.1 payload. qn.1 ships the load-bearing core — typed config,
YAML as source of truth, atomic canonical writes, `config validate`, a small Settings
page for safe keys, restart-required for the rest. **Generated doc-comments are CANCELLED by the
ruling above and land nowhere.** The rest
of the transparent-editor UX still lands with qn.6; **file-watch is `qn.6q`, inside v0.1.** The
contract (file-first, no secrets, no UI-only state) binds from day one.

**`restart-required for the rest` DESCRIBES qn.1's PAYLOAD AND IS NO LONGER THE STATE OF THE
PRODUCT.** `qn.6g` (quince#577) built propagation, so D12's *"needs no restart unless the spec says
why"* is now discharged **per key** rather than in the aggregate: contracts §6 carries the table,
with the stated why for each key that stays restart-required, and a third bin for the five keys
nothing reads. Corrected beside the sentence rather than inside it — that sentence is a true record
of what qn.1 shipped, and this paragraph is a staging decision rather than a status line.

**FILE-WATCH LIVE RELOAD IS `qn.6q`, INSIDE v0.1, AND BUILT.** Split out of `qn.6g` by Operator
ruling 2026-08-04, option (a), on
[quince#577](https://github.com/novkostya/quince/issues/577#issuecomment-5182609911); scheduled into
v0.1 by Operator ruling 2026-08-17 on
[quince#1094](https://github.com/novkostya/quince/issues/1094).

**`qn.6g` built PROPAGATION and `qn.6q` built DETECTION, and they are different mechanisms.**
Propagation is `config.Service` telling the running subsystems when **it** writes the file, so a UI
change takes effect. Detecting a change somebody **else** made is the second producer feeding those
same appliers. Both exist, so D12's *"edited by the UI and by hand equally"* is a description rather
than a destination.

**quince POLLS THE FILE. It does not use `inotify`, and the reason is measured rather than
preferred** — this paragraph carries the numbers deliberately, because *"we chose polling"* invites
re-opening on the dependency grounds the measurement already closes:

- **Reading and comparing the whole file costs 12.19 µs** on a realistic 218-byte `config.yml`; a
  bare `stat` costs 2.33 µs. At one tick per ten seconds that is roughly 0.0001% of a core, so the
  cheap option and the correct one are the same option, and the saving from a stat-first check buys
  nothing while costing a whole class of reasoning about mtime granularity.
- **A watch on the file PATH is dead after one write**, measured: `ATTRIB`, `DELETE_SELF`, `IGNORED`,
  and then nothing for any later change including in-place ones. So a correct `inotify`
  implementation is a directory watch plus name filtering plus requeue handling **in front of the
  content comparison it would still need**, because quince's own `AtomicWrite` and an editor's
  write-then-rename produce identical event sequences apart from the temp file's name. Polling is the
  subset, not the alternative.
- **No option costs a dependency**, which is what would have made this D-level: `golang.org/x/sys` is
  already a direct requirement of `core/go.mod` and exports the `inotify` calls, and the
  `_linux.go`/`_other.go` split is already in this tree three times. **So there is no new `D<N>`** —
  Operator ruling 2026-08-17, quince#1130. *"A `D<N>` is for a choice that constrains the stack; this
  one constrains nothing."*

**A network-filesystem argument was made for polling and is NOT the load-bearing one.** `inotify` sees
nothing when the write happens on another host, so a `/data` on NFS or SMB would be silently inert —
but the Operator overruled the premise on 2026-08-17 (*"who on Earth would place a config on nfs/smb
though?"*), and it is fair: `config.yml` lives beside the app DB in the data volume. **Cost carries
this alone.** Recorded so nobody defends it on the weak leg.

**RULED (was `PROPOSED (gap)`): a value quince only RENDERS is not a setting, and D12 does not reach it.**
Operator ruling, 2026-08-02, on quince#470. Raised by `docs/specs/public-demo/public-demo.md`.

**D12 gains a third category, named explicitly: *reported deployment facts*** — values quince renders
and never acts on. `/api/health`'s `version` is what the category already contains; it has been there
since qn.0 and never needed a D12 exception, because it was never a setting.

**The test that separates the categories is: does any code branch on this value?** If yes it is a
setting and both of D12's bins apply. If no — the process only reports it — D12 does not reach it.
That test is why this is a reclassification rather than an exception: *"anything awkward becomes a
`QUINCE_` var"* has no boundary, and *"does any code branch on it"* has one that is checkable by grep.

**Applied to the case that raised it:** the public-demo reset is an externally scheduled restart —
**quince runs no timer** — so nothing in the codebase can branch on the interval. It is somebody
else's schedule, rendered. Carried in env, read at startup beside the existing bootstrap config, and
surfaced read-only. There is no `PUT`, so a visitor cannot edit the promise, and no read-only-in-UI
affordance is needed: the UI already renders reported values non-editably and always has.

**The counter-argument was weighed and overruled, and it is kept because the reasoning that lost is
what makes a ruling checkable later** (`decisions/0006`). The precedent offered was inexact: `version`
is a *build-time* constant baked in by ldflags, and `muxers[].managed` is *derived* from a setting
that does exist (`devices.manage_muxer`), whereas the reset interval is neither — it varies per
deployment without a rebuild and has nothing behind it to be derived from. `version` is also
comfortable precisely because nothing could sensibly edit it, where an interval is something an
operator genuinely chooses. **The ruling was taken knowing that.** What survives it is the branch test
rather than the analogy: membership is decided by *reported, never acted on*, not by resembling
`version` in how the value arrives. The interval is the first member of its shape and the test admits
it cleanly.

**Two options are now closed.** A `config.yml` key — editable by every visitor in a mode where
everyone is authenticated, so the UI would state a promise the visitor had just falsified. And
stating no interval at all — which keeps the reset *announced*, the actual `no silent caps or
fallbacks` requirement, but drops the reassurance and was not the answer taken.

**Not ruled here:** the env var's exact name and read site (rung-local); whether any *existing* value
should be reclassified under the new category (`version` and `muxers[].managed` are cited as
precedent, not migrated — nothing changes about how they work); and the interval's actual value, which
the Operator left open deliberately — *decide it when the instance exists*.

**Story 6 of that spec is unblocked. The other six never were.**

## D13. Wi-Fi is a first-class transport — and the product model is ASSISTED backup

**Decision.** Wi-Fi backup is not an experimental extra; it is **the product's primary
use case** (Operator ruling, overriding external review advice to defer it). But there
is no such thing as an unattended backup on modern iOS: **starting an encrypted backup
requires the passcode to be entered on the device** (Operator-established; the lab
transcripts show `*** Waiting for passcode ***` on every run). So the automation model
is *assisted*, not *scheduled*:

```
phone goes on the charger → Shortcut sends an opportunity signal to quince
quince decides server-side: device visible on Wi-Fi? no job running? last good
backup stale? not nagged recently?  → if warranted, one push notification
user unlocks the phone, confirms, enters the passcode → backup runs over Wi-Fi
```

Consequences:
- **No auto-retry.** The old 1 → 5 → 20 min ladder is deleted — a retry would hang at
  the passcode prompt. A failed/torn job becomes an honest `user action required` state
  with a push explaining why; the retry is one tap. A `retry_of` link ties the new job
  to the failed one in history.
- The supervisor detects the passcode-wait phase from `idevicebackup2` output and
  surfaces it (`waiting_for_passcode`) — the liveness clock pauses there (the user may
  take minutes).
- USB remains required for initial pairing and preferred automatically when plugged in
  (faster); default transport policy is `auto`. No experimental flag.
- Flakiness is still absorbed by engineering, not flagged away: the timeout-patched
  libimobiledevice build lands before v0.1 (D2); the lab's torn-session transcripts are
  permanent replay fixtures; liveness is activity-based with staged states (design §4).
- v0.1 gate: a week of *real* Wi-Fi backups driven from the UI (phone in hand, zero
  cable, zero tmux), with failures producing honest actionable states. The full
  assisted-flow acceptance list (opportunity → push → one-tap start; no spam; correct
  no-op on fresh backups) is the qn.12 gate, once push exists.

**Why.** The value proposition is not "set and forget" (Apple forbids it) — it is
**"quince notices the right moment, reminds you, and shrinks the ritual to an unlock
and one confirmation."** That's still a decisive win over cable + desktop app, and it's
exactly why Wi-Fi must be in the core path: without it the assisted flow doesn't exist.

## Settled non-goals (v1)

- No multi-user/multi-tenant; single admin password, cookie session, LAN/reverse-proxy
  deployment assumed.
- No backup *restore* orchestration in v1 (export a snapshot in Finder/iMazing-compatible
  form instead; restore is a later epic).
- No iCloud anything.
- No attempt to resume torn MobileBackup2 sessions.
