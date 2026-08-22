# qn.6p — quince stops owning a muxer, and says honestly what it found instead

**Goal.** quince ships **without a muxer** and reaches devices through whatever the operator already
runs — a host `usbmuxd`, a sidecar container, another tool's daemon — dialing a unix socket or a TCP
address indifferently, and reporting what a real dial returned rather than what the config asked
for. The all-in-one profile is **descoped to a later version, not abandoned.**

Operator ruling, 2026-08-16, taken in session across five exchanges and recorded on the forge at
[quince#897 (comment)](https://github.com/novkostya/quince/issues/897#issuecomment-5306581561) —
**relayed by the implementer seat, not posted by the Operator**, which is the open question of
quince-devlog#254. Each decision is attributed at the point it is recorded below.

**That clause was weaker than the rest, and is not any more.** The profile decision arrived as
*"leaning towards"* rather than as a flat ruling, and the exchanges after it proceeded on that
basis — recorded here as a disclosure rather than tidied away. The architect put the question to
the Operator directly (*"confirm or reverse: does quince ship with no muxer, hardened-only, for
v0.1?"*) and the answer was **"Yes, confirm"** — relayed by the **architect** seat at
[quince#897 (comment)](https://github.com/novkostya/quince/issues/897#issuecomment-5306693531),
which is the citation to use. It is a flat ruling now.

**The confirmation is narrow, and reading it wider is the mistake to avoid.** It settles the
profile and the canon edits D8 names. It does **not** touch story 4, which stays blocked on a
measurement rather than a decision; it does not rule quince#326; and it does not discharge G8,
because a ruling is not a gate.

---

## The letter

`qn.6p` is free — measured across `docs/`, `core/`, `ui/src` and `bin/`: the string appears nowhere.
`qn.6o` is this rung's predecessor and is complete. `qn.6l` stays the documented hole, spoken for by
[quince#726](https://github.com/novkostya/quince/issues/726).

---

## Why this is a rung and not a fix to [quince#897](https://github.com/novkostya/quince/issues/897)

quince#897 is filed as four defects in the hardened profile. Working them through with the Operator
turned it into a change of which profile exists: two of its four items **dissolve** rather than get
fixed, and the two that remain change what the product is. That is a rung.

It also inherits M5's `compose.hardened.yml` deliverable and quince#721's two carried items, so the
rung is where they land rather than a sixth issue.

---

## What decided it

**The simple profile makes you choose between Wi-Fi and network isolation, and Wi-Fi is the primary
use case.** netmuxd discovers only by mDNS, mDNS is link-local, and netmuxd lives inside quince's
container — so `deploy/compose.nas.yml` instructs the operator to put **quince** on the host network,
and says so about itself:

> Honest tradeoff: host networking shares the host's network namespace with the container […]
> strictly weaker isolation than the bridged default, and **at odds with the hardened-profile story**.

Hardened does not rebalance that tradeoff; it removes it. The *muxer* takes host networking and
quince stays bridged, unprivileged, with no access to `/dev/bus/usb` at all. Operator, 2026-08-16:

> *"It's cleaner in a way that your quince container with web app can run perfectly fine in bridged
> network, unprivileged with no usb capacity at all."*

**And a host that already runs a muxer has no working quince configuration today**, which is
quince#897's finding and is a first-contact failure rather than an advanced one: `usbmuxd` ships as a
libimobiledevice dependency and many distros enable it, and **only one muxer may own the USB bus.**

---

## What is DESCOPED, and the difference from abandoned

**`manage_muxer` returns in a later version.** Operator, 2026-08-16: *"I want it to be back in future
versions, I'm not giving up on all-in-one."* Three consequences bind this rung:

1. **`muxsup`'s supervision path is PARKED, not deleted** — the supervision loop, capped-backoff
   restart, crash-loop detection, the refuse-loudly probe and the verified netmuxd argv are all built
   and hardware-proven. Their unit tests keep running under `make gates`, so the code cannot rot
   quietly, and the marker on them must read *parked* rather than *broken*. (Guard-vs-archaeology
   ruling, 2026-08-03: a reader who never knew the old state must not be misled.)
2. **The endpoint keys are NOT renamed** — see D3. A rename buys tidiness in the profile that ships
   and must be re-argued when the other returns.
3. **`manage_muxer` stays in the schema** — see D2. Deleting it would be a *silent* downgrade.

---

## Boundary

**In scope — the trees this rung may touch.** A later PR that wanders outside this list is
mis-sliced, which is what a Boundary is for when the slice runs to nine.

| tree | what changes | PR |
| --- | --- | --- |
| `core/internal/muxd/` | the address grammar; `Client.dial` | 2 |
| `core/internal/deviceops/` | `socketAddr`; `LockdownStore` write-probe | 2, 7 |
| `core/internal/backup/supervisor.go` | `socketAddr` — the third copy of the `UNIX:` logic | 2 |
| `core/internal/muxsup/` | `AddUnmanaged` probes; the supervision path parked | 3, 5 |
| `core/cmd/quince/` | `muxers.go`, `live.go` — one client per endpoint, not per key | 3, 4, 5 |
| `core/internal/config/` | `manage_muxer` validation; endpoint doc-comments; the dead default | 2, 5 |
| `core/internal/httpapi/` | muxer health entries; pair `409`; the pair-capability field | 3, 7 |
| `ui/src/` | the pairing control's unavailable state and its reason | 7 |
| `deploy/` | `Dockerfile` runtime stage; `compose.hardened.yml`; `compose.nas.yml` | 8, 9 |
| `docs/` | design §2/§9, contracts, stack D2 — and roadmap M5 in the devlog | 9 |

**Out of scope**, each with where it went instead:

- **the `quince-muxer` image** — its own unit of work; recommended for Wi-Fi, not required for a
  working deployment (D1), and the moment to decide the libimobiledevice coupling below;
- **reintroducing `manage_muxer`** — a later version, and D2 is what keeps that cheap;
- **quince#326's netmuxd-USB audition** — raised and not ruled;
- **whether `compose.hardened.yml` carries the storage side** — quince#721 did not say and neither
  does this rung;
- **transport for a dual-transport muxer** — architectural and blocked; see the gap section;
- **whether sharing the lockdown dir read-write is safe** — explicitly not ruled (D7).

**One thing that leaves scope by being removed, and returns when `quince-muxer` is built.** Today
the runtime `COPY`s quince's patched `libimobiledevice` over the `apk`-installed one *after* the
`apk add`, so the packaged `usbmuxd` links it — `apk audit --system` reports exactly two modified
files, both that library. Its `src/preflight.c` pairs and validates through
`property_list_service_receive_plist()`, which is the function patch `0001` changes from 30 s to
15 min. **So usbmuxd's Trust handshake currently runs on a timeout chosen for `idevicebackup2`, by
layer ordering rather than by decision.** D1 removes the daemon and with it the accident. Whoever
builds `quince-muxer` from these stages inherits the question as a choice, which is the improvement.

---

## Design — the decisions this rung settles

### D1 — quince ships no muxer daemon, and there is one profile

The runtime image drops the `usbmuxd` package and the `netmuxd-build` stage. Measured on the built
image: `netmuxd` is 7.1 MB of 84.5 MB, `usbmuxd` 84 KB; the larger saving is the Rust compile leaving
the build entirely.

**Nothing outside `muxsup` execs either binary** — verified by grep across `core/`: every other
reference is to the *socket path* or to the client library `libusbmuxd`, which stays. Operator,
2026-08-16, on keeping the daemon binary: *"I'd say no, why would that even be needed?"*

**The runtime deps that arrived transitively must become explicit.** The Dockerfile's own comment
says *"libssl3/libcrypto3 come transitively via usbmuxd"*. **That comment is wrong, and so was this
paragraph's own reading of it** — corrected here because quince#268 removed `python3`, which is the
backstop the original sentence named. Measured in the built image with `python3` already gone
(`apk info --rdepends libssl3`):

```
libssl3-3.5.7-r0 is required by:
libapk-3.0.6-r0  libcurl-8.21.0-r0  ssl_client-1.37.0-r31  libimobiledevice-1.4.0-r0
```

`usbmuxd` is **not** in that list, and neither was `python3`. Two of the four — `libapk` and
`ssl_client` — are the Alpine base itself, and `libimobiledevice` is quince's own hard dependency.
**So dropping `usbmuxd` cannot take `libssl3` with it**, and the accident this paragraph was
guarding against does not exist.

Whether D1 still wants them named explicitly is **this rung's call, not quince#268's** — the
argument from *"a runtime dep should be declared rather than inherited"* survives the measurement
even though the argument from *"it would otherwise vanish"* does not.

**Not in this rung:** a `quince-muxer` image. It is recommended for the Wi-Fi case and is **not
required** for a working deployment — a host `usbmuxd` is a legitimate answer, as is a sidecar, as is
another tool's daemon. Operator, 2026-08-16: *"something could be either quince-muxer […] OR
something external user can provide himself […] we don't care."* It gets its own unit of work.

### D2 — `manage_muxer` stays in the schema and refuses `true`

Config load uses plain `yaml.Unmarshal`, which **silently drops unknown keys**. This project already
has that incident on record — `core/internal/config/storages_validate_test.go:162`, a mistyped
`pathh:` that *"was dropped by yaml.Unmarshal and reported by nothing."*

So **removing** the key would upgrade an existing `manage_muxer: true` install into a muxerless
quince with no muxer configured, silently, and the operator would see devices vanish with nothing
saying why. That is the no-silent-fallbacks rule broken by a deletion.

The key therefore stays, `false` is the only accepted value, and `true` **refuses at startup** naming
what to do instead and the issue that returns it. Reintroduction is deleting one validation branch.

### D3 — one address grammar, three consumers

`devices.usbmuxd_socket` and `devices.netmuxd_addr` keep their names and both accept the same three
forms:

| written | meaning |
| --- | --- |
| `/run/mux/usbmuxd` | unix socket path |
| `UNIX:/run/mux/usbmuxd` | the same, in libusbmuxd's own spelling |
| `127.0.0.1:27015` | TCP |

**This is the whole of quince#897 item 1, and its diagnosis in that issue is one notch off.** The
registry dialer is not TCP-only: `muxd.Client.dial` already picks `unix` for a leading `/`. The real
defect is that the consumers of one string disagree about its format, so **no single value satisfies
both**:

| value | `muxd.Client.dial` | `USBMUXD_SOCKET_ADDRESS` for the CLIs |
| --- | --- | --- |
| `UNIX:/run/mux/usbmuxd` | ✗ dials tcp — the error quince#897 recorded | ✓ |
| `/run/mux/usbmuxd` | ✓ | ✗ libusbmuxd reads a bare path as host:port |

One normaliser parses once and hands each consumer its own form. **There are three consumers, not
two** — `muxd.Client.dial`, `deviceops.Tools.socketAddr`, and `backup.socketAddr`, the last of which
carries its own copy of the `UNIX:` logic (`core/internal/backup/supervisor.go:23-30`).

The names now under-describe what they accept; that is a doc-comment fix and **not** a rename (see
descoping, point 2).

### D4 — one muxer serving both transports is one client

Point both keys at the same value. `buildLiveStack` currently opens one `muxd.Client` per configured
address (`core/cmd/quince/live.go:57`), so today that yields **two clients on one socket** — two
registry sources, duplicated replay. It dedupes.

This is quince#897 item 3 dissolved rather than fixed: once a key means *an endpoint I dial*, absence
is natural and the documented `""`-vs-omitted trap has nowhere to live. The default that fills a dead
`127.0.0.1:27015` goes with it.

### D5 — health reports what a dial returned

`muxsup.Group.AddUnmanaged` builds its status from a literal string with no probe at all:

```go
Detail: address + " is served by an external muxer — quince does not own it",
```

Measured consequence, quince#897: `/api/health` reported `"state":"external"` for an address the
daemon was **simultaneously** logging `connection refused` against.

**In this rung that is the only muxer signal there is** — there is no supervised child whose crash
would surface. `external` is claimed only after a successful dial; otherwise the entry reports
`unreachable` and carries what the dial actually said.

The probe exists ten lines away and is unused on this path (`muxsup.Supervisor.probeServed`), but its
**semantics invert**: served means *healthy* here and *refuse to start* there. It must also be
**continuing** rather than one-shot, because an external muxer can die at any time. The `muxd.Client`
already dials that address in a loop and knows whether it is connected; preferring its state over a
second prober is a rung-local implementation choice.

**A measured instance of why asserted health is not good enough**, recorded 2026-08-16 on a lab
stand: a second netmuxd started with no `--socket-path` takes over `/var/run/usbmuxd` — it prints
`Listening on /var/run/usbmuxd` — leaving the real usbmuxd alive with its socket inode gone. Health
went on reporting `running` throughout, because the supervisor watches the **process** and the
process was fine. This rung removes the supervisor rather than fixing that, but the lesson is D5's.

### D6 — rescan re-reads the muxer

Today `POST /api/devices/rescan` restarts the managed USB muxer, because an unprivileged container
receives no USB hotplug. **That problem does not disappear — it moves to the muxer container**, which
quince cannot restart.

The honest minimum would be `409` always. Instead rescan drops and re-establishes the muxd
connection, which forces `sink.Reset()` and a fresh replay from the muxer — genuinely useful, and
honestly describable as *re-read the muxer* rather than *rescan the bus*.

**Operator, 2026-08-16, provisionally:** *"let's do re-read-the-muxer although I'm not sure. Build it,
then I'll test it and maybe amend later if needed."* Recorded as provisional so a later amendment is
a decision rather than a reversal.

The UI copy must not promise a bus rescan. What it can promise is that quince will ask the muxer
again — and the actionable advice when that finds nothing is to restart the muxer container, which is
the operator's to do.

### D7 — a read-only lockdown directory disables pairing, and says so

Operator, 2026-08-16: quince should detect a read-only lockdown dir *"and mention it in UI instead of
offering Pair button, like springback does."*

**Detected by write-probe** — create and remove a zero-byte file in the lockdown dir — because that
answers the question the UI needs (*can quince write a pairing record here*) rather than one mechanism
that produces it. A `:ro` bind mount is visible through `statfs`'s `ST_RDONLY`, but a permissions
problem and a full filesystem produce the same user-facing outcome and set no such flag.

It lands on an existing surface: `POST /api/devices/{udid}/pair` already documents a `409`
(`contracts.md:448`), so read-only becomes a reason on that path, plus a wire field so the UI can
render the control unavailable **with the reason** instead of offering a button that will fail.

`LockdownStore.Restore` must also stop warning *"pairing may not restore"* when the truth is that
another writer owns these records and they are already present. Its `overwrite: false` behaviour is
already correct for the shared case — *"a live/bind-mounted record wins"* — and is not changed.

**What this rung does NOT do:** rule on whether sharing the directory read-write is safe. Per-device
records are whole-file writes, one file per device; the shared mutable thing is
`SystemConfiguration.plist`, the host identity that every record's `HostID` refers back to. Operator,
2026-08-16: *"I think multiple writes could work, although probably not recommended. Idk but I guess
we don't want to mention it in readme at all and leave decision to users."* No rule, no
recommendation, no README line.

### D8 — canon says one profile

Design §9's *"Two deployment profiles […] a `compose.hardened.yml` example ships with qn.6"* becomes
one profile with the other named as descoped. Design §2's muxer-supervisor row, roadmap M5, and stack
D2's *"Consequence"* paragraph (which states the container ships netmuxd + usbmuxd) all move with it.

---

## An unruled gap this rung does NOT build past

**What a single dual-transport muxer reports as `transport` is unruled, and blocked on a
measurement.** quince#897 item 4 records that Wi-Fi devices appeared labelled `usb`. That does not
reproduce from the code: transport comes only from the muxer's `ConnectionType`
(`core/internal/muxd/protocol.go:84`), `mapTransport` returns `wifi` for anything that is not
literally `"USB"` — including an absent field — and the managed path is standing evidence that
netmuxd reports `Network` correctly, since supervised netmuxd runs `--disable-usb` and its devices do
show as Wi-Fi.

So the failure the code predicts is the opposite of the one observed, and quince has **no way to say
"this source is a Wi-Fi muxer"** — `Registry.Sink(addr)` takes the source and then ignores it for
transport purposes.

This touches the device model and `backup.preferred_transport`, so it is **architectural**, not
rung-local. Story 4 below is written and **not built**; the raw `/api/devices` output from
quince#897's run is what unblocks it. If it is to be pursued before that, the `PROPOSED (gap)` block
belongs in design §2 and this rung stops at that thread, per the gap protocol.

---

## Stories

1. **One address grammar.** A muxer endpoint written as a bare path, `UNIX:<path>` or `host:port`
   works identically for presence, for device ops, and for a backup subprocess. (D3)
2. **`manage_muxer: true` refuses.** quince does not start, and says what to do instead. (D2)
3. **Health reports a real dial.** An endpoint nothing listens on reports `unreachable` with the
   dial's own words; one that answers reports `external`. State follows the muxer going away and
   coming back. (D5)
4. **BLOCKED — one muxer, two transports, labelled honestly.** Written, not built; see the gap above.
5. **One muxer serving both transports is one client.** Both keys at one value yields a single
   connection and no duplicated replay. (D4)
6. **Rescan re-reads the muxer.** Devices are re-enumerated from the muxer's replay with nothing
   restarted; the UI copy claims only that. (D6)
7. **A read-only lockdown dir disables pairing with a reason**, on the API and in the UI, and startup
   does not warn about a restore that was never needed. (D7)
8. **The image ships no muxer daemon** and names the runtime deps that used to arrive with it. (D1)
9. **`deploy/compose.hardened.yml` ships, and has been RUN** — quince bridged and unprivileged with
   no `/dev/bus/usb` and no `device_cgroup_rules`, reaching a muxer over a shared-volume socket.
10. **Canon says one profile** — design §2/§9, stack D2, roadmap M5, `compose.nas.yml`. (D8)

---

## Gates

`make gates` throughout, plus:

| # | story | gate |
| --- | --- | --- |
| G1 | 1 | table test over the three address forms × three consumers; the two rows in D3's table that fail today are the regression fixtures |
| G2 | 2 | ~~`manage_muxer: true` in a config → non-zero exit, message names the replacement; `false` and absent both start~~ — **half SUPERSEDED, see below** |
| G3 | 3 | fake muxer listener: start → `external`; kill → `unreachable` with the dial error; restart → `external` again |
| G4 | 5 | ~~both keys at one path → exactly one connection observed at a fake muxer~~ — **SUPERSEDED, see below** |
| G5 | 6 | rescan against a fake muxer produces a fresh `Reset` + replay and starts no process |
| G6 | 7 | ~~lockdown dir chmod'd unwritable → pair returns 409 with the reason, wire field set, control disabled in `make dev`~~ — **SUPERSEDED, see below** |
| G7 | 8 | `make image`, then assert neither `usbmuxd` nor `netmuxd` is present and the CLIs still resolve their libraries |
| G8 | 9 | **owed to hardware** — Operator, on the lab rig: a real device over the cable and over Wi-Fi, through an external muxer, quince bridged and unprivileged |

**G8 cannot be run by an agent seat** and is named as owed with its owner, per quince#721's own
warning that shipping the example without a real run would reproduce quince#651.

### THREE GATES WERE SUPERSEDED BY LATER RUNGS, AND ARE STRUCK RATHER THAN DELETED (quince#1480)

**A gate is a claim that something was checked**, and this rung is recorded CODE COMPLETE with its
gates as the evidence. A gate that cannot pass leaves a reader either concluding the rung is failing
or re-deriving the whole thing to discover it was superseded. Struck rather than removed so a
citation to `G<n>` still resolves to the thing it cited.

**G6 — every clause is false, by RULING rather than by drift** (`qn.6r`). quince mounts no lockdown
directory at all, so there is nothing to chmod (D1); the pair route's `409` now means *quince cannot
reach the muxer*, a different condition with a different remedy (D3); `pairing: {writable, reason?}`
was removed from `GET /api/devices` (D7); and the `PairDialog` branch that rendered the control is
deleted along with its test. **Replaced by `qn.6r`'s own gates**, which check the post-check that
cannot lie rather than a pre-check that could.

**G4 — the configuration it tests can no longer be written.** *Both keys* means
`devices.usbmuxd_socket` and `devices.netmuxd_addr`, and `devices:` is retired (`qn.6q`/quince#1219).
The nearest expressible equivalent — two `muxers:` entries at one address — is **refused by
`Validate` as a duplicate**, not deduplicated at connect time, so the gate's expected outcome
inverted along with its input. The property it protected did not vanish: it moved from *dedupe
silently at connect* to *refuse at parse*, which `LegacyDevices.addresses()` deduplicating on the
retirement path exists to preserve.

**G2 — the first clause stands and the SECOND IS FALSE.** `manage_muxer: true` still refuses and
names the replacement, and the message is better than the gate asked for: it quotes the address you
actually wrote. But **`false` no longer starts.** `checkRetiredDevices` returns early only on
`d == nil`, so *any* `devices:` section is refused whatever `manage_muxer` says. That is deliberate —
a section that parsed and was ignored would take an operator's muxer address with it in silence —
and it makes the gate's *"`false` and absent both start"* wrong about half its cases.

**Measured rather than read, and the layer is part of the answer.** Neither refusal is a `Parse`
error, so a check written against `Parse` would have found nothing and concluded both gates still
held:

```
config                       Parse   Validate   CheckMuxers
devices.manage_muxer: false    ok       ok        REFUSED    ← G2 says this starts
devices.manage_muxer: true     ok       ok        REFUSED
devices absent                 ok       ok           ok
two muxers, one address        ok    REFUSED         ok       ← G4 says one connection is observed
```

`devices:` is refused on the **serve path** (`CheckMuxers`, from `cmd/quince/live.go`) and the
duplicate address at **validation**. The probe was a scratch test, run and deleted.

**Audited in full rather than spot-fixed.** quince#1480 was filed on G6 alone, found by accident,
and said so: *"finding one by accident is weak evidence there is only one."* All eight rows were then
checked against `qn.6q` and `qn.6r`. **G1, G3, G5 and G7 stand as written**, and G8 remains owed to
hardware with its owner named.
---

## Fixtures

- A fake muxer that listens on either a unix socket or TCP, speaks enough of the protocol for the
  Listen handshake, and can be killed and restarted — this is what G3/G4/G5 need and no such fixture
  exists today.
- The two failing address rows from D3, as regression cases.
- No new hardware transcripts: this rung changes no `idevicebackup2` interaction.

---

## Rule check

| rule | how this complies |
| --- | --- |
| **No silent caps or fallbacks** | D2 is this rule applied to a config key: `manage_muxer` is refused loudly rather than dropped silently, because `yaml.Unmarshal` would drop it. D5 replaces an asserted health state with a probed one. |
| **State honesty** | D5 is the rung's centre — health may not claim `external` without a dial. D6 renames the operation to what it does. G8 is declared owed to hardware rather than claimed. Story 4 is declared blocked rather than guessed. |
| **Docs are part of the diff** | D8 lands design §2/§9, stack D2, roadmap M5 and `compose.nas.yml` in the PRs that change the behaviour, not after. Coverage + a known-untested list per PR. |
| **Don't improvise architecture** | The transport-labelling question is classified architectural and **not built** — the one place this rung stops. Everything else is inside canon or is an Operator ruling recorded above. |
| **Config tidiness (D12)** | No new key. `manage_muxer` keeps its name and its place in the canonical key order (contracts §6); the endpoint keys keep theirs. Defaults still mean the file carries only what was set — and D4 removes a default that was actively harmful. |
| **Interface facts looked up live** | The netmuxd USB claim was checked against upstream releases rather than remembered (USB support landed in `v0.4.0`; quince pins `v0.4.3`). The muxer-image survey was checked live. No version is pinned by memory. |
| **Never mutate a committed version** | Not touched — no storage path changes. Named because the rung edits `deploy/` and `compose.nas.yml`, which sit beside the storage mounts. |
| **Secrets discipline** | Not touched. Named as a near-miss: D7 reads and probes the lockdown directory, whose records are private-key-grade (design §6). The probe writes a zero-byte file and removes it; no record content is read, logged, or served. |
| **Privacy is a commit-time gate** | The evidence for D5 and D7 came from a lab stand. No host, address, path, device UDID or registry name from it appears in this spec — the mechanisms are stated, the box is not. `make privacy-check` before every push. |
| **Every bug found on hardware becomes a replay fixture** | Near-miss, and it does not apply: the quince#897 findings are muxer-wiring, not device-protocol, so they become the G1–G5 fixtures rather than transcripts. Story 4's blocking measurement is device-facing and may yet need one. |
| **Subprocesses** | Unchanged in shape; D1 removes the only two the supervisor ever spawned. |

---

## PR slicing

Each carries one reviewable claim, each branched from `main` — **sequenced, not stacked**.

| PR | claim | proof |
| --- | --- | --- |
| 1 | **this spec** | reviewed before any code exists |
| 2 | one address grammar, three consumers | G1 |
| 3 | health probes; `external` only after a dial | G3 |
| 4 | one endpoint, one client | G4 |
| 5 | `manage_muxer: true` refuses; `muxsup` parked | G2 |
| 6 | rescan re-reads the muxer | G5 |
| 7 | read-only lockdown → 409 + wire field + UI | G6 |
| 8 | muxerless image, explicit runtime deps | G7 |
| 9 | `compose.hardened.yml` + canon | G8 (owed) |

PR 9 lands last on purpose: writing the compose before the dialer works would ship an example that
cannot run, which is the defect quince#721 named and quince#651 is the record of.
