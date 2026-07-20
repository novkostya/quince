# qn.3 — device ops + Devices page

**Goal.** Starting from a fresh container and using only the UI, a user pairs a USB
iPhone/iPad (Trust + passcode narrated), turns on backup encryption and sets the backup
password, and sees the device's real identity (name / model / iOS / paired / encryption)
— all driven by supervised libimobiledevice CLIs, with no backup password ever reaching
argv, logs, or the audit trail.

**Status: CLOSED (2026-07-20) — built, all gates green, and lab gate 8 PASSED on real
hardware** (pair via UI → recreate-still-paired → change_password + disable→enable, secrets
proven absent from argv/env/log; four findings caught + fixed + CI-validated — decisions log
(ba)). Originally **APPROVED at the pre-build spec-review gate — architect go with three
amendments + two rulings, all folded in below; Operator acks recorded.** (program loop
step 1 + decisions log (as)). The amendments: **(1)** persist pairing records into `/data`
(design §6's promise; qn.3 is the rung that creates them) and prove it in gate 8 by a
container recreate; **(2)** gate 8 branches on the device's real encryption reading (likely
already ON → `change_password`, not enable-from-off); **(3)** design §6's audit-trail list
gains the pair/encryption events (docs-part-of-diff). Rulings: pairing is confirmed safe (no
`unpair` anywhere in this rung — the CT pairing record is untouched); the backup password is
Apple's device-global password (== the future vault-unlock password) — a real one, stored,
encryption left ON. **Operator acks (this session):** hardware encryption coverage =
`change_password` **plus a disable→enable cycle** (ends safely: ON, known password); the
freshly-paired container is **kept standing** as the lab base for qn.4.

This rung inherits **"enrich muxd devices with lockdown identity"** from qn.2 (that spec's
Design §"Handoff to qn.3"): the qn.2 registry deliberately left `name`/`model`/
`ios_version`/`paired`/`backup_encryption` at their honest `""`/`"unknown"` defaults for
this rung to populate. It also implements the **backup-encryption-management** gap ruled
into scope by decisions log (s).

## Boundary

**In scope:**

- `core/internal/deviceops` (**new package**) — argv subprocess **wrappers** for
  `idevicepair` (validate / pair) and `ideviceinfo`, plus `idevicebackup2 encryption` /
  `changepw`; an **ops manager** owning the `Op` lifecycle (contracts §2) for the async
  pair/encryption flows; and an **enrichment driver** that runs `ideviceinfo` +
  `idevicepair validate` on attach and feeds identity into the registry. Same subprocess
  hygiene as `internal/muxsup` (argv slice, own process group, ctx-killed).
- `core/internal/device` — an **enrichment update path** (the `Enrich`/upsert the qn.2
  handoff named): overlay lockdown identity onto a muxd-known device and emit
  `device.updated`. No change to the muxer-presence merge model (qn.2).
- `core/internal/httpapi` — route + serve the **already-frozen** contract surfaces
  (contracts §1): `POST /api/devices/{udid}/pair` → `202 {op_id}`,
  `POST /api/devices/{udid}/pair/validate` → `{paired}`,
  `POST /api/devices/{udid}/encryption` → `202 {op_id}`, `GET /api/ops/{op_id}` → `Op`;
  a consumer-defined `DeviceOps` interface (primitives only, like `DeviceReader`/
  `MuxerControl`), satisfied by the real manager and by `--demo`.
- `core/internal/store` — audit rows for pair + encryption ops (event only, **no secret**),
  via the existing `audit` table (design §6). **Amendment 3:** design §6's audit-trail list
  ("login, unlock, file download, version delete") gains the **pair / encryption** events in
  the same diff (docs-part-of-diff).
