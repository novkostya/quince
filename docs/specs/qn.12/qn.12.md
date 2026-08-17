# qn.12 — PWA + Web Push: the notification half of the assisted model

**Goal.** A person whose phone is stale and reachable gets **one** notification on that phone, taps
it, and lands on the device page ready to unlock and confirm — and a person who cannot receive
notifications is told which of five reasons applies to them and what to do about it.

Rung issue: **quince#1124**, whose scope is the Operator's, settled 2026-08-17. The sequencing answer
this spec is written under is the architect's, on quince#1124 at `13:11:15Z`: **the VAPID gap blocks
the BUILD, not the SPEC.** So the keypair's home is a `PROPOSED (gap)` block in design §6 and every
story below is written to be independent of how it is ruled.

**quince#510 is answered here** (D7). It has been open since 2026-08-02 waiting for this rung, and
its own body says it is a constraint that *"should not be rediscovered"* — so it gets a defined
behaviour rather than a note.

---

## Boundary

**In scope.**

| tree | what changes |
| --- | --- |
| `ui/index.html`, `ui/public/manifest.webmanifest` | the install surface — the manifest **already exists** (interface fact 4); what it gains is what push needs |
| `ui/public/sw.js` (new) | the service worker, **push and notificationclick only** |
| `ui/src/features/notifications/` (new) | subscribe/unsubscribe, the five-cause status surface, the per-category toggles |
| `ui/src/pages/` | the Add-to-Home-Screen onboarding page; the Settings → Notifications section |
| `core/internal/push/` (new) | VAPID (RFC 8292), payload encryption (RFC 8291), the delivery client and its `410`/`404` handling |
| `core/internal/notify/` (new) | the notifier: what fires, when, and which kind |
| `core/internal/httpapi/` | `/api/notifications/*` — **all behind `authGuard`**, none exempted |
| `core/internal/store/` | the `push_subscriptions` table and the per-device reminder ledger |
| `core/internal/config/` | `automation:` → `notifications:`, the per-category keys, and the **renamed-section** warning shape (interface fact 5) |
| `docs/` | contracts §1/§6 and the frozen push-kind list; design §6 (the gap block) and §4 (the terminal-state → kind routing) |

**Out of scope**, each because quince#1124 decides it out rather than because it was missed:

- **The iOS Shortcut, `POST /api/automation/backup-opportunity`, and its short-lived token.**
  Deferred to a follow-on rung. **The frozen path in contracts §1 is not touched and not renamed** —
  `/api/device/checkin` was floated and explicitly deferred with it.
- **Scoped per-device access and QR enrollment** — its own rung, **pre-`v0.1`**; it reopens the
  `qn.1` security baseline (design §6). Operator ruling 2026-08-17, recorded on quince#1124 — it was
  banked as *"Later, not soon"* and that is overturned, so no roadmap section is named here.
- **The per-device × category notification matrix.** It cannot be specified before that rung decides
  what a principal is. Today's flat per-category keys are the **defaults** it will layer exceptions
  over, so nothing here renames or migrates when it lands.
- **Offline caching.** The service worker handles `push` and `notificationclick` and registers no
  `fetch` handler at all — see D2 for why that is a boundary and not a phase-1 simplification.
- **The `qn.12` "Wake up" spike** (roadmap, post-`qn.12`).

---

## Interface facts — measured 2026-08-17 in this checkout at `a784727`, re-checked at `ee34873`, not recalled

Canon binds interface facts to a live lookup, and quince#1124 item 5 demands it by name for the
Add-to-Home-Screen precondition. Each fact below says how it was established.

**1. Web Push on iOS requires the web app to be added to the Home Screen, and that is still true.**
WebKit, *Web Push for Web Apps on iOS and iPadOS*: *"A web app that has been added to the Home Screen
can request permission to receive push notifications … as long as that request is in response to
direct user interaction — such as tapping on a 'subscribe' button provided by the web app."* Shipped
in iOS/iPadOS 16.4. **Two consequences the UI must carry:** the install step is a precondition, not a
nicety, and **the permission prompt must hang off a real tap** — a `useEffect` that subscribes on
mount is refused by the platform.

