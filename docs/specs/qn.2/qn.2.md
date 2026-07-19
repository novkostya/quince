# qn.2 — muxd client + live device table

**Goal.** A real iPhone/iPad appearing on USB or Wi-Fi shows up in the UI within a second
(and vanishing removes it), driven by live muxer events — no polling, no demo fixtures.

**Status (2026-07-20): CLOSED.** Code complete (stories 1–5: muxd client + registry + wiring +
UI, `make gates`/image/e2e green — see progress decisions log `qn2-build`/`qn2-close`). **Lab
gates 6–7 (plug/unplug ≤1 s, netmuxd-USB audition) are DEFERRED** to a future hardware session:
they need a real device AND the in-container muxer-startup gap resolved (progress dashboard open
question 2 — nothing starts the muxer in the simple profile). Manual USB testing so far used a
temporary usbmuxd-in-CT stopgap on the staging box (`local/environment.md`).

## Boundary

In scope: `core/` only — a muxd protocol client (`internal/muxd`), a device registry that
merges N muxer sources into one table with per-transport presence (`internal/device`),
wiring it as the real `httpapi.DeviceReader` + `device.*` bus events in non-`--demo` mode,
and a record/replay **fake muxd socket** so all of it is CI-provable without hardware.

Out of scope (explicit): subprocess device ops — `idevicepair` / `ideviceinfo` and the
friendly `name` / `model` / `ios_version` / `paired` / `backup_encryption` fields they
populate — are **qn.3**; jobs (qn.4), storage (qn.5), and vault (qn.8) stay `httpapi.Empty`.
Contracts are consumed, not changed (the `Device` shape is frozen; qn.2 fills the subset
the muxer knows and leaves the rest at their honest "unknown"/empty defaults).

## Design

- **muxd client** (`internal/muxd`, per design §2 + stack D2). Speaks the usbmuxd
  binary-plist protocol (length-prefixed header + plist body; `Listen` request →
  `Attached` / `Detached` messages carrying `DeviceID` + `Properties{SerialNumber,
  ConnectionType, ...}`). One client per configured muxer socket, dialing **Unix**
  (`devices.usbmuxd_socket`, a path) or **TCP** (`devices.netmuxd_addr`, `host:port`) by
  the address form. Listen mode only (server→us push); reconnect with capped backoff on
  any read error or EOF; a tolerant frame parser logs and skips malformed/unknown messages
  rather than crashing (design §2 "unknown lines are logged, never fatal").
- **device registry** (`internal/device`, design §2/§3). Merges every muxer's stream into
  one table keyed by **UDID** (`SerialNumber`), with per-transport `last_seen`
  (`ConnectionType` → transport: `USB`→`usb`, `Network`/`WiFi`→`wifi`). Rules: presence is
  event-driven, never polled; an `Attached` sets that transport's timestamp and emits
  `device.attached` (with the transport edge); a `Detached` clears that transport and emits
  `device.detached`; a device with **no transports left** is removed from the table (same
  contract the `--demo` provider proved in qn.1). Implements `httpapi.DeviceReader`.
- **Topology from config, default per D2.** Both `usbmuxd_socket` and `netmuxd_addr` set →
  two clients (usbmuxd serves USB, netmuxd serves Wi-Fi) — the ruled default until the
  netmuxd-USB audition passes. Either alone works (single-muxer). N-socket design means the
  flip to single-muxer netmuxd is config-only, no code change.
- **Wiring** (`cmd/quince`). Non-`--demo`: build the clients + registry, start their Listen
  loops under the serve context, pass the registry as `Deps.Devices`. `--demo` is untouched
  (still the fixture provider). `AllowedOrigins`/jobs/versions unchanged.