- **Pairing-record persistence** (**amendment 1** — the substantive one). qn.3 is the rung
  that *creates* the private-key-grade `/var/lib/lockdown` records (design §6), so it must
  also make them **survive a container recreate** by persisting into `$QUINCE_DATA`
  (`0600`, never served, never logged). **Mechanism is rung-ruled** during the build —
  entrypoint/compose mapping of the lockdown dir onto `/data` vs. a post-pair copy — so
  `deploy/` (compose + entrypoint) **joins the boundary** if the mapping approach wins (the
  qn.2b "deploy is in scope when the gate needs it" lesson). Without this, a recreate
  silently discards the pairing the user just did — gate 8's own fresh-container premise
  demonstrates the loss.
- `core/cmd/quince` — non-demo wiring: construct the deviceops manager, subscribe it to
  attach events for enrichment, pass it to the API as `DeviceOps`.
- `core/internal/demo` — the demo `Provider` satisfies `DeviceOps` so `--demo` and the UI
  e2e drive pair + encryption without hardware (the demo already scripts `op.updated`
  narration; this rung wires it to the endpoints).
- `ui/` — Devices dashboard + Device-details: show real identity; make the currently
  **disabled** `Pair` and `Manage encryption` controls live (dialogs narrating Trust /
  passcode / on-device confirm via `op.updated`); wire the existing unencrypted-device
  warning banner's CTA; every refusal (e.g. "pairing needs USB") explained, never a dead
  button.

**Out of scope (explicit):**

- **Backups / jobs** (`idevicebackup2 backup`, the job state machine, preflight's
  `require_encryption` enforcement) → **qn.4**. This rung sets encryption *state*; it never
  runs a backup. `httpapi`'s `Jobs`/`Versions` stay `Empty`.
- **Storage** (qn.5), **vault/unlock** (qn.8): untouched. No `Version` is created; enabling
  encryption is a device-side setting, not a version mutation.
- **No new config key.** Device ops read existing `devices.usbmuxd_socket` /
  `devices.netmuxd_addr` (to point the CLIs at a muxer) and later `backup.require_encryption`
  is consumed by qn.4, not here. Schema v0 is unchanged (Rule check).
- **Live re-enrichment on a timer** — enrichment is event-driven (on attach) + on-demand
  (after a pair/encryption op, and the pre-job refresh hook reserved for qn.4), never
  polled (design §3).
- **`repair-working-copy`, retention, restore** — later rungs.
- **Contracts are consumed, not changed.** The pair/validate/encryption/ops shapes and the
  `Op` object + `op.updated`/`device.updated` events are already frozen (contracts §1/§2/§3);
  this rung implements them and treats any build-vs-contract mismatch as a **gap** (protocol),
  not a silent divergence.

## Design

Canon this rung implements (linked, not repeated): design **§2** (`device ops` component),
**§3** (device model + "backup encryption is a managed device property"), **§6** (secrets /
subprocess hygiene / pairing records), **§10** (observability); stack **D2** (drive Apple ops
via CLI subprocesses, pointed at the muxer via `USBMUXD_SOCKET_ADDRESS`), **D13** (assisted
model — the phone demands on-device confirmation); contracts **§1/§2/§3**; decisions log **(s)**.

- **Wrappers, not a library.** `idevicepair`, `ideviceinfo`, `idevicebackup2` are run as
  argv subprocesses (never shell strings) in their **own process group** (`Setpgid`,
  ctx-killed) — the `internal/muxsup` hygiene, reused. Each wrapper takes an overridable
  binary name + extra env (the `muxsup` `name`/`args`/`env` shape) so the fake-CLI tests
  inject a helper-process stand-in. UDIDs are **validated against a strict pattern** before
  they reach any argv (design §6). The CLIs are pointed at the right muxer by setting
  **`USBMUXD_SOCKET_ADDRESS`** in the child env — unix (`UNIX:/var/run/usbmuxd`) or TCP
  (`127.0.0.1:27015`) form per transport (exact accepted form **verified live**, see
  Interface facts).
