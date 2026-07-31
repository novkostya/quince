# qn.7 — Wi-Fi sync from quince

**Goal.** A user who has paired a device over USB in quince can see whether that device's
**Wi-Fi sync** is on, and turn it on from inside quince, with the on-device steps narrated —
so setting up Wi-Fi backups never requires a Mac.

**Status: PROPOSED — awaiting the spec-review gate.** No code exists. This is deliverable 2
of three for [quince#325](https://github.com/novkostya/quince/issues/325); deliverable 1
(the `roadmap.md` M4 rewrite) merged as
[quince-devlog#166](https://github.com/novkostya/quince-devlog/pull/166). Code is
deliverable 3 and does not begin until this is approved.

**What the rung is.** The D12 *"everything in quince"* promise is broken for the **primary**
transport: today Wi-Fi sync must be ticked in Finder/iTunes, so a user can pair over USB in
quince and then still needs a Mac. Closing that is the whole rung.

**What the rung is not.** Reliability work is **out** — Operator ruling, 2026-07-31, on a week
of real daily use. The chaos suite, liveness-threshold tuning, `-4`→`connection_lost`
reclassification and retry/resume proving are **dropped, not owed**; the remedy for a dropped
backup is `qn.12`'s push, which turns *"go find the laptop"* into one tap. The netmuxd-USB
audition is [quince#326](https://github.com/novkostya/quince/issues/326), not this rung.

---

## Boundary

**In scope:**

- `core/internal/deviceops` — a **read** wrapper for the Wi-Fi-sync value (a near-clone of
  `willEncrypt`, `wrappers.go:122`) and a **write** path, plus a `Manager.WifiSync` op
  mirroring `Manager.Encryption` (`manager.go:235`).
- `core/internal/device` — `Identity.WifiSync`, carried through `Enrich` and defaulted in
  `deviceShellLocked` (`registry.go:334`). A field not defaulted there serialises as `""` and
  breaks the contract enum; that is a real hazard, not a hypothetical.
- `core/internal/wire` — `Device.WifiSync`.
- `core/internal/httpapi` — one new route, `POST /api/devices/{udid}/wifi-sync`, and a
  `WifiSync` method on the consumer-defined `DeviceOps` interface (`deps.go:98`).
- `core/internal/demo` — the demo provider scripts the op and flips the fixture value, as it
  does for encryption (`demo/deviceops.go:101`).
- `deploy/patches/libimobiledevice/` — **a third patch**, if and only if story 3 says the write
  needs one. See *The write has no vehicle* below; this is the rung's one real unknown.
- `ui/` — a `wifi_sync` badge on the card and details page, and the toggle.
- `docs/` — contracts §1/§2, design §3, and §9 if the infeasible branch is taken.

**Out of scope, explicitly:**

- Everything the 2026-07-31 ruling dropped. This spec adds no chaos fixtures, tunes no
  thresholds, and touches no classification code.
- quince#326's audition.
- Turning Wi-Fi sync **off** as a user-facing feature is in scope only as far as story 5's API
  shape allows it; see *Rung-ruled decisions* for why disable is specified but not promoted.
- Automatically flipping the flag during pairing. The roadmap sketched *"quince pairs **and**
  flips Wi-Fi sync on"*; this spec deliberately does **not** do that — see *Rung-ruled
  decisions*.

---

## Interface facts — measured live, at the pinned tag

**Per the interface-facts rule, nothing here is remembered.** Measured 2026-07-31 against a
`--depth 1 --branch 1.4.0` clone of `libimobiledevice/libimobiledevice`, matching
`versions.env: LIBIMOBILEDEVICE_REF=1.4.0`. **No device was involved**; these are facts about
the tool, not about iOS.

| # | fact | evidence |
| --- | --- | --- |
| 1 | `lockdownd_set_value(client, **domain**, key, value)` exists and takes a domain | `src/lockdown.c:457`, `include/libimobiledevice/lockdown.h:199` |
| 2 | **No shipped CLI exposes a generic domain+key set.** There are exactly two callers in `tools/`, and **both pass `domain = NULL`** | `tools/idevicename.c:146` (`DeviceName`), `tools/idevicedate.c:238` (`TimeIntervalSince1970`) |
| 3 | `com.apple.mobile.wireless_lockdown` **is** in `ideviceinfo`'s known-domain list, tagged *iOS 4.0+* — so the **read** needs no new tooling | `tools/ideviceinfo.c:72` |
| 4 | **`EnableWifiConnections` appears nowhere in the source** | `grep -rn 'EnableWifiConnections\|wireless_lockdown' .` → one hit, fact 3 |
| 5 | The runtime binary is built `CGO_ENABLED=0`, so Go cannot link the C library | `deploy/Dockerfile:102` |
| 6 | quince writes **no** lockdown value today; every device write goes through `idevicebackup2` subcommands | `grep -rn 'lockdownd_set_value\|SetValue\|set_value'` over the repo → zero hits |

### What facts 3 and 4 change about the plan

The roadmap's hypothesis was *"a lockdown `SetValue` on `com.apple.mobile.wireless_lockdown`,
an `EnableWifiConnections`-ish key"*. Fact 3 confirms the **domain** is real and readable with
the tooling already in the image. Fact 4 says the **key name has no upstream corroboration at
all** — it is a guess, and this spec treats it as one.

**So the spike is split, and the read half comes first.** That ordering is not tidiness:

- `ideviceinfo -q com.apple.mobile.wireless_lockdown -x` dumps the **whole domain**, which
  *names the keys* instead of guessing one. Run twice — once with Wi-Fi sync off in Finder,
  once with it on — the **differential** identifies the key with no ambiguity.
- It needs **no new code, no patch, and no write to the device.** Writing a guessed key into a
  lockdown domain on the Operator's real phone is the one genuinely risky act in this rung, and
  doing it before the read would be doing it blind.

**The write has no vehicle.** Fact 2 means enabling Wi-Fi sync cannot be done by any binary in
the image today. Options, to be ruled at spec review:

| | option | cost | note |
| --- | --- | --- | --- |
| **A** | `0003-*.patch` adding a generic `--set-value <domain> <key> <bool>` to a tool | S | Follows `0001`/`0002` precedent exactly; `Tools`/`run` unchanged; reviewable as a diff |
| **B** | a pure-Go usbmux + lockdown + TLS client | L | Fact 5 forbids the cheap version; this is a protocol implementation, not a wrapper |
| **C** | rung returns *infeasible* | — | Only if story 3 shows the value is not settable at all |

**This spec proposes A** and does not build it until story 3 has named the key. B is recorded so
the review can reject it explicitly rather than have it resurface later.

---

## Design

**Wi-Fi sync is a managed device property, exactly like backup encryption.** That is the whole
design claim, and it is the Operator's ruling of 2026-07-31 — the setting lives *on the device*,
is per-device, and is wanted right after USB pairing. So it inherits qn.3's shape rather than
inventing one: status read back from the device, changed through quince, USB-trusted, on-device
steps narrated, `Op` lifecycle for the async half.

**The tri-state and its honesty rule are copied verbatim, because they were a shipped bug.**
`willEncrypt` (`wrappers.go:114-121`) returns `"unknown"` **only** on a genuine read failure;
exit 0 with empty output means the key is absent, which is `"off"`. Collapsing those two was
qn.4a finding (i)-A, fixed in qn.4c. `wifi_sync` uses the same rule and the same three values,
and the UI hides the badge while `"unknown"` rather than guessing (`DeviceCard.tsx:14-30`).

**The write is not a secret operation, and that is a deliberate divergence from qn.3.** The
value is a boolean. `pty.go` exists because a backup *password* must never reach argv; nothing
here is a password, so the write follows `wrappers.go:run` — argv slice, own process group,
ctx-killed — and **must not** acquire pty/secret machinery it does not need. Stated because the
instruction *"mirror the encryption op"* read literally would produce a pty flow guarding
nothing.

**Read gating follows `Info`.** The full read only runs when validate returns
`validatePaired` (`wrappers.go:150-157`), so background enrichment never triggers a Trust
prompt. `wifi_sync` joins that gated branch and stays `"unknown"` for an unconfirmed device.

### Contract changes — declared, and NOT built until ruled

`docs/contracts.md` says a contract change *"lands here first, version-bumped"*, and that
**field additions are non-breaking**. This rung proposes three:

1. **`Device.wifi_sync: "on" | "off" | "unknown"`** — a field addition. Non-breaking by the
   header's own rule.
2. **`Op.kind` gains `"wifi_sync"`** — an **enum extension**, which the header does not
   classify. qn.6a's `seeding` state set the precedent that an enum addition is ruled at spec
   review, not assumed; this follows it.
3. **`POST /api/devices/{udid}/wifi-sync`** → `202 {op_id}`, body `{"action": "enable" |
   "disable"}` — a new endpoint, additive.

Per `CLAUDE.md`, contract surfaces are stop-and-ask. **These are declared here for the review
to rule on, and no code implements them beforehand.**

### Inherited constraint — `decisions/0013`, and why the drop makes it matter more

`decisions/0013` rules that **network-level mitigation — AP or band steering, SSID separation,
roaming-threshold tuning — is a workaround, never the primary answer.** Its owed clause is now
in `roadmap.md` M4; its **spec half was owed to this document** and this paragraph discharges it.

It reads as vestigial now that reliability is out of the rung. It is the opposite. With no
reliability work in flight, **AP tuning is the only thing left that makes the symptom disappear**
— and a user whose roaming is tuned away stops seeing the failure, so `qn.12`'s push, the actual
answer, never gets exercised. The clause guards a trap the drop made *more* reachable, not less.

**0013's remedy half is superseded and its floor half is not.** The floor — a roam is
unrescuable in-flight, TLS state bound to the dead connection, no session reattach — stands.
The remedy it named, *"auto-retry-on-reconnect plus resume"*, does not: **auto-retry is
impossible**, because a retry inside iOS's recent-unlock window does not skip the passcode. The
honest words are **one-tap retry**, and `CLAUDE.md`'s flat *"no auto-retry"* stands unqualified.

---

## Stories

Each is independently checkable. **1, 2, 6 and 7 need no hardware. 3, 8 and 9 do.**

1. **The value is read back.** `ideviceinfo -q com.apple.mobile.wireless_lockdown -k <KEY>`
   populates `Identity.WifiSync` → `Device.wifi_sync`, gated on a confirmed validate, with
   `willEncrypt`'s unknown-vs-off rule. `<KEY>` is a build-time constant filled in by story 3;
   until then the wrapper exists and its tests drive the fake CLI.
2. **The UI shows it, and hides it when unknown.** A badge on `DeviceCard` and
   `DeviceDetailsPage`, following the encryption badge's three-way shape — `"unknown"` renders
   nothing.
3. **Spike A (hardware, read-only): the key is named, not guessed.** On a real paired device,
   dump the domain with Wi-Fi sync **off** in Finder and again with it **on**; diff. Output is
   the exact key and its value type, recorded in this spec. **No write occurs.** If the domain
   is empty or the differential is ambiguous, the rung takes the story-9 branch.
4. **The write vehicle exists and is reviewable.** Per the ruled option — proposed A, a
   `0003-*.patch` adding a generic set-value flag — plus a `Tools` wrapper using `run`, not pty.
   Proven CI-side against the fake CLI; the patch itself is proven by the image build listing
   the new flag in `--help`, as `0002` was.
5. **The op runs.** `POST /api/devices/{udid}/wifi-sync {action}` → `202 {op_id}`, a `wifi_sync`
   `Op` narrating any on-device step, re-enrich on success, and an honest `wire.JobError` on
   failure. Validation ladder copied from `Manager.Encryption`: bad udid 400, unknown device
   404, no transport 409, bad action 422.
6. **The UI toggle.** Beside the encryption control on the details page, gated on
   `paired === "yes"`, driven through `useDeviceOp`.
7. **A failed write is never silently a success.** A set that reports success but reads back
   unchanged must surface as a failed op with a distinguishable message. `decisions/0004` — *a
   mutation must be verified to have mutated* — applies directly, so the op **re-reads** and
   compares rather than trusting the exit code.
8. **Hardware gate (the rung's acceptance).** A device whose Wi-Fi sync is **off** reads back
   `off` in quince, is turned `on` through quince alone with on-device steps narrated, and then
   completes a **Wi-Fi backup with no Mac ever involved.**
9. **The infeasible branch, which is a real outcome and not a failure.** If story 3 or 4 shows
   the value cannot be set — or cannot be set without a reboot, a respring, or a re-Trust that
   makes it worse than Finder — the rung ships **onboarding documentation of the Finder step,
   honestly**, in design §9, plus whatever story 1 proved readable. M4's gate accepts this;
   only an unmeasured claim fails it.

---

## Gates

Beyond `make gates` / `make image` / `make gates-ui-e2e`:

| gate | command / observation | proves |
| --- | --- | --- |
| G1 | `go test ./internal/deviceops/ -run 'WifiSync'` | read tri-state incl. **unknown-vs-off**, both branches |
| G2 | `go test ./internal/device/ -run Enrich` | the new field defaults honestly in `deviceShellLocked` |
| G3 | `go test ./internal/httpapi/ -run WifiSync` | the validation ladder and CSRF guarding |
| G4 | `make image` then `<tool> --help` in the container | the `0003` flag is really in the built binary (the `0002` precedent) |
| G5 | vitest on the card + details page | badge hidden while `unknown`; toggle gated on paired |
| G6 | **hardware**, story 3 | the differential names the key — *the gate that unblocks story 4* |
| G7 | **hardware**, story 8 | off → on through quince → a Wi-Fi backup, no Mac |
| G8 | `make privacy-check REF=… TEXT=…` | no UDID, no serial, no lab topology in any of it |

**G6 and G7 are owed to a hardware session.** An implementer box has no device; the Operator
coordinates directly (quince#325). **G6 gates G4** — the patch is not written until the key is
known — so the rung has a genuine hardware dependency in its middle, not only at its end. That
is stated here rather than discovered in build.

---

## Fixtures

- **`helper_test.go` gains a Wi-Fi-sync arm, and the existing `-k` arm must be narrowed first.**
  Today `helper_test.go:137` dispatches on the presence of `-k` and matches **any** `-k` read,
  so a second `-q`/`-k` domain would silently be answered by `fakeWillEncrypt`. Narrowing it to
  dispatch on the **domain** string is a prerequisite, not a nicety — left alone it produces a
  green test that exercises the wrong code path.
- New `DEVICEOPS_FAKE` scenarios beside the `enc_*` set: `wifi_on`, `wifi_off`,
  `wifi_never_set` (exit 0, empty → `off`), `wifi_read_failed` (non-zero → `unknown`),
  `wifi_set_fail`, and **`wifi_set_lies`** for story 7 — reports success, reads back unchanged.
- `fakeUDID = "SYNTHETIC-UDID-AAAA-0001"` throughout. Real serials are personal data.
- **No new backup transcripts.** This rung adds none, and the chaos captures the old scope would
  have needed are dropped with it.
- If G6 or G7 finds a bug, it becomes a fixture before it is fixed, per the hard rule.

---

## Rule check

Written before building. Every rule this rung touches **or comes near**, including near-misses.

| rule | how this plan complies |
| --- | --- |
| **Don't improvise architecture** | The one architectural choice — the write vehicle — is put to the review as three named options with a recommendation, not decided here. Contract changes are declared and unbuilt until ruled. |
| **Contracts are stop-and-ask** | Three changes declared above: one field addition, one enum extension, one new endpoint. **No code lands before the verdict.** |
| **Interface facts looked up live** | Six facts measured at the pinned tag with file:line evidence, and two of them (2 and 4) **contradict the roadmap's hypothesis** — which is the whole reason the rule exists. Nothing here is recalled. |
| **State honesty** | `unknown` means a genuine read failure and nothing else; the UI renders no badge rather than guessing. Story 7 makes a lying write a **failure**, per `decisions/0004`. Story 9 makes *infeasible* a reportable outcome instead of a quiet omission. |
| **No silent caps or fallbacks** | There is no fallback path. A device that cannot be read stays `unknown` and says so; a failed set surfaces as a failed op. |
| **Secrets discipline** | **Near-miss, and the reason it is listed.** The op is modelled on encryption, whose write is a pty flow *because a password is involved*. Nothing here is a secret, so this rung uses `run`, not `pty` — and must not acquire secret machinery guarding a boolean. No new secret enters argv, env, or logs because none exists. |
| **Subprocesses: argv arrays, own process group, killed on end** | Both new calls go through `Tools.run`, which already does `setpgid` + `cancelKillGroup`. No new subprocess pattern. |
| **Privacy is a commit-time gate** | Synthetic UDID in fixtures; G8 sweeps diff, messages and PR text. **Sharpest risk: story 3's evidence is a dump of a real device's lockdown domain** — it is summarised as *key name and type*, and the raw dump never enters git, a PR, or this file. |
| **Never mutate a committed version** | **Near-miss by adjacency only.** This rung touches no storage code. Story 8 runs a real backup as its gate, which exercises the committed lifecycle without changing it; any behaviour change there would be out of scope and a finding. |
| **Docs are part of the diff** | contracts §1/§2 and design §3 change in the same PR as the code that implements them; §9 in the story-9 branch. Story 3's measured key is written back into this spec. |
| **Coverage declared** | Each PR carries `go test -cover` plus an explicit known-untested list. Expected standing entries: the `0003` patch's C code (proven by G4's `--help` and by G6/G7, not by Go tests) and the hardware-only paths. |
| **Every hardware bug becomes a fixture** | G6/G7 findings become `DEVICEOPS_FAKE` scenarios before they are fixed. |
| **Config tidiness (D12)** | **Deliberately adds no config setting.** Wi-Fi sync is device state, not quince state; a `config.yml` key would be a second copy of a value the device owns. Listed as a near-miss because "every setting lives in `config.yml`" reads as universal and this is a setting. |
| **No UI-only state** | The toggle's truth is the device read-back, refreshed by `reEnrich`; the UI stores nothing the device does not confirm. |
| **A rung starts from a spec** | This document, reviewed before any code — and deliverable 1 was reviewed before it. |
| **Approver ≠ author** | Implementer authors; the architect reviews. Unchanged. |

---

## Rung-ruled decisions

Recorded here per the gap protocol — rung-local, inside the boundary, changing no contract
surface beyond the three declared above.

1. **Pairing does not auto-enable Wi-Fi sync.** The roadmap sketched *plug → Trust → quince
   pairs **and** flips Wi-Fi sync on*. Rejected for this rung: it makes a **silent write to the
   user's device** a side effect of an action they asked for something else from, and quince has
   no way to tell an unset flag from one the user deliberately turned off. The explicit toggle
   ships first. Auto-enable is a legitimate follow-up once story 3's read tells us what "unset"
   actually looks like — it is cheap then and unjustifiable now.
2. **Disable is implemented but not promoted.** The API takes `enable | disable` because a
   toggle that cannot be reversed is worse than no toggle, and because story 3's differential
   needs both directions anyway. The UI leads with enable; disable is available, not
   advertised.
3. **The read ships even if the write does not.** Stories 1 and 2 depend on fact 3 alone and are
   valuable on their own: knowing Wi-Fi sync is off is what makes the Finder instruction
   actionable in the story-9 branch. So the first PR is read-only and lands regardless of how
   the vehicle question is ruled.
4. **Until story 3 names the key, the read returns `unknown` WITHOUT querying the device.**
   Decided during the build of PR 2, because story 1's *"`<KEY>` is a build-time constant filled in
   by story 3"* left the meantime ambiguous and the obvious reading is unsafe. `ideviceinfo -q
   <domain> -k <wrong-key>` is expected to exit 0 printing nothing, and the absent-key rule this
   rung deliberately copies turns that into **`off`** — so a placeholder key would make quince
   assert confidently that every device has Wi-Fi sync disabled. That is the exact shape of qn.4a
   finding (i)-A, which shipped once already.
   So `wifiSyncKey` ships **empty**, `wifiSync` short-circuits to `unknown`, and a test asserts the
   constant is still empty — the guard is against a future session "finishing" the feature by
   filling in a plausible name. Rung-local: no contract change (the field and its enum are as
   ruled), and no user-visible claim, because `unknown` renders no badge. Filling it in after
   story 3 is a one-line diff.

   **DISCHARGED 2026-07-31.** Story 3 ran; the key is `EnableWifiConnections` and the constant is
   populated. The guard test inverted rather than being deleted — it now pins the *measured* value,
   so editing it still requires a measurement. The empty branch stays reachable and tested, because
   it is the honest answer whenever a key is unknown and the situation recurs for any second
   unmeasured lockdown value.

## Story 3 — the measurement, 2026-07-31

Run read-only against the Operator's staging stand over Wi-Fi, with Operator-granted SSH.
**Nothing was written to the device.** `com.apple.mobile.wireless_lockdown` returned six keys:

| key | value | what it is |
| --- | --- | --- |
| **`EnableWifiConnections`** | **`<true/>`** | **the flag** — boolean, `true` while Wi-Fi sync was known on |
| `EnableWifiDebugging` | `<false/>` | a different feature |
| `SupportsWifi` | `<true/>` | capability, not state |
| `SupportsWifiSyncing` | `<true/>` | capability, not state |
| `BonjourFullServiceName` | *(withheld)* | string; embeds the device's MAC and link-local address |
| `InstanceName` | *(withheld)* | string; same |

The last two are Operator-private and appear only in the private layer. Key names are Apple's.

**Proven:** the domain is readable over Wi-Fi through netmuxd; the key exists, is a boolean, and is
`true` on a device with Wi-Fi sync on.

**NOT proven, and left owed rather than closed:** that this key is what *changes* when the setting
is toggled. One read with the flag on cannot separate it from `SupportsWifiSyncing`, also `true`.
The differential needs Finder — the detour this rung exists to remove — and was not run.
The internal evidence is strong but is still inference: within the same dump `EnableWifiDebugging`
was `false` while this was `true`, so **the `Enable*` family carries state** (two members, different
values) where `Supports*` is uniformly `true` and reads as capability. Only one device was
reachable; the iPad was offline on both transports, so there is no cross-device confirmation either.

**Operational fact, and the reason the read nearly failed to happen:** `ideviceinfo` in the
container cannot see a Wi-Fi device by default. netmuxd is supervised on a **private socket**
(qn.4c — its default is usbmuxd's, and binding that is a silent USB blackout), so `idevice_id -n`
returns nothing while netmuxd's own log shows the device attached. The tool must be pointed at
netmuxd explicitly via `USBMUXD_SOCKET_ADDRESS`, which is `Tools.socketAddr` done by hand. A session
that does not know this concludes "no devices" on a box where the device is plainly present.

---

## Known gaps and open questions

- **[quince#328](https://github.com/novkostya/quince/issues/328)** — six items `qn.6b` parked in
  `qn.7` that the rescope homes nowhere. **Does not gate this spec**: none of the six is in the
  rung under any reading. Recorded so the spec does not read as if the rescope were complete.
- **The write vehicle** is the one question this spec puts to the review rather than answering.
- **Whether `SetValue` on this domain requires the device to be unlocked, or fires a Trust
  re-confirm**, is unmeasured and unmeasurable without hardware. Story 5's narration must
  therefore be written to handle an on-device step it has not yet seen — the `onDeviceConfirm`
  hook already exists (`pty.go:29`) but is pty-bound, so a non-pty equivalent may be needed.
  Flagged as the most likely place for story 4 to grow.

## PR slicing

| PR | claim | hardware |
| --- | --- | --- |
| 1 | this spec | no |
| 2 | stories 1 + 2 — the read, the wire field, the badge (contract change 1) | no |
| 3 | story 3's measurement written back into this spec | **yes** — G6 |
| 4 | stories 4 + 5 + 7 — the vehicle, the op, the verified write (contract changes 2 + 3) | no |
| 5 | story 6 — the toggle | no |
| 6 | story 8 or story 9 — the acceptance, or the honest Finder branch | **yes** — G7 |
