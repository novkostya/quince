# qn.13 — a device-scoped passkey: its holder sees one device and nothing else

**Status: SPEC. Nothing here is built.** **Amended 2026-08-20 after the iOS measurement** — see *Measured*, D2.1, D2.2 and D4.1. Scoped by the Operator on 2026-08-20 (quince#1342), items
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
   **A FOURTH SITE IS A CEREMONY, NOT A PREDICATE**: `auth/reauth.go:158` calls
   `BeginDiscoverableLogin(opts...)` and passes an allow-list only for `OpRemovePasskey`, so
   `set_password` and `add_passkey` admit any credential at the rpId as proof. It fails the same way
   and is covered by the same invariant (D6, G1).
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
9. **Login sends an EMPTY allow-list.** `auth/passkey_login.go:35` is `wa.BeginDiscoverableLogin()`,
   so the platform selects the credential and quince learns which one only from the assertion. The
   library's `BeginLogin(user)` populates `allowCredentials` instead — same credentials, quince
   choosing. This is D2.2's whole mechanism.
10. **Registration sends a FULL exclusion list.** `auth/passkey.go:261` is
    `webauthn.WithExclusions(credentialDescriptors(existing))` over every row at the rpId, so a
    second credential on one authenticator is refused by the device. This is what made the
    measurement's state impossible to create without removing the line, and it is what D4.1 rules on.
11. **The browser already remembers a passkey fact.** `ui/src/lib/passkeyHint.ts` stores
    `quince.passkey.seen = "1"`, read at `usePasskeyLogin.ts:41` to decide whether to fire the sheet
    unprompted. Its header states the property D2.2 must preserve: *"it is a HINT and nothing hangs
    on it… nothing here is a credential, a secret, or an authorisation."*
12. **iOS collapses credentials on `(rpId, username)`, and every quince credential shares a
    username.** `WebAuthnName()` and `WebAuthnDisplayName()` both return `adminUsername =
    "quince-admin"` (`auth/passkey.go:157-158`, `:164`). Measured on hardware 2026-08-20: three
    credentials, one row. This is the finding behind D2.1.

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
capability is a property of a credential. **The no-account-picker question has been MEASURED** — see
*Measured 2026-08-20*, below — and it survives. What the measurement found instead is D2.1.

### D2.1 — A scoped credential MUST NOT be named `quince-admin` (Operator, 2026-08-20)

**Operator, on being shown the measurement: *"household member must not be quince-admin, that's
wild."*** **Given IN SESSION and relayed here by the implementer. It is NOT on the forge, and this
sentence is the whole of its provenance** — the same backing, and the same disclosure, as D7's.
Ruled, and the reason is state honesty at the one place the user actually looks: the iOS
sheet labels a credential with its `user.name`, so a household member's phone would tell them they
hold **admin** — the opposite of what their credential grants.

**A scoped credential carries its own `user.name`** — the device it is scoped to, which is the only
name that is both meaningful to its holder and true.

**This is a scoped EXCEPTION to quince#819, not a repeal of it, and the difference must be written
down or somebody will "fix" it back.** `auth/passkey.go:161` requires the username to be the same
string as the login form's anchor, because *"a passkey registered under a different name would file
itself as a second identity beside the password rather than beside it."* A scoped credential **is**
that second identity, deliberately: it belongs to a different principal, on a different phone, with
different authority. The constant stays correct for admin credentials and only for those.

**It also disarms the measurement's defect.** Two credentials with different usernames do not
collapse, so the unselectable single row cannot arise even on a phone holding both.

### D2.2 — quince chooses which credential is offered; the platform stops choosing

The measurement showed iOS selecting a credential with no way for the user to intervene. The fix is
for quince to stop asking it to.

**The lever already exists.** `auth/passkey_login.go:35` calls `BeginDiscoverableLogin()` — an empty
allow-list, which *is* "platform, you choose". The library's `BeginLogin(user)` populates
`allowCredentials` instead, so quince decides what is offered. Both use the same discoverable
credentials; only the offer differs.

**There are TWO call sites, not one, and only the login one is D2.2's.** `auth/reauth.go:158` also
calls `BeginDiscoverableLogin(opts...)` — and it **already narrows**, passing
`WithAllowedCredentials(allowed)` for `OpRemovePasskey` so the credential being removed cannot prove
its own removal. Every other operation stays discoverable, with the reason stated at that call:
`add_passkey` and `set_password` *"are about the credential SET rather than a member of it, so
restricting them would exclude nothing"*.

**That reason is what this rung changes, and the change belongs to D6 rather than to D2.2.** Once
credentials carry scope there is no longer *one* credential set: `set_password` and `add_passkey`
operate on the **admin's**, so admitting a scoped credential as proof would let a scoped holder
prove an admin operation. **So reauth's allow-list must become scope-correct — an admin operation
admits only admin credentials — and that is D6's invariant reaching the ceremony rather than the
predicate.** It is not a remembered-principal question: reauth already knows who is asking, because
a session is open.

**Stated as a difference rather than a shared mechanism**, because the two sites want opposite
things for opposite reasons. Login narrows for **convenience** and must fall back when the hint is
stale. Reauth narrows for **authorization** and must NOT fall back — a fallback there is the refusal
being skipped.

- **A remembered principal** → `BeginLogin` with that principal's credentials → one tap, one identity,
  no ambiguity.
- **"Change user"**, deliberately subtle → `BeginDiscoverableLogin()` → today's behaviour, which is
  the correct fallback rather than a separate mode.

**Where the memory lives: `ui/src/lib/passkeyHint.ts`, which already does this shape.** It stores
`quince.passkey.seen = "1"` and is read at `usePasskeyLogin.ts:41`. This replaces the boolean with an
identifier, keeping the file's existing degradation — private mode, disabled storage and quota all
fall back to the button.

**Store the CREDENTIAL ID, not the username.** `"1"` is not personal data and a household member's
name is; the credential id is already public in `allowCredentials`, names exactly which credential to
offer, and lets the server return the display name with the ceremony. Same UX, nothing personal in
the browser.

**The hint must stay a hint, which is what keeps `passkeyHint.ts`'s own security claim true** —
*"nothing here is a credential, a secret, or an authorisation."* It selects what is **offered**;
authority still resolves from `credential_id` **after** assertion, per D2. Nothing is granted by what
the browser remembers.

**A revoked remembered credential must fall back, not dead-end.** If `allowCredentials` names a
credential that no longer exists, the platform reports no passkey available and the user is stuck on
a page that should have worked. Fall back to the discoverable flow — the same degradation the file
already handles.

**This is what lets D4's exclusion-list question relax.** quince disambiguates rather than forbidding,
so one phone holding both an admin and a scoped credential becomes supportable instead of refused —
which is a real want, since the admin plausibly uses their own device page as its owner.

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

### D4.1 — The enrolment ceremony sends NO exclusion list, and that is a decision with a second reason

Registration today sends every credential at the rpId as an exclusion list
(`auth/passkey.go:261`, `WithExclusions(credentialDescriptors(existing))`), so the authenticator
refuses a duplicate on a device that already holds one. That is correct for an **admin** adding a
second passkey to a phone they already registered. It is wrong here, twice.

**It would disclose the admin's credential ids to whoever scanned the QR.** The enrolment page is
reached pre-authentication by definition — that is the whole ceremony — so an exclusion list makes
every admin credential id readable by anyone holding a QR, spent or not. The existing code already
names this concern in the neighbouring function: *"offering them as exclusions would tell the
authenticator about registrations it has no business knowing exist on this origin."* An unauthenticated
scanner has less business still.

**And it would forbid a state D2.2 makes supportable.** With the exclusion list, an admin cannot
enrol a scoped credential on their own phone at all — the authenticator refuses. That was the right
answer while a second credential was unselectable; D2.1 and D2.2 remove both halves of that problem,
so refusing now costs a real want for nothing.

**So the enrolment ceremony excludes nothing.** The duplicate it would have prevented is prevented
instead by the enrolment secret being single-use.

**The admin's own registration path keeps its exclusion list unchanged.** Two ceremonies, two
answers, and the difference is who is standing at the other end.


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

**AND IT BINDS CEREMONIES, NOT ONLY PREDICATES.** `auth/reauth.go:158` is a fourth site of the same
defect wearing a different shape: it does not *count* credentials, it decides which ones may
**prove** an operation, and it currently admits any credential at the rpId for `set_password` and
`add_passkey`. Those act on the **admin's** credential set, so a scoped credential proving one is a
scoped holder performing an admin operation — the same failure as a permissive count, arriving
through the allow-list instead. The generalisation:

> **Anything that asks *which credentials count here* must ask it about the RIGHT credentials.**
> Counting all rows, and admitting all rows as proof, are the same unsafe default.

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

## MEASURED 2026-08-20 — ONE entry, and the reason is the finding

**The question** (quince#1342 §2): do two discoverable credentials sharing one `user_handle` on one
rpId present as ONE entry or TWO on iOS? It decided whether the no-account-picker property `qn.6k`
chose discoverable credentials to obtain survives.

**Taken by the Operator on a real iPhone**, against a build with `WithExclusions` removed — that line
is what makes the two-credential state impossible, so it had to go for the state to exist at all. The
build was throwaway and was rolled back the same hour.

**Result: THREE credentials in quince's table, ONE row on iOS** — the site, and the username
`quince-admin`. **The property survives, and D2's premise holds.**

**But it survives for a reason the question did not anticipate, and that reason is a defect.** iOS
collapses credentials on `(rpId, username)`, and `WebAuthnName()` returns the constant
`adminUsername = "quince-admin"` for **every** credential (`auth/passkey.go:157`, `:164`). So the
credentials are not merely uncluttered — they are **indistinguishable and unselectable**. In the
measurement, sign-in used `A` and left `B` *"never used"*; the user was never asked and could not
have chosen.

**A picker would have been better than what we have.** A picker lets you choose. One row that
silently selects among credentials **carrying different authority** is the failure this rung must not
ship: on a phone holding both an admin credential and a device-scoped one, iOS would decide which
authority you signed in as.

**So the measurement did not confirm the design — it found the requirement in D2.1 below.**

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
12. **A household member's phone does not say `quince-admin`.** A scoped credential registered from a
    QR shows the device's name in the platform's sign-in sheet; an admin credential still shows
    `quince-admin`. (D2.1, G7)
13. **One tap signs in as the right principal, and the picker is reachable.** A browser that has
    signed in before offers that principal directly; *change user* falls back to the discoverable
    flow; a browser that has never signed in sees today's behaviour. (D2.2, G8)
14. **A revoked credential does not strand its browser.** With the remembered credential revoked,
    sign-in falls back to the discoverable flow rather than reporting no passkey available. (D2.2, G8)

---

## Gates

Beyond `make gates` / `make image`:
- **G1 — the scope-blind site gate.** A Go test asserting that a scoped-only install cannot reach
  passwordless (story 7), plus an enumeration over the sites that ask *which credentials count here*
  so a **fifth** one added later fails the build rather than silently admitting the wrong set. **It
  covers `auth/reauth.go:158` as well as the three predicates**: an operation on the admin's
  credential set must admit only admin credentials as proof, which is the same invariant reaching a
  ceremony instead of a count. D6's invariant is only worth stating if something asserts it for code
  nobody has written yet.
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
- **G6 — TAKEN 2026-08-20, on hardware, by the Operator.** **Reported IN SESSION and relayed here
  by the implementer, with two screenshots. It is NOT on the forge, and this sentence is the whole
  of its provenance** — and a MEASUREMENT attributed to somebody who cannot be shown to have taken
  it is a stronger claim than a ruling on the same backing, so it says so first. The iOS
  account-picker measurement. Result
  in *Measured* above: one row for three credentials, because the collapse key is `(rpId, username)`.
  The claim it was owed to is now a fact, and the two requirements it produced are G7 and G8.
- **G7 — no scoped credential is ever named `quince-admin`.** A test over the registration path
  asserting that a scoped ceremony sends the device's name as `user.name` and an admin ceremony sends
  the constant, so D2.1's exception cannot be collapsed back into quince#819's rule by a later
  refactor. **This is the gate that protects a household member from being told they are the admin.**
- **G8 — the hint selects, it never grants.** Tests that a remembered credential id changes only
  which credentials are OFFERED, that the resolved principal still comes from the assertion, and that
  a remembered credential which no longer exists **falls back to the discoverable flow** rather than
  dead-ending. The last one is the failure mode a revocation creates, and it is the one a
  happy-path test would miss.

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
| **State honesty** | The iOS measurement has been **TAKEN** and its result is recorded with what it actually showed — including that it did **not** confirm the design but found a defect (D2.1). The spec no longer carries an assumption where a fact now exists, which is the amendment's main job. D9 permits shipping without immediate revocation *only* if the screen says so. D5 refuses to encode a guessed address. D2.2 requires the fallback that keeps a revoked hint from stranding its browser. |
| **Troubleshooting is actionable** | D5 is this rule applied to the sharpest failure in the rung: three things break differently and two silently, so the message names the address baked in and the remedy, following `ErrUnsupportedRPID`'s house pattern. D6's refusal names why passwordless is unavailable rather than merely refusing. |
| **Interface facts looked up live** | All eight facts carry the checkout (`acdcfe7`) and date they were established, with file and line. Fact 6 corrects quince#1342 §5 by measurement rather than by memory. No version is pinned by this rung; it adds no dependency. |
| **Never mutate a committed version** | **Comes near, does not touch.** D3 grants *back up now*, *retry* and *cancel* to a new principal — the same job engine, entered by the same routes, with no new write path into `latest/`, `versions/` or a snapshot. *Delete a version* is refused (D3), which is the only capability in the list that could reach a committed artifact. Nothing here changes when quince#591's zfs in-place ruling is built. |
| **No silent caps or fallbacks** | D7 filters at send and surfaces the result; D8 refuses server-side rather than hiding; ruling 2 lists unused enrolment secrets rather than leaving live authority invisible; D6's whole subject is a predicate that fails *permissively* and silently. |
| **Config tidiness (D12)** | **No new `config.yml` key, and that is deliberate.** Scope is per-credential and the credential set is discovered rather than authored — the same reasoning `0013` used to keep per-device preferences out of `config.yml` (quince#728: the file holds only what the user set). The enrolment TTL is a constant with its reasoning in D4 unless review rules otherwise; a UDID-keyed or credential-keyed block would fill the file with values nobody typed. |
| **Secrets discipline** | The enrolment secret reaches the app DB and a QR and nothing else — never argv, env or a log line. D3 grants the backup encryption password to a new principal; the existing stdin/pty-only path is unchanged, and this rung adds no new way for it to be spelled. Test fixtures keep the password `test`. |
| **Subprocesses** | None beyond the existing job engine, entered by existing routes. |
| **Every hardware bug becomes a replay fixture** | No hardware path is touched. G6 names what hardware still owes and to whom. |
| **Docs are part of the diff** | `contracts.md` §1 (D10) and `design.md` §6 (the principal, the scope, and D6's invariant) move with the slices that change them, not with this spec — **this PR asserts no new canon; it proposes.** The roadmap row and the devlog dashboard follow the ruling, not the proposal. Coverage and a known-untested list ride each build slice. |
| **Don't improvise architecture** | The four proposed items are **ruled by the architect** (quince#1347 review; `/docs/specs/**` is that seat's under `CODEOWNERS`), and the measurement that was the one genuine gap has been **taken**. **Provenance is stated AT each ruling and enumerated here, because these have FIVE different origins and a list that omits one is the defect this row is about** (quince#409, and it happened to this very row in quince#1354's first revision): **(1)** D3's exception and **(2)** D6's invariant are **transcribed Operator rulings**, quoted verbatim from quince#1342 and checkable there; **(3)** D7's admin-mute rule, **(4)** D2.1's username rule and **(5)** G6's measurement are **in-session, relayed by the implementer and NOT on the forge** — each says so in its own words, because a citation a reader cannot follow is worse than none, and G6 says it first since a measurement carries more weight than a ruling on the same backing; D9's marked-rows requirement is **relayed by the analyst seat without a quote**. Written out in full here because git is where a decision survives. Nothing is built past any of it. |
| **Approver ≠ author** | Authored by an implementer session as `quince-coder`; reviewed by the architect. This spec touches the security model, so it is reviewed **before** code exists, which is why it is PR 1. |

---

## Slices

Each is one PR carrying one reviewable claim, **sequenced from `main`, not stacked**.

| | claim | gated? |
| --- | --- | --- |
| **1** | **the spec** — merged, quince#1347 | — |
| **2** | **the iOS measurement** — **DONE 2026-08-20**, Operator, on hardware. Result and consequences in *Measured*, D2.1, D2.2, D4.1 | — |
| **3** | the principal: `sessions_auth` gains its credential, `Authenticate` returns it, `authGuard` binds it into the request context — **no behaviour change, no scope yet** (D1, G2) | no |
| **4** | scope on the credential, and D6's invariant with its gate — **before anything can mint a scoped row** (D2, D6, G1) | no |
| **5** | quince#1001, and quince#1259's reachable-and-scope-aware `ErrLastCredential` (D9) | no |
| **6** | the credential username: scoped rows carry their device, admin rows keep `quince-admin` (D2.1, G7) | no |
| **7** | the remembered principal and the subtle *change user* — `passkeyHint` holds a credential id, login sends `allowCredentials` (D2.2, G8) | no |
| **8** | authorization at every route, and the shell's shape (D3, D8, G3) | no |
| **9** | the enrolment ceremony and the QR, against fact 8's precedent, excluding nothing (D4, D4.1, D5, G4) — **AFTER slice 8; see the ordering rule below** | no |
| **10** | the send-path filter and the preference's owner column, backfilled admin-owned (D7, G5) | no |
| **11** | the admin's view: marked rows, listed secrets, revocation from the device page (D9) | no |

**Slice 3 before slice 4, and slice 4 before anything mints a scoped credential.** D6's invariant is
worthless if a scoped row can exist before the predicates know what one is — that ordering is the
same reasoning `0008_passkeys.sql` used to ship `quince auth reset` before any credential could be
issued.

**AND AUTHORIZATION BEFORE ENROLMENT — slices 8 and 9 were the other way round until 2026-08-21, and
that order opens a hole rather than merely being untidy.** Enrolment is the first thing that can MINT
a scoped credential; authorization is what makes every route consult a principal's scope. Landing
enrolment first leaves a window — one merge wide, possibly longer — in which a scoped credential
EXISTS and every route still serves it in full. Its holder would reach Settings, storages, other
devices and the whole of the admin surface, which is the precise opposite of what issuing it means.

**The window is not hypothetical, because the QR is how a real person gets a credential.** A rung
half-landed on a running install is the state this project actually ships through — `qn.13`'s own
slices have been merging one at a time all night — so "we would not enrol anyone before authorization lands" is a
statement about intentions, not about what the code permits.

**It is the same rule as slice 4's, one layer up**, and worth stating as the general form rather than
as a second special case:

> **Nothing that CREATES a principal may land before the thing that CONSTRAINS it.**

Slice 4 applied that to the predicates and the scope column; this applies it to the ceremony and the
routes. Both are instances of the ordering `0008` established for this codebase.