**2. Declarative Web Push exists, needs no service worker, and does NOT relax fact 1.** WebKit's
Safari 18.4 features page: *"Declarative Web Push is now available on iOS and iPadOS 18.4 for web apps
added to the Home Screen."* The payload is a JSON envelope — `{"web_push": 8030, "notification":
{"title", "body", "navigate", "app_badge", …}}` — and *"If the event handler displays a replacement
notification properly, the proposed notification is ignored. If the event handler fails to display a
replacement notification in time, the fallback is used."* **This is new information relative to
quince#1124**, whose scope item 2 assumes a service worker is required. It is required **only** for
iOS 16.4 – 18.3. D2 settles what to do about that.

**3. RFC 8291 and RFC 8292 are reachable with the Go standard library alone.** Probed in the pinned
toolchain image — `golang:1.26.5-alpine3.24`, the `GO_IMAGE` in `deploy/Dockerfile:7` — by compiling
and running a program importing `crypto/hkdf` and `crypto/ecdh`: `hkdf.Key(sha256.New, …, 16)`
returns 16 bytes and `ecdh.P256()` yields a 65-byte uncompressed point, which are exactly the CEK
width and `as_public` encoding RFC 8291 asks for. **`github.com/SherClockHolmes/webpush-go` is at
`v1.4.0`, released 2025-01-02** (queried from `proxy.golang.org`, not remembered); there is no `/v2`.
So a dependency is an **option**, not a necessity — D3 decides.

**4. A manifest already ships, from `qn.6a`.** `ui/public/manifest.webmanifest` declares
`display: standalone`, `scope: "/"`, `start_url: "/"` and 192/512 maskable icons; `ui/index.html`
already carries `apple-mobile-web-app-capable`, the status-bar style, the title and the
`apple-touch-icon`, and its comments say in as many words *"Full PWA (manifest, icons, push) is
qn.12."* **`core/internal/webui/embed.go` already registers the `.webmanifest` MIME type** so it is
served as `application/manifest+json` rather than sniffed. Scope item 1 of quince#1124 is therefore
**partly built**, and this rung's manifest work is small.

**5. `automation:` → `notifications:` is a renamed *SECTION*, and the rename table cannot express one
yet.** `core/internal/config/renames.go` carries the mechanism quince#401 built for renamed keys and
says explicitly what it cannot do: *"`backup.transport` → `preferred_transport` … The old key sat
under a `backup:` section that no longer exists, so what `unknownKeys` reports is the unknown PARENT
— `backup` — and a row keyed on the leaf would never match. Whoever adds it has to decide what a
renamed SECTION should say, which is a different shape from a renamed leaf and wants its own
thinking."* **This rung is the one that has to do that thinking** — quince#1124 calls the rename
*"mechanical"*, and the leaf half is; the warning half is not.

**6. The security headers wrap the UI handler, and `script-src` is `'self'` with no `'unsafe-inline'`.**
`core/internal/httpapi/server.go:346-348` chains `securityHeaders` over `webui.Handler()`, and
`middleware.go` sets `script-src 'self'`. `worker-src` is unset, so it falls back through `child-src`
to `script-src` — a **same-origin** service worker is therefore already permitted and this rung needs
no CSP change for registration. **A finding fell out of reading this and is NOT this rung's:
`ui/index.html` carries an inline `<script>` (the pre-paint theme fix, quince#1074/#1081) that this
CSP forbids.** It is filed separately rather than fixed here.

**7. The SPA fallback would serve `index.html` for a missing `/sw.js`.** `webui.handlerFor` falls
through to `serveIndex` for any path that does not `fs.Stat`, with `Content-Type: text/html`. A
service worker served as HTML fails registration with a MIME error that names neither cause. So
**"the worker is present in the embedded `dist`" is an assertion the rung must make**, not something
to assume from a Vite config — G4.

**8. The job engine has ten error codes, and they split cleanly.** `core/internal/backup/backup.go:62-73`:
`device_disconnected`, `device_not_visible`, `not_paired`, `encryption_required`, `disk_low`,
`verify_failed`, `commit_failed`, `backup_failed`, `interrupted`, `cancelled`. D4's routing table is
total over this list, which is what makes `backup_failed`-the-kind a real closure rather than a
second name for the same thing.

**9. `authExempt` is FIFTEEN routes, not the five the tests' prose says.** Counted over the whole of
`authExempt` at `core/internal/httpapi/middleware.go:73-129` — a range that stops short of the
closing brace undercounts, which is how this fact was wrong at first reading. The fifteenth is
`POST /api/config/insecure-transport` at `:127`, and it is the least droppable of them: it is the
**only pre-auth mutation in the list that is not about obtaining a credential** (Operator ruling
2026-08-14, quince#908 slice 6), and its own comment says so. Recorded because the rung's rule check
asserts it adds none, and an assertion against a stale number proves nothing.

---

## Design

Canon this rung builds on, linked rather than restated: the ASSISTED model (stack **D13**), the
device model and derived `last_backup` (design §3), the job state machine and its terminals
(design §4), the security baseline (design §6), config tidiness (stack **D12**).

### D1 — The install step is a precondition, and the page says so before it asks for anything

Fact 1 makes Add to Home Screen load-bearing on iOS. The onboarding page therefore runs in this
order and no other: **detect → instruct → (installed) offer the subscribe button**. It never renders
an Enable button that the platform will refuse, because a button that does nothing is the *"no silent
caps"* failure in its most literal form.

Detection is `window.matchMedia('(display-mode: standalone)').matches || navigator.standalone === true`.
The instruction is the literal iOS gesture — **Share → Add to Home Screen** — with the Share glyph
named, because *"install"* is not a word that appears anywhere in iOS.

**Non-iOS clients are not made to pretend.** A desktop browser that supports Push subscribes without
any install step; the page shows the install instruction only where it is required.

### D2 — A service worker, AND the declarative envelope. One payload, both mechanisms

**Decided: the server sends the Declarative Web Push envelope, and a service worker is registered and
handles `push`.**

The reason is fact 2, in both directions. **Declarative alone strands every device on iOS 16.4 – 18.3**,
which is why the worker is not optional. And **the envelope is what makes a worker that fails to run
non-fatal**: WebKit's documented behaviour is *"If the event handler fails to display a replacement
notification in time, the fallback is used"* — so on iOS 18.4+ the notification still appears if the
worker does not start, whatever the reason. Sending it costs **one JSON field** (`"web_push": 8030`),
and the worker reads the same `notification` object it would otherwise have constructed, so there is
no second payload to keep in sync.

**The alternative — service worker only — is rejected on that fallback alone**, and is recorded so a
later reader does not re-derive it as a simplification. Note what is *not* claimed: nothing here
measures how often a worker fails to start on iOS, only that when it does, one field is the difference
between a notification and silence.

The worker registers **no `fetch` handler**. Caching the SPA buys little on a LAN and introduces
stale-UI-after-upgrade, which `webui.handlerFor`'s existing cache policy was written specifically to
avoid (`index.html` is `no-cache`, hashed assets are immutable). A worker that caches would defeat it.

### D3 — Encryption and signing: standard library, no new dependency

**Decided: `core/internal/push` implements RFC 8291 (`aes128gcm`) and RFC 8292 (VAPID, ES256) on
`crypto/ecdh`, `crypto/hkdf`, `crypto/ecdsa` and `crypto/aes` — no module is added.**

Fact 3 is the evidence it is reachable. Three reasons it is also right: `webpush-go`'s last release is
19 months old and this is a protocol quince must be able to fix on its own timetable; the whole of it
is one file of well-specified transforms with published RFC test vectors (G2); and *"no new Python in
the runtime image"* generalises — a self-hosted daemon aimed at low-end NAS hardware pays for every
dependency it takes.

**The honest cost:** cryptographic code we own. It is mitigated by G2, which tests against the RFC's
own vectors rather than against our own output.

### D4 — Five kinds, and the routing is total over the ten error codes

The frozen four at `docs/contracts.md:1040` become five. **`backup_failed` is the addition**, and
quince#1124 states the gap it closes: the frozen set routes every failure to `action_required`, which
tells a user to go unlock their phone when the disk is full.

The line between them is **who can fix it and where they must be standing**:

| kind | fires when | error codes (fact 8) | the tap leads to |
| --- | --- | --- | --- |
| `backup_available` | the reminder track, rank 1 — D5 | — | `/devices/<udid>` |
| `backup_overdue` | the reminder track, rank 2 — D5 | — | `/devices/<udid>` |
| `action_required` | a job ended and **the phone is the fix** | `device_disconnected`, `device_not_visible`, `not_paired`, `encryption_required` | `/devices/<udid>` |
| `backup_failed` | a job ended and **quince is the fix** | `disk_low`, `verify_failed`, `commit_failed`, `backup_failed`, `interrupted` | `/devices/<udid>` |
| `backup_completed` | a job reached `succeeded` | — | `/devices/<udid>` |

`cancelled` sends nothing: the user did it, and they were holding the phone when they did.

**`device_disconnected` → `action_required` is canon's own choice, not this table's.** The M8 gate in
`roadmap.md` reads *"a mid-backup disconnect produces an `action_required` push and a one-tap retry
works"*, and a mid-backup Wi-Fi drop terminates as `connection_lost` / `device_disconnected`.

**`interrupted` → `backup_failed` is this spec's call and the least obvious row.** `interrupted` means
quince itself restarted (`engine.go` reconciles crash-orphaned jobs to `connection_lost`). Nothing on
the phone is wrong, so `action_required` would send the user to the wrong place; the remedy is one tap
in quince, which is what `backup_failed` means here.

**Failure kinds are NOT cooldown-suppressed.** They are events, not reminders, and each one is
downstream of a job the user started — so the rate is bounded by the user's own hand. Only the
reminder track (D5) has a cooldown, because only it fires without anyone asking.

### D5 — One reminder track per device, so `backup_available` and `backup_overdue` can never both fire

quince#1124 item 3 is right that these will describe the same moment. The answer is that they are
**one reminder at two ranks**, not two reminders:

```
eligible(device) := visible on some transport
                    AND no job running for this UDID
                    AND age(last_backup) > notifications.staleness_days