- **Identity is muxd-minimal this rung** (Operator-confirmed — do NOT widen the rung with
  subprocess wrappers). The muxer knows `udid` + transports + `last_seen`; it does **not**
  know `name`/`model`/`ios_version` (lockdown/`ideviceinfo` — qn.3). Those fields serialize
  at their frozen defaults (`name: ""` → UI falls back to the UDID, `model: ""`,
  `ios_version: ""`, `paired: "unknown"`, `backup_encryption: "unknown"`, `last_backup:
  null`). The UI live table renders a device the moment it attaches; qn.3 enriches it. No
  contract change — only fewer fields populated.
  **Handoff to qn.3 (inherited scope — must not get lost):** qn.3 owns populating
  `name`/`model`/`ios_version`/`paired`/`backup_encryption` via `ideviceinfo`/`idevicepair`
  on attach (and refresh). The registry therefore exposes an **enrichment update path** (an
  `Upsert`/patch that layers identity onto a muxd-known device and emits `device.updated`)
  that qn.3 drives; qn.3's spec MUST list "enrich muxd devices with lockdown identity" as
  inherited from here.
- **DeviceID is per-connection — reconcile on reconnect (protocol subtlety, not optional).**
  A `Detached` message carries only the muxer's `DeviceID` (a per-connection integer,
  **reassigned after a reconnect**), never the UDID — so each client keeps a per-connection
  `DeviceID → (UDID, transport)` map and **resets it on every reconnect**. On re-`Listen`
  the muxer **replays its current attached set**; the registry must **reconcile against that
  replay**: a device present before the drop that does NOT reappear lost that transport while
  we were disconnected and must be cleared — otherwise it lingers as a **phantom device**.
  This "detached-while-away" path is the classic muxd stale-state bug and is tested
  explicitly (story 3).

## Stories

1. **Handshake + stream.** The client connects to a fake muxer socket, completes the
   `Listen` handshake, and turns a scripted `Attached`→`Detached` sequence into registry
   updates + `device.attached`/`device.detached` bus events (unit test against the fake).
2. **Per-transport merge.** One UDID attaching on `USB` then `Network` shows **both**
   transports; detaching one keeps the device present on the other; detaching the last
   emits `device.detached` and removes it (fake socket driving both `ConnectionType`s).
3. **N-socket topology + reconnect + detached-while-away.** Two configured muxers (Unix +
   TCP fakes) feed one merged table (a device on both shows both transports); a single
   configured socket works alone; when a fake socket drops, the client reconnects with
   backoff, **resets its per-connection `DeviceID` map**, and re-establishes Listen. **The
   reconnect replay is reconciled:** the fake replays a *reduced* attached set on
   re-`Listen`, and the test asserts a device that vanished while disconnected emits
   `device.detached` (no phantom lingers).
4. **Real REST + WS, no demo.** With the live registry wired (non-demo), `GET /api/devices`
   and `/api/devices/{udid}` serve muxer-derived devices and `device.*` events reach the WS
   — the qn.1 UI device table renders real attach/detach (verified via the fake muxd in an
   integration test; the browser path is the lab gate below).
5. **Robustness.** Malformed / truncated / unknown-type muxd frames never crash the client
   or wedge the Listen loop — they are logged and skipped (fake emits a garbage frame amid
   valid ones; the valid ones still land).

**Lab gates (manual, hardware — run on the lab CT, recorded in the rung report; not CI):**

6. Plug/unplug the lab iPhone → the device appears/disappears in the UI: **USB attach/detach
   within 1 s**; **Wi-Fi within a few seconds of the mDNS announcement** (announce timing is
   the phone's, not ours — record the observed value; don't fail an honest run on it). USB
   via the default usbmuxd topology, Wi-Fi via netmuxd mDNS (the lab device is pre-paired,
   Wi-Fi-sync enabled — pairing records in `local/environment.md`).
