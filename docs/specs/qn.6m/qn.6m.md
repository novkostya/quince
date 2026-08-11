# qn.6m — auth becomes its own page: password and/or passkey, and passwordless if you want it

**Goal.** First run asks for a password *and* offers a passkey on **one plain page**, not a card and
not two screens. Afterwards the same surface lives at `/settings/auth`, where the password can be
changed or **removed entirely** by someone who would rather sign in with their face. Signing out is
one tap and always visible.

Rung issue: [quince#841](https://github.com/novkostya/quince/issues/841), carrying **two Operator
rulings taken 2026-08-11** — **A**, the auth surface is a separate page linked from Settings rather
than a fourth block inside it; and **B**, **passwordless is allowed**, opt-in, never on the demo.
**B supersedes the quince#657 ruling** that *"a passkey is an addition, never a replacement"*.

Successor to [`qn.6k`](../qn.6k/qn.6k.md), from using the shipped version on hardware.

**Everything measured below was measured in this checkout at `094313f`, 2026-08-11**, from this
runner box. Where a claim is **not** measured it says so in those words.

---

## The letter, checked rather than inferred

`qn.6k` ([quince#657](https://github.com/novkostya/quince/issues/657)) is the last **allocated**
letter — `docs/specs/` holds `qn.6a … qn.6k` contiguously. **Neither `qn.6l` nor `qn.6m` appears
anywhere in this repository**, measured across `docs/`, product code and `bin/`; the only occurrence
of either string is `qn.6k.md:29`, which says quince#726 *"would take `qn.6l`"*.

**That is a FORECAST, not an allocation, and the distinction is why this rung takes `qn.6m` rather
than the vacant letter above it.** quince#841 states flatly that *"`qn.6l` is quince#726's"*, so the
letter is spoken for by two documents even though no directory holds it. Taking it here would
contradict merged, reviewed canon to close a cosmetic gap in a sequence.

**So `docs/specs/` will run `qn.6a … qn.6k`, `qn.6m` — with a HOLE at `qn.6l`, deliberately.** Named
here so the next reader does not file the gap as a lost spec, which is exactly the trap `qn.6k`
recorded for the journal's letter ids.

**WHETHER THIS IS A RUNG AT ALL IS THE OPERATOR'S, AND quince#841 SAYS SO IN AS MANY WORDS.** The
alternative is a `§8` amendment to `qn.6k`'s spec. This document assumes the rung, following
`qn.6k`'s own precedent — allocate, state the assumption in a section of its own, and name the cheap
reversal. If the Operator wants an amendment instead, or wants `qn.6l`, the rename is the whole of
the change.

**THE QUESTION IS STILL UNANSWERED, AND THE ESCAPE HATCH THIS PARAGRAPH OFFERED HAS CLOSED.** It read
*"a directory rename before this PR merges costs nothing"* — and this spec merged as quince#842,
after which six more PRs cited `qn.6m` in their titles, bodies and commit messages, and
`docs/contracts.md` gained two blocks naming `qn.6m D3` and `D4`. A rename is now a change across
merged canon and a set of unrewritable commit messages rather than a `git mv`.

**That is not an argument for leaving it undecided — it is the cost of the decision, stated where it
was previously understated.** Nothing here is irreversible; the letter is a label, the code does not
read it, and correcting it later costs a documentation sweep rather than any behaviour. Recorded
because a sentence promising a cheap reversal, left standing past the moment it was cheap, is the
defect this project files most often.

---

## Boundary

**In scope.**

- `core/internal/auth/` — the password becomes mutable; "configured" is redefined against passkeys.
- `core/internal/httpapi/` — three new endpoints, the demo stand-in, and the exact-path lists.
- `docs/contracts.md` §1 — **two existing endpoints change meaning**, plus additions. **Code-owned**;
  see the Rule check.
- `ui/src/pages/` — the auth pages; `OnboardingPasskeyPage` is **deleted**.
- `ui/src/features/auth/` — `PasswordForm` becomes a page rather than a card.
- `ui/src/routes/` — `/settings/auth`, and sign-out in the shell.

**Explicitly out of scope.**

- **The rest of the first-run redesign.** `/onboarding/https` and `/onboarding/storage` keep their
  shape; this rung makes the auth step their sibling, not the other way round.
- **Multi-user, delegated access, per-device passkeys for other people.** quince#841 names that as
  the Operator's actual destination — *"password-less single-device view passkeys"* — and it is a
  different rung with a different security model. This one keeps **one admin, no accounts**.
- **A printed recovery code.** Refused in `qn.6k` and not reopened; the console hatch is the answer.
- **Password strength policy.** `minPasswordLen` is 1 today and stays 1. Changing it is a separate
  argument and would break the `test` fixture password the secrets rule mandates.
- **Session-layer changes.** A passkey login already issues the same cookie; nothing here touches it.

---

## Design

### D1. A page, not a card — and the measurement is the argument

Ruling A. Measured in this checkout:

| surface | shape |
| --- | --- |
| `OnboardingHTTPSPage` | `mx-auto w-full max-w-2xl`, no card |
| `OnboardingStoragePage` | `mx-auto min-h-dvh max-w-xl`, no card |
| `PasswordForm` (setup **and** login) | `w-full max-w-sm rounded-card border bg-card` |
| `OnboardingPasskeyPage` | `max-w-sm rounded-card` |

Two and two. The auth surfaces are the odd pair, and `OnboardingStoragePage`'s own header comment
already carries the reasoning for the shape they should adopt: *"A first-run step is a DESTINATION,
not an interruption. It wants a URL, so a reload returns here."*

**`max-w-xl`, matching the storage step rather than the HTTPS one.** Both are steps the user *acts*
on; the HTTPS page is prose the user *reads*, which is why it is wider.

**LOGIN IS NOT AN ONBOARDING STEP AND KEEPS ITS CARD.** Ruling A is about the setup surface and the
settings surface. `/login` is a recurring destination on an existing install, it holds two fields and
a button, and a full-width page for that is emptier rather than calmer. Stated because
`PasswordForm` is shared by both and the natural refactor drags login along with it.

### D2. Onboarding auth and Settings auth are SIBLINGS, not one component

Ruling A: *"they are not one component, because one has a session and one does not."*

That difference is not cosmetic. The onboarding page runs pre-auth against `POST /api/auth/setup`
and cannot offer removal, change, or a passkey list; the settings page runs authenticated, offers all
three, and must never offer first-run setup. A single component switching on `status` would carry
both sets of affordances in one tree, guarded by a boolean — which is how a surface that must refuse
an unauthenticated caller ends up one inverted condition away from not refusing.

**They share the LAYOUT primitive and nothing else.** A small `AuthPage` shell — the `max-w-xl`
wrapper, the `quince` wordmark, the heading — with each page owning its own fields, calls and copy.

### D3. "CONFIGURED" IS REDEFINED: A PASSWORD **OR** AT LEAST ONE PASSKEY

**This is the load-bearing decision of the rung, and without it ruling B is an authentication
bypass.** Measured in this checkout, all three in shipped code:

```
core/internal/auth/service.go:123   Status()      → needs_setup when NO PASSWORD ROW EXISTS
core/internal/auth/service.go:155   SetPassword() → 409 guard is HasPassword() + SetSettingIfAbsent
core/internal/httpapi/middleware.go:78   "POST /api/auth/setup" is authExempt — PRE-AUTH
```

Nothing in any of the three consults the credentials table. So the moment a passwordless install
exists, the password row is gone and:

1. `GET /api/auth/status` answers **`needs_setup`** to an anonymous visitor, and the UI's `SetupGate`
   shows them the first-run screen;
2. `POST /api/auth/setup` **succeeds** for that visitor — no session, no rate-limit obstacle beyond
   the shared bucket — and `issueSessionResponse` **logs them straight in as the admin**.

Contracts §1 promises this endpoint *"can never be an unauthenticated password reset"*. Passwordless
falsifies that promise unless the definition of configured changes with it.

**Decision.** `configured` = a password hash exists **OR** the credentials table is non-empty.

- `Status()` returns `needs_setup` only when **neither** exists.
- `SetPassword()`'s first-run guard returns `ErrAlreadyConfigured` when **either** exists.
- `SetSettingIfAbsent` stays the atomic authority for the password row; the passkey check is an
  additional refusal in front of it, exactly as the `HasPassword()` short-circuit already is.

**COUNTED WITHOUT AN rpId FILTER, and that is the opposite of D4's rule.** `existingCredentials`
filters by rpId, because a credential bound elsewhere cannot *sign in* here. This check is not about
signing in — it is about whether this install has ever been claimed. A quince reachable at two
addresses whose only passkey is bound to the other one must **not** offer first-run setup to a
stranger at this one; it must offer login, which then fails honestly with the rpId mismatch message
`qn.6k` D2 built. **Filtering here would reopen the takeover through the second address.**

### D4. The password becomes mutable — three operations, three different guards

| | guard | why |
| --- | --- | --- |
| **set** (first run) | pre-auth, one-shot, 409 once **configured** (D3) | exists; D3 widens the 409 |
| **change** | session **+ the current password** | a stolen session must not be able to lock the owner out permanently |
| **remove** | session **+ at least one passkey for THIS rpId** | removing the last credential would leave the install `needs_setup`, i.e. open |

**Change requires the current password even though the caller already holds a session.** The session
is proof of a past authentication, not of present possession, and the one irreversible thing an
attacker can do with a stolen cookie is change the password and keep the owner out. It costs the
legitimate user one field they already know.

**On a passwordless install, "change" IS "set", and the same endpoint serves it** with
`current_password` absent. A separate add-a-password endpoint would be a fourth spelling of one idea,
and the state that decides which spelling applies is server-side anyway.

**Remove counts credentials for the CURRENT rpId** — the inverse of D3, deliberately, and for the
reason D3 gives: this question *is* about signing in. Removing the password while holding only a
credential bound to `other.example.com` locks the user out of this address, and the refusal must say
which domain the credentials it found belong to rather than answering "no passkeys".

### D5. First-run passwordless needs a pre-auth registration path, and it gets ITS OWN exact paths

**Passkey registration is `SESSION REQUIRED` and first run has no session.** So the combined screen
can offer *password plus a passkey* with no new surface — setup returns a session, registration runs
immediately after on the same page — but it **cannot** offer *passkey instead of a password* without
somewhere pre-auth to register against.

**Three candidates, and two are wrong.**

- **Exempt the existing registration pair.** Rejected. `authExempt` is by exact path *and
  unconditional*; making membership depend on `needs_setup` puts a state test inside the one
  structure whose value is that it has none. Contracts §1 says the lists are exact-path because *"a
  prefix would silently widen this every time a route is added"*, and a conditional does worse.
- **Generate a throwaway password, register, then delete it.** Rejected, and it is the tempting one
  because it needs no new endpoint. A password the user never sees exists between two calls, and if
  registration fails in that window they are locked out of their own install by a credential they
  cannot type. That is a lockout bug built on purpose.
- **A distinct pre-auth pair, one-shot like setup itself.** Taken.

```
POST /api/auth/setup/passkey/begin
POST /api/auth/setup/passkey/finish?ceremony=<key>&name=<label>
```

**Pre-auth by exact path, unconditionally in the lists, and 409 `already_configured` once D3 says
configured** — which is precisely the shape `POST /api/auth/setup` already has and has had since
`qn.1`. First run is first-come-first-served for the password today; this makes it first-come-first-
served for a credential on the same terms, so it adds no exposure that setup does not already carry.
`finish` issues a session, exactly as setup does.

**They join all three lists** — pre-auth exemption, storageless-reachable, and CSRF exemption —
for the same three reasons the assertion pair does, and `passkey_allowlist_test.go` asserts them in
both directions.

### D6. The demo refuses by not being wired, and the seam already exists

quince#841: *"quince has no demo flag at the API layer"*, and it must not gain one. Measured — there
is exactly **one** `httpapi.Deps{}` literal, `core/cmd/quince/main.go:285`, it has `demoMode` in
scope, and it already passes a demo-conditional helper: `StorageRequired: storageRequired(demoMode,
cfgSvc)`.

So:

```go
PasswordAdmin: passwordAdmin(demoMode, authSvc),   // nil in demo
```

and `NewRouter` installs `UnavailablePasswordAdmin{}` beside the four stand-ins already there
(`server.go:94–111`). It refuses with **503 and a stated reason** — *the public demo presets a shared
password, so it cannot be changed here* — which is the no-silent-fallback rule: the surface says why
rather than hiding the button.

**`DELETE` is refused by the same stand-in**, so the demo cannot be made passwordless either. Ruling
B says *never on the demo* and means both halves.

### D7. Passwordless states its cost ON THE SCREEN, not in the docs

quince#841 is explicit: *"it should be said on the screen that offers passwordless, not only in
docs"*, and it names the precedent — this rung's predecessor already puts the rpId hazard where the
credential is created.

Two sentences, both true, both measured facts about this build rather than warnings in general:

- **`quince auth reset` on the host becomes the only way back in**, and it clears every passkey and
  every session as well as the password (`auth/reset.go`).
- **That needs console or SSH access to the box.** A headless or remote install with no such access
  is unrecoverable.

**And the cost is owed a second time, in `qn.6k`'s own words.** `quince auth reset`'s partial-failure
path was **declared untested** on quince#827 and is untested still. That was acceptable for a
backstop. Ruling B makes it the primary recovery path, so **G6 below promotes it** rather than
leaving the declaration standing under a heavier load.

### D8. Sign out goes in the shell, not on the auth page

quince#841 leaves this rung-local and names putting it in the sidebar as a legitimate answer.

**Taken: the sidebar.** Signing out is a navigation action, not a setting — every product this one
resembles puts it in the chrome — and burying it two clicks deep inside Settings → Auth means the one
control a shared-screen user reaches for in a hurry is the hardest to find. It also keeps the auth
page a page about *credentials*, which is what makes ruling A's separation legible.

**The whole path exists and nothing enters it.** Measured: `POST /api/auth/logout` is in
`contracts.md` §1 and in the storageless-reachable list (`middleware.go:176`); the client wrapper
`logout()` is in `ui/src/lib/auth.ts:24`; and **nothing in `ui/src/` or `ui/e2e/` calls that
wrapper.** So this slice is a button and a query-cache reset, not a new capability — which is why it
is slice 2 and carries no contract.

---

## Stories

1. **First run is one page**: a password field and, where the device supports it, an offer of a
   passkey — both on one screen, no card, no second step.
2. **The user sets a password and adds a passkey in one pass**, without navigating between them.
3. **The user sets a password and skips the passkey**, which is normal and unremarked.
4. **The user goes passwordless at first run** — a passkey and no password — and the screen states
   what that costs before they choose it.
5. **On a device or tier that cannot do passkeys**, the screen offers a password only, and says why
   rather than showing a button that cannot work.
6. **A signed-in admin changes the password** at `/settings/auth`, giving the current one.
7. **A signed-in admin removes the password** at `/settings/auth`, having at least one passkey bound
   to this address, and is told what it costs first.
8. **Removing the last usable password-alternative is refused** with a message naming what is
   missing — not a generic failure, and not a lockout.
9. **A passwordless install offers LOGIN, never first-run setup**, to an anonymous visitor — including
   when its only passkey is bound to a different address.
10. **On the public demo, changing or removing the password is refused with a stated reason**, and the
    surface says so rather than hiding.
11. **Sign out is one tap from anywhere in the shell** and returns to `/login`.
12. **quince#840 stops existing**: `OnboardingPasskeyPage` is deleted, and with it the bug that it
    never rendered.

---

## Gates

Beyond `make gates`, `make gates-ui` and `make gates-ui-e2e`:

| # | Gate | How |
| --- | --- | --- |
| G1 | **A passkey-only install refuses `POST /api/auth/setup`** with 409 | Go handler test: seed one credential, no password row, assert 409 and **no session cookie** |
| G2 | **A passkey-only install answers `needs_login`, not `needs_setup`** | Go test on `Status()`, and one with the credential bound to a *different* rpId (D3's no-filter rule) |
| G3 | Change requires the current password | Go: wrong current → 401, right → 204, and the old password stops working |
| G4 | Remove refuses with no passkey for this rpId | Go, both directions, message names the domain the credentials it found belong to |
| G5 | The demo refuses change **and** remove, with a reason | Go, against a `Deps` with `PasswordAdmin` nil |
| G6 | **`quince auth reset` partial-failure path** — promoted from declared-untested by D7 | CLI test with a store that fails on the second of the three statements; assert the report names what went |
| G7 | The three new paths are in the right exact-path lists, and the registration pair still is not | extend `passkey_allowlist_test.go`, both directions |
| G8 | First-run setup passkey endpoints are one-shot | Go: after configure, both return 409 |
| G9 | The combined screen renders password-only where passkeys are unsupported | vitest |
| G10 | Sign out clears the session and lands on `/login` | e2e, added to the story-1 spec |
| G11 | The existing login path is untouched | existing e2e, unchanged and passing |

### Owed gates — an agent seat cannot run either, and both have a named owner

| Gate | Question | Owner |
| --- | --- | --- |
| **G12** | **Does first-run passwordless work end to end on the Operator's iPhone** — register at setup, close the tab, sign in with Face ID and no password in existence? | **Operator**, a phone, ~10 min |
| **G13** | **Does `quince auth reset` actually recover a passwordless install** on the staging stand — reset, then set a password again through the first-run screen? | **Operator**, console access |

**G13 is not ceremony.** Ruling B accepts a cost whose entire mitigation is that command, and nobody
has run it against an install that had no password to begin with. **G12 and G13 block the rung being
called done**; neither blocks a PR.

---

## Fixtures

- **A seeded DB with one passkey and no password row** — the passwordless install, needed by G1, G2
  and G4, and the state no existing fixture produces.
- **A second seeded credential bound to another rpId**, for G2's no-filter case and G4's message.
  **Two fictional domains**, per the privacy rule; `example.com` and `other.example.com`.
- **A failing store seam for G6**, failing on the second statement so the first has already committed.
- Test passwords remain `test`.
- **No hardware fixture and no transcript** — this rung reaches no device path.

---

## Slices

Sequenced from `main`, **not stacked**.

| | | contracts? | |
| --- | --- | --- | --- |
| **1** | **this spec** | no | quince#842, **merged** |
| **2** | **sign out in the shell** (D8) — wiring an endpoint that already existed and had no caller | no | quince#843, **merged** |
| **3** | **the auth screens become plain pages** (D1, D2) — the `AuthPage` shell; login keeps its card | no | quince#844, **merged** |
| **4** | **the combined first-run screen**: password + optional passkey, one page. **Deleted `OnboardingPasskeyPage`; closed quince#840** | no | quince#845, **merged** |
| **5a** | **`configured` = a password OR a passkey** — D3 alone | **YES** | quince#847, **merged** |
| **5b** | **the password becomes mutable** — change + remove, the demo stand-in | **YES** | quince#851, **merged** |
| **6a** | **`/settings/auth` exists** (ruling A) — the page, and the passkeys card moves onto it | no | quince#853, **merged** |
| **6b** | **the password controls on that page** — consumes 5b | no | quince#856, **merged** |
| **7** | **first-run passwordless** — D5's pre-auth pair, `contracts.md`, and the offer on slice 4's screen | **YES** | quince#858, **merged** |

**ALL NINE ARE MERGED. THE RUNG IS CODE-COMPLETE AND IT IS NOT DONE**, and the gap between those two
sentences is the whole of what remains: **G12 and G13 have never been run.** No automated gate on this
rung touches a real authenticator — vitest mocks the ceremony, and e2e cannot reach a secure context,
which story 1 now asserts outright rather than leaving as an assumption. So every green check here is
consistent with the feature being **inert**, which is exactly how `qn.6k` shipped in nine green pull
requests. See *Owed gates* below: both have a named owner and neither is reachable from an agent seat.

**SEVEN SLICES BECAME NINE, AND THIS TABLE STILL SAID SEVEN AFTER SIX OF THEM HAD MERGED.** Corrected
here rather than quietly, because `CLAUDE.md` names a status table as the second thing that describes
the whole and is therefore stale by default after every flip — the defect quince#408 and quince#409
were filed for, arriving in the document that cites them. Both splits were deliberate, both were made
at build time, and both contradict what this section originally argued, so the arguments are corrected
rather than deleted.

**5 SPLIT, AND THE PARAGRAPH ARGUING IT SHOULD NOT WAS RIGHT ABOUT A DIFFERENT CUT.** It said *"change
and remove are one claim, and D3's redefinition belongs to neither of them alone — splitting it would
leave the security change homeless."* That is true of splitting **change from remove**, which was not
done. The cut actually made was **D3 away from both**, which gives the security change its own home
instead of taking one away: 5a lands the guard, alone and reviewable, **before anything can create the
state it defends against**. Same ordering ruling `qn.6k` used to put `quince auth reset` ahead of the
first credential.

**6 SPLIT FOR A REASON THAT DID NOT EXIST WHEN THIS WAS WRITTEN.** The settings page consumes 5b's
endpoints, and 5b sits behind a code-owner review that 6 does not need. Building both together meant
either stacking on 5b's branch — which `CLAUDE.md` §1 rules against — or landing UI whose buttons 404
if the merge order slips. So 6a ships the page and the card move, calling **no endpoint that does not
already exist**, and 6b adds the controls once 5b lands.

**Slices 2, 3, 4, 6a and 6b do not wait behind a code-owner approval**, which is what quince#841
asked for.

**7 is separate from 4 on purpose.** Slice 4 delivers a genuinely combined screen using only surfaces
that exist; the *passwordless* option on it needs D5's endpoints, so it arrives with them rather than
holding the whole screen behind a code-owner review.

---

## Rule check

- **Don't improvise architecture.** A and B are ruled on quince#841 and cited rather than re-derived.
  D3 is **not** a gap left open: quince#841 ruled the feature, and the mechanism that keeps it from
  being a bypass is rung-local detail written into this spec and into `contracts.md` — which is the
  ruled route for a contracts change, not a `PROPOSED (gap)` block. D5 and D8 are the two places the
  ruling explicitly left rung-local, and both are decided here with the alternatives recorded.
- **Contracts.** Slices 5 and 7 touch `docs/contracts.md`, which `.github/CODEOWNERS` routes to
  `@novkostya`. **An architect verdict structurally cannot satisfy that** — an App cannot be a code
  owner. **Two existing ruled endpoints change meaning** (`GET /api/auth/status`, `POST
  /api/auth/setup`), which is more than the additive edit `qn.6k` made, and slice 5's PR body must
  say so in its first line. This spec PR touches no contract.
- **Security.** This rung changes the authentication model. D3 is the whole of the risk and is stated
  as a measured defect-in-waiting with file and line, not as a caution. G1 and G2 exist to make it
  impossible to ship slice 5 without it. The session layer is untouched.
- **State honesty.** D7 puts the cost of passwordless on the screen that offers it. G6 promotes a
  gate rather than letting a declared-untested path silently take on a heavier job. G12 and G13 are
  declared unrun with owners named, and the rung is not done without them.
- **No silent caps or fallbacks.** The demo refuses with a reason (D6). An unsupported tier says so
  (story 5). A refused removal names what is missing (story 8, G4).
- **Secrets discipline.** No credential material is logged. The current-password check in D4 reaches
  the server over the same POST body the login path already uses — never argv, never a query
  parameter, unlike the ceremony key and label, which are not secrets.
- **Config tidiness (D12).** **This rung adds no config key.** Whether an install is passwordless is a
  fact about the database, not a setting, and a `passwordless: true` key would be a second source of
  truth for something `configured` already answers.
- **Interface facts looked up live.** No new dependency. `qn.6k`'s `go-webauthn` pin is unchanged and
  is not re-litigated here.
- **Docs are part of the diff.** Slices 5 and 7 carry their `contracts.md` edits in the same PRs as
  the code. Coverage declared per PR with a known-untested list.
- **Never mutate a committed version / storage invariants.** **Not touched** — this rung reaches no
  storage path. Named because the Rule check asks for near-misses, and the auth DB is app state
  rather than a committed backup version.
- **Privacy.** No real domain, hostname, address or path enters any committed file, fixture or PR
  text; D3's and D4's examples are `example.com` and `other.example.com`. `make privacy-check` before
  every push.
- **Subprocesses.** None added.
- **Every bug found on hardware becomes a replay fixture.** quince#840 is a UI bug on no device path,
  so it has no transcript to become; slice 4 deletes the page instead, which quince#841 ruled is the
  fix.
