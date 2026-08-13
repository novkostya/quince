# qn.6n — changing what can sign in requires signing in

**Goal.** Every operation that changes the set of credentials — adding one, removing one, changing
the password — requires **presenting** a credential at the moment of the operation. A session proves
a *past* authentication; the set of things that can authenticate is changed only against a *present*
one.

Operator ruling, 2026-08-13, on [quince#888](https://github.com/novkostya/quince/issues/888) item 3,
relayed by the architect seat. **The mechanism is ruled: per-operation proof.** The sudo window was
rejected — its grant is ambient, so a stolen session acting inside it inherits exactly the authority
being defended against.

---

## The letter

`qn.6l` is spoken for by [quince#726](https://github.com/novkostya/quince/issues/726) — two merged
documents say so (`qn.6k.md:29`, `qn.6m.md`) and no directory holds it. `qn.6m` is this rung's
predecessor. **`qn.6n` is free**, measured across `docs/`, product code, `bin/` and `ui/`: the string
appears nowhere.

So `docs/specs/` runs `qn.6a … qn.6k`, `qn.6m`, `qn.6n`, with the hole at `qn.6l` that `qn.6m`
already documented. **Named here so the next reader does not file the gap as a lost spec**, which is
the trap `qn.6k` recorded for the journal's letter ids.

**This is a rung and not a `qn.6m` amendment**, because it changes four ruled endpoints, adds a
fifth, and moves the authority model. `qn.6m` asked the same question about itself and left it open;
here the ruling settles it — *"A spec first, per `CLAUDE.md` rung protocol — this is a design change,
not a bug fix."*

---

## Boundary

**In scope**

- `core/internal/auth/` — the proof mechanism, and the three rules applied to every mutating path.
- `core/internal/httpapi/` — one new endpoint pair, four changed endpoints, the allowlist assertions.
- `docs/contracts.md` — request fields and error codes on those endpoints. **Code-owned.**
- `docs/quince.design.md` §6 — the authority statement.
- `ui/src/features/settings/` — the proof prompt, and the copy that becomes false when this lands.

**Out of scope**

- **First run.** `POST /api/auth/setup` and `POST /api/auth/setup/passkey/*` are untouched. *First-run
  passwordless stays ONE ceremony* — Operator ruling 2026-08-12, not reopened here.
- **`quince auth reset`.** Unchanged, and it remains the answer for an install that cannot present
  anything. This rung makes that answer load-bearing; it does not alter it.
- **Renaming a passkey.** Stays session-only — see D6.
- **Session lifetime, rotation, CSRF, rate-limit tuning.** Untouched. The new endpoint joins an
  existing bucket rather than defining a policy.
- **quince#902** — the passwordless cost list. Landed separately as quince#904, because it is true
  today and this rung only sharpens it.

---

## Design

### D1. The three rules, verbatim, and what they cost

Restated from the ruling rather than paraphrased, because every decision below is derived from them:

1. **Adding a credential** — password or passkey — **requires presenting an existing credential**,
   password or passkey.
2. **Removing a credential requires presenting an existing credential OTHER THAN THE ONE BEING
   REMOVED.**
3. **Changing the password requires presenting any existing credential.**

**The only exception is an install with no credentials at all** — first launch, or after
`quince auth reset`. Nothing else.

**Rule 3 is not a relaxation and the ruling says why.** Today the same authority is reachable in two
steps: `DELETE /api/auth/password`, which needs only a passkey **row** to exist, then
`PUT /api/auth/password` with an empty `current`, which needs **nothing at all**. A session holder
can already change the password without knowing it. Rule 3 is that authority in one prompt instead of
two, with the two-step path closed rather than left open.

### D2. Rule 2 replaces machinery rather than adding it

*"Not the one being removed"* means the surviving credential is proven **usable** by the act of
presenting it. Three consequences, all subtractive:

- **The row-versus-usable defect dissolves.** `RemovePassword` counts a row and its comment claims
  usability (quince#892 made the comment honest and left the check alone, deliberately, pending this
  ruling). Under rule 2 nothing counts rows, because **a dead row cannot produce an assertion**.
- **quince#888 item 1 closes by construction.** Removing your only credential cannot satisfy rule 2,
  so the two-click takeover is unreachable without a special-case guard.
- **`ErrLastPasskey` and `ErrLastCredential` stop being guards.** They are not deleted: the ruling is
  explicit that **the message is worth keeping** — naming which credentials exist and where they work
  — because a refusal that says only *"present another credential"* is correct and less useful.

**So the net change to `auth` is smaller than it looks**: two lockout checks come out, one proof
check goes in, and the error types survive as message-carriers.

### D3. The proof is carried by a new endpoint pair, NOT by `passkeys/login/*`

**`POST /api/auth/reauth/begin` and `POST /api/auth/reauth/finish`.** The architect's position, taken
here as rung-local detail with the reasoning recorded:

`passkeys/login/begin|finish` are pre-auth **by exact path** in all three allowlists — `authExempt`,
`setupAllowed` and `csrfExempt`. Giving the three least-guarded routes in the system a second job,
whose whole purpose is to gate privileged operations, is the wrong trade. **The reauth pair requires
a session and is therefore in NONE of the three lists**, which is the same placement as registration
and as the password endpoints, and `passkey_allowlist_test.go` asserts it by exact path and method.

### D4. The proof is a single-use token BOUND TO ONE OPERATION, and it records WHICH credential proved it

The ruling chose per-operation proof over a sudo window precisely because *"it binds each proof to
one operation, no ambient window"*. So the binding is not an embellishment; it is the property that
distinguishes the ruled mechanism from the rejected one.

```
POST /api/auth/reauth/begin {operation, target?}   → 200 {ceremony, options}
POST /api/auth/reauth/finish?ceremony=<key>        → 200 {proof}
```

`operation` is one of `add_passkey`, `remove_passkey`, `remove_password`, `set_password`. `target` is
the credential id for `remove_passkey` and absent otherwise.

**The proof records its SUBJECT — which credential was presented — because rule 2 cannot be enforced
without it.** *"Other than the one being removed"* is a comparison, and there is nothing to compare
against unless the server knows what proved the request. This is the single most important field in
the rung.

**A password needs no ceremony.** Every mutating endpoint accepts `current_password` as an
alternative to `proof`, which is what `PUT /api/auth/password` already does; the subject in that case
is *the password*. Rule 2 then falls out arithmetically: removing a passkey may be proven by the
password, and removing the password may **not** be proven by the password.

**Single-use and short-lived**, reusing the existing ceremony-key machinery and its TTL rather than
inventing a second expiring-token store.

**AND BOUND TO THE SESSION THAT MINTED IT — four bindings, not three.** Added at spec review, because
an enumeration is read as exhaustive and slice 2 is where an omission becomes a contract. A proof is
a credential-equivalent for one operation, so it must not be usable by a client that did not earn it;
the ceremony key is returned only to the minting client today, which makes the gap narrow rather than
absent, and *narrow* is not a property worth relying on when the binding costs one comparison.

**`reauth/finish` MUST NOT ISSUE OR ROTATE A SESSION**, which is the one place it differs sharply
from `passkeys/login/finish` and the reason session binding is coherent at all. That endpoint's whole
job is to mint a session; this one verifies the same assertion and mints a *proof*. If it rotated the
session, it would be a second login path reachable from an authenticated context — and the proof
would be bound to a session id that no longer exists by the time the mutating call arrives.

### D5. There is NO exception for "a credential exists but cannot be presented here", and this is the sharpest part of the ruling

A passkey is bound to an `rp_id`, which quince derives from the request's **`Host` header**. So a
credential can exist in the database and be impossible to present — a proxy that stops preserving
`Host`, or a hand-edited row. That is the `elsewhere-only` state quince#895 named on the settings
surface.

An exception for it was **considered and rejected as unsafe**, on the Operator's argument:

> **An attacker holding a stolen session controls the `Host` header.** They need no proxy and no
> infrastructure — one crafted request with a `Host` matching no passkey, and quince concludes that
> no proof is possible and waives its own rule. **The waiver hands the attacker its own trigger.**

**The remedy for that state is `quince auth reset`**, which is the honest answer for a condition
caused at host level.

**It is narrower than it sounds.** The password is not `rp_id`-bound, so an install *with* a password
recovers ordinarily: sign in, present the password under rule 1, register a fresh passkey at the new
address. **Only a passwordless install has nothing left to present** — which is the cost quince#902
put on screen, and which this rung makes load-bearing rather than theoretical.

### D6. Renaming a passkey stays session-only

The architect's default, recorded so it is visibly a decision. A rename changes nothing about who can
get in; it is a label on a row. Applying rule 1 to it would put a biometric prompt in front of fixing
a typo, and would be the only proof-carrying operation in the system that guards nothing.

**Stated as the architect's rather than the Operator's**, per the ruling's own note — if the Operator
disagrees, this is the line to change.

### D7. Every proof-carrying endpoint shares the LOGIN rate-limit bucket

For the reason `ChangePassword` already carries: somebody holding a session must not get a fresh
budget to guess with. This is a widening of an existing rule to new endpoints, not a new policy.

**`reauth/finish` verifies an assertion, so it is rate-limited on the same bucket as
`passkeys/login/finish`** — the two are the same operation with different consequences.

### D8. The `elsewhere-only` copy changes IN THIS RUNG, not before it

[quince#903](https://github.com/novkostya/quince/issues/903) reports that `/settings/auth` tells a
user in `elsewhere-only` they can set a password without console access. **That copy is correct on
`main` today** — `PUT /api/auth/password` accepts an absent `current_password` on a passwordless
install — and becomes false the moment rule 1 ships.

**So it changes in the same diff that changes the endpoint.** Fixing it earlier would send a user to
a console to run `quince auth reset` — clearing every credential and every session — to escape a
state the form above would have fixed in one field. That is the same defect pointed the other way,
and the more expensive direction.

What the surface must then say, from quince#903's own analysis:

- **`elsewhere-only` and `unconfigured` need different sentences** and share a section today. Rule 1's
  exception applies to `unconfigured` and not to `elsewhere-only`. The section quince#895 correctly
  split from `passwordless` needs splitting once more.
- **Both remedies currently offered are refused** in that state: setting a password is rule 1, and
  *"or add a passkey for this address"* is also rule 1. The `passkeysSupported` condition added in
  quince#895's review handles the bare-IP case and does nothing for this one.
- What is genuinely true there is `quince auth reset`, which today's copy explicitly rules out.

---

## Stories

1. **Adding a passkey demands a credential.** On an install with a password, `POST
   /api/auth/passkeys/register/finish` without proof is refused; with `current_password` it succeeds.
2. **Adding a password on a passwordless install demands the passkey.** `PUT /api/auth/password` with
   no `current_password` and no `proof` is refused — the hole quince#888 named as *"nothing — an empty
   `current_password` is accepted by design"*.
3. **Changing a password demands the current password or a passkey.** Both routes work; neither is
   optional.
4. **Removing a passkey cannot be proven with that same passkey.** A proof whose subject is the
   target is refused; the password, or a different passkey, succeeds.
5. **Removing the password cannot be proven with the password.** It requires a passkey assertion.
6. **An install with one credential cannot remove it** — by rule 2, with no special-case guard in the
   code, and the refusal names what exists and where it works.
7. **A first-run install is exempt.** With no credentials at all, `POST /api/auth/setup` and the
   first-run passkey pair are unchanged and demand nothing.
8. **The reauth pair is session-required** and appears in none of the three exact-path allowlists.
9. **A proof is single-use, expiring, and bound to its operation** — replaying it, or using an
   `add_passkey` proof for a removal, is refused.
10. **`/settings/auth` tells the truth in `elsewhere-only`** — D8 — and still tells the truth in
    `unconfigured`, where the form really does work.
11. **The refusal is actionable.** It names which credentials exist and where they work, per the
    ruling's instruction not to lose `ErrLastCredential`'s message.

---

## Gates

Beyond `make gates` / `make gates-ui` / `make gates-ui-e2e`:

- **G1** — `passkey_allowlist_test.go` extended: the reauth pair asserted absent from all three lists,
  by exact path AND method. Non-vacuity probed by adding it to one list and watching the test fail.
- **G2** — a proof bound to `add_passkey` rejected by a removal endpoint; a proof whose subject is the
  target rejected by `remove_passkey`. Table-driven over all four operations.
- **G3** — replay: the same proof presented twice, second refused.
- **G4** — expiry: a proof past its TTL refused.
- **G4b** — the session binding (D4): a proof minted under one session refused when presented with
  another. And `reauth/finish` **issues no `Set-Cookie`** — asserted on the response, because the
  endpoint it is modelled on does issue one, and inheriting that would silently make this a second
  login path.
- **G5** — the first-run exemption, asserted on a store with **no** credentials, next to a store with
  one, in a single test — the pair, because the exception and the rule are one decision.
- **G6** — `PUT /api/auth/password` with neither `current_password` nor `proof`, on a **passwordless**
  install, refused. This is the specific hole story 2 names and it must have its own test.

### Owed gates — hardware, and an agent seat cannot run either

- **G7 (owner: Operator)** — **add a passkey while proving with a passkey, on a real authenticator.**
  This is assert-then-create in one user gesture, and the ruling names the friction as accepted. **iOS
  is the risk**: `navigator.credentials.get()` followed by `navigator.credentials.create()` may lose
  user activation for the second call. If it does, the flow needs a shape this spec has not designed,
  and finding that out on hardware is the point of the gate. **Nothing in CI can prove it** — vitest
  mocks the ceremony and e2e cannot reach a secure context, which `qn.6m` story 1 already asserts.
- **G8 (owner: Operator)** — **remove the password with a passkey assertion**, on hardware, at a
  domain address.

**Declared unrun until they are run.** `qn.6k` shipped in nine green pull requests and was inert;
`qn.6m` was called code-complete with both hardware gates outstanding. Every automated gate above is
consistent with this rung being inert in exactly the same way.

---

## Fixtures

- **No new transcript fixtures.** This rung touches no `idevicebackup2` path.
- Go tests reuse `newConfiguredService` / `seedPasskey` from `configured_test.go`, and the
  `example.com` / `example.net` domains that file already establishes — a real domain is
  Operator-private, so fixtures never carry one.
- UI tests reuse `passkeyAt` and the four-argument `renderControls` from
  `PasswordControls.test.tsx`, and `192.0.2.10` (TEST-NET-1, RFC 5737) for the bare-IP case.
- **A stub authenticator for the assert-then-create pair is the one genuinely new fixture**, and it
  proves the wiring only — see G7.

---

## Rule check

- **Don't improvise architecture.** The mechanism, the three rules and the no-exception decision are
  the Operator's, cited rather than re-derived. D3, D4, D6 and D7 are the four the ruling explicitly
  left to the architect for spec review, and each records its alternative. **Nothing here is a
  `PROPOSED (gap)`**: the gap was the ruling, and it has been taken.
- **Contracts.** `docs/contracts.md` changes on **four** endpoints and gains a fifth pair.
  `.github/CODEOWNERS` routes that file to `@novkostya`, and **an App cannot be a code owner**, so an
  architect approval cannot make those PRs mergeable. Planned for in the slice table rather than met
  at merge time — the ruling says so in as many words.
- **State honesty.** D8 is this rule applied to itself: the copy moves in the diff that makes it true,
  and not one PR earlier.
- **No silent caps or fallbacks.** Every refusal is surfaced with the server's own sentence, which is
  why the error types survive D2 as message-carriers.
- **Secrets discipline.** `current_password` travels in a request body over the existing authenticated
  channel, exactly as `PUT /api/auth/password` does today — never argv, env, or a log line. **The
  proof token is a credential-equivalent for one operation** and must not be logged either; the
  existing ceremony keys set that precedent and it is followed rather than re-argued.
- **Privacy.** No LAN address, hostname or UDID enters a fixture; the domains above are the ones
  already in use. `make privacy-check REF=origin/main...HEAD TEXT=<file>` before every push.
- **Docs are part of the diff.** `quince.design.md` §6 states the authority model and this rung moves
  it, so it changes in the slice that lands the mechanism.
- **Every bug found on hardware becomes a replay fixture.** Applies to G7/G8 if either finds one.
- **Coverage declared.** Each PR carries the `go test -cover` summary plus an explicit
  known-untested list, one line and reason each.

---

## Slices

Sequenced from `main`, **not stacked**. Code-owned slices need `@novkostya`; the others do not and
must not be made to wait behind one.

**THE COLUMN ASKS `code-owned?`, NOT `contracts?`, AND THE DIFFERENCE IS A TRAP** — spec review.
`.github/CODEOWNERS` routes **`docs/quince.design.md` exactly as it routes `docs/contracts.md`**, and
this rung's Boundary puts design §6 in scope. A column named for `contracts.md` reads as the whole
gating fact, so a reader adding a slice would check the wrong file, mark a design-touching slice `no`,
and discover at merge time that it needs the code owner. Nothing is mis-marked today — §6 lands in a
slice already marked **YES** — which is exactly when a naming trap is cheap to remove.

| | | code-owned? | |
| --- | --- | --- | --- |
| **1** | **this spec** — `docs/specs/**` is *not* code-owned | no | *this PR* |
| **2** | **the proof primitive** — `operation`/`target`/subject/session, single-use, expiring, in `auth` alone, with **no caller**. G2, G3, G4, **G4b's session half**. | no | quince#920, **merged** |
| **3** | **the reauth endpoint pair** + the allowlist assertions (G1) and **G4b's no-`Set-Cookie` half**. Still no mutating endpoint consumes it. | **YES** — `contracts.md` | quince#922 |
| **4** | **rule 1 and rule 3** — `PUT /api/auth/password` and passkey registration demand proof. **Carries the `quince.design.md` §6 edit.** G5, G6. | **YES** — `contracts.md` **+ design §6** | not open |
| **5** | **rule 2** — both removal paths, and the two lockout checks come **out** (D2). | **YES** — `contracts.md` | not open |
| **6** | **the UI prompt** — one component, used by every mutating control. | no | not open |
| **7** | **D8's copy** — lands with or after 4, never before. | no | not open |

**G4b IS THE ONLY GATE WHOSE HALVES BELONG TO DIFFERENT SLICES, AND IT WAS ASSIGNED TO NEITHER** —
review finding on quince#922. It appeared in the Gates list and in no row of this table, which is
how its second half nearly shipped untested: the session binding is slice 2's (quince#920 did it),
and the no-`Set-Cookie` assertion is slice 3's, because that is where the endpoint first exists.

**This is the defect quince#910 amended this table to prevent, one column over.** That PR's sentence
was *"an unassigned canon edit is the one that lands in whichever PR notices it last"*, and a gate is
the same shape. The rule generalises: **anything the Gates section names must appear in a row**, and
a gate spanning two slices is named in both rather than in the later one.

**§6 GOES IN SLICE 4 BECAUSE THAT IS WHERE THE MODEL ACTUALLY MOVES.** Rule 1 is the point at which a
session stops being sufficient to change the credential set; slice 5 completes the picture but
changes no statement §6 makes. Naming the slice matters more than which one it is — an unassigned
canon edit is the one that lands in whichever PR notices it last.

**2 IS SEPARATE AND HAS NO CALLER ON PURPOSE**, which is `qn.6m` slice 5a's ordering and `qn.6k`'s
before it: the guard lands, alone and reviewable, before anything can depend on it. It is also the
slice most likely to be got wrong, because D4's subject field is the whole of rule 2.

**5 COMES AFTER 4 BECAUSE IT REMOVES A GUARD.** Rule 2 makes the lockout checks redundant *given rule
1*; taking them out first would leave a window in which nothing prevents the quince#888 takeover.

**7 IS A SEPARATE SLICE RATHER THAN PART OF 4** so that the copy can be reviewed against the shipped
behaviour rather than against a promise, and so a wording objection does not hold up a security fix.

---

## What this rung does NOT settle

- **Whether the assert-then-create pair works on iOS.** G7. If it does not, the *shape* of adding a
  passkey changes and this spec is amended rather than worked around.
- **What an install that can present nothing should be told, beyond `quince auth reset`.** D5 makes
  the console the answer; whether the UI should offer anything more useful is a question this rung
  raises and does not answer.