7. **netmuxd-USB audition on pinned `v0.4.3`** (stack D2), run **manually with the raw CLIs**
   pointed at netmuxd via `USBMUXD_SOCKET_ADDRESS` (pairing/backup *wrappers* are qn.3/qn.4,
   not this rung), with these guards:
   - **A fresh USB pairing destroys the lab's pairing record.** Fresh pairing means
     `idevicepair unpair` first, which invalidates the treasured July `/var/lib/lockdown`
     record. **Back up `/var/lib/lockdown` before unpairing**; old copies go stale once
     re-paired (note it in the lab CT). Requires **physical presence** (Trust tap + passcode).
   - **Backup traffic = a manual `idevicebackup2 backup` into a throwaway scratch dir**,
     deleted after — **never** the real backup dataset (no storage backends until qn.5; a raw
     run must not pollute the dataset's layout). The point is only that real traffic crosses
     the 64 KiB message boundary immediately (the workload that failed on 2026-07-13).
   - Clean → flip the configured default to single-muxer netmuxd and credit the v0.4.3 mux
     fix. Reproduces the lab's `message was too large (65536 bytes, max = 65535)` → file the
     upstream issue with the exact log line (patch-in-pinned-build optional, the qn.7
     pattern). Either outcome is config-only for the N-socket client.

## Gates

- `make gates` + the replay tests (fake muxd) green; stories 1–5 are Go tests in CI.
- Stories 6–7 are the lab checklist — outcome (esp. the audition verdict + any upstream
  issue link) recorded in the rung report and the progress decisions log.

## Fixtures

`core/internal/muxd/testdata/`: usbmuxd plist message sequences — `Attached` (USB),
`Attached` (Network), `Detached`, a reduced re-`Listen` replay (for the detached-while-away
test), and a malformed frame — byte-accurate to the wire protocol, built with the same plist
encoder the client decodes. **Privacy (hard rule): a real capture's `SerialNumber` IS the
device UDID — personal data that `make privacy-check` rightly blocks.** So committed CI
fixtures use **synthetic UDIDs only**; any real lab capture is rewritten with a synthetic
UDID before committing or stays Operator-local. **Every bug found on the lab box becomes a
replay fixture before it is fixed** (program hard rule) — including the audition's failure
signature if it reproduces (synthetic UDID).

## Rung-ruled decisions (qn.2 — settled during the build; *rung-ruled* canon)

Settled while building stories 1–5; a later rung changes them only via the gap protocol.

- **plist library** = `howett.net/plist v1.0.1` (looked up live at pin time — the current
  stable and the standard Go transcoder go-ios uses; low release cadence is expected for a
  frozen format, same rationale as the decryption lib). In `core/go.sum`.
- **Package split** = `internal/muxd` (wire protocol client) + `internal/device` (registry).
- **ConnectionType → transport**: `USB`→`usb`, everything else (`Network`)→`wifi`. A
  `Detached` removing a device's **last** transport drops it from the table (identical to the
  qn.1 demo, so the UI contract is unchanged); per-transport, per-source presence means one
  source dropping never clears a transport another source holds.
- **Reconnect backoff** = 500 ms → ×2 → 30 ms cap (`muxd/client.go`); no jitter (single
  long-lived socket, not a thundering herd).
- **Reconnect reconcile** = **reset-on-(re)connect**: `muxd.Client.Run(ctx, Sink)` (Sink =
  `{Reset(); Apply(Event)}`); on each successful dial the client calls `Reset()` (registry
  drops that source's edges → `device.detached` where a transport leaves the table) then the
  replay `Apply`s re-add what's still attached, so a device that detached while disconnected
  is cleared (no phantom). Trade-off: a present device's transport briefly re-asserts on
  reconnect (honest — we lost visibility). **The no-flicker variant — buffer the replay burst
  into an atomic per-source snapshot + diff (idle-debounce in the client, tested via
  `testing/synctest`) — is the documented refinement** if reconnect churn ever bites.
- **Default muxer topology** = usbmuxd (USB) + netmuxd (Wi-Fi); the client loop skips empty
  sockets, so the single-muxer flip after story 7 is config-only.
- **Identity muxd-minimal**; qn.3 enriches via lockdown (handoff in the Design section).
- **Tests**: stories 1/2/5 at the muxd protocol layer (`muxd_test.go`, in-memory fake muxer);
  stories 1–4 reconcile/merge at the registry (`device/registry_test.go`, pure `Sink`
  calls + a real bus). Story 4's REST/WS serving is covered by composition — the registry
  publishes to the same bus `ws_test.go` fans out and satisfies the `DeviceReader`
  `httpapi` already golden-tests — so no separate httpapi integration test was added.