- **Transport selection.** `ideviceinfo` / `idevicepair validate` point at the socket for
  the transport the device is present on (prefer USB when present, else Wi-Fi). **Pairing is
  USB-only** at the protocol floor (stack D2, decisions log (ag)): a `pair` for a device not
  present on USB returns an **actionable 409** ("pairing needs a USB connection"), never a
  silent failure.
- **Identity enrichment path (the qn.2 handoff).** `internal/device` gains an identity
  overlay keyed by UDID (`Enrich(udid, Identity{name,model,ios_version,paired,
  backup_encryption})`): `mergedLocked` layers it over the muxd-minimal shell, so a device
  renders instantly on attach (qn.2) and its identity fills in a beat later. `Enrich` emits
  `device.updated` only when a field actually changes (keep the WS quiet). The deviceops
  **enrichment driver** subscribes to `device.attached` on the bus, debounces per UDID, runs
  `ideviceinfo` (+ `idevicepair validate`, + the `com.apple.mobile.backup`/`WillEncrypt`
  read), and calls `Enrich`. Enrichment runs **off the request path** so `GET /api/devices`
  stays < 100 ms (perf budget); the read endpoints serve cached identity. A field quince
  cannot determine stays at its honest `""`/`"unknown"` — never guessed.
- **Op lifecycle (async pair/encryption).** The ops manager owns `Op` objects keyed by
  `op_id`, publishes `op.updated`, and serves `GET /api/ops/{op_id}` as the poll/refresh
  fallback. `POST …/pair` and `…/encryption` return `202 {op_id}` immediately; a supervised
  goroutine drives the subprocess and the Op state (`running → waiting_for_user → succeeded |
  failed`). The narration text is plain-language (contracts §2: "Tap Trust on the phone…",
  "enter the passcode on the device"). The exact **when** of `waiting_for_user` follows the
  CLI's real interaction (see Interface facts) — e.g. `idevicepair pair` on an un-Trusted
  device, and `idevicebackup2 encryption` blocking on the device's passcode confirm.
- **Backup-password handling (the load-bearing secret path).** The password arrives in the
  TLS request body → held only in memory → handed to the short-lived `idevicebackup2` child
  by **pty prompt** (preferred) or the **`BACKUP_PASSWORD` env fallback** (same-uid, short-
  lived) — **the mechanism is verified live** (Interface facts); **argv is forbidden**
  (world-readable `/proc`). It is **never logged, never stored, never persisted**; the audit
  row records the *event* (`encryption enabled`/`changed`/`disabled` for `{udid}`) **without
  the secret** (design §6, contracts §1). This is Apple's device-global backup password — the
  same one that later unlocks versions in the vault; **quince sets it and never keeps it.**
- **Encryption actions** (contracts §1: `enable | change_password | disable`) map to
  `idevicebackup2 encryption on` / `changepw` / `encryption off`; `disable`/`change` require
  the old password. Disabling is allowed but the UI copy discourages it (design §3). On
  success the driver re-enriches the device so `backup_encryption` flips in the UI.
- **Pairing-record persistence** (amendment 1; design §6). A successful `pair` writes a
  `/var/lib/lockdown/<udid>.plist` — a **private-key-grade** secret. quince persists it under
  `$QUINCE_DATA` (`0600`) so a container recreate keeps the device `paired: yes` with no fresh
  Trust prompt. Preferred mechanism (rung-ruled at build): the entrypoint maps/symlinks the
  container's lockdown dir onto `$QUINCE_DATA/lockdown` so libimobiledevice writes straight to
  persistent storage (no post-hoc copy of a moving target); the fallback is a copy-after-pair.
  Records are **never served over the API and never logged** (design §6); the backup-your-
  appdata docs already call them out.