```

`last_backup` is **derived from the newest committed version** (design §3), so this needs no new
state and is correct across a restart and for adopted versions.

At most **one** outstanding reminder exists per UDID. When a device becomes eligible and
`reminder_cooldown_hours` have passed since that device's last reminder, quince sends exactly one push,
whose kind is:

- **`backup_overdue`** if `age(last_backup) > notifications.overdue_days`;
- **`backup_available`** otherwise.

**The cooldown belongs to the track, not to the kind.** Escalating from rank 1 to rank 2 does not reset
it and does not send a second push — which is the double-notification quince#1124 predicts, prevented
structurally rather than by tuning.

**A device that has never been backed up gets `backup_available`, never `backup_overdue`.** Its age is
unbounded, so the naive rule would greet a phone paired ninety seconds ago with *"overdue"*. The first
backup is an invitation; only a lapsed one is a reproach.

**`overdue_days` is a declared key, defaulting to 14, rather than a multiple of `staleness_days`.**
*Overdue* is a claim about the user's own tolerance, and a derived threshold is a policy nobody can
see or change. The alternative (`staleness_days × 3`, no key) is recorded as rejected for that reason.

**What evaluates it:** presence events (muxd-driven, never polled — design §3), job terminal events,
and a **fixed one-hour tick**. The tick is not configurable and that is deliberate: the thresholds are
in days, so an hour is already an order of magnitude finer than the finest question it answers, and a
knob here would be a setting whose only correct value is the default (D12).

### D6 — "Notifications are off" has five causes and quince names the one that applies

quince#1124 item 4 names this as the quince#940 defect in waiting — one true, useless sentence over
distinguishable states with different fixes. The status surface therefore reports exactly one of:

| state | how quince knows | what the screen says to do |
| --- | --- | --- |
| `unsupported_not_installed` | `PushManager` absent, `serviceWorker` present, not standalone | **Share → Add to Home Screen**, then come back |
| `unsupported_platform` | `PushManager` and `serviceWorker` both absent | this browser cannot receive push — D7 covers the Lockdown Mode case |
| `permission_not_granted` | `Notification.permission === "default"` | tap Enable (and the tap is what the platform requires — D1) |
| `permission_denied` | `Notification.permission === "denied"` | **iOS Settings → Notifications → quince.** quince cannot re-prompt, and says so |
| `category_off` | the kind's key in `notifications:` is `false` | turn it on here |
| `subscription_expired` | the endpoint returned `404`/`410` — D8 | re-enable on **this** device; the row names which |

Six rows for five causes, because `unsupported` splits into two with different remedies — and the
split is the whole value of the surface: *"not installed"* is one gesture away from working and
*"platform"* is not.

**These are computed client-side and reported honestly.** The server never guesses at a browser's
capabilities; it knows only whether a live subscription exists.

### D7 — Lockdown Mode: named, not silently broken (quince#510)

`r11` measured that WebKit disables `ServiceWorkersEnabled`, `ServiceWorkerInstallEventEnabled`,
`ServiceWorkerNavigationPreloadEnabled`, `PushAPIEnabled` and `NotificationsEnabled` declaratively via
`disableInLockdownMode: true`, **regardless of certificate**. So this rung's headline feature is
unavailable to a class of user by design.

**The defined behaviour, in three parts.**

1. **It is detectable, distinctly.** Service Workers shipped in Safari 11.1 / **iOS 11.3** (WebKit,
   *New WebKit Features in Safari 11.1* — looked up, not recalled), so on any iOS a person is
   plausibly running, `navigator.serviceWorker` being **absent** is the Lockdown Mode signature —
   `unsupported_platform` in D6, with copy that names Lockdown Mode as the *likely* cause rather than
   asserting it. **This heuristic is UNVERIFIED on hardware and is owed** (G7).
2. **The non-push path is the in-app one, and this rung has to finish it.** `DeviceCard` already shows
   each device's last-backup time (measured at `ui/src/features/devices/DeviceCard.tsx:47`) — a
   *fact*, not a judgement. **What this rung adds is the judgement**: a device past `staleness_days`
   carries a persistent overdue affordance on the Devices list, in slice 7. For everyone else that is
   redundancy; for a Lockdown Mode user it is the whole loop, which is why it is a story rather than a
   nicety.
3. **quince states the limit rather than implying a workaround.** The copy is *"Lockdown Mode turns off
   web notifications for every website. quince cannot work around it — open quince to see what needs
   backing up."*

**`r11`'s open unknown is NOT resolved here and must not be guessed at.** iOS has
**Settings → Privacy & Security → Lockdown Mode → Configure Web Browsing → Excluded Safari Websites**,
and whether excluding a site restores service workers is unmeasured. There is a **second** unknown
stacked on it that quince#510 does not name: that list is **Safari's**, and a Home Screen web app is
not Safari — so it may not apply to the surface this rung uses at all. Both are one device-test away
and both are G7.

**One negative worth keeping, because the natural inference is wrong:** `WebSocket` does **not** carry
`disableInLockdownMode`. quince is *"REST + one WebSocket"* and the entire live surface rides it, so
**Lockdown Mode does not touch quince's live UI** (`r11`, quince#510).

### D8 — Subscriptions: a capability, stored like one, and expired loudly

A subscription is `{endpoint, keys.p256dh, keys.auth}`. **Anyone holding it can push to that phone**,
so it is capability-grade: it lives in the app DB beside the argon2id hash, it is never logged, never
served to another session, and never enters a fixture.

**`410 Gone` / `404` pruning is surfaced, per quince#1124 scope item 3.** On either status the row is
marked `expired` with a timestamp; **it is not deleted**, because deleting it is precisely what makes
a phone that quietly stopped receiving invisible. It is cleared when the same device re-subscribes or
the user removes it — **there is no time-based pruning and therefore no cap to surface.**

**And it is surfaced where it will be seen, not only in Settings.** If a device has **no** live
subscription at all, the Devices page carries a banner — a subscription list buried in Settings is
where a dead phone goes unnoticed, and the first symptom would otherwise be a missed backup.

Every other delivery status is a transport error and is retried on the next occasion the notifier
evaluates; it never marks a subscription expired, because *"the NAS was offline"* and *"the phone is
gone"* are different facts.

### D9 — `automation:` becomes `notifications:`, and the rename has two halves

```yaml
notifications:
  staleness_days: 3            # unchanged, and now READ
  reminder_cooldown_hours: 24  # unchanged, and now READ
  overdue_days: 14             # new — D5
  backup_available: true
  backup_overdue: true
  action_required: true
  backup_failed: true
  backup_completed: false      # a push per successful nightly backup trains people to swipe
