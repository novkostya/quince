# qn.6o — the server says what would satisfy it, and one surface asks for it

**Goal.** When a credential-changing operation is refused for want of a present credential, the
**server states which factors would satisfy it**, and the UI asks for one of them in **a single
challenge** — the same challenge everywhere. No surface derives what is acceptable, and no surface
invents its own affordance for asking.

**It opens by repairing a regression `qn.6n` shipped**: on a password-only install, **adding the
first passkey is impossible**. Operator-measured on the staging stand, 2026-08-14.

Operator ruling, 2026-08-14, taken in session across three exchanges: the `accepts` shape, the single
challenge, and the placement of the name field. Each is attributed at its decision below.

---

## The letter

`qn.6o` is free — measured across `docs/`, `core/`, `ui/src` and `bin/`: the string appears nowhere.
`qn.6n` is this rung's predecessor and is complete; `qn.6l` remains the documented hole.

**This is a rung and not a `qn.6n` amendment.** It adds a field to the error envelope — which is
every error in the product, not one endpoint — retires a surface, introduces a shared one, and
changes what a client is responsible for deciding. `qn.6n` applied the same test to itself and came
to the same answer.

---

## The regression, stated first because it is the reason to start

**Before `qn.6n`:** `POST /api/auth/passkeys/register/begin` demanded nothing. Add a passkey worked
on every install.

**After rule 1:** it demands a present credential. `contracts.md` documents `current_password` as the
lighter factor and the server honours it — **and no client has ever sent it.** So:

| install | adding a passkey today |
| --- | --- |
| password **and** a passkey | works — the client retries with an assertion |
| **password only** | **impossible** — nothing to assert with, and no field to type the password into |
| passwordless with a passkey | works |
| unclaimed (first run) | works — exempt by `Configured()` |

The middle row is the common one for anybody who has not yet made a passkey, which is everybody at
the moment they try to make their first.

**The cause is a pattern this project has now paid for three times**, and it is worth naming in a
spec rather than in a third commit message. Each time, a guard was verified where it was written and
never at the screen that calls it:

1. **quince#930** — rule 1 at `register/begin`; the first-run offer never presented the password.
2. **quince#930 review** — the fix was pinned at `registerPasskey` and not at the screen; reverting
   the call site left the suite green.
3. **here** — `AddPasskeyDialog` held its **own copy** of the ceremony, so neither the fix nor the
   retry reached it. `lib/webauthn.ts` exists because three copies of that ceremony once existed, and
   its header warns that *"a fourth surface would have copied it again."* One had.

---

## Boundary

**In scope**

- `core/internal/auth/` — computing the acceptable factors, and returning them on the refusal.
- `core/internal/wire/` + `core/internal/httpapi/` — the `accepts` field and the handlers that set it.
- `docs/contracts.md` — the error envelope and the endpoints that populate it. **Code-owned.**
- `ui/src/features/auth/` — one challenge surface.
- `ui/src/features/settings/Passkeys.tsx` — the inline add row; `AddPasskeyDialog` is retired.

**Out of scope**

- **The rules themselves.** Rules 1–3 are `qn.6n`'s and are not reopened. This rung changes how a
  client LEARNS what would satisfy them, never what satisfies them.
- **`quince#931`'s parked half.** The other four dialogs stay as they are. The challenge is a
  *challenge* in that ruling's vocabulary, so it keeps no URL and does not touch the parked question.
- **`DELETE /api/auth/passkeys/{id}`'s existing fallback.** `qn.6n` slice 6b already ships a working
  password fallback there, inferred from `last_credential` + `has_password`. Folding it into the
  challenge is D8 and is deliberately a LATER slice, not a precondition.
