# qn.13 — a device-scoped passkey: its holder sees one device and nothing else

**Status: SPEC. Nothing here is built.** Scoped by the Operator on 2026-08-20 (quince#1342), items
2, 3 and part of 5 of the 2026-08-17 phone-first vision. Prerequisites are done: passkeys at `qn.6k`
(quince#657), Web Push at `qn.12` (quince#1124).

---

## Goal

The admin issues a QR code from a device's page. Whoever scans it registers a passkey on their own
phone, signs in with that passkey alone, and lands on **that one device as their Home** — able to
back it up, watch it, read its history, and receive its notifications, and able to reach nothing
else in quince.

---

## Boundary

**In scope.** A principal carried past authentication; a scope stored on the credential; an
enrolment ceremony reached by QR; authorization applied at every route, at the push send path, and
in the shell's shape; the admin's view of what they have issued, and revocation of one credential.

**Out of scope, named so the absence is a decision rather than an oversight.**

- **Multiple devices per scoped holder.** One credential grants exactly one device. A household
  member with an iPhone and an iPad gets two credentials. Merging them is a later rung's question.
- **Restore.** It is not built (`qn.8`+), and the roadmap's 2026-08-17 note already calls it a
  dangerous scope. D3 pre-answers it rather than leaving it to be discovered.
- **The per-(device x category) notification matrix**, deferred by `qn.12`'s D5 and still deferred.
- **The public demo** (quince#444). It presets a shared password and has no passkey story; whether a
  scoped principal is reachable there is not considered here.
- **Any recovery ceremony for a lost scoped phone.** D9 explains why one would be wrong.

---

## Interface facts — measured 2026-08-20 in this checkout at `acdcfe7`, not recalled

1. **There is no principal to discard, because the session does not carry one.**
   `store.AuthSession` is `{ID, CreatedAt, LastSeenAt, ExpiresAt}` (`core/internal/store/sessions.go:10`)
   and `sessions_auth` (`0001_init.sql:10`) matches it. `httpapi/middleware.go`'s `authGuard` writes
   `if _, err := d.Auth.Authenticate(...)`, which reads as a principal being thrown away — the value
   discarded there has no identity in it to begin with. **This is one step deeper than quince#1342's
   ruling comment states**, and it is what makes D1 a migration rather than a one-line fix.
2. **`user_handle` is a single constant shared by every row**, in `settings` under
   `passkey_user_handle` (`auth/passkey.go:29`, and `0008_passkeys.sql`'s header states the reason:
   *"ONE ADMIN, NO ACCOUNTS, which is what permits DISCOVERABLE credentials"*).
3. **`passkeys` is keyed on `credential_id`** because *"an assertion arrives carrying exactly this
   and nothing else to look up by"* (`0008_passkeys.sql:16-18`). That is the hook scope hangs from.
4. **Discoverable credentials are required and `BeginDiscoverableLogin` sends an empty allow-list**
   (`auth/passkey.go:212-222`). The server never names a credential at login; the platform chooses.
5. **Three credential predicates are scope-blind**, and all three fail *permissively* the moment a
   row of a new kind exists: `auth/accepts.go:87` (`if len(creds) > 0`), `auth/reauth.go:195-210`
   (`ListPasskeys`, then `ErrLastCredential`), and `auth/passkey.go:332-361`
   (`existingCredentials`, filtered by rpID and nothing else).
6. **The per-device notifications switch is `device_notification_prefs`** (`0013_...sql`), keyed on
   `udid`, with an `enabled` boolean. quince#1342 §5 reported finding no such column because it
   searched for `notifications_enabled`; **that item is closed.** The migration's own comment
   instructs this rung: *"When single-device-scoped passkeys land, a second principal exists and the
   row gains an owner column... DO NOT ADD THAT COLUMN NOW."*
7. **`push_subscriptions` has no principal column and no device column** (`0011_...sql`), and its
   index comment states the read: *"Reads are 'every live subscription', which is what a send does."*
8. **`POST /api/auth/setup/passkey/begin|finish` is a pre-auth ceremony that issues a session**
   (`contracts.md` §1). It is the structural sibling of D4's enrolment ceremony, and D4 is specified
   against it rather than invented.

---

## Design

### D1 — The rung's first act is to give quince a principal at all

Not *add a scope field* — **start carrying an identity past authentication**. Per fact 1 the session
row records only its own lifetime, so before any route can consult a caller's rights there must be a
caller to consult.

**The session records the credential that created it.** `sessions_auth` gains the authenticating
`credential_id` (nullable: a password login has none, and that is the admin by construction).
`Authenticate` returns a principal resolved from it; `authGuard` binds that principal into the
request context; every handler reads it from there.

**Why on the session and not recomputed per request:** a passkey can be removed while its session
lives. Recomputing would make a revoked credential's session fail *open* on the join miss, or fail
in a way each call site handles differently. Stored, the join miss is a single fact with a single
answer, which is D9's business.

**Nullable means admin, and that is a decision with a hazard.** It makes the migration additive and
every existing session keeps working. The hazard is fact 5's shape in a new place: a NULL that means
*admin* is a default that grants. It is accepted here — and only here — because the alternative is
invalidating every live session on upgrade, and because this column is written at exactly one site
(session creation) rather than being a set that can gain members. **G2 asserts it.**

### D2 — Scope lives on the credential, because the credential is the only thing that exists

`passkeys` gains a scope: admin, or device-scoped to one `udid`. `user_handle` stays the single
constant of fact 2, so no second account appears and no user table is invented. Scope resolves
**after** assertion, from the `credential_id` the assertion carries (fact 3).

This is the honest model rather than a trick to preserve a UX property: there are no accounts, so a
capability is a property of a credential. The no-account-picker question is real and is **owed a
measurement** — see *Owed before build*, below.

### D3 — What a scoped holder may do: a rule, and the one exception the Operator ruled

**The rule (architect, quince#1342): a scoped holder may CREATE and READ their device's data, and
CONTROL operations on it. They may not DESTROY data, and they may not affect the admin's ability to
restore.**

| | | |
| --- | --- | --- |
| back up now, retry | **yes** | creating and controlling their own device's work |
| cancel | **yes** | control over an operation the admin may have started — permitted because canon's *a failed job keeps its dirty `working/`* makes a cancelled backup resumable. **If that ever stops being true, this row changes with it.** |
| history, versions, browse, download (`qn.8`+) | **yes** | reading their own device's data |
| their own notification preference | **yes** | it is theirs (D7) |
| **the backup encryption password** | **yes — the exception** | see below |
| delete a version | **no** | it sits on the admin's storage under the admin's retention policy; causing something is not owning it |
| restore to the device | **no** | it overwrites a device; out of scope here, and the rule pre-answers it |
| Settings, storages, other devices, pairing, issuing passkeys | **no** | not their device, or not their authority |

**The exception, stated with its reason because the rule alone generates the wrong answer.** The
architect's rule yields **no** for the encryption password: a scoped holder changing it can
invalidate the admin's restore path. **The Operator ruled yes** (2026-08-20): *"backup password can
be changed by the user of course. if quince doesn't let you do it, you can easily do that in
Finder."* The generalisation is the load-bearing part — **a control the platform trivially bypasses
is not a control, it is an inconvenience.** Refusing protects nothing and makes quince worse than the
tool it replaces.

**So the rule has a boundary and it must be written next to the rule, not left as an absent row.**
The clause *may not affect the admin's ability to restore* holds only where quince's refusal is the
thing preventing it. Where the platform grants the capability regardless, quince **surfaces the
consequence instead of pretending to withhold it**: the admin's older backups keep the password they
were made with, which is true today, with or without this rung, and with or without quince.

**RULED — REQUIRED** (quince#1347 review): the admin is notified when a scoped holder changes it — *"the encryption password for
`<device>` was changed"*. That is the state-honesty rule applied to a real consequence, not a
permission check. See *Ruled at spec review*, item 3.

### D4 — Enrolment: a one-shot authorization for a registration, not a token quince stores

**The durable credential is a passkey** (Operator, 2026-08-17, roadmap). The QR opens a page that
*creates* one, so what lands in the phone's secure enclave is a passkey, and the enrolment secret
shrinks to a one-shot authorization for that registration alone.

The enrolment secret is **single-use** (consumed at registration, not at scan — a scan that fails
must not burn it), **short-lived** (minutes), **revocable before use**, and **carries its scope from
generation**. A token whose scope is chosen by the scanner is not a scoped token, and a scoped
enrolment must never mint an admin credential.

**It is a URL, and a URL leaks** — history, screenshots, chat forwards (roadmap, 2026-07-22). The
one-shot-plus-minutes shape is what makes a leaked URL worth little: by the time it is forwarded it
is spent or expired.

**Specified against fact 8's precedent.** `POST /api/auth/setup/passkey/*` is already a pre-auth
ceremony that issues a session; the difference is what authorizes it — there, that the install is
unconfigured; here, the enrolment secret. Same shape, different gate, and the existing one is not
disturbed.

### D5 — The address in the QR binds three things at once, so quince must not guess it

One URL simultaneously fixes the **rpId** (a passkey is bound to a domain; `auth.ErrUnsupportedRPID`
already refuses an IP), the **push origin** (`0012_push_subscription_origin` exists because *"a
notification must open the app at the address THAT phone knows"*), and the **Home Screen web clip's
URL**, frozen at install.

**If the admin generates the QR at one address and the phone reaches quince at another, all three
break, differently, and two of them fail silently.** This is quince#1068's address-identity problem
with three consequences instead of one.

**So the QR encodes the address the admin is currently using, and quince says so on the screen that
generates it** rather than encoding a guess. Where quince cannot tell what address the phone will
use, it names the address it is baking in and what happens if that is wrong — which is the
*troubleshooting is actionable* rule, and follows `ErrUnsupportedRPID`'s existing house pattern of
naming the address it was reached at together with the remedy.

### D6 — Every "is there a credential" predicate must ask "is there an ADMIN credential"

**Operator, 2026-08-20: *"to go passwordless you have to have admin passkey, scoped passkey is not
enough."*** This is a lockout, not a preference: an install with zero admin passkeys and one scoped
passkey could have its admin password removed, after which the admin cannot get in, the scoped
holder cannot administer anything by construction, and **nobody can administer quince**. The only
way back is `quince auth reset`, which destroys every credential.

All three sites in fact 5 count rows at an rpID and would silently start counting the wrong set —
**in the permitting direction.** So the invariant is rung-wide rather than a fix at each site:

> **A predicate that asks *is there a credential* must ask *is there an ADMIN credential*. Counting
> all rows is the unsafe default.**

This is the defect shape this project has hit repeatedly — a predicate that enumerated a set
correctly until the set gained a member of a new kind. **G1 is the gate**, and it is written to catch
the *next* site somebody adds, not only today's three.

**It makes quince#1259 load-bearing.** `ErrLastCredential` is already unreachable on the
`RemovePassword` path (the proof is required before the last-credential check). With scope in play
that refusal is the one thing standing between a scoped-only install and a lockout, so it must be
both **reachable** and **scope-aware** — two separate fixes, and the first is quince#1259's.

### D7 — Notifications filter at SEND, and the admin's mute is the admin's alone

Per fact 7 a send reads every live subscription, so a scoped holder subscribing today receives every
device's notifications.

**The subscription row gains its principal, and the filter goes in the send path.** Not at subscribe
time: scope can change, a credential can be revoked, and a device can be deleted, all after the
subscription exists — and a scope change must take effect without the phone re-subscribing.

**`device_notification_prefs` is the ADMIN's preference — Operator, 2026-08-20, given IN SESSION and
relayed here by the implementer. It is NOT on the forge, and this sentence is the whole of its
provenance.** Stated that way deliberately: an Operator ruling carries weight here, so one cited
without a findable artifact is worse than an unattributed one — the next session reads it as settled
by the seat that cannot be overruled and has nothing to check it against. **The prior reasoning IS
findable and agrees**: quince#1270's body, decided by the architect seat, works the same question to
the same answer — *"if the admin silences a household member's iPhone, does its owner stop hearing
about their own phone?"* has *"an obvious right answer — no, the admin was silencing their own
noise"*, and *"the global reading becomes the admin's-own-preference reading."*

A scoped holder is not affected by it. So the owner column fact 6 anticipates is added **with
existing rows backfilled as admin-owned, not as global**: an admin who has muted device X keeps
their own mute, and the scoped holder of X still receives their device's notifications. Reading
the existing rows as global would silently import the admin's preference into a principal who
never expressed it — and it fails in the direction that makes the feature look broken, since a
scoped holder would be enrolled into silence with nothing on screen saying why.

The two conditions compose by AND within one principal: this principal's device preference, then the
global category switches `qn.12` already ships.

### D8 — IA: the device page becomes Home

The scoped holder's Home **is** their device page. The devices list is **unreachable, not merely
unlinked**; Settings is **hidden, not merely empty**. quince#1316 is the nearest precedent for a
surface whose shape follows the principal.

**Unreachable is a server property, not a routing one.** The shell hides what a principal cannot
use; the API refuses it regardless. A hidden route that answers 200 to a typed URL is not
confinement.

### D9 — Revocation, and the recovery ceremony this rung must NOT build

**No recovery ceremony.** `qn.6k` designed around a lost phone locking the user out of their own
backups. **That argument does not apply here**: the admin re-issues a QR. Stated in as many words so
nobody rebuilds the ceremony out of symmetry.

The admin revokes **one** scoped credential without touching the others, from the device page it was
issued from. **The admin's Sign-in settings must mark each row as admin or device-scoped, and for a
scoped one, which device** (relayed as the Operator's by the analyst seat on quince#1342, 2026-08-20 — that comment's header names the Operator but, unlike its other two items, carries no quote, so read this as relayed rather than transcribed) — without it the admin cannot answer *what have I
issued* or revoke intelligently. This composes with the existing per-row rpId state (`worksHere`)
rather than replacing it: a scoped passkey can also be registered at an address that no longer works,
and those are two independent facts about one row.

**quince#1001 is a prerequisite, not a neighbour.** Removing a credential does not today end other
sessions, and a revoked scoped credential whose session keeps working defeats the entire confinement
claim. **This rung ships with revocation immediate, or it states on screen that it is not.** There is
no third option, and the second is not the one to prefer.

`quince auth reset` clears scoped credentials with everything else — **confirm at build, and make
the screen say so.**

### D10 — The API surface

Contracts §1 gains the enrolment ceremony (D4) and the revocation/listing shape (D9); every existing
route gains an authorization answer rather than a new spelling. **The contract change that matters is
not a new endpoint — it is that "authenticated" stops being the whole of the answer**, which is why
this spec exists before any of it.

---

## RULED at spec review — all four, by the architect (quince#1347)

`/docs/specs/**` is the architect's under `CODEOWNERS`. These were proposed in this spec and ruled in
its review, which is the `qn.8` pattern (quince#270, quince#1343): one round trip instead of blocking
the rung. **The heading moved with the body** — a `PROPOSED` heading over a ruled body is quince#408's
gate-able signature, and this section is exactly the shape that produces it.

1. **Passkey-only for scoped principals, no password ever — CONFIRMED.** The Operator's stated
   inclination was *"not even letting you choose password"*; the ruling's own reason is stronger than
   the inclination — a password for a scoped principal would put a second credential class in the
   same tables D6's invariant must already be scope-aware about, and passwords need reset paths,
   which is admin surface a scoped holder must not reach.
2. **The enrolment secret: single-use, minutes-long TTL, revocable, and unused ones ARE listed** on
   the device page — **CONFIRMED**, with the listing named as the part not to trade away. An
   issued-and-unscanned QR is live authority, and *authority nobody can see is authority nobody
   revokes* is this project's no-silent-anything rule wearing a security hat.
3. **The admin is notified when a scoped holder changes the encryption password (D3) — CONFIRMED AS A
   REQUIREMENT, not merely permitted.** It follows from state honesty rather than from permissions:
   the consequence is real and asymmetric — the admin's older backups keep the password they were
   made with — and a consequence the admin cannot see is *troubleshooting is actionable* failing at
   the only moment it matters.
4. **The `qn.13` number — CONFIRMED**, verified independently by the reviewer: `docs/specs/` has no
   `qn.13` and no doc references one.

---

## OWED BEFORE BUILD — one measurement, and this box cannot take it

**Do two discoverable credentials sharing one `user_handle` on one rpId present as ONE entry or TWO
on iOS?** (quince#1342 §2.) It decides whether the no-account-picker property `qn.6k` chose
discoverable credentials to obtain survives.

**Owner: the Operator. It needs a real iPhone, and no session box has one.** Declared unrun rather
than assumed — a spec that assumes a branch without saying it assumed is the thing quince#1342 §11
names as not defensible.

**What this spec assumes, and why it is narrower than a fork.** D2 is written as though the property
survives. The reasoning: **a picker can only appear on a phone that holds two credentials for this
rpId at once**, and in the household case each phone holds exactly one — the admin's phone has the
admin credential, the scoped holder's phone has theirs. The overlap cases are an admin who enrols a
scoped credential on their **own** phone, and a holder of two scoped devices who signs in to both
from one phone.

**So the measurement most likely decides a named UX consequence in a narrow case, not the shape of
the design** — D2's mechanism (scope on the row, resolved from `credential_id` after assertion) is
unchanged either way, because it never consults `user_handle`.

**This is reasoning, not measurement, and it is flagged as such.** If the measurement comes back
*two entries* and the Operator judges the picker unacceptable even in the narrow case, D2 reopens.
Slice 1 below is the measurement for that reason.

---

## Stories

1. **A principal exists.** A session created by a password login and one created by a passkey
   assertion are distinguishable at every handler; an existing session survives the upgrade. (D1)
2. **A QR issues a scoped credential.** From a device page the admin generates a QR; scanning it on
   another phone registers a passkey; that phone signs in with it alone. (D2, D4)
3. **The secret is one-shot and short-lived.** A second registration with the same secret is refused;
   an expired one is refused; a revoked one is refused before use; a scan that fails to complete does
   not burn it. (D4)
4. **The holder's Home is their device.** After sign-in the scoped holder lands on `/devices/<udid>`;
   the devices list, Settings and every other device are **refused by the API**, not merely absent
   from the shell. (D8)
5. **The capability table holds, in both directions.** Every **yes** row of D3 succeeds for the scoped
   holder; every **no** row is refused. (D3)
6. **The encryption password is changeable, and the consequence is surfaced.** A scoped holder changes
   it; the screen states that older backups keep the password they were made with. (D3)
7. **A scoped-only install cannot reach passwordless.** With zero admin passkeys and one scoped
   passkey, removing the admin password is refused, and the refusal names why. (D6)
8. **Notifications are filtered at send.** The scoped holder receives their device's notifications and
   no others; a scope change takes effect with no re-subscription. (D7)
9. **The admin's mute does not reach the scoped holder.** An admin who has muted device X still sees
   the scoped holder of X receiving X's notifications; the admin's own mute is unchanged by the
   upgrade. (D7)
10. **The admin can see and revoke what they issued.** `/settings/auth` marks each row admin or
    device-scoped and names the device; revoking one leaves the others working; the revoked
    credential's session **ends immediately, or the screen says it does not**. (D9)
11. **A wrong address is diagnosable.** A QR generated at an address the phone cannot reach produces a
    message naming the address baked in and the remedy — not a silent failure of three things. (D5)

---

## Gates

Beyond `make gates` / `make image`:

- **G1 — the scope-blind predicate gate.** A Go test asserting that a scoped-only install cannot
  reach passwordless (story 7), plus an enumeration over the credential-counting sites so a
  **fourth** one added later fails the build rather than silently counting the wrong set. D6's
  invariant is only worth stating if something asserts it for code nobody has written yet.
- **G2 — the NULL default is admin, at exactly one writer.** A test that session creation always
  records the authenticating credential where one exists, so D1's accepted hazard cannot spread from
  one call site to several.
- **G3 — confinement is server-side.** A test that hits every out-of-scope route directly with a
  scoped session and asserts a refusal, rather than asserting the shell hides them (D8).
- **G4 — the enrolment secret's lifecycle.** Single-use, expiry, pre-use revocation, and
  not-burned-on-failure, over an injected clock (story 3).
- **G5 — the send filter, including the admin's mute.** A test that a scoped subscription receives
  only its device's sends and that an admin-owned `device_notification_prefs` row of `0` does not
  suppress the scoped holder's (D7, story 9).
- **G6 — OWED TO HARDWARE, owner named.** The iOS account-picker measurement above. **Owner: the
  Operator.** It gates no story — every story is correct whichever way it falls — but D2's prose and
  the enrolment copy must be re-read against the answer.

---

## Fixtures

- **No new backup transcripts.** This rung touches no `idevicebackup2` path.
- **No captured push endpoints and no real credential ids**, per `qn.12` D8: a real endpoint is a
  live capability against a real phone. Synthetic endpoints and locally generated keypairs only.
- **Credential fixtures are generated, never captured from a device**, following `0009`'s reasoning
  that flags a real authenticator produced are facts about that authenticator.

---

## Rule check

Every hard rule this rung touches *or comes near*, written before building.

| rule | how this complies |
| --- | --- |
| **Privacy is a commit-time gate** | No UDID, serial, hostname or LAN address enters this spec, any fixture, or the QR discussion — D5 is written about *an* address, never one. Credential ids and enrolment secrets are capability-grade and never logged, never committed, never served to another session. `make privacy-check REF=origin/main...HEAD TEXT=<path under $HOME/scratch/r63>` before push. |
| **State honesty** | The iOS measurement is declared **unrun with its owner named**, and the assumption it licenses is marked as reasoning rather than measurement. D9 permits shipping without immediate revocation *only* if the screen says so. D5 refuses to encode a guessed address. |
| **Troubleshooting is actionable** | D5 is this rule applied to the sharpest failure in the rung: three things break differently and two silently, so the message names the address baked in and the remedy, following `ErrUnsupportedRPID`'s house pattern. D6's refusal names why passwordless is unavailable rather than merely refusing. |
| **Interface facts looked up live** | All eight facts carry the checkout (`acdcfe7`) and date they were established, with file and line. Fact 6 corrects quince#1342 §5 by measurement rather than by memory. No version is pinned by this rung; it adds no dependency. |
| **Never mutate a committed version** | **Comes near, does not touch.** D3 grants *back up now*, *retry* and *cancel* to a new principal — the same job engine, entered by the same routes, with no new write path into `latest/`, `versions/` or a snapshot. *Delete a version* is refused (D3), which is the only capability in the list that could reach a committed artifact. Nothing here changes when quince#591's zfs in-place ruling is built. |
| **No silent caps or fallbacks** | D7 filters at send and surfaces the result; D8 refuses server-side rather than hiding; ruling 2 lists unused enrolment secrets rather than leaving live authority invisible; D6's whole subject is a predicate that fails *permissively* and silently. |
| **Config tidiness (D12)** | **No new `config.yml` key, and that is deliberate.** Scope is per-credential and the credential set is discovered rather than authored — the same reasoning `0013` used to keep per-device preferences out of `config.yml` (quince#728: the file holds only what the user set). The enrolment TTL is a constant with its reasoning in D4 unless review rules otherwise; a UDID-keyed or credential-keyed block would fill the file with values nobody typed. |
| **Secrets discipline** | The enrolment secret reaches the app DB and a QR and nothing else — never argv, env or a log line. D3 grants the backup encryption password to a new principal; the existing stdin/pty-only path is unchanged, and this rung adds no new way for it to be spelled. Test fixtures keep the password `test`. |
| **Subprocesses** | None beyond the existing job engine, entered by existing routes. |
| **Every hardware bug becomes a replay fixture** | No hardware path is touched. G6 names what hardware still owes and to whom. |
| **Docs are part of the diff** | `contracts.md` §1 (D10) and `design.md` §6 (the principal, the scope, and D6's invariant) move with the slices that change them, not with this spec — **this PR asserts no new canon; it proposes.** The roadmap row and the devlog dashboard follow the ruling, not the proposal. Coverage and a known-untested list ride each build slice. |
| **Don't improvise architecture** | The four proposed items are now **ruled by the architect** (quince#1347 review; `/docs/specs/**` is that seat's under `CODEOWNERS`), and the one genuine gap — the iOS measurement — is a **pointer with an owner**, not an assertion about state, per the 2026-08-18 retirement of the `PROPOSED (gap)` block (quince#1219). **Provenance is stated per ruling rather than in bulk, because three of these have three different origins**: D3's exception and D6's invariant are **transcribed Operator rulings** quoted verbatim from quince#1342; **D7's admin-mute rule is an Operator instruction given in session and relayed here** — not on the forge, and D7 says so in its own words rather than leaving a citation that cannot be followed; D9's marked-rows requirement is **relayed** by the analyst seat without a quote. Written out in full here because git is where a decision survives. Nothing is built past any of it. |
| **Approver ≠ author** | Authored by an implementer session as `quince-coder`; reviewed by the architect. This spec touches the security model, so it is reviewed **before** code exists, which is why it is PR 1. |

---

## Slices

Each is one PR carrying one reviewable claim, **sequenced from `main`, not stacked**.

| | claim | gated? |
| --- | --- | --- |
| **1** | **this spec** | — |
| **2** | **the iOS measurement** (G6) — a report, not code. Re-reads D2's prose against the answer. | Operator hardware |
| **3** | the principal: `sessions_auth` gains its credential, `Authenticate` returns it, `authGuard` binds it into the request context — **no behaviour change, no scope yet** (D1, G2) | no |
| **4** | scope on the credential, and D6's invariant with its gate — **before anything can mint a scoped row** (D2, D6, G1) | no |
| **5** | quince#1001, and quince#1259's reachable-and-scope-aware `ErrLastCredential` (D9) | no |
| **6** | the enrolment ceremony and the QR, against fact 8's precedent (D4, D5, G4) | no — ruled 2 |
| **7** | authorization at every route, and the shell's shape (D3, D8, G3) | no — ruled 1 |
| **8** | the send-path filter and the preference's owner column, backfilled admin-owned (D7, G5) | no |
| **9** | the admin's view: marked rows, listed secrets, revocation from the device page (D9, ruling 2) | no — ruled 2 |

**Slice 3 before slice 4, and slice 4 before anything mints a scoped credential.** D6's invariant is
worthless if a scoped row can exist before the predicates know what one is — that ordering is the
same reasoning `0008_passkeys.sql` used to ship `quince auth reset` before any credential could be
issued.