```

The defaults are quince#1124's. All are live (no restart) and editable in the UI — D12 — and the
`automation.*` row in contracts §6's live-key table stops being *"nothing reads it (declared)"*.

**The mechanical half** is the struct, the tags, the four contracts line references and the UI type.

**The half that is not mechanical is the warning**, per interface fact 5. `unknownKeys` reports the
unknown **parent** for a section that no longer exists, so `renamedKeys` — keyed on leaves — matches
nothing, and an operator with a hand-written `automation:` block gets *"unknown config key
`automation` (ignored)"*: no mention that a key by that name used to exist, no successor, no echo of
what they lost. That is the exact defect quince#401 was built to fix, arriving through the one shape
it left unhandled.

**Decided: `renamedKeys` gains a section form** that names the successor **section** and echoes the
child keys the file carried, and the `automation` → `notifications` row is its first occupant —
discharging the debt `renames.go` records against `backup.transport` at the same time it is incurred
here. **No auto-migration**, following that file's own ruling: *"Silently rewriting a config to mean
what quince guesses is a bigger promise than this is worth."*

### D10 — The API surface

All behind `authGuard` and the CSRF guard; **nothing is added to `authExempt`** (fact 9).

```
GET    /api/notifications              → {vapid_public_key, categories{...}, subscriptions:[{id,label,state,created_at,expired_at?}]}
POST   /api/notifications/subscriptions {endpoint, keys:{p256dh,auth}, label} → 201 {id}
DELETE /api/notifications/subscriptions/{id}                                  → 204
POST   /api/notifications/test          → 202; sends one push to every live subscription
```

`vapid_public_key` is public by construction — it is the `applicationServerKey` every subscription
must carry. `POST …/test` exists because *"is this working?"* is otherwise unanswerable without
waiting three days for a device to go stale, and it is what the rung's click-list uses.

Category toggles are **not** a fifth endpoint: they are config, written through the existing
`PUT /api/config`, which is what makes them hand-editable and restart-free (D12).

---

## PROPOSED (gap): where the VAPID keypair lives

**Written into `docs/quince.design.md` §6 in this PR, and nothing below is built until it is ruled.**
Recorded here only so this document is readable on its own; §6 is the canonical copy.

The short of it: a VAPID keypair is a **persistent** secret — the public half is baked into every
subscription, so regenerating it invalidates every subscribed phone silently — and **D12 forbids
secrets in `config.yml`, ever.** That makes its home a security-model question rather than a
rung-local one. Three candidate homes (app DB · a `0600` file under `/data` beside the pairing
records · operator-supplied) with their trade-offs are set out in §6.

**Per the architect's ruling on quince#1124, this blocks slice 3 and nothing else.** No story in this
spec has acceptance criteria that only make sense under one answer, which is the condition that ruling
attached.

---

## Stories

1. **Install.** On an iPhone, quince can be added to the Home Screen and opens standalone; the
   onboarding page detects whether it already has been and shows the Share → Add to Home Screen
   instruction only when it has not. (D1, fact 4)
2. **The renamed section.** `automation:` in a hand-written config produces a warning naming
   `notifications:` and echoing the child keys that are not in force; `notifications:` parses; both
   old keys are now read. (D9, fact 5)
3. **Subscribe.** From an installed web app, tapping Enable prompts for permission and registers a
   subscription that appears in the list with a label. (D1, D8, D10)
4. **A push arrives and lands somewhere useful.** `POST /api/notifications/test` delivers a
   notification to the phone; tapping it opens `/devices/<udid>`. (D2, D3, D10)
5. **The reminder track sends one push, not two.** A device stale past `staleness_days` and visible
   produces exactly one `backup_available`; the same device crossing `overdue_days` produces
   `backup_overdue` at the next cooldown boundary and **never** a second `backup_available`; a fresh
   backup produces nothing. (D5)
6. **A never-backed-up device is invited, not reproached.** It produces `backup_available`. (D5)
7. **Failures route by who can fix them.** Each of the nine non-`cancelled` error codes produces the
   kind D4's table names; `cancelled` produces nothing. (D4, fact 8)
8. **The five causes are distinguishable.** Each row of D6's table renders its own state and its own
   remedy; no two collapse into one sentence. (D6)
9. **Lockdown Mode is a defined state, not a dead end.** A client with no `serviceWorker` gets
   `unsupported_platform` with the Lockdown Mode copy, and the Devices-page overdue affordance is
   present for that user. (D7, quince#510)
10. **An expired subscription is loud.** A `410` marks the row expired, keeps it, and a device with no
    live subscription is called out on the Devices page. (D8)
11. **Categories are honoured and editable.** Turning a kind off suppresses it and nothing else;
    `backup_completed` is off by default. (D9)

---

## Gates

Beyond `make gates` / `make image`:

- **G1 — the routing table is total.** A Go test enumerating the error-code constants in
  `core/internal/backup/backup.go` and asserting every one maps to exactly one kind or to none, so a
  future eleventh code fails the build instead of silently sending nothing. (fact 8, D4)
- **G2 — RFC vectors, not our own output.** `core/internal/push` is tested against the published test
  vectors of RFC 8291 §5 and RFC 8292 §2/§3. A crypto implementation tested against itself proves
  nothing, and D3 takes on this code on the strength of this gate.
- **G3 — one reminder, driven by a clock we control.** The notifier's evaluation is tested over an
  injected clock: eligible → one push; cooldown not elapsed → none; rank escalation → one push of the
  new rank and no repeat of the old.
- **G4 — the worker is really there.** An assertion that the built `ui/dist` contains `sw.js` and that
  `webui.handlerFor` serves it with a JavaScript content type — because interface fact 7 says the
  failure mode is a silent fall-through to `index.html`.
- **G5 — the install precondition is not bypassed.** A UI test that the Enable control is absent when
  detection says not-installed, and that subscription is only ever initiated from a click handler.
- **G6 — no capability in a log.** A test that a subscription endpoint never reaches the logger, in
  the shape design §6 already uses for the backup password.
- **G7 — OWED TO HARDWARE, with no owner yet.** Three claims this spec makes that only a device can
  settle: (a) the Lockdown Mode detection heuristic in D7.1; (b) whether **Excluded Safari Websites**
  restores service workers at all; (c) whether that list reaches a Home Screen web app, which is a
  separate question from (b). **Declared unrun, per state honesty.** They gate no story — every one of
  D7's three parts is correct whichever way they fall — but the copy in D7.1 must be re-read against
  the answer.

---

## Fixtures

- **RFC 8291 §5 / RFC 8292 §2–§3 vectors**, transcribed from the RFCs into
  `core/internal/push/testdata/`. Their provenance is a public standard, so nothing here comes from a
  device.
- **No captured push endpoints, ever.** A real endpoint is a live capability against a real phone
  (D8). Fixtures use a synthetic endpoint and a locally generated keypair.
- **No new backup transcripts.** This rung touches no `idevicebackup2` path.

---

## Rule check

Every hard rule this rung touches *or comes near*, written before building.

| rule | how this complies |
| --- | --- |
| **Privacy is a commit-time gate** | No hostname, LAN address, UDID or serial enters this spec or any fixture; the deep link is written `/devices/<udid>`. Push endpoints are capability-grade and never committed, never logged (G6). `make privacy-check REF=origin/main...HEAD TEXT=<path>` before push. |
| **State honesty** | G7 is declared **unrun** with its three claims named, rather than folded into the prose. The `unsupported_platform` copy says Lockdown Mode is the *likely* cause because detection cannot prove it. A `permission_denied` state says quince cannot re-prompt instead of offering a button that would do nothing. |
| **Interface facts looked up live** | All nine facts above carry how and when they were established — WebKit's own pages for 1 and 2, the pinned toolchain for 3, this checkout for 4–9. No version is pinned by this rung: D3 adds no module. |
| **Never mutate a committed version** | **Comes near and does not touch.** The notifier reads `last_backup`, which design §3 derives from the newest committed version. It is a **reader**; nothing in this rung opens a storage backend for write, and no story changes when quince#591's zfs in-place ruling is built. |
| **No silent caps or fallbacks** | The whole of D6 and D8. An expired subscription is kept and surfaced rather than pruned; a device with no live subscription is called out on Devices, not only in Settings; there is **no** time-based retention and therefore no cap needing disclosure. The declarative-envelope fallback (D2) is a *platform* fallback that produces the same notification, not a degraded mode. |
| **Config tidiness (D12)** | Every new key has a default, is UI-editable, and needs no restart. **One key is added** (`overdue_days`) with its reasoning in D5, and one is deliberately **not** added (the evaluation tick). No secret enters `config.yml` — which is what makes the VAPID question a gap rather than a schema line. `config.yml` still contains only what the user set: D9 explicitly does not auto-migrate. |
| **Secrets discipline** | The VAPID private key is unruled and unbuilt. Subscription keys reach the app DB and nothing else — never argv, never env, never a log line (G6). The backup password is untouched by this rung. |
| **Subprocesses** | None. This rung spawns no process. |
| **Every hardware bug becomes a replay fixture** | No hardware path is touched; G7 names what hardware still owes and to what. |
| **Docs are part of the diff** | Contracts §1 — the frozen kind list at **`:1949-1950`** and the new endpoints; contracts §6 — the `automation.*` live-key row at **`:2843`** and the config block at **`:3073`**; design §4 (terminal → kind) and design §6 (the gap block, in **this** PR). All move with the code that changes them. Coverage and a known-untested list ride each build slice. **quince#1124's own line references — `:1872`, `:2707`, `:2727`, `:2927` — do not resolve in this checkout;** the three above were grepped in **this clone**, at `a784727` and re-checked at `ee34873` after a rebase, and are what the build slices should follow. **The frozen kind list sits directly under `POST /api/automation/backup-opportunity` at `:1941`, which this rung deliberately does not touch** — named because the next reader editing the list is standing next to the endpoint whose deferral is a decision. |
| **Don't improvise architecture** | VAPID storage is a `PROPOSED (gap)` block and stops that thread. Everything else settled here — D2, D3, D4's `interrupted` row, D5's track, D9's section-rename shape — is inside this rung's boundary and is decided *in the spec*, reviewed before code, which is what §8 asks for. The frozen `/api/automation/backup-opportunity` path is not touched, and its naming question stays deferred. |
| **Approver ≠ author** | This spec is authored by an implementer session and reviewed by the architect, who has already claimed the review on quince#1124. |

---

## Slices

Each is one PR carrying one reviewable claim, **sequenced from `main`, not stacked**.

| | claim | gated on the ruling? |
| --- | --- | --- |
| **1** | **this spec** + the `PROPOSED (gap)` block in design §6 | no |
| **2** | `automation:` → `notifications:`, the per-category keys, and the renamed-**section** warning (D9, story 2) | no |
| **3** | `core/internal/push` — VAPID + RFC 8291, against the RFC vectors (D3, G2) | **yes** |
| **4** | the subscription store and `/api/notifications/*` (D8, D10) | **yes** |
| **5** | the service worker, the manifest delta, and the install onboarding page (D1, D2, G4, G5) | no |
| **6** | the notifier — the reminder track and the routing table (D4, D5, G1, G3) | no |
| **7** | the Settings surface, the five-cause status, and the Devices-page surfaces — the overdue affordance (D7.2) and the no-live-subscription banner (D8) | no |

Slices 2 and 5 can run while the ruling is outstanding, which is the point of the architect's
sequencing answer.