- **Demo path.** The demo `Provider` implements `DeviceOps`: `pair`/`encryption` return an
  `op_id` and drive the existing scripted narration (`running → waiting_for_user →
  succeeded`); `validate` returns the fixture `paired`. So the UI and the e2e story exercise
  the full flow with zero hardware, and README/release screenshots stay live.

### Interface facts to verify live (part of the rung's evidence — looked up, never remembered)

Per the hard rule extended to **interface facts** (decisions log (al) + qn.2b amendment 1):
each is checked against the **shipped binaries in the built image** (`--help` + source),
recorded in Rung-ruled decisions, and the code is built to match — not assumed. A finding
that contradicts canon (e.g. the CLI supports *neither* pty nor env for the password) is a
**gap** (`PROPOSED`), stopped and reported, not worked around.

1. **`idevicebackup2` password channel** — does this build read the encryption password from
   a **pty prompt**, from **`BACKUP_PASSWORD`**, or only from **argv**? Pick pty > env; if
   only argv is possible, STOP (gap — it would violate secrets discipline).
2. **`USBMUXD_SOCKET_ADDRESS`** accepted forms (`UNIX:<path>` vs `<host>:<port>`), so the
   wrappers point at usbmuxd (USB) and netmuxd (Wi-Fi) correctly.
3. **`idevicepair pair` interaction** — does it block awaiting Trust, or error-and-retry?
   (Decides the `waiting_for_user` loop: poll `validate` with backoff until paired/timeout,
   vs read a blocking prompt.) And its SUCCESS/ERROR output strings.
4. **`ideviceinfo` keys** — `DeviceName`, `ProductType` (→ `model`), `ProductVersion` (→
   `ios_version`), and `-q com.apple.mobile.backup -k WillEncrypt` (→ `backup_encryption`).
5. **`idevicepair validate`** exit/text semantics for paired vs not-paired vs no-device.

## Stories

Each independently checkable; CI stories are proven by running a test (fake-CLI helper-
process stand-ins, the `muxsup` `GO_WANT_HELPER_PROCESS` discipline — no hardware in CI).

1. **Identity enrichment.** On a `device.attached` (USB or Wi-Fi), the enrichment driver runs
   `ideviceinfo`(+`validate`+`WillEncrypt`) and the registry `Enrich` overlays
   name/model/ios_version/paired/backup_encryption and emits **one** `device.updated`;
   `GET /api/devices/{udid}` then returns the populated fields; an undeterminable field stays
   `""`/`"unknown"`. (fake-CLI + registry/bus test.)
2. **Pair op.** `POST /api/devices/{udid}/pair` → `202 {op_id}`; the Op narrates
   `running → waiting_for_user` ("tap Trust… then enter the passcode") `→ succeeded`, each
   step on `op.updated`; `GET /api/ops/{op_id}` returns the latest. A device **not on USB** →
   actionable **409**. (fake-CLI drives the Trust-wait then success; httpapi test for the 409
   + the op_id contract.)
3. **Pair validate.** `POST /api/devices/{udid}/pair/validate` → `{paired: bool}` from
   `idevicepair validate`, both outcomes. (fake-CLI + httpapi test.)
4. **Encryption management.** `POST /api/devices/{udid}/encryption {enable|change_password|
   disable, …}` → `202 {op_id}`; drives the right `idevicebackup2` subcommand; the password
   reaches the child via the **verified** channel (pty/env); the Op narrates the on-device
   passcode-confirm step; on success the device re-enriches to `backup_encryption: on/off`;
   an audit row is written. (fake-CLI + httpapi test per action.)
5. **Secrets discipline, proven.** A focused test asserts, for an encryption op, that the
   password appears in **none** of: the child's argv (`/proc/<pid>/cmdline` equivalent — the
   fake records its argv), the captured logs, or the audit row; and that a **failed** op ends
   `failed` with an error carrying **no** secret. (This is the rung's headline gate — state
   honesty + secrets discipline in one test.)