- **The keychain prompt (quince#929).** Separate thread, separate ruling, unaffected.

---

## Design

### D1. The refusal carries what would satisfy it — Operator ruling

> *"Would it be feasible to make POST /api/auth/passkeys/register/begin with no credentials first,
> which can return specific error that challenge is required, with possible challenge types in error
> response body? Then web app does the challenge and repeats the request?"*

**Ruled yes.** The `401 reauth_required` body gains `accepts`, a list of the factors that would work:

```json
{"error": {"code": "reauth_required", "message": "…", "accepts": ["password", "passkey"]}}
```

**Computed for THIS operation, THIS address, and the credentials this install actually holds** — not
what the operation permits in principle. `password` appears only if a password exists; `passkey` only
if a credential exists that can assert at this rpId. Rule 2's two exclusions are applied here: the
password is absent for `remove_password`, and a target passkey does not count itself for
`remove_passkey`.

**This deletes the only part of the earlier design that made the implementer uncomfortable.** The
alternative required the CLIENT to encode which factors an operation accepts. That rule now has one
copy, on the side that enforces it.

**And it resolves a trade rather than choosing a side of it.** The two shapes considered were
*up-front* (predict the demand, show the field with the form) and *reactive* (ask, be refused, then
challenge). Reactive was worse on one axis only: it put `navigator.credentials.create()` two round
trips from the user's click, and losing user activation across an await is a **measured** failure on
this surface (`AddPasskeyDialog`'s own header records it). Under D1 the challenge's own submit is a
fresh gesture, so `create()` is one round trip from a real user action. Reactive stops costing
anything.

**AND THAT IS A CONSTRAINT ON SLICE 4, NOT A PROPERTY IT GETS FOR FREE** — architect, reviewing this
spec. A new click grants new transient activation, so the argument holds **only while the challenge's
submit reaches `create()` with at most the one `begin` round trip between them.** Verify the password
somewhere first, then `begin`, then `create()`, and the fresh gesture has already been spent on the
way — the identical failure one level in, and the one `AddPasskeyDialog`'s header records having been
bitten by.

**One await IS measured, which is what makes the shape viable rather than hopeful.** `registerPasskey`
does `begin` → `create()` inside a single handler, and `SetupPasswordPage` records that as working on
hardware: *"`registerPasskey` already issues `register/begin` in the same gap, and that ships and
works on hardware."* So slice 4's flow — challenge submit → `begin{factor}` → `create()` — is the
measured shape, not a new one.

**What slice 4 must not do is add a SECOND await.** Written down here rather than left to be
rediscovered on a device, because the failure is silent from the client's side: `NotAllowedError`,
indistinguishable from a user dismissing the sheet, which is exactly how it hid the first time.

### D2. It is guidance, and never a control

**The rules are enforced where the credential is presented.** A client that ignores `accepts` and
offers the password for `remove_password` is still refused by rule 2's subject comparison. If
acceptability were decided by this list, the guard would have moved to the client.

Stated in the contract, at the field, because the next reader will ask.

### D3. It discloses nothing new

`GET /api/auth/passkeys` already gives the same session `has_password` and the entire credential
list. The caller is the admin. Said explicitly rather than left to be re-derived, because an error
body that describes the install invites the question.

### D4. It is never empty; a dead end is a different refusal

*"Nothing this install holds can authorise this at this address"* already has a shape —
`last_credential`, carrying a sentence that names the remedy. Reusing it beats `accepts: []`, which
would make the client responsible for turning emptiness back into an explanation, and would put a
prompt with nothing in it one bug away.

So `accepts` is present-and-non-empty, or absent. **No client ever meets an empty challenge.**

### D5. One challenge surface, reusing `PasswordForm`

The challenge renders `PasswordForm` — which already carries the two things every password surface in
quince is required to have: the **credential anchor** (quince#819, so the keychain can tell these
apart) and a **real `<form>` submit**. It offers the password field and a *use a passkey* button
according to `accepts`, and returns `{current_password}` or `{proof}`.

**`passkeys` STAYS OFF.** That prop arms *conditional mediation* — browser autofill on load — and a
challenge must be modal. `lib/reauth.ts` already states the rule: *"ALWAYS MODAL, never conditional…
a non-modal request sitting in an autofill dropdown is for a login form nobody has committed to
yet."* The passkey button calls `proveWithPasskey`, which is that modal ceremony.

**It replaces affordances rather than adding one.** Without it, this rung would add a password field
to Add-a-passkey beside the bespoke fallback slice 6b already built for removal — a third ad-hoc way
of asking the same question. That is `quince#908` §4's *"four different kinds of thing wearing one
costume"*, arrived at one surface at a time.

### D6. The name moves to the page, and `AddPasskeyDialog` is retired — Operator ruling

> *"I don't want 2 dialogs in a row. Which means either custom dialog for passkey addition case, or
> move passkey name input to the page itself prior to challenge."*

**Ruled: the name goes on the page.** A custom dialog would be the third bespoke affordance; two
dialogs in a row is the thing being avoided.

**The name is not a challenge, so it does not belong in one.** It is a parameter of the action, like
the New-password field already sitting inline in `PasswordControls` on the same page — which makes
the two halves of `/settings/auth` consistent for the first time.

**Nothing forces the ordering**, which is worth recording so a later reader does not think it does:
the name is used only at `register/finish`, as a query parameter. It plays no part in `begin` or in
`create()`. Collecting it up-front is a UX choice.

The add row sits **below** the list: the list is what the user came to read, the action is what they
do after, and a new passkey then appears directly above the row that created it.

### D7. The add row does not use `border-dashed`

**In this product a dashed border means ABSENT or BROKEN**, consistently, across five sites:
`VersionList` (a **missing** backup — dashed, `opacity-80`, a `danger` badge), `StorageCard` (an
**unreachable** storage), and three empty states.

A dashed add-row on `/settings/auth` would therefore read as *a passkey that is broken* — the worst
possible misread on the one screen that warns when a credential no longer works at this address.

So the row takes the **same geometry** as the list items — same padding, radius, solid border — and
lets its **content** carry the difference: a text input and a button do not look like a row of text.
Cohesion without borrowing a signal that already means something else.

### D8. The label is visually hidden, not absent

The placeholder (`my iPhone`) is a **hint**, not a name: it disappears the moment the user types, and
a screen reader announces an unnamed field. The `<Label>` stays, visually hidden.

This codebase already pays that cost deliberately elsewhere — the `Device` anchor is labelled,
`readOnly` and `tabIndex={-1}` with a paragraph explaining why.

**The placeholder stays an EXAMPLE rather than an instruction.** *"Enter a name"* would occupy the
same space saying less.

### D9. The unsupported tier is designed for, not discovered

At a bare IP a passkey cannot be created at all, and today that is a disabled button plus a sentence.
An always-visible input must handle it too: **disabled, with the existing explanation**, never a
live-looking field that fails on submit. `qn.6g`'s rule — a remedy the user cannot follow is the same
defect as a silent failure.

---

## Stories

1. **A password-only install can add its first passkey.** Type the name, press Add, meet the
   challenge, type the password, get one authenticator prompt, and the passkey is created.
2. **An install with an existing passkey can add another** by asserting instead — the challenge
   offers both, and this is the path G7 tests.
3. **The challenge asks for exactly what the server listed** — no password field on a passwordless
   install, no passkey button where none can assert at this address.
4. **Removing the password never offers the password**, because `accepts` omits it (rule 2).
5. **A dead end shows a sentence, never an empty challenge.**
6. **Only one dialog appears** in the add flow, ever.
7. **The add row is legible as an action**, not as a broken passkey.
8. **A screen reader names the field** even after the placeholder is gone.
9. **At a bare IP the row is disabled and says why.**
10. **A client ignoring `accepts` is still refused** by the rule it tried to bypass.

---

## Gates

Beyond `make gates` / `make gates-ui` / `make gates-ui-e2e`:

- **G1** — `accepts` is computed per operation: table-driven over all four, on installs with
  password-only, passkey-only, both, and neither. Non-vacuity probed by returning a constant list and
  watching the table fail.
- **G2** — **rule 2's exclusions appear in the list**: `remove_password` never lists `password`;
  `remove_passkey` never lists `passkey` when the target is the only credential at this rpId.
- **G3** — **guidance, not control**: a request presenting a factor `accepts` did NOT list is still
  refused by the rule. This is the test that keeps D2 true.
- **G4** — a credential bound to another rpId does not put `passkey` in the list.
- **G5** — the challenge renders from `accepts` alone: no password field when unlisted, no passkey
  button when unlisted.
- **G6** — `passkeys` is not passed to `PasswordForm` by the challenge (D5). Asserted on the prop,
  because the failure — an autofill prompt on a dialog — is invisible in jsdom.
- **G7 (owner: Operator)** — **add a passkey on a password-only install, on hardware**: one
  authenticator prompt, no assert. This is the regression, and it is the story that cannot be proven
  in CI.
- **G8 (owner: Operator)** — **add a second passkey by asserting**, on hardware. This is `qn.6n`'s G7
  under its own name, still unrun, and this rung is what finally makes it reachable from a UI.

**Declared unrun until they are run.** `qn.6n` shipped seven green slices and its two hardware gates
are still owed; this rung starts by repairing a defect that every one of those gates was consistent
with.

---

## Fixtures

- No new transcript fixtures — no `idevicebackup2` path is touched.
- Go tests reuse `newConfiguredService` / `seedPasskey` and the `example.com` / `example.net` domains.
- UI tests reuse `renderControls`, `passkeyAt` and `192.0.2.10` (TEST-NET-1) for the bare-IP case.
- **`accepts` needs no fixture of its own** — it is derived from the store, so the existing seeds
  already produce all four combinations.

---

## Rule check

- **State honesty.** `accepts` reports what the install can actually do, computed at the moment of
  refusal. The empty case is a different refusal with its own sentence rather than a silent gap (D4).
- **No silent caps or fallbacks.** A dead end is stated; a disabled row says why (D9); a client that
  ignores the list is refused rather than quietly succeeding (D2/G3).
- **Docs are part of the diff.** `contracts.md` changes in the same PR as the field. Coverage is
  declared per slice with a known-untested list.
- **Don't improvise architecture.** The three decisions this rung rests on are Operator rulings,
  quoted at D1, D6 and (for the dashed border) taken as a finding in D7. Nothing here reopens
  `qn.6n`'s rules or `quince#931`'s parked half.
- **Privacy.** No new user-supplied value reaches a URL. The name is already a query parameter at
  `finish` and is unchanged; the challenge's credentials travel in bodies.
- **Secrets discipline.** Unchanged — passwords reach the server in a body and the core over a pty.
- **Interface facts looked up live.** The `PasswordForm` props, the five `border-dashed` sites and
  the `passkeys`/conditional-mediation behaviour were all read from the tree rather than recalled.

---

## Slices

Sequenced from `main`, **not stacked**.

| | | code-owned? | |
| --- | --- | --- | --- |
| **1** | **this spec** | no | *this PR* |
| **2** | **`accepts` on the wire** — the field, the per-operation computation, G1/G2/G3/G4. No client reads it yet. | **YES** — `contracts.md` | not open |
| **3** | **the challenge surface** — `PasswordForm`-based, driven by `accepts`, G5/G6. No caller yet. | no | not open |
| **4** | **the add row + retiring `AddPasskeyDialog`** — D6, D7, D8, D9. Where the regression actually closes. | no | not open |
| **5** | **fold slice 6b's removal fallback into the challenge** — D8's deferred half. | **YES** — `contracts.md` | not open |

**2 BEFORE 3 BEFORE 4, and the ordering is `qn.6n`'s lesson rather than taste.** That rung put a
server demand on `main` ahead of any client that could satisfy it, and the rule it wrote for itself
was *no slice may leave `main` with a demand no shipped client can satisfy*. Here the direction is
the opposite and the same rule applies: **`accepts` is additive and inert** — a client that ignores
it behaves exactly as today — so it can land first and be reviewed alone. The challenge lands next,
also inert. Slice 4 is the first one a user can see.

**5 IS LAST AND IS NOT A PRECONDITION.** The removal fallback works today. Folding it in is
consolidation, and bundling it with the repair would put a merged, working surface at risk inside the
PR that fixes a broken one.

---

## What this rung does NOT settle

- **Whether assert-then-create works on iOS.** G8, still owed. This rung makes it *reachable from a
  UI*; it does not answer it.
- **Whether the other four dialogs should become pages.** `quince#931`, parked by ruling.
- **The keychain save prompt.** `quince#929`, and the encryption page is its own thread.
- **Whether `accepts` should appear on refusals other than `reauth_required`.** Slice 5 answers it
  for the two removals; whether it generalises further is deliberately not decided here.
