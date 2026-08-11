# qn.6k — passkeys: Face ID instead of a password typed on a phone keyboard

**Goal.** The Operator opens quince on their phone, taps the password field, picks the passkey from
the same dropdown that offers saved passwords, and is signed in with Face ID — with the password
still there, still working, and a console escape hatch that predates the first credential.

Rung issue: [quince#657](https://github.com/novkostya/quince/issues/657), **Operator ruling
2026-08-11: BUILD IT, as its own rung, BEFORE v0.1** — relayed by the architect seat on that issue,
which is the citable record. The ruling settles the login surface, recovery, and where setup lives;
those are cited below rather than re-litigated.

**Everything measured below was measured in this checkout at `b7f13e6`, 2026-08-11**, from this
runner box. Where a claim is **not** measured it says so in those words.

---

## The letter, checked rather than inferred

`qn.6j` ([quince#728](https://github.com/novkostya/quince/issues/728)) is the last allocated.
**`qn.6k` appears nowhere** — measured across both repositories: not under `docs/specs/`, not
elsewhere in `docs/`, not in product code, and not in the devlog's `roadmap.md`, `progress.md`,
`program/` or `decisions/`. `docs/specs/` holds `qn.6a … qn.6j` contiguously.

**ALLOCATING THIS LETTER SETTLES SOMETHING THE RULING DELIBERATELY LEFT OPEN, and that is stated
here so it can be overruled cheaply.** quince#657 and
[quince#726](https://github.com/novkostya/quince/issues/726) were each ruled *"its own rung,
immediately before v0.1"*, and the ruling says in as many words: *"The relative order of #726 and
this one is NOT ruled here … Whoever allocates the letters settles the order."* Taking `qn.6k` for
passkeys therefore places this rung **before** #726, which would take `qn.6l`.

**That ordering is an assumption, not a ruling.** It follows the only thing available to order them
by — quince#657 is `ready` with a build ruling and a settled design, and quince#726 is still
`needs-operator` — but a directory rename before this PR merges costs nothing, and reversing it after
code exists costs a great deal. **If the Operator wants #726 first, say so on this PR and the
rename is the whole of the change.**

Follows `qn.6j`'s precedent, which checked its own inferred letter rather than adopting it.

---

## Boundary

**In scope.**

- `core/internal/auth/` — WebAuthn registration and assertion, credential storage, rpId handling.
- `core/internal/store/migrations/0008_*.sql` — the credentials table.
- `core/internal/httpapi/` — four new endpoints, and their entries in the two exact-path allowlists.
- `core/cmd/quince/admin_cmd.go` — `quince auth reset`.
- `ui/src/features/auth/` — conditional mediation on the login form.
- `ui/src/features/settings/` — a passkeys surface: list, rename, remove, and the rpId hazard.
- `docs/contracts.md` §1 — additive, **and code-owned**; see the Rule check.

**Explicitly out of scope.**

- **Replacing the password.** Ruled: passkeys are an addition, never a replacement. The public demo
  ([quince#444](https://github.com/novkostya/quince/issues/444)) presets a shared password and a
  passkey cannot be shared.
- **`uiMode: "immediate"`.** A Chrome origin trial, renamed from `mediation: "immediate"` in the
  November 2025 spec update, with Safari and Firefox uncommitted. When Safari ships it this becomes
  a small additive change — which is the reason to build the conditional path now.
- **Multi-user / delegated access.** One admin, no accounts. That is what permits discoverable
  credentials and no account picker.
- **A printed recovery code.** Offered by the issue as option 3 and redundant given the console
  escape hatch.
- **Push, Lockdown Mode behaviour, and `qn.12`'s flow.** This rung makes the login half one tap; it
  does not build the notification half.

---

## Design

### D1. The dependency, re-read live at implementation time

Canon requires interface facts be looked up live rather than remembered, and the ruling explicitly
said to re-read this rather than trust its own figures. Measured 2026-08-11 from the GitHub API:

| | |
| --- | --- |
| `github.com/go-webauthn/webauthn` | **v0.17.4**, published `2026-05-22T12:32:17Z`, not a prerelease |
| repository | **not archived**, last push `2026-08-09T11:22:30Z`, 15 open issues, 1309 stars |
| licence | **BSD-3-Clause** |
| `go` directive | **1.25.0** — exactly quince's `core/go.mod`, so **no toolchain bump** |

**Pure Go, so `CGO_ENABLED=0` holds** — the property the release image depends on.

**What it costs, stated rather than discovered at review.** It pulls **nine direct dependencies**
(`fxamacker/cbor/v2`, `go-viper/mapstructure/v2`, `go-webauthn/x`, `golang-jwt/jwt/v5`,
`google/go-tpm`, `google/uuid`, `stretchr/testify`, `tinylib/msgp`, `go.uber.org/mock`). quince's
`core/go.mod` is currently lean. This is the largest supply-chain addition the project has made, and
the alternative — implementing WebAuthn verification by hand — is worse in every respect that
matters. Named so the diff does not surprise a reviewer.

### D2. rpId: derived from the origin, STORED per credential, and used to explain a mismatch

**The hazard the ruling names.** A credential is bound to a domain. Move between `qn.6f` access
tiers — reverse proxy to Tailscale, or a domain change — and **every passkey silently stops working
while the phone still lists them.** Nothing in the protocol warns about this.

**Decision.** The rpId is the effective domain of the origin serving the request, **and it is stored
on the credential row at registration.** At assertion, quince compares the current origin's rpId
against the stored one and, on a mismatch, returns a **named error saying which domain the
credential was registered for** — rather than a generic failure.

**Why not a config key.** A pinned `rp_id` that disagrees with the origin fails every assertion with
no way for the user to see why, and D12 says config carries what the user sets, not what the
software could derive. Deriving is also the only option that stays correct across the four tiers
without the user editing YAML.

**Why store it anyway.** Because deriving alone makes the failure *silent*, which is precisely the
hazard. Storing turns "your passkey stopped working" into "this passkey was registered for
`<domain>`; you are on `<other>`" — the difference between a mystery and a sentence. This is the
state-honesty rule applied to a credential.

**Two tiers cannot support passkeys at all** — self-signed at an IP (an IP cannot be an rpId, and a
certificate error is not a secure context) and the plain-HTTP opt-in. On those, the registration
surface must say so and refuse, rather than offering a button that cannot work.

### D3. The login surface — ruled, and reproduced here so the code has one source

- `navigator.credentials.get({mediation: "conditional", publicKey})` with
  `autocomplete="username webauthn"` on the field. **Non-modal**: the passkey appears in the same
  autofill dropdown as saved passwords. Safari 16+ on iOS/iPadOS 16+, which is this project's client.
- **Gated on `isConditionalMediationAvailable()`.** Unguarded it produces user-visible errors where
  it is absent.
- **The password field stays visible and functional**, because **there is no way to detect that a
  passkey is registered** — no API answers it, by design, since it would be a fingerprinting vector.
  An unconditional modal would therefore fire at users who have none.

**This lands on the field quince#819 just built.** That form already carries
`autocomplete="username"` on the `quince-admin` anchor; conditional mediation wants
`"username webauthn"` on the field the credential is offered against. The two are the same surface
and the spec that touches it must not undo the anchor —
[quince#824](https://github.com/novkostya/quince/issues/824) is live on that file and its measurement
(Safari programmatically focuses the anchor ~256 ms after mount) is the kind of thing this rung will
meet again.

### D4. The credentials table

`0008_passkeys.sql`. One row per credential: credential id (unique), public key, sign count, AAGUID,
transports, the **stored rpId** from D2, a user-chosen name, `created_at`, `last_used_at`.

**The user handle is a constant** — one admin, no accounts — and must be a **stable random
identifier generated once and stored**, never derived from the password, so that changing the
password does not orphan every credential.

**Sign count is stored and checked.** The library performs clone detection; a regression is a
signal, and per the no-silent-fallback rule it must surface rather than be swallowed.

### D5. Rate limiting and the pre-auth allowlists — the part that is easy to miss

Two of the four endpoints are **pre-auth by definition** (assertion begin/finish); two require an
authenticated session (registration begin/finish), which is why registration lives in Settings.

**The two pre-auth endpoints must be added to BOTH exact-path allowlists in `contracts.md` §1** —
the pre-auth exemption list *and* the storageless-reachable list. Missing the second means **a
passkey cannot be used to sign in on a storageless install**, which is exactly the onboarding state
where the ruling says passkeys are offered. Both lists are deliberately by-exact-path, because *"a
prefix would silently widen this every time a route is added"*.

**Assertion shares the existing per-IP login rate limiter** (`core/internal/auth/ratelimit.go`).
A passkey endpoint that is not rate limited is a bypass of the limiter on the endpoint beside it.

### D6. `quince auth reset` — a precondition, not a feature

**Ships in the first code slice, before any credential can be issued.** Ruled. Root on the box is
already total access, so it adds no attack surface; it mirrors the break-glass shape canon uses for
the Operator's Mac.

Lands in `core/cmd/quince/admin_cmd.go` beside the existing `qn.4b` operator escape hatches, which
are CLI-only with no REST surface — the same shape.

**It must state what it does and does not clear**, and the honest split is: it clears the admin
password so a new one can be set, and it **removes every passkey**, because a credential list the
locked-out user cannot reach is not recovery. A reset that left them would leave the box
authenticatable by a phone that is, by hypothesis, lost.

---

## Stories

1. **`quince auth reset` on the host clears the admin password and every passkey**, prints what it
   cleared, and leaves the daemon in `needs_setup`. Ships before any credential can be issued.
2. **A signed-in admin registers a passkey from Settings**, names it, and sees it listed with its
   creation time and the domain it is bound to.
3. **The registration surface states the rpId hazard where the credential is created** — that moving
   between access paths breaks every passkey while the phone still lists them.
4. **On an access tier that cannot support passkeys** (IP + self-signed, plain HTTP), the
   registration surface says so and offers no button that cannot work.
5. **The admin signs in with a passkey** on the login form: tap the field, the passkey appears in the
   autofill dropdown beside saved passwords, Face ID, in. The session cookie and CSRF double-submit
   are unchanged.
6. **The password still works, always**, on the same form, with no passkey registered or with several.
7. **A passkey presented against a different rpId than it was registered for fails with a message
   naming the registered domain**, not a generic failure.
8. **Passkeys are listed, renamed and removed individually in Settings**; several devices per admin.
9. **A passkey is offered during onboarding** after the password is set, and skipping is normal.
10. **The conditional-mediation call is gated** and produces no user-visible error where the API is
    absent.

---

## Gates

Beyond `make gates`, `make gates-ui` and `make gates-ui-e2e`:

| # | Gate | How |
| --- | --- | --- |
| G1 | Registration + assertion round-trip, in Go | `go test ./core/internal/auth/...` against the library's own test vectors — no browser |
| G2 | The four endpoints match contracts | httpapi golden fixtures (`make gen-golden` pattern) |
| G3 | Both pre-auth endpoints are in BOTH exact-path allowlists | a test that asserts each list by exact membership, so adding a route cannot silently widen either |
| G4 | Assertion is rate limited | a test that trips the limiter on the assertion endpoint, not only on `/login` |
| G5 | rpId mismatch names the registered domain | unit test, both directions |
| G6 | `quince auth reset` clears password **and** credentials | CLI test against a seeded DB |
| G7 | Conditional mediation is gated | vitest: absent `isConditionalMediationAvailable` → no call, no error |
| G8 | The login form still works with no passkey | existing story-1 e2e, unchanged and passing |

### Owed gates — NEITHER can be run by an agent seat, and both have a named owner

| Gate | Question | Owner |
| --- | --- | --- |
| **G9** | **Does a real passkey register and assert from the Operator's iPhone**, end to end, against the staging stand's real domain? | **Operator**, a phone, ~10 min |
| **G10** | **Do passkeys survive iOS Lockdown Mode?** [quince#510](https://github.com/novkostya/quince/issues/510) establishes Lockdown Mode disables Service Workers and the Push API regardless of certificate. Apple's documentation does not name WebAuthn. **Unconfirmed either way.** | **Operator**, a phone |
| **G11** | **Does `.local` work as an rpId?** The ruling says *measure it; do not reason about it* — an rpId must be a registrable domain suffix of the origin's effective domain and must not itself be on the Public Suffix List; `.local` **is** on that list, which by the letter makes `host.local` registrable, and sources disagree. Needs a trusted certificate first or the origin is not a secure context at all. | **unassigned** — needs a `.local` origin with a trusted cert |

**G10 is upstream of more than this rung.** If passkeys survive Lockdown Mode and push does not, then
passkeys are the *more* available half of the `qn.12` phone-first story rather than the exotic one,
which changes what `qn.12` is built around. **G11 blocks nothing here** — it decides whether the
zero-config case is reachable, and the four tiers this rung supports do not depend on it.

Per `CLAUDE.md`, an unrun gate is declared unrun with its owner named. **None of G9–G11 blocks the
spec, and G9 blocks the rung being called done.**

---

## Fixtures

- **WebAuthn test vectors** from the library's own suite for G1 — registration and assertion
  responses, so no browser or authenticator is needed in CI.
- **A seeded DB with one passkey** for G6, so the reset gate proves removal rather than a no-op on an
  empty table.
- **No hardware fixture, and no transcript.** This rung touches no device path, so the
  every-bug-becomes-a-replay-fixture rule has nothing to bite on here.
- Test passwords remain `test`, per the secrets rule.

---

## Slices

| | | |
| --- | --- | --- |
| **1** | **this spec** | reviewed before any code exists |
| **2** | `quince auth reset` + the `0008` credentials table | **the recovery precondition — lands before any credential can be issued** |
| **3** | the four endpoints, rpId storage and mismatch error, rate limiting, allowlists + **`contracts.md` §1** | **needs Operator approval**, see Rule check |
| **4** | login: conditional mediation, gated | |
| **5** | Settings: list / rename / remove, the rpId hazard, the unsupported-tier refusal | |
| **6** | the onboarding offer | |

Sequenced from `main`, not stacked. Slice 3 carries the contracts edit and therefore a code-owner
approval that no architect verdict can substitute for.

---

## Rule check

- **Don't improvise architecture.** Every decision this rung rests on is ruled on quince#657 and
  cited, not re-derived. Where the ruling left something open — the rung *letter*, and with it the
  relative order against quince#726 — this spec **says so in its own section** and names the cheap
  reversal, rather than deciding quietly.
- **Contracts.** The four endpoints are **additive to `docs/contracts.md` §1**, which
  `.github/CODEOWNERS` routes to `@novkostya`. **An architect verdict structurally cannot satisfy
  that** — an App cannot be a code owner. Slice 3 needs Operator approval; this spec PR does not
  touch `contracts.md`.
- **Security.** This adds an authentication method. It does **not** change the session layer: a
  WebAuthn assertion sets the same `quince_session` cookie and the CSRF double-submit is untouched.
  The blast radius is the surface that has already had defects (quince#423, quince#372), which is
  the argument for keeping it small and for G3/G4 existing at all.
- **Secrets discipline.** A passkey public key is not a secret; the private key never leaves the
  authenticator. **No credential material is ever logged**, and the user handle is a stored random
  identifier rather than anything derived from the password.
- **State honesty.** D2 is this rule applied to a credential: a passkey that cannot work says which
  domain it was registered for. Story 4 refuses on tiers that cannot support it instead of offering
  a button that fails. G9–G11 are declared unrun with owners named.
- **No silent caps or fallbacks.** A sign-count regression surfaces rather than being swallowed. An
  unsupported access tier is stated, not silently degraded to password-only.
- **Config tidiness (D12).** **This rung adds no config key.** The rpId is derived rather than
  configured, which is D2's second argument and keeps `config.yml` carrying only what the user set.
- **Interface facts looked up live.** D1's library figures were re-measured from the GitHub API at
  spec time, 2026-08-11, rather than taken from the ruling — and the ruling asked for exactly that.
  The Go directive was read from the tagged `go.mod`, not assumed.
- **Docs are part of the diff.** Slice 3 carries the `contracts.md` edit in the same PR as the
  endpoints. Coverage is declared per PR with a known-untested list.
- **Never mutate a committed version / storage invariants.** **Not touched** — this rung reaches no
  storage path. Named because the Rule check asks for near-misses too, and `quince auth reset`
  touches the DB, which is app state and not a committed backup version.
- **Privacy.** A domain name is Operator-private under the privacy rule. **No real domain enters any
  committed file, fixture, test or PR text**; fixtures use `example.com`, and the rpId mismatch test
  uses two fictional names.
- **Subprocesses.** None added.