6. **Wiring — non-demo + demo.** Non-demo serve constructs the manager, subscribes it for
   enrichment, and routes the four endpoints; `--demo` satisfies `DeviceOps` with the scripted
   op flow (pair + encryption reachable end-to-end from the browser). (cmd/httpapi test +
   the e2e story below.)
7. **UI.** Device-details shows real identity (name/model/iOS); `Pair` is live → a dialog
   that narrates Trust/passcode from `op.updated` and resolves to paired; `Manage encryption`
   opens a dialog (enable / change / disable) with password fields that are **never** echoed
   to logs/URLs, narrating the device confirm; the unencrypted-banner CTA opens the enable
   flow; a 409 ("needs USB") is explained inline. (vitest for the components + one Playwright
   story driving pair→encryption against `--demo`.)

**Lab gate (manual, hardware — OWNED and RUN this rung; recorded in the rung report; not CI):**

8. **Fresh container → paired (persistent) → real encryption action, UI only.** On the lab
   CT, with qn.2b's supervised in-container usbmuxd (**already proven on hardware**, decisions
   log (aw)) and a USB device. Legs:

   **(a) Pair, and prove persistence (amendment 1).** Bring up a **fresh** quince (empty
   `/data` → no pairing record); in the UI, **Pair** → Trust + passcode on the phone → device
   shows `paired: yes` and real identity (name/model/iOS). Confirm the record landed under
   `$QUINCE_DATA` (`0600`). Then **recreate the container** → the device is **still
   `paired: yes` with no new Trust prompt** (this is what turns design §6's promise from
   aspirational into proven-at-rung-close). No `unpair` is run anywhere (ruling: the CT's
   existing pairing record is untouched — fresh container = new trusted host alongside it).

   **(b) Encryption — branch on the real reading (amendment 2 + Operator ack).** Enrichment
   will almost certainly show `backup_encryption: on` (the device already has a backup
   password from the feasibility era), so the exercised hardware action is **`change_password`**
   (old → new) — needs the **current** password on hand; the new one is a **real** password
   the Operator stores in a password manager (this is also the future qn.8 vault-unlock
   password). **Operator ack — also run a `disable → enable` cycle** on the real device for
   full hardware coverage, ending safely with encryption **ON** and a known password. Confirm
   the on-device passcode-confirm step was narrated for each action; `backup_encryption`
   tracks the real state through the cycle. (Enable-from-off, if the device were ever off, is
   covered by fake-CLI + the `--demo` e2e.)

   **(c) Secrets-absence on real hardware.** Verify the password is in **no** argv (`ps -ww` /
   `/proc/<pid>/cmdline` of the `idevicebackup2` child), **no** log line, and **no** audit row.
   Record outcomes + observed timings.

   **Post-gate (Operator ack):** **keep the freshly-paired container standing** as the lab
   base — with leg (a)'s persisted record, qn.4 (backups) starts already-trusted, no second
   Trust dance. **Report caveat:** repeated fresh-pair testing accumulates trusted-host
   entries on the phone — harmless, prunable in iOS Settings.

## Gates

- `make gates` (go + vault + ui) + `make image` + `make gates-ui-e2e` green in `quince-dev`.
- New CI assertions: stories 1–6 as Go tests (fake-CLI helper-process stand-ins);
  story 5 (secrets-absence) is the headline gate; story 7 as vitest + one Playwright story.
- **Coverage declared** in the rung report (program loop step 3): `go test -cover` summary
  for `internal/deviceops` + the touched packages, plus a known-untested list with reasons.
- If amendment 1's mechanism touches `deploy/` (lockdown-dir mapping), the compose example is
  syntax-checked in the gate ladder where cheap (the qn.2b pattern) and proven live by gate 8
  leg (a); a copy-based mechanism is instead unit-tested in `internal/deviceops`.
- Lab gate 8 recorded in the rung report + progress decisions log (goal proven on hardware
  this rung — the "provable at rung close" rule; the CI stories prove the wrappers/endpoints/
  UI, gate 8 proves the fresh-container→paired→encrypted goal end-to-end).

## Fixtures

- **Fake-CLI helper processes** (`GO_WANT_HELPER_PROCESS`, the `muxsup` pattern) standing in
  for `idevicepair` / `ideviceinfo` / `idevicebackup2`: canned `ideviceinfo` key/value output
  (incl. `WillEncrypt`), pair Trust-wait-then-success and failure transcripts, and an
  encryption op that records its argv + env so the secrets-absence test can inspect them. No
  committed real data.
- **Privacy (hard rule):** a real `ideviceinfo` dump contains the device UDID (== SerialNumber)
  **and a person's `DeviceName`** ("… 's iPhone") — personal data. CI fixtures use **synthetic
  UDIDs and synthetic device names**; any real lab capture is rewritten or stays Operator-local;
  `make privacy-check` before every commit. **Every bug found on the lab box becomes a replay
  fixture before it is fixed** (with synthetic identifiers).

## Rule check (mandatory — written before building; program spec shape + decisions log (as))

Every hard rule / canon boundary this rung touches *or comes near*, one compliance line each.

- **Secrets discipline** (hard rule; design §6 — the rung's central rule). Backup password:
  TLS body → memory → `idevicebackup2` via **pty or `BACKUP_PASSWORD` env** (mechanism
  **verified live**), **never argv**, never logged, never stored; audit event carries no
  secret; **story 5 proves absence** in argv/logs/audit. `change_password`/`disable` also take
  the **old** password (same channels, same discipline — never argv). Test fixtures use `test`;
  the *lab* device uses a **real** Operator-chosen password stored in a password manager (it is
  Apple's device-global backup password == the future qn.8 vault-unlock password — ruling), so
  the `test` convention is fixtures-only. **Pairing records** (`/var/lib/lockdown`) are
  **private-key-grade** secrets (§6): persisted `0600` under `$QUINCE_DATA`, **never served,
  never logged**. **Near-miss flagged:** the `BACKUP_PASSWORD` env fallback is same-uid
  exposure — used **only** if pty is unsupported, and which channel was used is logged (no
  silent fallback). **Complies.**
- **Subprocesses: argv arrays, own process group, supervised, killed on end** (hard rule;
  design §1/§6). All wrappers use argv slices (never shell), `Setpgid`, ctx-kill — reusing the
  `muxsup` hygiene. UDIDs validated against a strict pattern before reaching argv. **Complies.**
- **State honesty** (hard rule). `paired`/`backup_encryption` are set only from real CLI
  output — an undeterminable field stays `"unknown"`, never guessed; an Op reaches
  `waiting_for_user` only when the CLI actually waits, and `failed` says so with a
  secret-free error; the UI shows enrichment-pending honestly (no fabricated identity).
  **Complies.**
- **No silent caps or fallbacks** (hard rule). pty→env fallback is **surfaced** (logged);
  "pairing needs USB" is an explicit actionable **409**, not a quiet no-op; a wrapper failure
  becomes a visible `failed` Op. **Complies.**
- **Config tidiness — D12** (hard rule). qn.3 adds **no** config key (reads existing
  `devices.usbmuxd_socket`/`netmuxd_addr`; `backup.require_encryption` is qn.4's to enforce).
  **Near-miss flagged:** nothing to add → trivially compliant; no UI-only state, no new
  env var. **Complies.**
- **A rung's goal is provable at rung close** (hard rule). The goal needs hardware → **lab
  gate 8 is owned and run this rung**, building on qn.2b's **already-landed, hardware-proven**
  supervised usbmuxd — a dependency on a *landed* rung, not a future one. CI stories 1–7 prove
  the wrappers/endpoints/UI with fakes. **Amendment 1 makes design §6's pairing-persistence
  promise provable here too** — gate 8 leg (a) recreates the container and re-checks
  `paired: yes`, so "backed up into `/data`" stops being aspirational. No acceptance gate
  depends on a future deliverable. **Complies.**
- **Perf budgets** (hard rule: device list API < 100 ms). Enrichment runs **off the request
  path** (background, on attach); read endpoints serve cached identity and never block on a
  subprocess. **Complies.**
- **Privacy is a commit-time gate** (hard rule). CI fixtures use synthetic UDIDs **and**
  synthetic device names (a real `DeviceName` is personal data); real lab dumps are rewritten
  or Operator-local; no UDIDs/names/paths in committed files, messages, or branch names;
  `make privacy-check` before every commit. **Complies.**
- **Version pins / interface facts are looked up, never remembered** (hard rule + qn.2b
  amendment 1). No new version pins (the CLIs ship since qn.0). The five **interface facts**
  above are verified live against the shipped binaries and recorded as evidence; a
  contradiction with canon is a gap, not a workaround. **Complies.**
- **Never mutate a committed version / storage invariants** (hard rule). This rung touches
  **no** storage and creates **no** version; enabling encryption is a device-side setting.
  **Complies (nothing to touch).**
- **Docs are part of the diff** (hard rule). contracts §1/§2/§3 (pair/validate/encryption/ops,
  `Op`, `op.updated`/`device.updated`) and design §2/§3/§6 already describe this rung — qn.3
  **implements to them and verifies the match** (a mismatch is a gap). **Amendment 3:** design
  §6's audit-trail list gains the **pair/encryption** events in this diff (the code that writes
  them lands here), and §6's pairing-record-persistence promise is realized by amendment 1's
  code. Updates the progress dashboard + decisions log at rung end. **Complies.**
- **Contract / boundary discipline** (program loop step 2). qn.3 owns its slice of `core/` +
  `ui/` + `demo` as the sole frontier session; it **routes already-frozen** contract surfaces
  (implementation, not a contract change). **Near-miss flagged:** adding
  `/api/ops/{op_id}` + the pair/encryption routes is first-time implementation of frozen
  shapes — not a contract-change rung. **Complies.**

## Rung-ruled decisions (settled during the build; *rung-ruled* canon)

### Interface facts — verified live in `quince:local` (libimobiledevice **1.4.0**), 2026-07-20

The gating evidence (looked up, never remembered — decisions log (al) + qn.2b amendment 1).
Verified by running the shipped binaries' `--help` and inspecting their strings.

1. **`idevicebackup2` password channel — STOP-gap CLEARED.** This build supports **both**
   `-i`/`--interactive` (pty getpass prompts: "Enter backup password", "Enter old/new backup
   password") **and** the env vars **`BACKUP_PASSWORD`** / **`BACKUP_PASSWORD_NEW`**; argv
   (`encryption on|off [PWD]`, `changepw [OLD NEW]`) is the third, **forbidden** option. Per
   the spec's stated preference (pty > env, env only if pty unsupported), **qn.3 uses the pty
   path** (`-i`, password written to the child's controlling pty, never in argv/env/log). Set
   commands prompt twice and compare (`ERROR: passwords don't match`); `changepw` prompts old
   then new; each device-side step ends with "Please confirm changing the backup password by
   entering the passcode on the device." (→ the Op's `waiting_for_user`).
2. **`USBMUXD_SOCKET_ADDRESS`** — `UNIX:<path>` for a unix socket (verified `UNIX:` prefix in
   libusbmuxd), `<host>:<port>` for TCP. Wrappers set it per transport: usbmuxd
   (`UNIX:/var/run/usbmuxd`, USB) vs. netmuxd (`127.0.0.1:27015`, Wi-Fi, with `-n`).
3. **`idevicepair` — error-and-retry, not blocking.** `pair` returns `SUCCESS: Paired with
   device <udid>` (exit 0) or, when the user hasn't accepted yet, `ERROR: Please accept the
   trust dialog on the screen of device <udid>, then attempt to pair again.` / `ERROR: Could
   not validate ... because a passcode is set. Please enter the passcode on the device and
   retry.` — so `waiting_for_user` is a **poll-`pair`-until-`SUCCESS`/denied/timeout loop**,
   not a blocking read. Denial: `ERROR: Device <udid> said that the user denied the trust
   dialog.` Pairing over a network connection: `ERROR: Pairing is not possible over this
   connection.` (→ the **USB-only 409**; `-w` wireless is Apple TV only).
4. **`ideviceinfo` keys** — `-x` (XML plist, robust to parse) for identity; `DeviceName` →
   `name`, `ProductType` → `model`, `ProductVersion` → `ios_version`; `-q com.apple.mobile.backup
   -k WillEncrypt` → `backup_encryption` (`true`/`false`/absent→`unknown`); `-s` (simple, avoid
   auto-pair) for the lightweight enrichment read.
5. **`idevicepair validate`** — `SUCCESS: Validated pairing with device <udid>` (exit 0, paired)
   vs. `ERROR: Device <udid> is not paired with this host` (not paired); the passcode-set error
   (fact 3) is a distinct "paired-but-locked" state surfaced honestly.

### Other rung-ruled decisions (settled during the build)

- **Password channel = pty** (interactive `-i`), per the spec's stated preference — both pty
  and the `BACKUP_PASSWORD`/`_NEW` env are supported live, so the env fallback is unused
  (`internal/deviceops/pty.go`, `creack/pty v1.1.24` — looked up live, newest stable, pure Go).
- **Package split**: `internal/deviceops` holds the CLI wrappers (`Tools`), the pty encryption
  path, the `Op` lifecycle `Manager`, the `EnrichDriver`, and the `LockdownStore` — separate
  from `internal/device` (registry, which gains only `Enrich` + an `Identity` overlay).
- **Enrichment**: the driver subscribes to bus `device.attached`, **per-UDID debounced 250 ms**,
  runs `ideviceinfo`(+`validate`+`WillEncrypt`) off the request path, and calls `registry.Enrich`;
  identity is cached by UDID (retained while absent, refreshed on each attach); a subscription
  overflow resubscribes + refreshes all present devices (no silent drop). The full read runs
  only when `validate` already reports a pairing (never auto-pairs an unpaired device).
- **`DeviceOps`** consumer interface (httpapi) returns primitives + `wire.Op` (no cross-package
  types): `Pair(ctx,udid)→(opID,status,reason)`, `Validate→(paired,status,reason)`,
  `Encryption(ctx,udid,action,pw,old,new)→(opID,status,reason)`, `Op(id)→(Op,ok)`;
  `UnavailableDeviceOps` is the 503 stub; `*deviceops.Manager` (non-demo) and the demo provider
  both satisfy it. **pair-USB-only → 409**; missing/invalid encryption input → 422; unknown
  device → 404.
- **`Op` poll loop**: pair polls `idevicepair pair` every **2 s** (bounded by a **2 min**
  timeout), narrating `waiting_for_user` on the trust/passcode errors; encryption is a single
  pty run bounded by **5 min**, narrating `waiting_for_user` when the "confirm on the device"
  line appears. Ops run under the serve context (shutdown cancels them).
- **Pairing-record persistence mechanism = whole-dir copy** (amendment 1), NOT a symlink: a
  `LockdownStore` copies `*.plist` (host identity + per-device records) between
  `/var/lib/lockdown` and `$QUINCE_DATA/lockdown` (`0600`) — `Restore()` at startup (never
  clobbering a live record), `Backup()` after a successful pair. Chosen over an
  entrypoint symlink because it is safe against a package-created dir or an operator bind mount
  and is unit-testable; **`deploy/` was NOT touched** (the Go mechanism suffices — the /data
  volume is already persistent).
