# quince — cross-track contracts (v0)

> The frozen interfaces that let the core, UI, and vault tracks build in parallel.
> A contract change is a cross-track event: it lands here first, version-bumped, and
> every affected track gets a rung. Field additions are non-breaking; renames/removals
> are breaking and need Operator sign-off.
>
> Wire casing: `snake_case` JSON everywhere. Times: RFC 3339 UTC strings. IDs: ULIDs.

**BREAKING IS CHEAP HERE, AND THE CORRECT MODEL BEATS THE COMPATIBLE ONE.** Operator ruling,
2026-08-01 (quince#378): *"I am the only user atm and I see no reason to accumulate
backward-compatibility garbage."* The project is **pre-release with one operator**, and **the only
consumer of this API is the in-repo UI, shipped from the same commit** — a fact this document
already relies on elsewhere when it suits an argument (§`Op.kind`, the `wifi_sync` enum extension).
So when a change is between *the right shape* and *the shape that does not break a client*, take the
right shape. There is no client to break, and a compatibility path added now is one nothing will be
brave enough to remove in two years.

**Read the paragraph above this one accordingly.** *"Field additions are non-breaking; renames/
removals are breaking and need Operator sign-off"* still describes the **classification** and the
**sign-off**, and both stand — but it must not be read as a preference for additive changes. It was,
twice in one review: `qn.6c`'s gaps 1 and 3 were both first recommended on compatibility grounds, one
of them proposing a permanent implicit fallback to avoid editing a single YAML file on a single
machine. The Operator overruled it, and the architect's dissent is retracted on the record
(quince#378).

**IT APPLIES TO INTERFACE SHAPE, NOT TO PERSISTED STATE, AND THAT IS THE LOAD-BEARING LIMIT.** The
premise is *the only consumer ships from the same commit*. **A `quince-version.json` written last
month has no commit to ship with.** Neither does a `versions/<ts>/` directory, a `@quince-*` snapshot,
or a SQLite row behind a committed backup. For **data at rest the premise is simply false** — a
breaking change there is a **migration against data that cannot be regenerated**, and *never mutate a
committed version* is a hard rule sitting on the other side of it. Wire shape, config surface and
error codes are cheap to break; the things a backup is made of are not.

**This matters right now rather than in the abstract.** `qn.6c` — the rung that produced this ruling —
**is a data-model change**: multi-storage in the DB (`0006_storage`) plus `quince-storage.json` on
disk. A session designing that migration while reading *"breaking is cheap here"* is reading a true
statement in a context where its premise does not hold. Gap 4's *"written into a root that already
holds committed versions"* is exactly the class this limit governs.

**What this does NOT license.** It is not an argument against migrations, against `PROPOSED (gap)`,
or against Operator sign-off — a breaking change is still a **cross-track event** that lands here
first and still needs the ruling. It is cheaper, not free, and *"the UI ships from the same commit"*
is the whole of why. **The clause expires when that stops being true**: the first external consumer,
published API, or independently-deployed client retires it, and whoever adds one owns deleting this
paragraph.

**That "not an argument against migrations" sentence was too weak and is kept only as the record of
why** — it framed the limit as **process** (*still do the migration*) when the real point is that the
cheapness argument's **premise does not reach data at rest at all**. Doing the migration carefully is
the consequence; the premise failing is the reason. Caught by the supervisor seat on review, after the
paragraph had already been approved by the architect.

**One worked example, so the line is not read as "always break".** `qn.6c` gap 1 keeps
`Version.backend` — and that is *not* the compatibility reflex. A version can outlive its storage
once remove-a-storage exists, leaving `storage_id` dangling and the backend unrecoverable by join, so
the field is not derivable in all futures. It is a distinct fact, kept on modelling grounds. **The
test is whether the field earns its place, not whether removing it would break someone.**

## 1. REST API (`/api`)

Auth (endpoints ruled in qn.1, Operator 2026-07-19):

```
GET  /api/auth/status  → {state: "needs_setup" | "needs_login" | "authenticated",
                          csrf_token}
     // first-run detection + reload-auth check + CSRF-token delivery in one call;
     // always reachable without a session.
POST /api/auth/setup {password}  → 200 {state, csrf_token} + session cookie
     // FIRST-RUN ONLY: 409 if this install is already CONFIGURED — setup succeeds exactly
     // once and can never be an unauthenticated password reset. Auto-logs-in on success.
     // 426 insecure_origin, BEFORE the password is stored (see below).
POST /api/auth/login {password}  → 200 {state, csrf_token} + HttpOnly session cookie
     // 401 on bad password; 429 when the per-IP login rate limit trips;
     // 426 insecure_origin, BEFORE the password is checked (see below).
POST /api/auth/logout            → 204, clears the cookie.
```

The admin password is MUTABLE (qn.6m D4; Operator ruling B on quince#841):

```
PUT    /api/auth/password {current_password, new_password}  → 204
       // SESSION REQUIRED, and the CURRENT password besides. A session is proof of a PAST
       // authentication, not of present possession, and the one irreversible thing an attacker
       // holding a stolen cookie can do is change the password and keep the owner out.
       // `current_password` IS OMITTED EXACTLY WHEN NO PASSWORD EXISTS — on a passwordless
       // install "change" IS "set", and the server decides which case applies from its own
       // state, so there is no client flag to get wrong. Where a password DOES exist, an empty
       // value is simply a wrong one and takes the same 401.
       // AND SINCE qn.6n IT TAKES `proof` AS AN EQUAL ALTERNATIVE — a token from
       // POST /api/auth/reauth/finish, minted for operation `set_password`. Either field
       // satisfies rules 1 and 3; the password is the lighter of the two and a passkey is what
       // an install with no password to type must use.
       // THE OMITTED-`current_password` CASE WAS PROVED BY NOTHING AT ALL until that ruling,
       // which quince#888 item 3 named: a stolen session could mint a credential the owner
       // could not revoke without console access. It now requires the passkey.
       // THE ONLY EXEMPTION IS AN INSTALL WITH NO CREDENTIALS AT ALL — `configured` false,
       // first launch or after `quince auth reset`. There is deliberately NO exemption for
       // "a credential exists but cannot be presented here": an attacker holding a stolen
       // session controls the Host header and could manufacture that state on demand, so the
       // waiver would hand them their own trigger. The remedy for it is `quince auth reset`.
       // 401 bad_password · 401 reauth_required when nothing usable was presented, carrying the
       // server's own sentence · 422 weak_password · 429 rate_limited — THE SAME BUCKET as
       // POST /api/auth/login, because it verifies a password and holding a session must not
       // buy a fresh budget to guess in · 503 unavailable on the demo (below).
DELETE /api/auth/password                                   → 204
       // SESSION REQUIRED. Makes the install PASSWORDLESS. Ruling B superseded qn.6k's "a
       // passkey is an addition, never a replacement"; `quince auth reset` is what carries the
       // recovery cost, and it needs console access to the box.
       // 409 last_credential when no passkey exists FOR THIS rpId. The message names the
       // address this request arrived on AND the addresses the credentials it did find belong
       // to — "no passkeys" at a box that visibly has some reads as quince being broken.
       // 503 unavailable on the demo (below).
```

**BOTH ARE IN NONE OF THE THREE EXACT-PATH LISTS** — the opposite of the assertion pair, the same as
registration: they need a session by definition. `PUT` in the pre-auth list would be an
unauthenticated password change, which is the thing `POST /api/auth/setup` promises never to be
arriving through another door; and `csrfExempt` is the one that looks harmless and is not, since
these are state-changing requests from an authenticated browser. Asserted by exact path **and by
method**, in `passkey_allowlist_test.go`.

**PROPOSED (gap): a PRE-AUTH CONFIG WRITE, for the one escape a stranded first-run user can take.**

<!-- gap-heading-check: ignore — the next marker in this file is the qn.6e FIRST-RUN STORAGE ruling
     ("RULED and IMPLEMENTED: the FIRST-RUN SETUP STATE"), a different question about a different
     subsystem, which this block neither cites nor depends on. This block's span ends at its own
     last paragraph and nothing in it is answered below: the pre-auth config WRITE is open. -->

Raised by the implementer seat 2026-08-13 while building quince#908; **nothing is built and nothing
here is decided.** That issue's slice 6 — the plain-HTTP confirm — is stopped pending a ruling.

**The dead end has no unauthenticated exit today.** On plain http at a LAN address
`refuseInsecureOrigin` refuses `POST /api/auth/setup` **before** examining the password, so no
credential can be obtained — and **no config-writing route is `authExempt`**; the list is nine
routes, all auth, health, or an onboarding *read*. quince#908 §3 rules that an actionable config
control **is** safe in `needs_setup` and only there, on the argument that `setup` is already
authExempt and one-shot, so anyone reaching the port can claim the install outright and a config
write grants strictly less than that. **That settles the policy and not the mechanism**, and the
mechanism is a contracts change: the first pre-auth **mutating** endpoint in this product that is not
about obtaining a credential.

**Why it is more than plumbing: the `needs_setup` bound must be enforced by the SERVER.** An attacker
does not use the UI, so a client-side gate is not a control. Without a server-side `Configured()`
guard, the same route on a *configured* install is an unauthenticated *turn off the transport
requirement* primitive — flip it, wait for the admin to sign in over plain http, read the cookie.
That is exactly the downgrade quince#908 §3 says must not be generalised, and it would arrive by
implementing the ruling carelessly rather than by disagreeing with it.

**The shape that seems to follow**, offered as the implementer's reading rather than a decision: a
narrow route — *not* `PUT /api/config` exempted, which would expose every key — writing only
`sessions.allow_insecure_transport`, guarded by `auth.Configured()` exactly as `POST /api/auth/setup`
is, closing the instant the install is claimed. `authExempt` is exact-path with no prefix support,
which the qn.6f ruling calls a constraint rather than a style, so this would be one more literal path.

**The questions that are the Operator's rather than the implementer's:** whether a pre-auth config
write should exist at all, when the alternative is telling the user to edit `config.yml` on the box;
whether `Configured()` is the right guard or the bound should be narrower; and whether such a route
belongs under `/api/onboarding/`, where the qn.6f ruling deliberately exempted **step 1 only, by
exact path**, so that no future onboarding step could be exempted by accident.

**THE rpId FILTER ON `DELETE` IS THE OPPOSITE OF `configured`'s, AND BOTH ARE CORRECT.** The two ask
different questions and the pair now lives in one file, so it is written here rather than left to be
re-derived:

| question | filter by rpId? | guards |
| --- | --- | --- |
| has this install been **claimed**? | **no** | first-run setup (D3, below) |
| can the user still **sign in** here? | **yes** | the lockout, on `DELETE` |

A credential bound to another domain cannot sign in at this address, so counting it on `DELETE`
would let somebody go passwordless and lock themselves out — with the phone still listing a passkey
that cannot help. That is `qn.6k` D2's hazard reached from the other side.

**NOT ON THE PUBLIC DEMO — and the refusal is an UNWIRED CAPABILITY, not an `if demo` branch.**
quince#841 is explicit that quince has no demo flag at the API layer and that no handler contains
such a branch. `PasswordAdmin` is left nil in demo mode and `NewRouter` installs
`UnavailablePasswordAdmin` beside the four stand-ins already there, so **both routes still exist and
refuse with 503 and a stated reason** rather than 404ing. The demo publishes a shared password on its
own login screen; one visitor changing it — or removing it against a credential only they hold —
would lock out every other visitor until the periodic reset.

**`CONFIGURED` MEANS A PASSWORD HASH **OR** AT LEAST ONE PASSKEY — qn.6m D3, and it is what keeps
`needs_setup` from becoming an unauthenticated admin takeover.** It is the predicate behind both
`needs_setup` above and the 409 on `POST /api/auth/setup`; neither consulted the credentials table
before 2026-08-11.

**No endpoint is added or removed by this and two change MEANING**, which is why it is written here
rather than left in the spec. On an install with a passkey and no password: `GET /api/auth/status`
answers **`needs_login`**, not `needs_setup`, and `POST /api/auth/setup` answers **409**.

Without it, the moment a passwordless install can exist the password row is gone, `status` tells an
anonymous visitor `needs_setup`, the UI shows them first run, and `setup` — **pre-auth by exact
path** — succeeds and issues them an admin session. That also falsifies the promise three lines
above, that setup *"can never be an unauthenticated password reset"*.

**THE PASSKEY COUNT IS NOT FILTERED BY rpId, WHICH IS THE OPPOSITE OF THE RULE EVERYWHERE ELSE IN
THIS SECTION.** Assertion and registration filter, because a credential bound to another domain
cannot be *used* here. This question is not about using one:

| question | filter by rpId? |
| --- | --- |
| can this credential **sign in** here? | **yes** |
| has this install ever been **claimed**? | **no** |

A quince reachable at two addresses, whose only passkey is bound to the other one, must offer
**login** at this address — failing honestly with `passkey_rp_mismatch` and the domain name — rather
than offering first-run setup to a stranger. **Filtering here reopens the takeover through the
second address.**

`quince auth reset` clears the password *and* every passkey, so it still returns an install to
genuinely unclaimed and first run stays reachable. That is load-bearing rather than incidental: it
is the whole of the recovery story ruling B leans on (quince#841).

Passkeys — registration (qn.6k; Operator ruling on quince#657, 2026-08-11):

```
POST /api/auth/passkeys/register/begin  → 200 {ceremony, options}
     // SESSION REQUIRED. `options` is the W3C PublicKeyCredentialCreationOptions verbatim, for
     // navigator.credentials.create(). `ceremony` is an opaque SINGLE-USE key with a 2-minute
     // TTL, held in memory and lost on restart — the remedy is to press the button again.
     // 409 passkeys_unsupported_here when this address cannot be a relying party: an rpId must
     // be a DOMAIN, so a bare IP is refused HERE rather than minting a credential that could
     // never be used.
POST /api/auth/passkeys/register/finish?ceremony=<key>&name=<label>  → 201 {passkey}
     // SESSION REQUIRED. THE BODY BELONGS TO THE AUTHENTICATOR — it is the credential response,
     // whose exact bytes are what the signature covers — so these two parameters are in the
     // query rather than alongside it.
     // 400 no_ceremony (expired or already used) · 400 passkey_rejected (failed verification)
     // 409 passkey_rp_mismatch (the ceremony began on a different domain) · 422 name_required
```

**The rpId is DERIVED from the request host, never configured.** A pinned value that disagreed with
the origin would fail every ceremony with nothing for the user to see, and deriving is the only thing
that stays correct across the four `qn.6f` access tiers without editing YAML. It is **stored on the
credential**, so a passkey presented on another tier can name the domain it belongs to instead of
failing the way "no credential here" fails — `docs/specs/qn.6k/qn.6k.md` D2.

**NEITHER REGISTRATION ENDPOINT IS PRE-AUTH, so neither joins any exact-path list.** Registration is
something an authenticated admin does.

First-run passkey registration — **a DIFFERENT pair, pre-auth and one-shot** (qn.6m D5):

```
POST /api/auth/setup/passkey/begin  → 200 {ceremony, options}
     // PRE-AUTH, and `POST /api/auth/setup`'s sibling: first run has no session because
     // creating one is what this does. 409 already_configured the moment Configured() is
     // true — a password OR any passkey — so it closes as soon as the install is claimed.
     // 429 rate_limited, THE SAME BUCKET as login and setup · 426 insecure_origin
     // 409 passkeys_unsupported_here, as the authenticated begin is.
POST /api/auth/setup/passkey/finish?ceremony=<key>&name=<label>
                                    → 200 {state, csrf_token} + session cookie
     // PRE-AUTH. ISSUES A SESSION, exactly as POST /api/auth/setup does — the caller has
     // just proved possession of a credential this install now holds.
     // THE RESPONSE IS AuthStatus, NOT the 201 {passkey} the authenticated finish returns:
     // this call's outcome is "you are signed in", not "here is a row you can manage".
     // 409 already_configured — RE-CHECKED HERE, not only at begin: two ceremonies can begin
     // on a virgin install, the credential write decides, and the loser must be refused
     // rather than silently adding a second admin credential to somebody else's box.
     // 400 no_ceremony · 400 passkey_rejected · 422 name_required · 409 passkey_rp_mismatch
```

**THIS PAIR IS IN ALL THREE EXACT-PATH LISTS, AND THE AUTHENTICATED PAIR IS STILL IN NONE.** They
look almost identical and belong on opposite sides of every list, which is why
`passkey_allowlist_test.go` asserts both directions by exact path.

**WHY A SECOND PAIR RATHER THAN EXEMPTING THE FIRST.** `authExempt` is exact-path **and
unconditional**, and that is its whole value; making membership depend on `needs_setup` would put the
first state test into the one structure that has none. The alternative considered and rejected was
generating a throwaway password, registering, then deleting it — which needs no new endpoint and
locks the user out of their own install if registration fails in the window, by a password they never
saw.

**AND WHY IT ADDS NO EXPOSURE.** First run is **already** first-come-first-served for the password:
anyone who can reach an unconfigured quince can call `POST /api/auth/setup` and own it. This makes it
first-come-first-served for a credential on the same terms and behind the same one-shot guard.
**What is NOT atomic** is the check-then-write across a ceremony pair, so two registrations begun
simultaneously on a virgin install can both finish; both belong to whoever was at the machine during
first run, and `quince auth reset` removes every credential.

Re-authentication — **a THIRD pair, session-required, minting a PROOF rather than a session**
(qn.6n D3, D4):

```
POST /api/auth/reauth/begin {operation, target?}  → 200 {ceremony, options}
     // SESSION REQUIRED, and therefore in NONE of the three exact-path lists — the opposite of
     // the assertion pair below, which is in all three. That placement IS the decision: those
     // routes are the least-guarded in the system, and a pair whose purpose is gating
     // privileged operations must not share them (D3).
     // `operation` is one of add_passkey · remove_passkey · remove_password · set_password.
     // A CLOSED SET, and `rename_passkey` is deliberately absent (D6).
     // `target` is the credential id, REQUIRED for remove_passkey and refused for the rest —
     // rule 2 compares the presented credential against it, and a target on an operation that
     // reads none is a binding that becomes decorative.
     // 422 bad_operation names what was wrong · 429 rate_limited, THE SAME BUCKET as login,
     // because holding a session must not buy a fresh budget to guess with (D7)
     // 409 passkeys_unsupported_here at an address that cannot be a relying party.
POST /api/auth/reauth/finish?ceremony=<key>       → 200 {proof}
     // SESSION REQUIRED, AND IT SETS NO COOKIE. `passkeys/login/finish` sets two and returns
     // {state, csrf_token}; this returns a token and nothing else. Issuing a session here
     // would make it a second login path reachable from an authenticated context, and would
     // bind the proof to a session id that is gone by the time the mutating call arrives.
     // THE PROOF CARRIES FOUR BINDINGS: single-use, the operation (with its target), the
     // CREDENTIAL THAT ASSERTED, and the session that BEGAN the ceremony. The third is what
     // lets rule 2 refuse a removal proven by the credential being removed; it cannot be added
     // later without changing this contract.
     // The ceremony's own rpId and session win over the request's — the authenticator signed
     // for the domain the challenge was issued on, and a ceremony finished by another client
     // is not a re-authentication of that client.
     // 401 unauthorized for a rejected OR unknown credential, deliberately indistinguishable
     // · 400 no_ceremony · 429 rate_limited · 409 passkey_rp_mismatch.
```

**NOTHING CONSUMES THE PROOF YET.** Slices 4 and 5 of `qn.6n` make the mutating endpoints demand
one; this pair is the way to obtain one, landed first so the two can be reviewed apart. A proof
minted today is spendable nowhere, which is the same shape `qn.6k` slice 2 shipped `InsertPasskey`
in and `quince auth reset` before it.

Passkeys — assertion (qn.6k):

```
POST /api/auth/passkeys/login/begin  → 200 {ceremony, options}
     // PRE-AUTH. Discoverable: no username, no account picker — one admin, no accounts.
     // 429 when the per-visitor login rate limit trips — THE SAME BUCKET as POST /api/auth/login,
     // because they are the same resource and the same attacker.
     // 426 insecure_origin, as the password login is.
POST /api/auth/passkeys/login/finish?ceremony=<key>  → 200 {state, csrf_token} + session cookie
     // PRE-AUTH. The SAME response shape as POST /api/auth/login: a passkey login IS a login, and
     // the session layer is untouched by this rung.
     // 401 unauthorized for BOTH an unknown credential and a rejected one — deliberately
     // indistinguishable, or the endpoint answers "does this quince know this passkey" to anyone.
     // 429 rate limited · 409 passkey_rp_mismatch · 400 no_ceremony
```

Passkeys — management (qn.6k):

```
GET    /api/auth/passkeys        → 200 {passkeys: [...], rp_id, supported, has_password}
       // SESSION REQUIRED. Each row: {id, name, rp_id, created_at, last_used_at|null}. NO public
       // key and NO credential material — the list exists so a human can recognise a device and
       // remove it, and sending more would widen what a compromised session can enumerate.
       // `rp_id` is what THIS request resolved to and `supported` is whether it can be a relying
       // party at all, so the surface can mark rows bound to another address, and refuse to offer
       // a button on a tier that cannot work, WITHOUT re-deriving the domain in the browser.
       // `has_password` is whether an admin password exists at all (quince#855) — see below.
DELETE /api/auth/passkeys/{id}   → 204
       // SESSION REQUIRED. 204 WHETHER OR NOT A ROW WENT: removing a credential that is already
       // gone is the state the caller wanted, and a 404 would make a retry, or a second tab, look
       // like a failure the user must act on.
       // 409 last_credential when the install has NO PASSWORD and this is the last passkey that
       // works at this rpId — the mirror of DELETE /api/auth/password's refusal, which this
       // endpoint shipped without (quince#888). Without it, password → passkey emptied the
       // credential set in two clicks, at which point `configured` is false, `auth/status`
       // answers `needs_setup`, and POST /api/auth/setup is pre-auth by exact path: anyone who
       // could reach the address completed first run.
       // THE SAME CODE AS THE PASSWORD PATH, not a second one: both mean "this removal would
       // leave you no way in", and a client already knows which endpoint it called. The two
       // messages differ, because the remedies do — there, add a passkey; here, set a password
       // or add another.
       // IT IS A CLAIM ABOUT THE RESULTING STATE, which is what keeps the 204 above intact: an
       // id matching no row changes nothing, so it cannot be the last credential.
PATCH  /api/auth/passkeys/{id} {name} → 200 {passkey}
       // SESSION REQUIRED. 404 when there is no such credential — UNLIKE delete, because the
       // caller asked for a specific end state that did not happen. 422 name_required.
       // THE NAME IS THE ONLY MUTABLE FIELD: everything else is the authenticator's or a fact
       // about creation, and a setter that could reach them would be a way to make the record
       // disagree with the credential it describes.
```

**`has_password` IS ON THIS ENDPOINT AND NOT ON `GET /api/auth/status`, WHICH IS THE OBVIOUS HOME AND
THE WRONG ONE** (quince#855). `auth/status` is **pre-auth**, so the field there would tell an
anonymous visitor whether this quince has a password. That is close to free today — the login screen
renders a password field either way — but it is a **disclosure decision rather than a field**, and
nobody has ruled it. This endpoint already requires a session, so it discloses only to somebody who
is already the admin, and no ruling is owed.

**It also fits what this payload has become.** `rp_id` and `supported` are not facts about the listed
credentials either; they are what the auth **surface** needs in order to render honestly, and so is
this. The cost is that the endpoint's name now under-describes its body — cheaper than a fourth auth
endpoint, and stated rather than left for a reader to notice.

**What it fixes.** `/settings/auth` said *"Change your password / Current password"* on a passwordless
install, where the field had to be left blank and nothing said so. `PUT /api/auth/password` already
handled that case correctly, so the defect was entirely in what the surface **claimed** — the
state-honesty rule applied to a form's own labels.

**If a PRE-AUTH consumer ever needs this**, that is a different question with a real disclosure
trade-off, and it should be ruled then rather than assumed now.

**THE ASSERTION PAIR IS IN THREE EXACT-PATH LISTS, NOT TWO** — the pre-auth exemption, the
storageless-reachable set (a storageless install is exactly where onboarding offers a passkey), and
**the CSRF exemption**, because no CSRF cookie exists before login. The third was not anticipated
when this section was first written; each omission fails differently and none of them fails
obviously, so all three are asserted by exact path in `passkey_allowlist_test.go`, in both
directions — the registration pair must stay OUT of all three.

Onboarding (qn.6f):

```
GET  /api/onboarding/https → {complete: bool, detected: "tls" | "forwarded_proto" | "none"}
     // PRE-AUTH — the fifth exempt route, and the only onboarding one, BY EXACT PATH.
     // complete = this origin is already encrypted, so nothing needs doing.
```

**RULED and IMPLEMENTED: the FIRST-RUN SETUP STATE — quince serves with no storage (`qn.6e`).**
Operator ruling 2026-08-07 on quince#502, option (a): **any zero-storage start IS the onboarding
state.**

quince used to **refuse to start** without a declared storage. On a fresh install `/data/config.yml`
does not exist, so the genuine first run was: start → exit → hand-write YAML into a bind mount →
start again. It now serves, and **refuses every API outside the setup surface with `503
storage_required`**:

```
503 {"error":{"code":"storage_required","message":"quince has no storage declared yet — …"}}
```

**Reachable while storageless, BY EXACT PATH** — a prefix would silently widen this every time a
route is added:

```
GET  /api/health                    GET/PUT /api/config
GET  /api/auth/status               POST    /api/config/storage
POST /api/auth/{setup,login,logout} POST    /api/storages/probe
GET  /api/onboarding/https          POST    /api/storages/probe/hook
```

**The two probes are not an afterthought.** The storage step's whole job is to let a user check a
path and a helper *before* declaring anything, so a mode that refused them would serve a form that
cannot fill itself in. `POST /api/config/storage` is likewise inside the surface: it is the one write
that **ends** the mode.

**There is no flag, no persisted step and no new endpoint.** The condition is read from the live
config per request, so the mode clears the instant a storage is added — no restart, nothing to reset.
A client asks `GET /api/config` and looks at `storage`; **`/api/onboarding/storage` deliberately does
not exist.** `https` needed an endpoint because its *evidence* (`detected`) is a property of the
connection and appears in no other payload; storage emptiness is already in one the client fetches,
so a second source of truth for one boolean would be the thing that goes stale.

**503, and it is a statement about the SERVER rather than the request** — the condition clears when
the operator finishes setup, not when the client changes anything. `409`/`422` would say the caller
did something wrong.

**The guard runs AFTER auth**, so an unauthenticated caller gets `401` and never learns the install
is unfinished. That is a disclosure decision, not an ordering detail.

**MALFORMED STILL REFUSES TO START**, and every other refusal in `CheckStorages` stands. A file that
could not be **parsed** is not an empty declaration: `Load()` falls back to `Default()`, so serving
would silently ignore what the operator wrote and invite them to add a storage to a document that
already has one it cannot read (quince#508).

**The accepted cost, recorded as accepted:** *onboarding* and *misconfigured* are byte-identical at
startup, so a config whose `storage:` list someone emptied by hand gets the setup page rather than a
refusal. Ruled **not** a state-honesty downgrade — the page is true in both cases, and the daemon
becomes fixable from a browser instead of a shell.

**The path names its SUBJECT, not a position** — Operator ruling 2026-08-02. An ordinal would be
anchored to nothing: design §9 describes first-run onboarding as *guided checks* and names four of
them, unnumbered — backups dir writable, backend probe, usbmuxd reachable, optional Wi-Fi toggle —
**and does not name this one at all.** Whether those checks ever acquire numbers is quince#558, and
it is unruled.

That matters more than an ordinary naming preference, because **`authExempt` is keyed to the literal
string.** Named for its subject the exemption reads as its own justification — *the https check is
pre-auth because you cannot log in without https*. Anchored to an ordinal nobody has fixed, the one
pre-auth route in the product would be guarded by an accident of ordering.

`https` rather than `tls`, deliberately: `complete` is true for `r.TLS != nil` **or**
`X-Forwarded-Proto: https`, and the second is not TLS at quince at all. `https` is the one word that
covers both, and it is the user's word.

**This is the FIRST onboarding surface in the product**, and its shape is precedent for steps 2 and
3 — which is the argument for it being this narrow. `detected` is the **evidence** and `complete`
the **verdict**; both are sent although one is derivable from the other, so a client deciding
whether to show the setup options never keeps its own list of which reasons count. That list is
exactly what goes stale when a fourth reason appears.

**Pre-auth is an Operator ruling** (2026-08-02, quince#501), and the chicken-and-egg is the whole
rung: over plain http to a LAN address the browser discards the session cookie, so the page
explaining that cannot sit behind the door the defect locks.

**By EXACT PATH, step 1 only.** `authExempt` matches on method-plus-path with no prefix support, so
an `/api/onboarding/*` exemption would mean changing the matcher and silently exempting every future
step. `GET /api/onboarding/step2` is not exempt, and neither is `POST` to step 1.

**Loopback is NOT complete**, though a session cookie works perfectly there. The step asks whether a
*phone* can reach quince; a browser on `http://localhost` cannot answer that on its behalf, and
saying otherwise would be the same false assurance one layer up that this rung exists to remove.

**`426 insecure_origin` on both credential endpoints.** When the session cookie a request
would earn is marked `Secure` while the origin is one the browser does not consider secure
— plain http to a non-loopback host, outside `--demo` — the endpoint refuses rather than
answering `200` with two cookies the browser then discards. Without it the client lands back
on the login form with no error of any kind, which is quince#497: a phone is not loopback,
so this is what the primary client of a Wi-Fi backup tool meets first. The response carries
an `Upgrade` header because RFC 9110 §15.5.22 makes one a MUST on a `426`; no browser acts
on it.

Two properties of the refusal are contract rather than implementation detail. It fires
**before the credential is examined**, so it answers identically for a right and a wrong
password and cannot become a password oracle over the one channel that is not encrypted —
and on `/setup` that is also what stops a refusal leaving the password set behind an error.
And it is conditioned on **the `Secure` decision**, not on the scheme: an option that turns
`Secure` off for plain-http transport turns the refusal off with it, which is how `qn.6f`'s
plain-http opt-in lands without a second switch to keep in step.

Everything else requires the session cookie. State-changing requests (POST/PUT/DELETE,
except `login`/`setup`) must echo the CSRF token in the `X-CSRF-Token` header — a
double-submit check against the readable `quince_csrf` cookie. The session cookie is
`HttpOnly` + `SameSite=Strict` + `Secure` (Secure relaxed only for loopback-http and
`--demo`, so local/e2e over plain http still works — never in production). Errors are
`{error: {code, message}}` with sensible HTTP statuses.

### Health — and `reconciling`, the one field with a promise attached

```
GET  /api/health  → {status, version, mode, demo_reset_minutes?, reconciling, insecure_origin,
                     muxers[]}
```

**`/api/health` is deliberately NOT frozen** — it says so at its own definition, and `qn.6` used that
to add `mode`. What follows is therefore a promise about *meaning*, not a frozen shape.

**`insecure_origin: true` means NO CREDENTIAL CAN BE ESTABLISHED OVER THIS CONNECTION** (`qn.6f`,
quince#908). A session cookie earned here would be marked `Secure` and then discarded by the browser,
so `POST /api/auth/setup` and `POST /api/auth/login` both answer `426 insecure_origin` **before
examining the credential** — on a fresh install that means first-run setup cannot be completed at all.
It is `refuseInsecureOrigin`'s own predicate rather than a second copy of it, so this field and the
426 cannot disagree.

**IT IS A PROPERTY OF THE CONNECTION, NOT OF THE DAEMON**, and it is the only field here that is.
`mode` and `demo_reset_minutes` are the same for every caller; this one differs between a browser on
`https://name` and a browser on `http://ip` talking to the same process. That is what makes it usable:
a client learns it about the connection it is actually on, which no daemon-wide fact could tell it.

**It is NOT `GET /api/onboarding/https`'s `complete`, and the two disagree on loopback.** That step
reports `complete: false` on `http://localhost` deliberately — it asks *"can you reach quince from
your phone"* — while a session cookie survives loopback perfectly well, so `insecure_origin` is
`false` there. A client that keyed first-run routing on the onboarding fact would send every developer
on `localhost` to the HTTPS page. Both are correct answers to different questions, and the questions
are easy to conflate.

**A client must act only on a POSITIVE answer.** An unreachable or still-loading health probe is not
evidence of a secure origin *or* an insecure one, and the failure that matters is routing somebody
away from a page that would have worked.

**`reconciling: true` means A VERSION LIST MAY BE SHORT** — Operator ruling 2026-08-08 (quince#731,
blocker 2), built in `qn.6i`. Reconciliation no longer completes before the listener binds, so quince
can be serving a registry it knows is incomplete: versions present on disk but not yet adopted are
absent from `GET /api/versions` and from `Storage.backup_count`, and rows whose artifact has vanished
are not yet marked `missing`.

**IT IS A DECLARED PROVISIONAL STATE, NOT AN EMPTY RESULT.** A client must not conclude *this disk has
no backups* while it holds. That distinction is why the field exists rather than the window being left
unannounced: the alternative on the table was refusing to serve until reconciled, and it was rejected
because it keeps quince#592's dead window and multiplies it by the schedule.

**`false` means the last triggered pass COMPLETED.** It does not mean the disk was read a moment ago;
the counts remain what the database says, which is what they have always been.

**Daemon-wide, not per storage.** One disk scanning while another is idle is a real distinction quince
knows and this field does not carry — deliberately deferred rather than dropped, because `Storage`
already has three fields describing its condition and a fourth is a larger change than the rung needed.

**A boolean rather than a string**, unlike `mode`, which is a string precisely so a third mode needs no
second field. Here there are two states and no candidate third; a later `deferred` would be a widening,
and this sentence is the note that says so.

### Devices

```
GET  /api/devices                      → {devices: Device[]}
GET  /api/devices/{udid}               → Device
POST /api/devices/{udid}/pair          → 202 {op_id} | 404 | 409
     // 409: device not present on USB — pairing is USB-only at the protocol floor,
     // surfaced actionably ("pairing needs a USB connection"). The 202 op narrates
     // "tap Trust on the phone" / "enter the passcode on the device".
     // 409 ALSO: another device op is already in flight for this udid — the
     // single-flight rule, stated once under wifi-sync below. The action is *wait*.
POST /api/devices/{udid}/pair/validate → {paired: bool} | 404 | 409
     // paired == "a pairing is CONFIRMED valid right now". A locked device cannot be
     // confirmed (`idevicepair validate` reports "passcode set" for ANY locked device,
     // paired or not — qn.3 hardware finding), so Device.paired shows "unknown" until
     // an unlocked validate succeeds; the endpoint's bool is the confirmed check only.
POST /api/devices/rescan               → 202 | 409
     // Restarts the MANAGED in-container USB muxer (devices.manage_muxer: true) so USB
     // devices missed by an unprivileged container's absent hotplug re-enumerate;
     // reuses the client's reconnect→Reset→replay reconcile (no new table semantics).
     // 409 when the muxer is external (manage_muxer: false) — quince doesn't own it.
     // Ruled from qn.2's gap capture; landed by qn.2b.
     // USB-ONLY by design (qn.4c, ruled (bz)): quince may also supervise netmuxd, but
     // rescan never restarts it — Wi-Fi has no hotplug problem to solve, and a restart
     // would tear a live Wi-Fi backup. Per-daemon state lives in GET /api/health.
POST /api/devices/{udid}/encryption
     {action: "enable" | "change_password" | "disable",
      password?, old_password?, new_password?}     → 202 {op_id} | 404 | 409 | 422
     // 404 and 409 were REACHABLE HERE BEFORE THEY WERE DECLARED, and quince#465 found
     // it: this line read `202 {op_id} | 422` while the handler passed the manager's
     // 404 (no such device) and 409 (device not connected) straight through. Declared
     // now, alongside the single-flight condition below — a client written to this
     // contract would have treated both as undocumented.
     // 409: device not connected; or another device op is already in flight for this
     // udid — the single-flight rule, stated once under wifi-sync below.
     // 422: bad action or a missing required password field. Drives `idevicebackup2
     // encryption`/`changepw`. Passwords travel in the TLS body and reach the
     // subprocess over an interactive pty (`idevicebackup2 -i` — VERIFIED qn.3,
     // libimobiledevice 1.4.0; the BACKUP_PASSWORD env fallback exists in the CLI but
     // quince does not use it); NEVER argv (world-readable /proc), never logged, never
     // stored. The phone will demand its own passcode confirmation — the op narrates
     // that state to the UI.
     // NOTE: this is Apple's device-global backup password — the SAME password later
     // used to unlock versions in the vault. quince sets it, never keeps it.
POST /api/devices/{udid}/wifi-sync
     {action: "enable" | "disable"}                         → 202 {op_id} | 404 | 409 | 422
     // qn.7, ruled at that rung's spec review (quince#332). Writes lockdown
     // com.apple.mobile.wireless_lockdown/EnableWifiConnections so Wi-Fi backups can be
     // turned on WITHOUT Finder — the D12 "everything in quince" promise for the PRIMARY
     // transport. 409: not connected, or NOT PAIRED — a lockdown write needs a trusted
     // session, and "not paired" is a state the user can act on where "the device
     // rejected it" is not. 422: unknown action.
     //
     // SINGLE-FLIGHT, stated here once for all three op routes (pair, encryption,
     // wifi-sync). 409 ALSO means: an op is already in flight for this udid. ONE op per
     // DEVICE, whatever its kind — not one per (device, kind) — ruled on quince#465,
     // 2026-08-02. The finer key would permit `wifi-sync` to run beside `pair` or
     // `encryption`, and that is the one combination measured to break: wifi-sync SEVERS
     // the transport the other two run over (quince#363/quince#366, where SetWifiSync
     // verified its write by reading back over the connection the write had just cut).
     // It is also the ASSISTED model's constraint — two ops prompting on one screen give
     // the user no way to tell which dialog belongs to which request.
     // Same key and same reading as `POST /api/jobs`, which is per-UDID for the same
     // reason. The action is *wait*; a refused op records nothing and returns no op_id.
     // NOT ruled, and deliberately left open: whether a device op should also be refused
     // while a BACKUP is running for that device. That is a precondition rather than
     // single-flight, and it wants its own reasoning.
     // NO PASSWORD, and that is a design point rather than an omission: the value is a
     // boolean, so this op uses the plain argv path, NOT the pty machinery that exists to
     // keep a password out of argv.
     // THE OP VERIFIES ITS OWN WRITE (decisions/0004). lockdownd_set_value succeeding means
     // the device ACCEPTED the request, not that the setting took effect — unverified for
     // this domain, which quince had never written before. So the op re-reads and compares,
     // and reports four distinguishable failures because the remedy differs:
     //   wifi_sync_failed        the device rejected it — retryable
     //   wifi_sync_not_applied   accepted and not applied; the state is UNCHANGED, not unknown
     //   wifi_sync_unconfirmed   accepted, and the read-back could not RUN — the state is
     //                           UNKNOWN, which is neither of the two above (quince#363)
     //   wifi_sync_unavailable   this build does not know the key, so quince will not guess
     // The unconfirmed/not_applied split is load-bearing rather than pedantic: conflating them
     // reported a write that had WORKED as "Wi-Fi sync is unchanged" on hardware, and the user
     // was told to expect a device that still syncs while holding one that needed a cable.
     // unconfirmed does NOT appear on the one path where an unreadable read-back is EXPECTED:
     // disabling over Wi-Fi severs the connection the read-back would use, so there the op
     // SUCCEEDS and the value becomes wifi_sync: "unknown" (ruled quince#363).
POST /api/devices/{udid}/reset-working {storage_id?} → 202 {note} | 404 | 409 | 500 | 503
     // qn.6h ADDS A FAILURE THE ZFS BACKEND CAN RETURN, and it is the one code here that means
     // "nothing happened": 500 {note} when the backend REFUSED to abandon. Reset is a `zfs
     // rollback` on that backend, and ZFS can decline it — most often because a snapshot NEWER
     // than the one quince would restore exists (a host snapshotter firing every few minutes is
     // enough), less often because the mount is busy. The note carries `zfs`'s own words verbatim
     // and names the remedy for THE FAILURE THAT OCCURRED — for a newer snapshot, destroy the
     // intervening ones or do nothing because the head is still resumable; for a busy mount, stop
     // the container. Offering the wrong one is a state-honesty failure, since stopping the
     // container does nothing in the first case. On this path the head stays dirty, the work
     // sentinel stays, NO audit line is written (nothing was discarded), and quince does NOT
     // retry — a retry is the identical call against the identical mount.
     //
     // qn.5b Reset (accepted contract proposal, decisions (co)): DISCARD the device's dirty
     // work area so the next backup starts clean — losing only the partial, NEVER a committed
     // version. WHAT "discard" MEANS IS THE BACKEND'S: reflink/hardlink/copy remove working/;
     // zfs rolls the dataset back to its newest @quince-* snapshot (qn.6h), or empties the head
     // when the device has no committed version to roll back to.
     // Idempotent (a device with nothing to abandon is already clean → 202). 409 while
     // a backup is running for the device (single-flight; cancel it first); 404 unknown device;
     // 503 no backup engine wired (--demo). The backend op is RepairWorkingCopy (CLI:
     // `quince device reset-working <udid>`). The honest COMPANION of a kept-dirty working/: on
     // failure the partial is kept so a retry RESUMES (no re-transfer); Reset is the explicit
     // discard. Audited (reset event, no secret, NAMING THE STORAGE); touches no version.*
     // / latest surface.
     //
     // qn.6c (Operator ruling 2026-08-02, quince#448): the endpoint is DEVICE-scoped and a
     // device can now have a dirty working/ on more than one storage, so "reset this
     // device's working copy" stopped having one answer. `storage_id` is OPTIONAL:
     //   present, usable       reset exactly that one
     //   present, unknown      404, matching unknown-device
     //   present, unreachable  409, carrying that storage's own unreachable_reason —
     //                         the job path's code, not a new one for a known condition
     //   omitted, 0 dirty      202, already clean (unchanged)
     //   omitted, exactly 1    reset it, and NAME it in the note
     //   omitted, 2 or more    409, listing them, saying to name one
     // REFUSE AND NAME RATHER THAN GUESS WELL: a dirty working/ is a resumable multi-hour
     // partial, so "reset all" would discard a transfer on a disk the user was not
     // thinking about — the same answer quince#435 gave a job that names no storage.
     // "DIRTY" IS A BACKEND QUESTION SINCE qn.6h and the two no longer agree: on
     // reflink/hardlink/copy it is `working/<udid>` existing, INCLUDING a killed seed; on zfs it
     // is the WORK SENTINEL existing, because the tree is the dataset root and there is no
     // working/ to stat. A populated zfs head is NOT dirty by itself — that is the steady state
     // between backups. Unreachable storages
     // are NAMED as not-inspected, never silently skipped. CLI: `--storage <name>`.
     // No deployment with one storage can reach the new refusal.
GET  /api/ops/{op_id}                  → Op
     // pair/encryption return 202 {op_id}; the op's narration (e.g. "tap Trust on the
     // phone", "enter the passcode on the device") streams via `op.updated` WS events,
     // with this endpoint as the poll/refresh fallback.
```

### Jobs

```
POST /api/jobs {udid, transport: "usb"|"wifi"|"auto", retry_of?}   → 202 Job
     // Error codes: 409 a backup is already running for this UDID (never two concurrent jobs
     // per device); 422 bad/omitted transport, OR transport:"auto" when the device is present
     // on NEITHER transport (qn.4b, design §4/(bp): auto resolves against current presence —
     // prefer USB when plugged, else Wi-Fi — and refuses actionably when absent, since a guessed
     // transport would persist a dishonest Job.transport; the Job stores the resolved concrete
     // usb|wifi, never "auto"); 404 unknown device; 503 no backup engine wired (e.g. --demo before
     // qn.4b; from qn.4b --demo scripts on-demand jobs and the command surface is live). retry_of
     // (optional) sets the assisted-model retry chain: the new job inherits intent_id from the
     // chain root and increments attempt.
GET  /api/jobs?cursor&limit&udid                        → {jobs: Job[], next_cursor}
     // cursor pagination newest-first; the cursor is the last job id of the prior page.
GET  /api/jobs/{id}                                     → Job
POST /api/jobs/{id}/cancel                              → 202 Job
     // (qn.4a): 409 the job is not running (already terminal); 404 unknown job; 503 no engine.
GET  /api/jobs/{id}/log                                 → text/plain (full so-far; live tail is WS)
```

**RULED and IMPLEMENTED (was `PROPOSED (gap)`): a storage collection, and a job that names one — `qn.6c`, quince#378.** The READ half — `GET /api/storages`, the `Storage` object and `POST /api/storages/{name}/recheck` — ships in story 5c. `POST /api/jobs {storage_id}` is RULED AND IMPLEMENTED too — it landed with `qn.6d` story 6 (quince#584), which is the PR canon named. The request field is accepted at `handlers_jobs.go` and genuinely consumed: `ResolveChoice` maps it to a concrete storage, `BindJobStorage` records it for the life of the job, and a retry inherits it.
Storage becomes plural at `qn.6c`, so a backup must be able to say *where*. Additive:

```
GET  /api/storages                                        → {storages: Storage[]}
GET  /api/storages?udid=<udid>                            → {storages: Storage[]}  // adds will_be_full
POST /api/storages/{name}/recheck                          → 200 {storage} | 404
POST /api/storages/probe {path}                            → 200 {probe} | 422
POST /api/storages/probe/hook {parent_dataset, ssh_user, ssh_host,
                               ssh_port?, ssh_key?}        → 200 {check} | 422
POST /api/storages/zfs/key    (NO BODY — see below)        → 200 {key} | 500
GET  /api/storages/zfs/helper?parent_dataset=<ds>          → 200 {script, path} | 422 | 500
POST /api/storages/zfs/hostkey {ssh_host, ssh_port?}     → 200 {found, host_key, reason} | 422
POST /api/storages/zfs/hostkey/trust {line}                → 200 {trusted, path} | 422
POST /api/jobs {udid, transport, storage_id?, retry_of?}  → 202 Job
```

**RULED and IMPLEMENTED: `POST /api/storages/probe` — what IS this path? (`qn.6e`, quince#502.)**

The add-a-storage probe. It answers one question about a path that is **not declared and may never
be**, and it answers it **without changing the path**: it never creates a directory and never mints a
storage marker. Creating and declaring are separate, explicit acts a user takes after seeing the
answer. `storage.Inspect` carries that guarantee — quince#415's *"NOBODY CREATES A STORAGE ROOT"*,
reached from the form rather than from startup.

**`probe` is not a `Storage`, and must not become one.** A `Storage` is declared, has an identity and
is being served. Sharing the object would mean lying about `id` and `default`, or making them
nullable on a resource where they are guarantees. §2 carries the shape.

**THE 422 LINE IS DRAWN AT THE QUESTION, NOT AT THE ANSWER, and this is the part most likely to be
"corrected" later.** A `422` means the **question was malformed** — no body, no `path`, or a path
that is not absolute. Everything the probe can say about a real absolute path is a **`200`, refusals
included**: *that path does not exist*, *that is a file*, *quince cannot write there* are the answer
to what-is-this-path, not a failure to answer it.

Three reasons, in the order they decide it:

1. **A form renders a refusal beside the same field, in the same place, as a success.** Statuses
   would make the client branch twice on one thing.
2. **A refusal still carries facts** — `marker` and the filesystem figures are reported on refusals
   too, so a client can say *"this IS storage X, and the path is read-only"* rather than only the
   second half. A `404` would discard them.
3. **The non-absolute case is on the `422` side on purpose**, matching `config`'s `validate.go`: a
   relative path is not one quince could ever store, so the form's refusal and the config's refusal
   say the same thing about the same string instead of disagreeing.

The `422` body is the shared `{errors: [{path, message}]}` at `path: "path"`, so a client that
renders one renders this one.

**Not auth-exempt.** `authExempt` is five literal method+path strings and this is not one of them.
`GET /api/onboarding/https` is pre-auth **by exact path** because you cannot log in without https;
nothing about a storage probe is a prerequisite of logging in. Stated because the probe touches the
filesystem on a caller-supplied path, so the guard is load-bearing rather than incidental.

**`probe` is a literal segment beside `/{name}/recheck` and does not shadow it** — they differ in
segment count, so a storage may even be *named* `probe`. Gated, because a literal added beside a
wildcard is exactly the shape that bites.

**RULED and IMPLEMENTED: `POST /api/storages/probe/hook` — `Test helper` (`qn.6e`, quince#502.)**

The zfs branch's load-bearing control. Without it, *"did I install the helper correctly?"* is
answered by a failed multi-hour Wi-Fi transfer at commit time: the key, the forced command in
`authorized_keys`, and the `$PARENT` baked into the helper are three things that must line up, and
**none of them is observable from the path**. That is also why `qn.6e` descoped *deriving*
`parent_dataset` — derivation proves what the filesystem thinks, and this proves what the **helper**
was configured with.

**Two read-only verbs, in this order, and the order is part of the answer.** `capacity` first —
it takes **no caller argument at all** (the helper's own comment calls that *"TIGHTER than the arms
above"* — `core/internal/storage/zfshelper/quince-zfs-helper`, moved out of a `deploy/storage.md`
fence by quince#818 piece C), so a failure there is unambiguously about reachability. Then `list <typed parent>`, whose
`case "$target" in "$PARENT"|"$PARENT"/*` guard is the only thing that can see a parent
disagreement. Reversed, one refusal would be two hypotheses. Nothing here can create, destroy or
write.

**Four outcomes, frozen, because the remedies differ and a user cannot guess between them:**

| `outcome` | means | remedy |
| --- | --- | --- |
| `ok` | both verbs answered | none |
| `not_migrated` | `capacity` refused, `list` answered | add the `capacity)` case — `qn.6d`'s operator migration. Cards read *"free space unavailable"*; backups are unaffected |
| `parent_mismatch` | `capacity` answered, `list` refused | the typed dataset is not the helper's `$PARENT` |
| `unreachable` | neither answered | key, forced command, or host |

**AN EMPTY `list` IS SUCCESS.** `list` returns the `@quince-*` snapshots under the parent, and a
storage with no backups yet has none — so the correct, working, freshly-installed case answers exit
`0` with **nothing on stdout**. Reading emptiness as failure fails on first run and only on first
run, which is the one day this button matters most. Gated.

**`detail` carries the transport's own output** — ssh's *"Permission denied (publickey)"* is the
whole answer and quince cannot improve on it. **It may name the operator's host**, so: shown to the
authenticated admin in their own browser, and **never logged, never in a fixture, never pasted into a
PR or an issue**. That is the privacy gate's actual scope rather than a redaction rule on a running
product. **The argv is never included** — the composed transport carries `user@host` by
construction, where the output only sometimes does.

**IT TAKES THE TRANSPORT STRUCTURED SINCE quince#818** — `ssh_user`, `ssh_host`, and optionally
`ssh_port` / `ssh_key` — and **composes the argv with the same function the saved storage uses**.
That identity is the point rather than tidiness: if this endpoint built its own command, the button
could pass against a transport the running backup never takes, which is the exact failure `Test
helper` exists to prevent. A form that has not asked for port or key sends neither, and both default
server-side exactly as the config does.

**DECLARED: this endpoint EXECUTES A REQUEST-INFLUENCED ARGV**, and quince#818 narrowed the
influence rather than removing it — four typed fields through one composer, where it used to be the
entire command line. It adds no capability an authenticated admin lacks — `PUT /api/config` already
stores a transport that quince execs at the next job — but it shortens the loop from *next backup*
to *now*. Bounds: behind `authGuard` and `csrfGuard`, nothing added to the five-entry exempt set, an
argv **array** and never a shell string, the dataset name validated against `datasetPattern` before
anything runs, a bounded timeout, and the subprocess killed by process group on cancellation.

The `422` line is the probe's, unchanged: a malformed **question** — no `parent_dataset`, no
`ssh_host`, or no `ssh_user` — is a `422`; every verdict about a real pair, `unreachable` included,
is a `200`. A user who has not installed the helper has asked a perfectly good question. **`ssh_port`
and `ssh_key` are absent from that list deliberately**: both default, so an omitted one is the
ordinary request rather than a malformed one.

**§2 carries `StorageHookCheck`.** Spec: `docs/specs/qn.6e/qn.6e.md`, G8.

**RULED and IMPLEMENTED: `POST /api/storages/zfs/key` — quince's own helper key** (quince#818 piece
B, under the ruling relayed at
[issuecomment-5245496176](https://github.com/novkostya/quince/issues/818#issuecomment-5245496176)).

It answers *"what do I put on my ZFS host?"* — returning the key at `/data/keys/zfs` and
**generating one only if there is nothing there.**

**IT TAKES NO BODY, AND THAT IS THE SECURITY SHAPE RATHER THAN A MISSING FEATURE.** A
caller-supplied path would make this an authenticated *write-a-file-anywhere* primitive whose
contents happen to be a private key. Taking none means the endpoint has no reachable target but
quince's own. An operator who keeps a key elsewhere sets `ssh_key` by hand and never presses the
button — which is why that field stays settable.

**DISCOVERY BEFORE GENERATION, and it is the property that protects existing installs.** A key
already at that path may have its public half in an `authorized_keys` on a host quince cannot see;
replacing it breaks a working storage **silently**, with the failure surfacing at the next backup
rather than at the press. So there is no force flag, and **a file that is not a key is a refusal**
rather than a reason to overwrite.

**`created` IS ON THE WIRE BECAUSE THE SCREEN MUST SAY WHICH.** *"quince made you a key"* and
*"quince found the one it made earlier"* call for different next steps — the first has to be pasted,
the second may already be installed — and guessing wrong invites replacing an entry that works.

**BOTH THE PUBLIC KEY AND THE COMPLETE `authorized_keys` LINE ARE SERVED**, and the line is the
artifact. `command="/usr/local/sbin/quince-zfs-helper"` is what pins the helper regardless of what
the client asks for, so serving a bare key would invite pasting one — an unconstrained shell login
on the storage host rather than a helper bounded to one dataset.

**THE PRIVATE HALF NEVER REACHES THE RESPONSE.** Not on the type, never logged, never in a fixture —
the discipline backup passwords already follow. `ed25519` is generated **in-process**
(`crypto/ed25519` + `x/crypto/ssh`, already a direct dependency) rather than by `ssh-keygen`, so it
never passes through argv, a temp file or another process. `ssh-keygen` **is** in the runtime image;
this is a choice, not a necessity.

**`500` rather than a `422`**, because neither reachable failure is the caller's fault: a `/data`
quince cannot write, and something at that path which is not a key. Both carry the daemon's own
sentence, since a generic *"could not create key"* would name neither.

**§2 carries `StorageZFSKey`.**

**The `?udid=` form and the re-probe were BUILT by `qn.6c` and never listed here** — they existed
only in the prose below and in §2's `will_be_full` comment. Listing them is a drift correction, not
a new surface (`qn.6d`, quince#443).

**The route is keyed on `name`, and that is a hazard avoided rather than a preference.** quince#570,
2026-08-02: the API addresses a storage by its config `name`, not the marker UUID, because **an
unreachable storage has no UUID and is the only storage the button exists for** — nothing is ever
minted for a path quince has not reached. `qn.6d`'s Forget (`DELETE /api/config/storage/{name}`) was
written to that form from the start; this route was **built to it by quince#610**, which is also the
measurement of what keying on the id had cost: the client sent `POST /api/storages//recheck`, the
router path-cleaned that to a **`307`** — method-preserving, so the browser re-sent the POST — at a
target matching no pattern, and the user got a silent `404` on the one storage they most wanted to
recheck.

**The general rule, since it outlives this route: never key a route on a value that can legitimately
be empty.** The `307` is Go's own `ServeMux` and is not going away. What a route controls is whether
a client can produce that URL at all.

Three sub-decisions, each with the rung's recommendation:

- **`storage_id` omitted** → the storage marked `default`, of which there is exactly one.
  Recommended over a 422 because it is what keeps every existing single-storage client working
  unchanged, which is what makes the whole change additive.
- **The chosen storage is unreachable** → **409**, not 422. It is a state conflict the user can
  act on (plug the disk in) — the same reading `POST /api/devices/{udid}/pair` already uses for
  *not present on USB*. A 202-then-queue is explicitly refused: queuing fights the assisted model
  (D13), and the multi-storage epic's own point 5 says an offline target is an honest "can't right
  now", never a background retry.
- **Unknown `storage_id`** → 404, matching unknown-device.

`Job` gains `storage_id` (non-null, the **resolved concrete** storage — never the word "default",
exactly as `transport` stores the resolved `usb`/`wifi` and never `auto`).

Open inside this proposal: whether the *"this will be a full transfer"* claim (see `Storage` in
§2) makes `GET /api/storages` device-scoped via `?udid=`, or is carried elsewhere. Both work; they
differ in whether a storage list is a device-independent resource.

Spec: `docs/specs/qn.6c/qn.6c.md`, gap 2. **RULED 2026-07-31 — as recommended, with the `?udid=`
sub-question settled. NOT YET BUILT: this block is flipped to its ruled form by the slice that
implements it (slice 5).** This sentence read *"Not built until ruled"* until 2026-08-01, which
stopped being true the moment the ruling landed and would have told a session to stop on a question
that is decided — the same inverted-marker defect quince#408 gates for. The distinction that matters
to a reader: **ruled-and-unbuilt** is work to do, where **unruled** is a thread to stop.

**Two surfaces this proposal does NOT cover** and which story 5 needs, both proposed in the spec and
neither built: a **re-probe endpoint** — the 2026-08-01 ruling makes reachability changeable without
a restart, *plug the disk in and press the button*, and there is no button in this contract — and
the **shape of `unreachable_reason`**, now that `missing_medium` and `unreachable` must be
distinguishable by it.

**RULED (was `PROPOSED (gap)`): how a storage is FORGOTTEN — `qn.6d`, Operator ruling 2026-08-03, relayed on quince#443.**

**Forget is detach-and-forget: the declaration goes, the data on the disk does not.** It is a
**config mutation, not a resource-delete** — there is no live deregistration, and the class `qn.6c`
declined in its rung-ruled decision 1 stays declined.

```
DELETE /api/config/storage/{name}  → 200 {config, warnings, source} | 404 | 422
```

The storage is addressed by its config `name`, never the marker UUID (quince#570, ruled
2026-08-02) — an unreachable storage has no UUID, and the storage a user most wants to forget is
the one that never came up.

Three decided points:

1. **A narrow endpoint rather than the existing `PUT /api/config`.** It splices server-side, so it
   **cannot** drop a sibling entry's `zfs:` / `retention:` keys. A full-document `PUT` decodes into
   a zero-valued `config.Config`, so a client that reconstructs the list rather than splicing a
   fetched one silently resets every key it did not render.
2. **Forgetting the DEFAULT storage is refused with `422`**, naming the storage and the remedy —
   *make another storage default first*. Exactly one storage is `default`, so on a single-storage
   install the only storage *is* the default; this subsumes the last-storage case that
   `config.Service.Replace`'s `CheckStorages` floor already covers.
3. **Whatever did not take effect is SURFACED, never silent.** The response carries the same
   `warnings` the config endpoints already return and the UI shows them — never a silent success
   over a card that then lingers with no explanation.

   **This point read *"the restart is SURFACED"* until `qn.6g`**, and the change of subject is the
   whole point: with the applier wired the storage is already gone by the time the response is
   written, so there is no restart to surface. What the channel carries now is anything an applier
   could **not** take. The rule is unchanged; the thing it names is.

**Recheck after a Forget: THE PENDING WINDOW NEVER ARISES, and this paragraph predicted that.** It
read *"`POST /api/storages/{name}/recheck` keeps answering for the slot the process is still
serving, and the card carries `forgotten · restart to apply` … if live-apply lands later the pending
window never arises and the same rule `404`s naturally because the slot is gone."* Live-apply landed
in `qn.6g`, so the second limb is the live one: the slot is gone at the moment of the write and
recheck `404`s. Kept rather than replaced because a rule written to stay true under both models,
which then did, is worth more as a record than as a tidy sentence.

**General config live-apply was a SEPARATE RUNG and explicitly not `qn.6d`. It is `qn.6g`
(quince#577), and it has landed** — project-wide config→runtime propagation, with storage as its
first consumer. This paragraph read *"`config.Service` has no `Apply`, `Reload`, `onChange` or
`Subscribe` at all, so restart-to-apply is the status quo for **every** setting"*; it now has
`Subscribe`, and storage is wired to it.

**Restart-to-apply is therefore no longer the blanket status quo, and §6 now says what replaced it:**
[the per-key table](#which-settings-apply-live--the-per-key-answer). Three bins rather than two,
because five keys are read by nothing and calling those *restart-required* would promise that
restarting makes them work.

This paragraph said the table was **owed**, which it was for two PRs. Narrowed in the diff that
landed it, rather than left to age into a false claim that canon is incomplete — the inverse of the
defect `CLAUDE.md` names, and just as misleading to a session deciding what is safe to build on.

Spec: `docs/specs/qn.6d/qn.6d.md`, gap B.

### Config

```
GET /api/config   → {config, warnings: [], source: {path, mtime}, file_text, discarded}
PUT /api/config   → full-document replace; validated then atomically written to
                    /data/config.yml; 422 {errors: [{path, message}]} on invalid
POST /api/config/storage
                  → add one storage: 200 {config, warnings, source} | 422.
                    Splices SERVER-SIDE, for the identical reason the DELETE does.
                    REFUSED 422 while the file on disk was DISCARDED at load — the
                    splice would replace a config quince could not read. See below.
DELETE /api/config/storage/{name}
                  → forget one storage: 200 {config, warnings, source} | 404 | 422.
                    Splices SERVER-SIDE, which is the whole reason it is not a PUT —
                    see the gap B ruling above for what a reconstructed full document
                    silently loses.
```

**RULED and IMPLEMENTED: `discarded` — THE FILE ON DISK WAS REFUSED AT LOAD** (Operator ruling
2026-08-12, [quince#849](https://github.com/novkostya/quince/issues/849)). A boolean, from
`Loaded.OK` and nothing else: quince is running on `Default()` and **nothing the file declares is in
effect**.

**IT CARRIES THE FATALITY; `warnings` CARRIES THE CAUSE.** That split is the whole design. `warnings`
is non-empty in two states that want **opposite** answers — a discarded config, where the declared
storage is not running and nothing is backing up, and a config that parsed with an ignored unknown
key, where the storage is fine. §6 makes an unknown key a warning and never an error, so the second
is ordinary rather than exotic.

**No client could infer it.** `config.storage: null` does not separate the two — a fresh install with
a typo has that too — which is what makes this a field rather than a derivation.

**A BOOLEAN RATHER THAN AN `errors: []`, on evidence rather than taste.** Only **one** of `Load`'s
three discard paths fills `Errors`: an unreadable file and invalid YAML both return `OK: false` with
it empty. A client keying off an error list would therefore tell somebody whose config cannot be
parsed that their storage is fine and a key was ignored — worse than shipping nothing.

**The consumer rule: branch the HEADLINE on this, render `warnings` either way.** Treating it as
*"should I show the warnings at all"* would put one signal behind a second gate, which is the defect
quince#849 was filed about. `OnboardingStoragePage` is the reference implementation.

**Its companion invariant is gated, because the screen now depends on it: EVERY discard path records
its cause in `warnings`.** All three do deliberately — the validation branch copies each error across
in an explicit loop, the other two write their own sentence — but a boolean whose detail can go
silently empty would name a problem and show nothing to fix. `TestEveryDiscardPathIsDiscardedAndCarriesItsCauseInWarnings`.

**Same shape as `has_password` on `GET /api/auth/passkeys`** ([quince#855](https://github.com/novkostya/quince/issues/855), landed hours earlier): a fact the client
cannot derive, added to an endpoint that **already requires a session**, so no disclosure question
arises. This endpoint is in the storageless-reachable set rather than `authExempt`.

**RULED and IMPLEMENTED: `file_text` — `config.yml` AS IT IS ON DISK** (`qn.6j`, Operator ruling
2026-08-09 on [quince#728](https://github.com/novkostya/quince/issues/728)). The Settings panel is
titled *Current configuration* and its subtitle says *"You can edit the file by hand instead"*, so it
must show the **file**, not a re-rendering of the parsed document.

**`config` and `file_text` are two DIFFERENT documents, not two renderings of one**, and `qn.6j` is
what made them different. `config` is the **resolved** configuration — every key, defaults filled,
`backend: auto`, `zfs.mode: hook`. `file_text` is **only what was set**. They answer different
questions: *what is quince's configuration* versus *what does my file contain*.

**The alternative was the UI serializing YAML client-side, and it is rejected**: the server is the
only thing that knows what it actually wrote, and after `qn.6j` a client-side serializer would have to
reproduce Go's **omission** decisions as well as its quoting and ordering.

**IT IS READ AT REQUEST TIME AND NEVER CACHED**, which is a condition of the ruling rather than an
implementation detail. There is no reload path — `Load` runs at construction and nothing re-reads the
file (quince#727, post-`v0.1`) — so a cached copy of what quince last *wrote* goes stale the moment
somebody hand-edits the file, under a subtitle inviting exactly that edit.

**So it can show a file quince has not adopted**, and that is the point: after a bad hand-edit the
running configuration is last-good while the file on disk is the broken one, which is precisely when a
user needs to see what is on disk. `warnings` and `source` are how a client says so. `""` when there
is no file yet, which `source.mtime` being empty already distinguishes.

**A later switch to an attributes view costs no contract change.** `config` is already on the wire and
quince#756 gates that every field of it is present in every response; an attributes view is a UI change
against data that already exists. What it would cost is removing `file_text`, which is the breaking
direction — nearly free today (one user, no `v0.1` tag) and more expensive after release.

**RULED and IMPLEMENTED: `POST /api/config/storage` — the add (`qn.6e`, quince#502).**

**Forget's mirror in every respect that matters**, and the shape follows from the same reading: it is
a **config mutation, not a resource-create**, so it returns the config-endpoint body rather than a
`201`, and the client re-renders from the payload `GET`, `PUT` and `DELETE` already hand it.

**A narrow route rather than `PUT /api/config`** — gap B's argument, unchanged: it splices
server-side, so it **cannot** drop a sibling entry's `zfs:` or `retention:` keys. A full-document
`PUT` decodes into a zero-valued `config.Config`, so a client that reconstructs the list rather than
splicing a fetched one silently resets every key it did not render, and **no UI surface renders
`zfs:` or `retention:`**.

**THE 422 CARRIES THE FIELD THE CALLER TYPED** — `path`, `name`, `backend`, `default` — **not
`storage[i].…`**. A caller adding one entry cannot map an index in the merged list back to its own
input. `replaceLocked`'s document-wide errors still arrive in the indexed form underneath, so the
`{errors: [{path, message}]}` shape a client renders is unchanged; only the addressing differs, and
it differs toward the question that was asked.

**IT CARRIES NO `CheckStorageBackends` CALL OF ITS OWN, and the absence is the design.**
quince#683's ruling put that check in `replaceLocked`, which `config.AddStorage` writes through — so
this path inherits it along with `Validate` and `CheckStorages`. Two call sites for one invariant is
how they diverge.

**RULED and IMPLEMENTED: THE ADD IS REFUSED WHILE THE FILE ON DISK WAS DISCARDED AT LOAD**
(Operator ruling 2026-08-12,
[quince#852](https://github.com/novkostya/quince/issues/852)). A `422` whose `path` is the offending
**config** path — `storage[0].zfs.mode`, not a field the caller typed — and whose message names that
line, its message, the file, and the remedy.

**It is the one refusal on this endpoint that is about the SERVER'S state rather than the caller's
entry**, which is why it runs before every check above it: nothing the caller could type makes it
right, and reporting it against a form field would name the wrong thing.

**What it prevents was measured, not reasoned** (2026-08-12, real container). When `Load` cannot
validate the file it returns `OK: false` and `Config: Default()` — so `Current()` has **no storage**,
and a splice over it writes *defaults plus the new entry*. A `config.yml` declaring a zfs storage
with a `parent_dataset` and a `hook_cmd` became three lines; the response carried `warnings: []`, so
every surface afterwards reported health. No backup data was at risk. The operator's **declaration**
was, by the single action the first-run screen invites.

**`DISCARDED` IS NOT `HAS WARNINGS`, and conflating them would be a second bug.** A file that parses
with warnings — an unknown key, which §6 makes a warning and never an error — keeps its `Storage`
through the load, so a splice over it loses nothing. The guard is on the discard, so quince does not
decline to work on a config it is perfectly happy to run.

**The two alternatives were considered and ruled against.** A UI confirmation is a guard on the
browser and `curl` walks around it, leaving the destructive path reachable by everything that is not
the UI. Preserving the unparseable entries through the splice would have quince write back keys its
own loader could not validate — a larger promise than this endpoint should make, and it yields a file
that can be rewritten while containing something the daemon refuses to start on.

**The remedy names a RESTART, and that is not padding.** There is no reload path (quince#727), so
editing `config.yml` is not enough on its own; a remedy that left the operator pressing the same
button again would be the wrong half of the answer.

**`PUT /api/config` IS DELIBERATELY NOT REFUSED** by this guard, and the asymmetry is the point: a
full-document replace is how an operator without shell access repairs the very file this refusal is
about. A successful replace **clears** the discard record in the same process, so the guard cannot
outlive the condition it guards against.

**`DELETE /api/config/storage/{name}` needs no equivalent** — measured: on a discarded config
`Current().Storage` is nil, so the forget answers `404` and writes nothing. The hazard is specific to
the path that *adds* to a list it believes is empty.

**Two fields are refused rather than defaulted, and both refusals are load-bearing:**

- **`backend` must be concrete** — `zfs | reflink | hardlink | copy`. An empty value is a **`422`**,
  not an `auto`: the add flow exists to record the backend quince just probed and showed, so
  defaulting an omission would hide a client bug and reintroduce `auto` as stored state by the back
  door. (`auto` itself remains legal in a hand-written file — *absorbed, not removed*.)
- **`default` cannot be claimed.** The first storage is default by implication, and a later one must
  not steal it: honouring the flag would silently re-point every backup that names no storage.
  Re-designation is a separate edit on an existing storage and this rung does not build it.

**`PUT /api/config` TAKES THE OPPOSITE POLICY ON THE SAME FIELDS, AND THE ASYMMETRY IS DELIBERATE**
— ruled 2026-08-08 on [quince#754](https://github.com/novkostya/quince/issues/754). A `PUT` body may
omit `backend`, `zfs.mode`, `zfs.seed` and `default` exactly as `config.yml` may, and the server
fills them; this endpoint still answers `422`.

**The reason is what each endpoint IS.** `PUT /api/config` is a **full-document replace of the
file**, so it must mean what the file means — an API that refuses documents `config.yml` accepts is
the editing surface disagreeing with the thing it edits, which contradicts D12 rather than
extending it. `POST /api/config/storage` is a **narrow add whose caller has just watched quince probe
a path and name a concrete backend**: an omission there really is a client bug, and defaulting it
would hide one.

**It is preserved by ORDERING rather than by a flag**, which is why no handler has to remember which
door a document came through: `validateAddition` runs before the list is resolved, so the add's
strict gate fires first and the write path's resolve is an idempotent second pass behind it.

**Stated here because it looked accidental in both places.** Until quince#754 the `PUT` path simply
never resolved — so it refused these fields by omission rather than by policy, and the two endpoints
agreed for the wrong reason.

**THE FORGET's `422` answers TWO different questions, and the second one is new** (qn.6g, Operator
ruling 2026-08-06 on quince#577). The original asks *is this a valid set of storages?* — forgetting the
default, or the only storage, is refused. The addition asks *is quince busy?*: **a forget is refused
while a backup is running on that storage**, and the message names the job so the remedy — wait for
it, or cancel it — is actionable.

Stated here as a decision rather than left to be met as an inconsistency, because a liveness refusal
on a config endpoint is otherwise a surprise: every other refusal on this route is a statement about
the document, and this one is a statement about the moment. It is the same `{errors:[{path,message}]}`
shape, at `path: "storage"`, so a client that renders one renders the other.

**THE ORDER IS PART OF THE CONTRACT: the declaration refusals outrank the liveness one.** A storage
that is both the default and has a backup running on it is refused for being **the default** — the
permanent reason — and the transient remedy is not offered. Reversed, a user is told *"wait for it to
finish, or cancel it"*, waits out a multi-hour Wi-Fi transfer, retries, and is then told *"it is the
default"*: a remedy that was never going to work, which is the same defect as a silent failure.

**Not a corner case, and it shipped in the first implementation.** The default storage is where
backups go, so *default and busy* is the ordinary state. Every Go gate passed on the wrong order; the
ui-e2e caught it on the first run, because `--demo` keeps a job running on its default storage.

**The alternative was letting the running job die, and it is forbidden by design §4.** Every write
phase re-resolves its storage from the job's binding, so a forget landing between verify passing and
commit completing leaves the commit unable to resolve — and restart-time recovery fails identically,
because the storage is no longer declared. *"A commit failure must not destroy a multi-hour Wi-Fi
transfer"* is the rule that decides it.

**The 200 carries NO restart notice as of qn.6g.** It used to, and had to: the storage stayed served
until the process restarted. `config.Service` now propagates the write to the storage subsystem
before the response is written, so the storage is already gone from `GET /api/storages`. The
`warnings` channel still carries anything an applier could **not** take — that is what keeps the
degraded case from being silent.

### Automation (shape frozen now; implemented in qn.12 — the assisted-backup flow, stack D13)

```
POST /api/automation/backup-opportunity {udid, trigger: "connected_to_power" | "manual"}
     → {action: "notify" | "none",
        reason: "backup_stale" | "backup_fresh" | "device_not_visible"
              | "job_running" | "recently_reminded"}
```

The Shortcut is a dumb opportunity signal (short-lived token auth); ALL policy is
server-side: device visibility, staleness threshold, active-job check, reminder
cooldown. Push kinds (Web Push, qn.12): `backup_available`, `action_required`,
`backup_completed`, `backup_overdue` — each deep-links to the device page.

### Versions & browsing

A **Version** is one immutable committed backup — on the zfs backend it IS a
`@quince-*` snapshot; on namespace backends it is the `latest/` dir (newest) or a
rotated-out `versions/<ts>/` dir. The password is never persisted — unlock is
per-session, always.

```
GET    /api/versions?udid              → {versions: Version[]}
DELETE /api/versions/{id}              → 202 | 404 | 503   // confirmed destructive action
     // 202: artifact (snapshot or dir) + registry row removed, audited (event, no secret),
     // version.deleted emitted. 404: unknown id. 503: no storage subsystem wired (--demo
     // deletes fixtures). Error codes recorded qn.5 (implemented the frozen shape).
POST   /api/versions/{id}/unlock {password} → Session
POST   /api/sessions/{id}/lock         → 204
GET    /api/sessions/{id}/browse?domain&prefix&cursor   → {entries: FileEntry[], next_cursor}
GET    /api/sessions/{id}/file/{file_id}                → streamed decrypted content
```

Domain endpoints (messages, photos, overview) are specified in their rungs (`qn.9+`) and
appended here when built; they are session-scoped lazy reads (`/api/sessions/{id}/...`)
following the same pagination/casing rules. **Only the domain envelope is frozen now**
(external-review point, accepted — concrete fields are designed after a research spike
on real iOS schemas, never before):

```jsonc
{"capabilities": ["threads", "attachments", "search"],   // what this adapter can do
 "adapter_version": "sms-ios17-26.v1",
 "warnings": ["attributedBody fallback used for 12 messages"],
 "unsupported_reason": null,      // set when the adapter can't serve this backup at all
 "page": {"items": [...], "next_cursor": "..."}}
```

## 2. Objects

```jsonc
Device: {
  "udid": "00008140-...",
  "name": "family-iphone",
  "model": "iPhone17,2",            // raw; UI maps to marketing name
  "ios_version": "26.0.1",
  "transports": {"usb": "2026-07-18T...", "wifi": "2026-07-18T..."}, // present keys only
  "paired": "yes" | "no" | "unknown",
  "backup_encryption": "on" | "off" | "unknown",   // lockdown com.apple.mobile.backup/WillEncrypt
  "wifi_sync": "on" | "off" | "unknown",           // lockdown com.apple.mobile.wireless_lockdown (qn.7)
     // Added at qn.7, ruled at that rung's spec review (quince#332) as a non-breaking field
     // addition. Domain and key are both MEASURED on hardware, 2026-07-31: the key is
     // `EnableWifiConnections`, a boolean, read `true` on a device whose Wi-Fi sync was on.
     // It was NOT taken on trust — the name appears nowhere in libimobiledevice 1.4.0, so the
     // roadmap's guess (which turned out correct) could not be known to be until a device said so,
     // and the server shipped answering `unknown` WITHOUT querying until it did. Still owed: an
     // off/on differential, which is what would prove this key is the one that CHANGES rather than
     // `SupportsWifiSyncing`, also true in the same dump. `unknown` continues to mean quince does
     // not know — a failed read, or an unconfirmed pairing, never a guess.
  "last_seen": "...",
  "last_backup": {"at": "...", "job_id": "..." | null, "status": "succeeded"} | null
     // job_id NULLABLE — ratified at the qn.4c spec review ((bz)). last_backup is derived
     // from the newest COMMITTED VERSION, not from job history: versions are the source of
     // truth for "has this device been backed up", so the field survives restarts AND covers
     // ADOPTED versions (a restored/replicated dataset, or quince reinstalled over existing
     // backups) — which have no job at all, hence null. A non-null job_id links to the run
     // that produced it. Semantics follow: this is the last SUCCESSFUL backup (a committed
     // version implies success); a failed last *attempt* lives in the intent-grouped job
     // history, never here. Fabricating a job id for an adopted version would be exactly the
     // state-honesty violation this project forbids.
}

Job: {
  "id": "01J...", "udid": "...", "kind": "backup",
  "transport": "usb" | "wifi",
  "state": "queued" | "waiting_for_device" | "preflight" | "seeding" | "backing_up" | "verifying"
         | "committing" | "succeeded" | "failed" | "cancelled" | "connection_lost",
  // qn.6a (cu opt 1 / cv): `seeding` is emitted between `preflight` and `backing_up` while storage
  // Seed reflink/hardlink-clones latest/ → working/<udid> (O(files); ~23 s on a 34 GB iPhone,
  // near-instant on a resume). The UI narrates "Preparing — cloning from your last backup…" instead
  // of dead air before the on-device passcode prompt. progress.phase mirrors it; it is a running
  // (non-terminal) state.
  "progress": {"phase": "receiving",                          // incl. "waiting_for_passcode"
               "percent": 63.0,                               // percent nullable
               "bytes_done": 2400000000, "bytes_total": 3600000000,
               "files_received": 149,
               "liveness": "active"},   // "" | active | silent_but_connected | suspected_stall
  // A TERMINAL JOB CARRIES NO LIVE PROCESS CLAIM (qn.4a, corrected by quince#313). Once `state` is
  // terminal — succeeded | failed | cancelled | connection_lost:
  //   • `liveness` is `""` for EVERY terminal state, succeeded included. The other three values are
  //     claims about a process that is running; a finished job has none. `""` is a WIDENING of what
  //     was a closed enum, and it is why this line changed.
  //   • `phase` is `""` on failure/cancel/connection_lost and `"done"` on success. The asymmetry is
  //     deliberate: `done` is a true statement about a succeeded job, where `active` is not.
  //   • `percent` is NOT cleared. On a failure it is the last true measurement of how far the job
  //     got — information about the past rather than a claim about now.
  // A client rendering `waiting_for_passcode` on a failed job tells the user to act on something
  // that is over, which is the `State honesty` rule failing at its own example.
  //
  // Clients should still gate live narration on `state` rather than on `phase`. This paragraph says
  // what the server sends; a consumer that asks "is this job running" before quoting a running
  // field is correct whatever a producer does, and both consumers that got this wrong got it wrong
  // by reading `phase` without reading `state`.
  "started_at": "...", "finished_at": "..." | null,
  "error": {"code": "device_disconnected", "message": "..."} | null,
  "retry_of": "01H..." | null,          // set when the user retried a failed job
  "intent_id": "01H...",                // root of the retry chain (== id for a first
                                        // attempt); groups attempts into one user-level
                                        // "I wanted a backup" operation
  "attempt": 2,                         // 1-based position within the intent
  "version_id": "..." | null            // set on succeeded
}
// UI contract: history is grouped by intent_id — a failed-then-retried-then-succeeded
// night renders as ONE operation ("Backup completed after 1 retry"), with attempts
// expandable for diagnostics. GET /api/jobs returns attempts; grouping is client-side
// (or via ?group=intent later). A full Intent entity (server-side object owning
// attempts, wired to automation pushes) is a parked future evolution — retry_of +
// intent_id carry the model until it's needed.
//
// A GROUP HAS ONE INSTANT, AND IT IS THE ONE ITS LABEL DESCRIBES (quince#813, architect ruling
// 2026-08-10). The summary is past-tense for every terminal state, so a terminal group is dated
// from `latest.finished_at` and a running one from `attempts[0].started_at` — the FIRST attempt's,
// because one retried night is one operation and it began when the first attempt did. A running
// row must word its instant as a start ("started 19 minutes ago"); an unworded start under a
// past-tense label is the defect this rule exists for, and the error it produces equals the
// backup's duration — half an hour on a 36 GB Wi-Fi backup.
//
// THE NEWEST-FIRST SORT KEYS ON THAT SAME INSTANT, not on a field chosen separately. Two
// overlapping intents — a long backup started first and finished second — otherwise render in an
// order their own visible timestamps contradict.

Version: {
  "id": "...", "udid": "...", "backend": "zfs" | "reflink" | "hardlink" | "copy",
  // HOW THIS VERSION WAS MADE — not what its storage uses now. Those are different facts
  // (qn.6c gap 1, Operator ruling 2026-08-01) and `Storage.backend` carries the second.
  // They agree permanently, because a storage's backend is immutable once chosen (design §5).
  // Kept on MODELLING grounds rather than compatibility: a version can OUTLIVE its storage —
  // once remove-a-storage exists, detach-and-forget leaves `storage_id` dangling and the backend
  // unrecoverable by join, so this is not derivable in all futures. It is a fact about the
  // version, not a cached copy of the storage's.
  // zfs: a version IS a snapshot; browse_root goes through .zfs (read-only by nature).
  // namespace backends (reflink/hardlink/copy): a version is an immutable dir.
  "zfs_snapshot": "rpool/.../<udid>@quince-2026-07-18T02-30-01J..." | null,   // zfs backend only
  // qn.5b: snapshot name is quince-<YYYY-MM-DDTHH-MM>-<ULID> (date-first for readable `zfs list`
  // ordering; the ULID == version id is the collision-free tail, decisions (co)).
  "browse_root": "/backups/<udid>/.zfs/snapshot/quince-2026-07-18T02-30-01J.../latest"  // zfs
              |  "/backups/<udid>/latest"                                // namespace backends, newest
              |  "/backups/<udid>/versions/2026-07-18T02-30-11Z",        // namespace, rotated-out
  // qn.5b: on zfs, browse_root goes through .zfs/snapshot/<snap>/LATEST (was /working) — the
  // commit atomically exchanges the verified tree into latest/ before snapshotting, so the
  // snapshot IS latest/ = the version. browse_root is computed per request on namespace backends:
  // a version moves from latest/ to versions/<ts>/ when the next commit rotates it.
  "created_at": "...", "job_id": "..." | null,
  // job_id null = adopted: a quince-format version found on disk/in snapshots without
  // a DB record (e.g. dataset replicated/restored to a fresh host; reconciliation
  // re-registers from quince-version.json). Adopted, listed, protected from retention
  // until the user says otherwise.
  "kind": "full" | "incremental" | "unknown",
  // qn.5b (finding #9(a), decisions (cj)/(ck)): kind is derived AUTHORITATIVELY from whether the
  // per-job working/ was seeded from an existing latest/ (incremental) or started empty (a first/
  // full backup) — NOT from Status.plist.IsFullBackup, which the lab proved lies (a first 33 GB
  // backup writes IsFullBackup:false). Every quince version is a COMPLETE, independently-restorable
  // backup regardless of kind; kind stays internal (it gates the encrypted verify's blob-shard
  // check — asserted only on a genuine full) and is dropped from the version CARD in the UI (qn.6a),
  // because "incremental" imports a false fragile-chain mental model.
  "encrypted": true,        // unencrypted versions are permanently badged incomplete
  "is_latest": true,
  // PER (DEVICE, STORAGE) — Operator ruling 2026-08-01 (quince#378). "the newest committed
  // version of this device ON ITS STORAGE". A device backed up to two storages has TWO
  // versions with is_latest: true, one each; a consumer that assumed at most one per device is
  // wrong. No field changed shape — what changed is what the field MEANS, which is why it
  // needed a ruling rather than a patch.
  //
  // Scoped because browse_root READS it: a single global latest would leave every storage but
  // the winner with its newest version flagged false, resolving browse_root to a versions/<ts>/
  // directory that does not exist — the artifact is still in latest/. A replug would silently
  // change which version the UI calls latest on the internal pool.
  //
  // Unattributed rows (storage_id null) form their own group and get their own latest. Excluding
  // them was considered and REJECTED: a device whose rows are all null would then have no latest
  // at all, which is the same unresolvable browse_root reached from the other side.
  "structure_verified_at": "..." | null,   // set at commit (structural verification)
  "content_verified_at": "..." | null,     // set by verify_canary on a later unlock
  "logical_bytes": 42400000000, "physical_bytes": 3400000000,  // best-effort
  "missing": false
  // qn.6a (cr(a) / cv): true = the registry row survives but its on-disk artifact is GONE
  // (reconciliation could not find the snapshot/dir — roll-forward keeps the row, never drops it).
  // store.VersionRow.Missing already exists and is honoured by LastBackup / recomputeLatest /
  // Delete / VerifyVersion; qn.6a crosses it to the wire so the UI renders such a version explicitly
  // DEAD — no size claim, no Unlock, an "artifact gone — remove?" action on DELETE /api/versions/{id}
  // — instead of asserting a backup that does not exist. The row is NEVER omitted: omission would
  // silently shrink history, masking exactly the drift a soak exists to surface.
}

Op: { "id": "...", "udid": "...", "kind": "pair" | "encryption" | "wifi_sync",
      "state": "running" | "waiting_for_user" | "succeeded" | "failed",
      "message": "Tap Trust on the phone…",   // plain-language narration for the UI
      "error": {"code", "message"} | null }
     // `wifi_sync` added at qn.7 and ruled at that rung's spec review (quince#332). An ENUM
     // EXTENSION, which this document's header does not classify — it covers field additions
     // and breaking changes only. Ruled additive here on the same reasoning as qn.6a's
     // `seeding`: the only consumer is the in-repo UI, so a new member breaks nothing but an
     // exhaustive switch with no default. Twice now by precedent rather than by rule; the
     // header is owed a clause saying so.
     // `wifi_sync` emits no `waiting_for_user`: whether iOS demands an on-device confirmation
     // for a lockdown write is UNMEASURED, and narrating a passcode prompt the op may never
     // fire would teach a flow that does not exist. It gains one if hardware shows one.

Session: { "id": "...", "version_id": "...", "expires_at": "..." }

FileEntry: { "file_id": "ab12...", "domain": "CameraRollDomain",
             "relative_path": "Media/DCIM/100APPLE/IMG_0001.HEIC",
             "kind": "file" | "dir" | "symlink", "size": 123, "mtime": "..." }
```

**RULED (was `PROPOSED (gap)`): a `Storage` object, and how a job picks one — `qn.6c`, quince#378.**
**Nothing in this block is unruled.** The `Storage` object and `GET /api/storages` are ruled AND
BUILT (story 5c); `POST /api/jobs {storage_id}` is BUILT too, with `qn.6d` story 6
— which is *work to do*, not *a thread to stop*.

The heading said `PROPOSED (gap)` until 2026-08-01 while its own body already read *"now ruled AND
built"* — the sixth instance of this defect in one day, and the third caught by a reviewer reading a
block rather than its heading. The mechanism is worth naming: **a diff that edits the body does not
force anyone to look at the heading**, and the heading is the part describing the whole. That is
quince#408's argument, made by a PR that had been asked in terms to clear exactly this marker.

The multi-storage epic names `Version.backend` as *the symptom* of a modeling error: a version's
backend is really its **storage's** backend. `qn.6c` fixes the model; this proposal is about how
much of that reaches the wire.

**Two halves have left this proposal and are RULED below: `Version.backend` and
`Version.storage_id`.** The **`Storage` object** and **`GET /api/storages`** are now ruled AND built
as well (story 5c), together with `POST /api/storages/{name}/recheck`. What remains **ruled but
unbuilt** is **the job's** `storage_id` on `POST /api/jobs`, which lands with story 6.

This sentence listed the `Storage` object and `GET /api/storages` as *open* until 2026-08-01 — after
the ruling that decided them — and was found while preparing a ruling on a question it made look
unresolved. **Ruled-and-unbuilt is work to do; unruled is a thread to stop**, and prose that
conflates the two costs a round trip to the seat that has already answered.

**`PROPOSED (gap)` is a load-bearing marker meaning *nothing may be built on this yet*, not a
title**, so a heading naming a half that has been decided tells a reader searching for open
questions the opposite of the truth. **This heading has now been narrowed twice, and the second
time it was wrong for a day** — quince#403 narrowed it off `Version.backend`; quince#405 then ruled
and shipped `Version.storage_id` on the wire, added the `RULED NULLABLE` note below, and **left
this heading and the sentence above still listing it as open.** The architect reviewed that PR,
having blocked the previous one for this exact defect, and merged it without re-reading the
heading (quince#407).

**The lesson is mechanical, not moral: the PR that flips a half must narrow the heading in the
same diff**, because nothing else will notice. A gap block shrinks one half at a time, and the
heading is the only part that describes the whole.

```jsonc
Storage: {
  "id": "01J...",              // the UUID from quince-storage.json (design §5) — stable across
                               // replug, which a PATH is not. Never the config `name`, which the
                               // user may change.
                               //
                               // EMPTY means the storage was NEVER CREATED: quince has not reached
                               // the declared path, so no UUID was ever minted. It does NOT mean
                               // "not currently readable" — an unplugged disk quince created before
                               // KEEPS its id, because the identity lives in the `storages` row and
                               // the marker is only where it is normally READ from (`qn.6d`,
                               // quince#582). Until that fix the id vanished on unplug and returned
                               // on replug, making the stability promised one line above false
                               // across exactly the transition it names.
                               //
                               // ADDRESSING does not use this field. The API keys on the config
                               // `name` (quince#570, ruled 2026-08-02), precisely because a
                               // never-created storage has no id and is the one a user most needs
                               // to reach. `id` is for ATTRIBUTION — `Version.storage_id` joins on
                               // it — and an empty id correctly matches no versions, because a
                               // storage that was never created has none.
  "name": "pool",              // from config.yml; the label the UI shows
  "path": "/backups",
  "backend": "zfs" | "reflink" | "hardlink" | "copy" | "unknown",
                               // "unknown" = never yet reached, so quince does not know. Not a guess.
  "default": true,             // exactly one storage is default
  "reachable": true,
  "unreachable_code": null,    // the machine-readable cause; BRANCH ON THIS, show the reason.
                               // Null when reachable and NEVER absent — a present null is a fact,
                               // an absent key is a version-skew question (same ruling as
                               // Version.storage_id). Two fields rather than one because prose
                               // cannot be branched on and an enum cannot be shown.
                               //
                               // DECLARED VALUES ARE WRONG IN THE CODE TODAY — see quince#569. The
                               // daemon also emits `unreachable` (for an unreadable path, where
                               // this enum says `path_unreachable`) and `corrupt_marker`, neither
                               // of which is below. Documented here as the field's EXISTENCE, which
                               // was the drift; the values are quince#569's to fix and are
                               // deliberately not corrected by inventing a third answer.
  "unreachable_reason": null,  // set when reachable is false; SHOWN, never thrown — an unreachable
                               // storage must not block backups to any other (epic point 5).
                               // THREE distinguishable causes, because the remedy differs:
                               //   path_unreachable  the path itself cannot be read
                               //   missing_medium    the path reads, the marker is GONE, and the DB
                               //                     knows this storage — an unplugged disk's bare
                               //                     mountpoint. Refuses; never re-creates. Added at
                               //                     spec review (quince#381): without it an unmounted
                               //                     mountpoint is created as a NEW storage and
                               //                     backups land on the system disk.
                               //   backend_mismatch  the marker and the probe disagree (remount)
  "will_be_full": true,        // this device's next backup here is a FULL transfer, because
                               // incremental is scoped to (device, storage) and there is no prior
                               // version on this one. Present ONLY when `?udid=` is passed —
                               // the list is device-independent by ruling (2026-07-31).

  // --- space and counts (qn.6d gap A, Operator ruling 2026-08-03) ---
  "filesystem_free_bytes":  1200000000000,
  "filesystem_total_bytes": 3600000000000,
                               // `statfs` on this storage's path — of the FILESYSTEM, NEVER of the
                               // storage. Two storages that are two directories on one disk report
                               // IDENTICAL figures and nothing distinguishes them: `filesystem_id`
                               // and a `filesystem_shared` boolean were both offered and both
                               // DECLINED. The card renders no caveat and a user may read
                               // 1.2 + 1.2 as 2.4 TB — a RULED ACCEPTANCE, not a bug to file.
                               // NULL when unreachable, never 0: a zero is a measurement and this
                               // is an absence.
  "backup_count": 14,          // versions attributed to this storage. MISSING versions COUNT,
  "device_count": 2            // matching UDIDsWithVersions and deliberately unlike the
                               // will_be_full test, which needs a USABLE artifact. Versions with a
                               // null storage_id are charged to nobody.
                               // Properties of the STORAGE: present with or without `?udid=`.
                               // Counts stay populated when the disk is gone; capacity does not.
}

Version: { ..., "storage_id": "01J..." | null }
  // RULED NULLABLE — Operator, 2026-08-01 (quince#378). null = NOT YET ATTRIBUTED.
  //
  // TRANSITIONAL, and that is the difference from `job_id`, whose null (= adopted) is permanent
  // and CORRECT. This one should disappear: a version committed before qn.6c has no storage id
  // because the value is a UUID from its storage's quince-storage.json, written at the storage's
  // creation moment. Migration 0006 deliberately does not fabricate one — backfilling an invented
  // identity onto data that cannot be regenerated is the class the data-at-rest limit governs.
  //
  // A client must NOT read null as "no storage" or substitute a default. It means the server has
  // not worked out which storage this is yet, and it stops meaning that once the storage has a
  // marker. A gate asserts none remains null past that point, because a nullable-with-meaning
  // field whose meaning is "temporary" decays into a permanent unknown unless something says so.
```

**RULED (was `PROPOSED (gap)`): `Version.backend` is KEPT, and it means something DIFFERENT from
`Storage.backend` — `qn.6c`, Operator ruling 2026-08-01, relayed on quince#378.**

> **`Version.backend` is *how this version was made*. `Storage.backend` is *what this storage uses
> now*.**

Two distinct facts that agree permanently, because a storage's backend is immutable by design §5's
own rule — chosen at the creation moment, recorded in `quince-storage.json`, never re-selected.

**This is NOT the compatibility reflex, and it was checked rather than assumed.** The same review
retired an implicit env-var fallback precisely because *"keep it for compatibility"* is how
permanent cruft arrives, so *"keep `Version.backend`"* deserved the same scrutiny. It survives on a
different footing: **a version can outlive its storage.** Remove-a-storage is out of `qn.6c` but
coming, and *detach-and-forget* is one of its candidate semantics — once the storage row is gone,
`storage_id` dangles and the backend is **not recoverable by join**. The field is therefore not
derivable in all futures, which makes it a genuine fact about the version rather than a cached copy
of somebody else's. **The test is whether the field earns its place, not whether removing it would
break someone** — the header's worked example, and this is the case it was written from.

**The proposal offered (a) "keep it, denormalized" and (b) "remove it, breaking", and the ruling is
neither.** (a) framed the field as a convenience copy carrying an implied future breaking removal;
the redefinition makes it a **distinct, permanently true field that never needs removing**. So the
epic's *"`Version.backend` is the symptom"* framing turns out not to apply to the wire at all — only
to the DB, where `versions.storage_id` fixes it.

`Version.browse_root` also stops being universally `/backups/<udid>/…` once roots are plural. That
is a **documentation** change and not a shape change — it is already computed per request from the
root, so only the literals above go stale.

**The OTHER half of this gap — `Version.storage_id` and the `Storage` object — is NOT flipped
here.** It lands with the `0006_storage` migration that creates the column, in its own PR, and is
**ruled nullable** (`null` = *not yet attributed*). Split on the architect's direction so the
redefinition does not wait on the migration: a reviewer of either PR should not go looking for the
other half in the same diff.

Spec: `docs/specs/qn.6c/qn.6c.md`, gap 1.

**RULED (was `PROPOSED (gap)`): `Storage` gains space and counts — `qn.6d`, Operator ruling 2026-08-03, relayed on quince#443.**

`qn.6d` puts a storage card in front of a user choosing a disk, and the object carried nothing to
put on it. Added:

```jsonc
Storage: {
  ...,
  "filesystem_free_bytes":  1200000000000,  // statfs Bavail × Bsize on this storage's path
  "filesystem_total_bytes": 3600000000000,  // NULL when the storage is unreachable, never 0
  "backup_count":  14,                      // versions attributed to this storage. MISSING versions
                                            // COUNT — history the user should still see (qn.6d
                                            // rung-ruled decision 3). §1 states this beside the same
                                            // field; THIS copy was silent on it, which is how the
                                            // demo came to implement the opposite rule with a green
                                            // e2e sitting over the disagreement (quince#661).
  "device_count":  2                        // distinct devices with a version here, missing included
}
```

Three decided points:

1. **The prefixed names are kept.** `statfs` reports the **filesystem**, not the storage, so two
   storages that are two directories on one disk report identical figures. `free_bytes` on a
   `Storage` object would read as the storage's own; the prefix is ugly and the ugliness is doing
   the work, because nothing else in the payload says why the numbers match.
2. **Capacity is `null` when unreachable, never `0`** — a zero is a measurement and this is an
   absence. **Counts stay populated**, because they are the DB's answer and the DB is reachable.
   **That asymmetry is carried by the fields themselves** — counts present, capacity `null`.
3. **Counts are properties of the STORAGE**, present with or without `?udid=`, which continues to
   add only `will_be_full`. The list stays device-independent.

**The CARD renders no filesystem caveat, and that is a ruled acceptance rather than an oversight.**
`qn.6d`'s spec first committed the card to *"1.2 TB free on this filesystem"* when storages share
one and plain *"1.2 TB free"* when they do not. **That branch is not implementable with these
fields**: equal byte counts do not prove a shared filesystem, and no field carries filesystem
identity. A `filesystem_id` and a `filesystem_shared` boolean were both offered and both declined.
So the card always says plain *"1.2 TB free"*.

**The accepted cost, written down so it is not rediscovered as a bug:** two storages that are two
directories on one disk each show the same figure with nothing in the UI saying it is the same
space, and a user may read 1.2 + 1.2 as 2.4 TB. **Do not "fix" this by reintroducing the
distinction and do not file it.** The wire names stay prefixed regardless, so the contract remains
honest for API clients even where the card renders no caveat.

**Two pieces of pre-existing drift are corrected by the PR that builds this**, and neither is part
of the ruling: §1's code block omits both the `?udid=` form and `POST /api/storages/{name}/recheck`,
and this section has never documented `unreachable_code` — whose declared values are wrong in a
second way, see quince#569.

Spec: `docs/specs/qn.6d/qn.6d.md`, gap A.

### StorageProbe (`qn.6e`)

`POST /api/storages/probe`'s answer. **A candidate, not a resource** — see §1 for why it is not a
`Storage`.

```jsonc
{
  "path":       "/mnt/nas-backups",       // what the client sent, verbatim
  "clean_path": "/mnt/nas-backups",       // filepath.Clean of it; quince acts on this one

  "outcome": "new",                       // adopt | new | missing | not_a_directory
                                          // | unwritable | corrupt_marker | unreadable
  "reason":  "/mnt/nas-backups is usable and holds no quince storage yet",

  "backend":        "reflink",            // "" on every refusal
  "backend_reason": "FICLONE clone-sharing probe passed on /mnt/nas-backups: the clone's extents are reported shared (FIEMAP_EXTENT_SHARED) and the clone is independent of its source",

  "marker":   null,                       // {storage_id, backend, created_at} when one was readable
  "non_empty": false,                     // data at the path that is not quince's own
  "zfs":       "none",                    // path | host | none

  "filesystem_free_bytes":  1800000000000,
  "filesystem_total_bytes": 2000000000000
}
```

**`outcome` is FROZEN.** A client renders different prose *and a different next action* for each, so
adding a value is a contract change. Five of the seven are refusals and they are deliberately not
collapsed: *missing* and *unwritable* have different remedies, and a single *unusable* would tell a
user nothing they could not already see.

**`reason` is the daemon's sentence and always names the path** (quince#514). A client shows it
rather than composing its own, for the reason `unreachable_reason` gives one section up: quince knows
which path and which marker, and a client's copy of an enum cannot.

**`marker` is reported on ANY outcome**, not only on `adopt` — so a form can say *"this IS storage X,
and the path is read-only"* rather than only the second half. It is a **subset** of the on-disk
marker: no `checksum`, no `app_version`. Those are quince's own integrity detail and version history,
and publishing them would freeze them into a form's contract.

**`backend` on `adopt` is not a recommendation.** A storage's backend is written at its creation
moment and is immutable; a later probe that disagrees is a remount, not a re-selection. The form
shows it and offers no selector.

**`non_empty` is a FACT, never a refusal.** A path holding backups from before storage markers
existed has no marker and is not empty, and is exactly what an upgrading operator types.

**`zfs: "none"` MEANS NO SIGNAL AND MUST NEVER BE RENDERED AS *"ZFS not supported"*.** In `hook` mode
the container holds no `zfs` userland at all and zfs works perfectly through the host helper, so a
negative reading is a **guaranteed false negative for the supported containerised topology** — the
one most deployments use. *Not detected*, or silence; never a capability claim. This is the
no-silent-caps rule pointed the other way: **do not assert an absence you cannot observe.**

**The filesystem prefix carries the same meaning as on `Storage`** and for the same reason: two
candidate paths on one disk return identical figures and nothing here distinguishes them. Both are
`0` when the path could not be stat'd — a probe of a missing path has nothing to measure, and unlike
`Storage` there is no reachable/unreachable axis to hang a `null` on.

Spec: `docs/specs/qn.6e/qn.6e.md`.

### StorageHookCheck (`qn.6e`)

`POST /api/storages/probe/hook`'s answer. §1 carries the verb order, the four outcomes and their
remedies; this is the shape.

```jsonc
{
  "outcome": "ok",          // ok | not_migrated | parent_mismatch | unreachable — FROZEN
  "reason":  "the helper answered and its parent dataset matches — quince can snapshot here",
  "detail":  "10\t20"       // the TRANSPORT'S own output, verbatim
}
```

**`detail` may name the operator's host.** Shown to the authenticated admin in their own browser;
**never logged, never in a fixture, never pasted into a PR or an issue.** The argv is never included
— the composed transport carries `user@host` by construction. See §1 for why blanking it would be worse: ssh's
own message is the whole answer to why a key does not work.

**`reason` is quince's sentence and is safe to render anywhere**, which is the split: `reason` for
the UI, `detail` for the user's eyes on their own machine.

### StorageZFSKey (`quince#818`)

`POST /api/storages/zfs/key`'s answer. §1 carries the endpoint's rules; this is the shape.

```jsonc
{
  "path":            "/data/keys/zfs",   // where the PRIVATE half lives; never its contents
  "public_key":      "ssh-ed25519 AAAA… quince",
  "authorized_keys": "command=\"/usr/local/sbin/quince-zfs-helper\",no-port-forwarding,… ssh-ed25519 AAAA… quince",
  "created":         true                // false when quince FOUND a key already there
}
```

**EVERY FIELD IS SAFE TO RENDER.** The private half is not on the type, is never logged and is never
in a fixture — the discipline `StorageHookCheck.detail` needs a paragraph for, this one gets by
construction.

**`authorized_keys` IS THE ARTIFACT and `public_key` is context.** The forced command is what bounds
quince on the host, so the two are one string rather than a key plus a suggestion.

### StorageZFSHelper (`quince#818` piece C)

`GET /api/storages/zfs/helper?parent_dataset=<ds>`'s answer — the constrained helper script with the
operator's own dataset already substituted.

```jsonc
{
  "script": "#!/bin/sh\n… PARENT=\"tank/backups/iphone\" …",  // the WHOLE file, saveable as-is
  "path":   "/usr/local/sbin/quince-zfs-helper"               // where it goes
}
```

**THE SCRIPT IS SERVED RENDERED, NOT AS A TEMPLATE.** The substitution is one line and a client could
do it — but the value goes inside a double-quoted assignment in a script the operator runs **as root
on another machine**, so whoever substitutes must also validate. Server-side keeps the validation,
the placeholder guard and the refusal in one place, beside the pattern that already guards dataset
names for argv use.

**A `parent_dataset` that could break out of the quotes is `422`, naming that field** — refused
rather than escaped. Every legal ZFS name already matches `datasetPattern`, so nothing valid is lost,
and an escaping routine is a thing that can have a bug where a refusal cannot.

**`path` IS ON THE WIRE BECAUSE IT IS HALF THE INSTRUCTION.** It is the same constant the
`authorized_keys` line pins as its forced command, so the two cannot drift — a helper saved anywhere
else is never reached, and a script with no destination leaves the operator something to look up,
which is what this endpoint exists to end.

**`500` is reserved for one case that should be unreachable:** the embedded script no longer carries
the line quince substitutes. Serving it anyway would hand over a valid script pointing at the
placeholder dataset, so it refuses; a build-time test asserts the placeholder exists, which is what
makes the `500` a backstop rather than a live path.

**No state, no write, nothing about this install** — the answer is a pure function of the embedded
script and one query parameter, which is why it is a `GET` where the key endpoint beside it is a
`POST`.


### StorageZFSHostKey (`quince#912`)

`POST /api/storages/zfs/hostkey` and `POST /api/storages/zfs/hostkey/trust` — the two halves of the host-key ceremony.

```jsonc
// scan → 200
{
  "found": true,
  "host_key": {
    "host": "nas.local", "port": 22,
    "key_type": "ssh-ed25519",
    "fingerprint": "SHA256:…",                  // the form `ssh-keygen -lf` prints
    "line": "nas.local ssh-ed25519 AAAA…"       // the complete known_hosts entry
  },
  "reason": ""
}

// trust ← {"line": "…"}   → 200
{ "trusted": true, "path": "/data/keys/known_hosts" }
```

**WHY IT EXISTS AT ALL.** quince composes `StrictHostKeyChecking=yes`, and `config/zfsssh.go` argues for it correctly — `accept-new` trusts whatever answers on the first connect, which is exactly the moment an attacker would want. But nothing put an entry in `known_hosts`, and the file is **inside the container**, so the only remedy was `docker exec`. That made the zfs branch of the add-storage form impossible to finish from the UI (quince#912).

**THE SPLIT IS THE SECURITY PROPERTY, not ergonomics.** Scan reads what the host offers and returns its **fingerprint**; it authenticates nothing, sends no credential and writes nothing. Trust records **the line the caller passes back** — the one the operator was shown — and never re-scans. If it re-scanned, a host answering differently between the two calls would be recorded *after* the operator confirmed a different fingerprint, and the confirmation would be theatre.

**A CHANGED KEY IS A `422` AND THE EXISTING ENTRY SURVIVES.** An entry for that host with a different key means either a rebuilt host — ordinary — or an impersonation. quince cannot tell them apart and must not choose: silently replacing would make this button trust an attacker as readily as the real host. The refusal names both possibilities and the file, and the operator removes the old line by hand once they know which.

**EVERY ANSWER ABOUT A REAL ADDRESS IS A `200`, including "nothing answered"** — the rule `probe` and `probe/hook` already follow. A host that is not up yet has answered the question. Only a malformed request — no `ssh_host`, or a trust with no `line` — is a `422`.

**Recording the same key twice is a no-op**, not a second line: a `known_hosts` with a hundred identical entries is one nobody reads when it matters.

**Nothing here is secret.** A host's public key is handed to every client that connects. The private half of quince's OWN key is a different thing and is never on this or any wire (see `StorageZFSKey`).
## 3. WebSocket (`/api/ws`)

One socket per client, server→client only (commands go via REST). Envelope:

```jsonc
{"type": "job.updated", "ts": "2026-07-18T...", "data": { ... }}
```

| type | data | notes |
| --- | --- | --- |
| `device.attached` / `device.detached` | `Device` + `{transport}` | emitted per transport edge |
| `device.updated` | `Device` | name/pairing/info refresh |
| `job.updated` | `Job` | every state or progress change; progress throttled to ≤2/s |
| `job.log` | `{job_id, chunk}` | raw log tail chunks |
| `op.updated` | `Op` | pair/encryption op narration + state changes |
| `version.created` / `version.deleted` | `Version` | includes adopted versions found on disk |
| `session.locked` | `{session_id, reason: "user" \| "ttl" \| "vault_crash"}` | UI drops decrypted views |
| `hello` | `{server_version, time}` | first frame after auth |

Client contract: reconnect with backoff + `GET` refresh of current views on reconnect
(events are notifications, not a replayable log).

## 4. Vault RPC (core ⇄ `quince-vault serve`)

JSON-RPC 2.0, newline-delimited, over stdio. The first frame MUST be `initialize` —
password and backup path travel inside it (stdin-only, never argv/env; raw RPC frames
are never logged). The vault is spawned with its **session scratch root as its only
writable directory**; no filesystem destination ever crosses the RPC boundary — the
vault writes only under its root and returns opaque handles with scratch-relative paths.
The version dir is passed read-only. **This protocol is the replaceable seam**: the core
talks to a `vault.Vault` Go interface; any implementation (today's Python process, a
future all-Go port) must pass the golden conformance suite (`vault/conformance/`) —
recorded request/response pairs against fixture backups — before it can ship.

```
initialize  {password, backup_path}          → {protocol_version, device_name,
                                                ios_version, file_count, manifest_sha256}
list        {domain?, prefix?, cursor?, limit} → {entries: FileEntry[], next_cursor}
stat        {file_id}                          → FileEntry
materialize {file_id}                          → {handle, rel_path, size}
                                               // decrypted under scratch root; core
                                               // resolves rel_path against the root it
                                               // owns, streams, unlinks
verify_canary {}                               → {ok}   // decrypt one small known file;
                                               // basis for content_verified_at
lock        {}                                 → {}     // then process exits 0
```

Domain methods (`overview.*`, `messages.*`; `photos.*` if ever revived) are appended
here with their rungs (`qn.9+`); all reads are lazy (domain DBs decrypted to scratch on
first use) and paginated. Errors: JSON-RPC error with `data.code ∈ {bad_password, corrupt_manifest, io,
not_found, unsupported_ios}`. The core treats malformed output or nonzero exit as a vault
crash: session dies, `session.locked{reason: "vault_crash"}`, user sees it honestly.

## 5. Derived caches (`/cache`)

No persistent index of backup content exists (Operator decision — lazy session reads
only). The narrow exception: derived artifacts genuinely too expensive to rebuild per
session. **Currently this section has no consumer** — photos (the only planned one) are
parked at lowest priority, and if they return, the first move is reusing Apple's own
prebuilt thumbnails inside the backup (`CameraRollDomain → Media/PhotoData/Thumbnails`),
which may make this section permanently unnecessary. The contract stays defined for
whatever earns it:

```
/cache/derived/<version_id>/<artifact>/...
/cache/derived/<version_id>/fingerprint    // {manifest_sha256, artifact_schema_version}
```

Rules: validate fingerprint against the live version before *every* use; on mismatch or
missing source, drop silently and rebuild or serve without; wiping `/cache` at any time
is always safe and never user-visible beyond latency. Session scratch lives in
`/cache/scratch/<session_id>/` and is wiped on lock.

## 6. Config

**Bootstrap env** — deployment topology only, everything a container needs before the
app can run (unknown `QUINCE_*` vars are a startup warning, typo guard):

```
QUINCE_DATA=/data   QUINCE_CACHE=/cache
QUINCE_LISTEN=:8968
QUINCE_TRUSTED_PROXIES=203.0.113.5,198.51.100.0/24   # default: empty = trust none
QUINCE_DEMO_RESET_MINUTES=30                         # default: unset = state no schedule
```

**`QUINCE_TRUSTED_PROXIES`** lists the IPs/CIDRs whose `X-Forwarded-*` headers quince believes
(design §6: *"reverse-proxy trust headers only from configured addresses"*). **Empty is the
default and means trust none** — the login rate limiter buckets on the peer address, which is
what a direct-LAN deployment wants and is byte-for-byte the pre-quince#464 behaviour.

Set it when a reverse proxy terminates TLS in front. Without it every visitor arrives as the
same peer, so **ten wrong password guesses deny login to everybody**, correct password included
(quince#464). **Env rather than `config.yml`** — Operator ruling 2026-08-02, quince#549:
`--public-demo` deletes its config at startup, so the deployment that most needs a trust list
could never carry one; and in that mode every visitor can `PUT /api/config`, which would make a
file-based list editable by the population it protects against.

**`QUINCE_DEMO_RESET_MINUTES`** is how often the **deployment** restarts a `--public-demo`
instance, in whole minutes. quince runs no timer and performs no reset — the restart is
performed from outside the process (public-demo spec D4) — so this is a fact quince is *told*
purely so the login screen can warn a visitor that their work will be wiped, and how soon
(story 6). **A reported deployment fact, not a setting**, Operator ruling 2026-08-02
(quince#470): D12 governs settings, and the test that ruling gives is *does any code branch on
this value*. Nothing does.

**Unset is the default and means "state no schedule"** — the login screen then says the demo
resets *periodically*, never nothing at all. A present-but-unusable value (`30m`, `30 minutes`,
`0`, a negative) is **dropped with a startup warning** rather than refusing to start; a dropped
interval and an unset one render identically, so the log is the only place that difference can
appear. It is reported on `GET /api/health` **only** in `public_demo` mode, and setting it
elsewhere warns: nothing restarts `--demo` or the shipping product, so an interval there would
be a destructive promise on a screen where it is false.

**`QUINCE_BACKUPS` was RETIRED at `qn.6c`** (gap 3, Operator ruling 2026-07-31 — quince#378).
Backup locations are **declared**, in `storage:` below: no env var, no implicit storage,
no fallback. **Setting it now produces the ordinary unknown-`QUINCE_`-variable warning** — it is
gone, not merely unread, and that is asserted rather than assumed.

The retired variable carried a built-in `/backups` default, so every deployment had a working
storage while declaring nothing. That implicit path is what the ruling removed: *"I see no reason
to accumulate backward-compatibility garbage."* Both agent seats recommended keeping a fallback
and both were wrong for the same reason — the argument priced a population of deployments that
does not exist.

**Everything else**: `/data/config.yml` — single source of truth, edited by the UI and
by hand equally (stack D12: atomic validated writes, canonical order, **only the keys the user set
and no generated annotation** — ruled 2026-08-08, quince#728; this line said *"canonical order +
generated doc-comments"* until then — file-watch pickup, invalid edits keep last-good + UI banner, no
secrets ever).

**THE TWO EDITING PATHS DIFFER TODAY, AND THAT IS A RULING'S COST RATHER THAN AN OVERSIGHT.**
A setting changed **through the UI applies immediately**; the **same setting hand-edited in
`config.yml` still needs a restart**, because nothing watches the file. `qn.6g` (quince#577) builds
propagation — `config.Service` telling the running subsystems about its own write — and file-watch
was **split into its own, unallocated rung** by Operator ruling 2026-08-04, option (a), relayed on
[quince#577](https://github.com/novkostya/quince/issues/577#issuecomment-5182609911).

So *"edited by the UI and by hand equally"* above is the **destination**, and *"file-watch pickup"*
in that list is **not yet built**. Stated here rather than left for a reader to discover: it was the
condition the ruling was accepted on, and a document describing a wider reality than the one that
exists is this project's most-filed defect. Until file-watch lands, a hand-edit is picked up at the
next start.

### Which settings apply live — the per-key answer

**THREE BINS, NOT TWO, and the third is why this is a table rather than a sentence.** *Live* and
*restart-required* cannot classify these keys honestly, because **five of them are read by nothing at
all**. A two-bin table would have to file those under one heading or the other, and both are false:
calling an unread key *restart-required* promises that restarting makes it work.

Verdicts measured against the code rather than read off the schema (`qn.6g`, quince#577).

| key | verdict | why |
| --- | --- | --- |
| `backup.preferred_transport` | **live** | The backup applier swaps one synchronized field; read per job, so a running job keeps the answer it started with. |
| `backup.require_encryption` | **live** | Same applier, same guarantee. |
| `storage[]` membership | **live** | The storage applier — `qn.6g`'s first consumer. An **add** also **enqueues a reconciliation pass**, so a disk that already holds backups shows them **shortly after**, without a restart. It used to scan *inline*, inside the request and under `writeMu`, which hung the button for a full walk and queued the next config write behind it (quince#715); `qn.6i` made it a trigger. While that pass is outstanding `GET /api/health` reports `reconciling` — see §1, where what that promises is written down. |
| `storage[].path` · `.backend` · `.zfs.*` | **live** | Re-resolved by the same `resolveSlot` a restart uses, so a live apply and a restart cannot disagree about what a storage IS. |
| `storage[].retention.*` | **live** | Rides the storage applier: `policyFor` reads retention off the slot list, so `ApplyStorages` is the only path by which an edit can reach `Prune`. |
| `storage[].default` | **live** | Position decides it — re-ordering moves `slots[0]`. Safe only because forgetting the default is refused; a *re-designation* takes effect for the next unbound job. |
| `reconcile.interval_minutes` | **live** | `qn.6i`. The runner re-reads it when it schedules the **next** wait, and a change WAKES the scheduler so it applies to the wait already in progress — without that, turning six hours down to fifteen minutes would take up to six hours to bite, which is live in name only. **`0` disables the SCHEDULE and nothing else**: startup, storage-added and job-end are correctness triggers where the schedule is hygiene, and one key should not turn off both. Turning it back on needs no restart either. |
| `devices.manage_muxer` · `.usbmuxd_socket` · `.netmuxd_addr` | **restart** | **D12 requires this sentence:** a netmuxd restart tears a live Wi-Fi backup, and Wi-Fi is the primary transport — so applying these live means first ruling on what happens to a running transfer. Out of scope for `qn.6g`, named rather than silent. |
| `tls.cert_file` · `.key_file` | **live** | Rotation was always live — `tlsx.Keeper` re-reads the files. The *paths* became live in quince#900: the mux is bound on every start, so the TLS half exists whether or not a certificate does, and the `tls` applier hands the `Keeper` the new pair. **Turning TLS on and off are both live**, which is what an apply-and-revert flow needs. **An unusable pair is SAVED, WARNED and NOT APPLIED** — an `Applier` runs after the write and structurally cannot refuse it, so the daemon keeps serving the certificate it had and says so in the `PUT` response; it picks the new pair up with no restart once both files are readable. That is deliberately *unlike* startup, where `config.CheckTLS` **refuses to start** — coming up on plain http for somebody who asked for https is a silent downgrade, and there is no response to warn into. A bad edit cannot lock anyone out: the plain half redirects on a certificate being **loaded**, not configured. |
| `sessions.allow_insecure_transport` | **live** | `qn.6g`'s fifth consumer (quince#900). **Both** consumers moved, and moving one would have been worse than moving neither: the plain half of the mux now reads it **per request** rather than choosing its handler at bind, and the `sessions` applier calls the auth service's setter with **whatever the file says** — including `false`. It was `restart` for two reasons and they were different: the handler choice was fixed at bind, and `applyInsecureTransportOptIn` returned before its setter when the opt-in was off, so a settable field was a **one-way latch** that nothing in a running process could lower. Turning it **on** returns the degraded-mode warning with the `PUT`, which `DegradedModeWarnings` used to emit on load only. |
| `automation.staleness_days` · `.reminder_cooldown_hours` | **nothing reads it (declared)** | `qn.12`'s — declared debt rather than a defect. |
| `ui.theme` | **already live** | Client-side, applied from the `PUT` response. |

**`backup.transport` IS ABSENT BECAUSE THE KEY NO LONGER EXISTS.** `qn.6g`'s spec listed it as
*"nothing reads it"*, true when written and false four days later: quince#654 renamed it
`preferred_transport` and gave it a consumer. Recorded here because the spec still carries the stale
row and points at this table for the correction.

**`sessions.ttl_minutes` IS ABSENT FOR A DIFFERENT REASON: THE KEY WAS REMOVED RATHER THAN WIRED.**
It sat in the third bin — validated, documented, editable, read by nothing — and the Operator ruled
on 2026-08-04 to delete it and mint it again when something reads it (quince#656), on quince#378's
precedent: *"I see no reason to accumulate backward-compatibility garbage."* **That is the opposite
answer to `backup.transport`'s directly above, and the difference is not inconsistency:** that key
had a consumer already in the tree, so wiring it made an existing undocumented policy visible. This
one had nothing to wire to, and relabelling would only have produced a more accurately-named setting
that still did nothing. **A key earns its place by being read.** A user who set it gets `unknown
config key "sessions.ttl_minutes" (ignored)` and loses a value that never had an effect; quince#401
does not apply, because there is no successor to name.

**A key in the third bin is not made to work by a restart**, which is the whole reason that bin
exists. Its remaining occupant is the `automation.*` pair, which is **declared debt for `qn.12`**
rather than a defect: live-apply cannot make an unread field take effect, and folding a fix in would
let this table claim a key works when nothing consumes it.

**THE UI RENDERS THIS VERDICT AND STORES NOTHING.** *"Restart to apply"* appears where this table
says **restart** and nowhere else — which today means it appears on no field the Settings form
renders, since every key that form edits is **live**. That sentence read *"live or unread"* until
quince#656 removed the one unread field the form carried. The notice was deleted rather than made
conditional, and if a **restart** key is ever added to that form it comes back attached to that
field.

Schema v0:

```yaml
backup:
  preferred_transport: usb  # usb | wifi. WHICH transport an `auto` request uses when the device is
                            # present on BOTH. IGNORED when only one is available — a device on
                            # Wi-Fi alone is backed up over Wi-Fi whatever this says.
                            #
                            # A PREFERENCE, NEVER A RESTRICTION. The other reading would make a
                            # Wi-Fi-only device silently unbackupable through a setting whose name
                            # does not say so, and Wi-Fi is the PRIMARY transport (design §4).
                            #
                            # RENAMED FROM `transport`, WHICH WAS READ BY NOBODY (quince#654,
                            # Operator ruling 2026-08-04). It was parsed, range-checked and editable
                            # in Settings while changing nothing. `transport: usb` reads as "use
                            # USB"; the only true meaning is "prefer USB". Setting the old key now
                            # produces the ordinary unknown-key warning (quince#401 — it does not
                            # name the successor, which is why the rename happened while the key
                            # still did nothing and nobody could lose a value by it).
                            #
                            # NO `auto` HERE, and this is NOT the request enum: as a preference,
                            # `auto` would mean "prefer whatever is already preferred". `auto`
                            # remains legal as a REQUEST transport on POST /api/jobs and as the
                            # CLI's `--transport auto`. Two enums, two of their values shared.
                            #
                            # Default `usb` because it PRESERVES today's behaviour, and for no other
                            # reason — no throughput claim is made in either direction.
  require_encryption: true  # preflight fails (actionably) on an unencrypted device;
                            # false permits unencrypted backups behind persistent UI
                            # warnings (no Health/Keychain/passwords in such backups)
storage:                    # REQUIRED, qn.6c. `storage:` IS THE LIST (quince#473) — no wrapper
                            # key, no globals, no inheritance. It is the ONLY key with no
                            # default: quince REFUSES TO START without at least one, naming the
                            # key and printing what to write. There is no sane default for where
                            # a user's backups live now that QUINCE_BACKUPS is retired, and the
                            # honest form of "no default" is a refusal, never a guess (D12
                            # near-miss, declared).
                            #
                            # A SINGLE STORAGE IS JUST A PATH — everything else has a default:
                            #
                            #     storage:
                            #       - path: /backups
                            #
  - name: local             # OPTIONAL — defaults to `path` (ruled 2026-08-01, quince#504). On a
                            # single-storage install `name: backups, path: /backups` says the
                            # same thing twice. It is the stable identity across replug, where a
                            # path is not, and it keys the DB row.
    path: /backups          # REQUIRED; absolute; unique across entries
    default: true           # OPTIONAL with exactly ONE storage, where it is implied. With
                            # several, exactly one must carry it — declaring none of several is
                            # an ERROR, not a pick: order is not intent.
    backend: auto           # auto | zfs | reflink | hardlink | copy. Defaults to `auto`.
                            # auto: zfs when this entry's zfs block is configured, else probe
                            # reflink → hardlink → copy on THIS path.
                            #
                            # `auto` IS STILL LEGAL, AND NOW BY RULING RATHER THAN BY
                            # DEFERRAL. The 2026-08-02 direction that only a CONCRETE
                            # backend may land here was ruled ABSORBED, NOT REMOVED
                            # (quince#502, 2026-08-07). The loader is unchanged; what
                            # changed is that the ADD FLOW writes the concrete backend it
                            # probed, refusing an empty or `auto` one with a 422.
                            # Do not tidy it out: `auto` is the ONLY thing that checks a
                            # declaration against the medium (storage.Select returns an
                            # explicit backend WITHOUT probing), so removing it would make
                            # quince-storage.json record a guess and fail at seed time —
                            # and this file's own one-line form, which the startup
                            # refusal teaches, IS `auto`.
    zfs:                    # THIS storage's zfs settings. No global to inherit from and none to
                            # opt out of, so the `zfs: {}` idiom is gone with quince#458 — a
                            # second storage can no longer be handed a parent dataset that was
                            # written for the first.
      parent_dataset: ""    # e.g. rpool/userdata/iphone-backup; one child dataset per device.
                            # Two storages sharing one parent dataset are REFUSED: they would
                            # create the same <parent>/<udid> per device and each believe they
                            # owned it. That refusal survived the flattening because it was
                            # never about inheritance (quince#473).
      mode: hook            # hook — the ONLY value, and the default. `exec` ran `zfs` in the
                            # container and was REMOVED (Operator ruling 2026-08-10, quince#697):
                            # the shipped image has no `zfs` binary. A file still carrying
                            # `mode: exec` is REFUSED by path rather than ignored — the key
                            # outlived its second value so that the refusal exists at all.
      ssh_host: nas.local   # WHERE the quince-zfs-helper runs. quince composes the whole ssh
                            # command from these four — including the host-key options — so the
                            # file no longer carries an argv. Operator ruling, relayed at
                            # quince#818 comment 5245496176.
      ssh_user: zfsuser     # WHOSE authorized_keys carries the forced command. That `command=`
                            # entry is what bounds quince on the host, which is why SSH is the
                            # only shape rather than one transport among several.
      ssh_port: 22          # DEFAULTS. The happy path writes neither (D12), and `ssh_key` stays
      ssh_key: /data/keys/zfs   # settable because the path was already settable inside hook_cmd —
                            # removing it would be a narrowing dressed as a simplification.
      hook_cmd: ""          # RETIRED, and the key is kept ONLY so a file carrying it is REFUSED by
                            # path — naming ssh_user/ssh_host/ssh_port/ssh_key — rather than
                            # reported as an unknown key and ignored. Same shape as `mode: exec`.
                            # A config still carrying it is DISCARDED at load; quince#852 and
                            # quince#849 are what make that safe and legible.
                            # (forced-command: snapshot/destroy/list @quince-*, create children,
                            #  seed working/<udid> from latest/, capacity; dataset destroy
                            #  impossible via the key)
                            # THE VERBS ARE FIXED COMMANDS — the helper dispatches on the verb and
                            # DISCARDS the rest of the argv. quince must never send flags expecting
                            # them to reach `zfs`: it did once, and got a snapshot list back at
                            # exit 0 (quince#600, ruled 2026-08-03). `capacity` takes NO argument
                            # at all — the helper uses its own $PARENT — which is why the fix was a
                            # new verb rather than a `list` that forwards flags. Adding a verb is a
                            # change to THIS list and an operator migration; see deploy/storage.md.
      seed: auto            # qn.5b: in-container strategy to clone latest/ → working/<udid> at job
                            #   start (renamed from `mirror` when the reflink moved commit→seed).
                            #   auto (reflink → copy) | reflink | copy — hardlink is NEVER used for
                            #   the seed (it would alias the committed latest/; gate 12c). In hook
                            #   mode the host-side `seed` verb does the reflink and this is moot.
    retention:              # THIS storage's keep policy. ABSENT falls back to the code defaults
                            # below, which D12 permits — a setting with a sane default the file
                            # need not spell out. Prune groups a device's versions by storage and
                            # applies each one's policy, so `keep_recent: 10` means ten ON THIS
                            # DISK; a single policy across storages would have let a second disk
                            # silently change what the first one keeps.
      keep_recent: 10
      keep_daily: 30
      keep_weekly: 12
devices:
  manage_muxer: true        # true = SIMPLE profile: quince owns the lifecycle of EVERY muxer
                            # daemon it is configured to reach — usbmuxd (USB) and netmuxd
                            # (Wi-Fi) — as supervised subprocesses with restart-w/-backoff,
                            # each refusing loudly at startup if its address is already served
                            # (no silent adoption). false = HARDENED/external: quince only
                            # dials both and reports them `external` in /api/health.
                            # ONE flag for both daemons (D12; qn.4c ruling (bz)).
  usbmuxd_socket: /var/run/usbmuxd    # authoritative: the managed usbmuxd gets -S <this>
  netmuxd_addr: 127.0.0.1:27015       # authoritative: the managed netmuxd gets --host/--port
                            # from this (plus a private --socket-path and --disable-usb).
                            # Wi-Fi discovery is mDNS-only, so the container must be on the
                            # LAN — see deploy/compose.nas.yml.
tls:                        # qn.6f — the certificate quince serves ITSELF, for the tier with no
                            # reverse proxy in front of it. BOTH EMPTY (the default) MEANS TLS IS
                            # OFF, and that is a correct configuration, not a degraded one: it is
                            # what the reverse-proxy and `tailscale serve` tiers want, and what
                            # `--demo` runs. Setting exactly ONE of the two is a 422 naming the
                            # other — it can only be a mistake, and unreported it reads as "off".
  cert_file: ""             # PEM certificate chain
  key_file: ""              # PEM private key — a PATH. A key body never enters this file (D12);
                            # config.yml is hand-editable and carries no secrets, ever.
                            #
                            # WHETHER THESE FILES EXIST, PARSE, OR MATCH EACH OTHER IS NOT A
                            # VALIDATION ERROR. An invalid config is DISCARDED in favour of
                            # last-good/defaults, and the defaults have no TLS — so a certificate
                            # fault raised as a validation error would start quince on plain HTTP
                            # for somebody who asked for HTTPS, behind a warning banner they
                            # cannot see because they are not connected. It is a FATAL check on
                            # the serve path instead: quince refuses to start, names the file and
                            # the reason, in the shape the storage requirement already uses.
sessions:
  allow_insecure_transport: false
                            # qn.6f — the user's own opt-in to plain http on a network they
                            # trust. OFF by default. Operator ruling 2026-08-02, option (b);
                            # design §6 carries the reasoning and the rejected alternatives.
                            #
                            # It RELAXES THE FALLBACK ONLY: `r.TLS != nil` and a BELIEVED
                            # `X-Forwarded-Proto: https` still force Secure, so *the header can
                            # only ever upgrade* is preserved. Only the non-loopback-host branch
                            # becomes conditional on this flag.
                            #
                            # "BELIEVED" since quince#555: the header counts only from an address
                            # in QUINCE_TRUSTED_PROXIES — an UNSET list believes anyone, which is
                            # the default and the old behaviour. The unconditional phrasing was
                            # true of the COOKIE and false of the two consumers that invert the
                            # predicate: the 426 refusal and the onboarding check both fail
                            # toward "everything is fine" on an injected header.
                            # Under `sessions:` and not `tls:` because it
                            # governs the session and CSRF cookies, and applies precisely when
                            # there is no TLS.
                            #
                            # "Trusted" is the user's BLANKET ASSERTION — one boolean, never a
                            # host/CIDR allowlist: an allowlist changes which requests get a
                            # usable cookie without changing who can read the wire, which reads
                            # as security it does not provide.
                            #
                            # A DEGRADED MODE, so it is surfaced and never merely permitted: a
                            # startup line on stderr, a config warning (this file's `warnings`,
                            # rendered in Settings), and a non-dismissible in-app banner —
                            # quince#539, NOT yet built. Applied at process start; schema v0 has
                            # no live reload.
                            #
                            # It also disarms the `426 insecure_origin` refusal in §1, with no
                            # second switch: the refusal is defined in terms of the Secure
                            # decision, so relaxing that relaxes both together.
                            #
                            # NEVER send HSTS while this is reachable, or a user who enables it
                            # is locked out with no in-browser recovery. quince sends none.
automation:                 # assisted-backup policy (consumed from qn.12)
  staleness_days: 3         # last good backup older than this → backup_available push
  reminder_cooldown_hours: 24
ui:
  theme: system             # system | light | dark
```

Schema is versioned by presence/absence of keys (missing keys = defaults, written back
on next save); a key the app doesn't know is a warning surfaced in UI, never an error.

**RULED (was `PROPOSED (gap)`): storages are DECLARED; `QUINCE_BACKUPS` is retired — `qn.6c`,
Operator ruling 2026-07-31, relayed on quince#378.** Implemented above.

**Option (b) was chosen over the recommended (a).** (a) kept the env var as an implicit fallback
synthesizing one storage when the list was empty; (b) retires it outright. **Both agent seats
recommended (a) and both were wrong for the same reason** — the argument was *"this breaks every
deployment in the field"*, and there is no field. There is one instance; the cost is editing one
YAML file once. Against that, an env var with a built-in default that quietly conjures a storage
is a permanent implicit path: cheap now, expensive later, load-bearing by the time anyone wants it
gone.

**What the ruling requires, and each is implemented rather than described.** No `storage:` key →
**refuse to start**, name the key, print the remedy (`config.CheckStorages` + `StorageRequirement.Explain`,
called on the serve path). The variable is **gone rather than unread** — it is no longer in
`knownBootstrapVars`, so setting it produces the unknown-variable warning. A still-set
`QUINCE_BACKUPS` is echoed in the refusal as the likely reason a working deployment stopped, and
its value is suggested as the path to declare.

**Why the refusal is NOT a `Validate` error, which is the load-bearing design point.** `Load()`
discards a config that fails `Validate` and returns `Default()` with `OK:false`; `NewService`
logs *"running on last-good defaults"* and continues — never fatal, by its own contract. Routing
"no storages" through that path would start a daemon that serves a healthy-looking UI and can back
nothing up. **That silent zero-storage start is the one outcome the ruling forbids**, so the check
lives where it can stop the process. `Validate` still owns well-formedness of a list the user *did*
declare (empty name/path, relative path, duplicate name or path, not exactly one `default: true`) —
those are 422s, not exits.

**`PUT /api/config` is a THIRD path and answers 422, which is not the same check twice.** The
requirement is enforced in `Service.Replace` as well as at startup, because `Replace` has the
opposite property from `Load`: it returns the errors and **writes nothing**, so the hazard that
justifies keeping this out of `Validate` does not exist there. Without it the UI could remove the
user's last storage, receive a **200**, and the running daemon would keep serving on its
already-loaded config — the user discovering at the next restart that backups were disabled. D12
makes the UI the editing surface, so that is two clicks away; a silent acceptance of an edit that
disables backups is exactly what *no silent caps or fallbacks* forbids. Found at review
(quince#394): the exclusion had been reasoned from `Load`'s behaviour and applied to a path that
does not share it.

**`--demo` is unaffected and that is deliberate:** it serves fixture data and never builds the
storage subsystem, so the refusal sits inside the live branch. A check placed before it would
refuse every demo and every `ui-e2e` run over a subsystem they do not use.

**Second half — OVERRULED 2026-08-02 (quince#458).** It read *"ruled as recommended: `backend`, `zfs`
and `retention` stay global; a declared entry inherits them"*, and `backend` and `zfs` are now
**per-entry overrides with the global as the inherited default**. `retention` stays global.

**The recommendation's own reasoning is what fails.** It argued that per-storage zfs settings *"only
start mattering when a second zfs storage exists, which this rung cannot create"* — true, and it
addresses the wrong configuration. The breaking one is **one zfs storage beside a non-zfs one**,
which this rung creates trivially, and which is the first thing an operator tries: a USB disk
alongside a zfs default. It got a zfs backend whose parent dataset pointed at another pool.

**Interface fact 4 had already required otherwise** — *"a per-storage backend therefore needs
per-storage zfs settings **or an explicit rule that only one storage may be zfs**"* — and neither was
built. The word *ruled* in this sentence was also wrong: the spec's `RULED` block on gap 3 settles
`(b)`, the `QUINCE_BACKUPS` retirement, and is silent on this half. **A recommendation was
implemented as a ruling, and this line is how it came to look decided.**

**One hard refusal follows**, because it is not a degraded mode to surface: two storages sharing a
zfs `parent_dataset` would create the same `<parent>/<udid>` per device and each believe it owned it,
which voids every per-storage guarantee this rung adds. quince refuses to serve and names both
storages and the remedy.

~~A **restart** is still required to pick up a `storage:` change — D12 permits that only if the spec
says why, and `docs/specs/qn.6c/qn.6c.md` says why.~~ **NO LONGER TRUE as of `qn.6g` (quince#577):
a `storage:` change — the list, a path, a backend, a zfs block, or retention — takes effect in the
running process.** The deviation D12 permitted is spent rather than renewed; `docs/specs/qn.6c` still
says why it was taken, which is the record of a cost that has since been paid off.
**RULED (was `PROPOSED (gap)`): `storage:` becomes a list of fully-specified storages; `auto`
REMAINS LEGAL and its removal is descoped to `qn.6e` — `qn.6c`, quince#473, quince#502.** Operator
ruling 2026-08-02, relayed by architect session `arch1` on quince#500 — a **relay of an out-of-band
decision**, which is the citable record rather than a forge artefact the Operator authored.

**What lands, in full and unchanged:** `storage:` **is** the list, with no global `backend`, `zfs`
or `retention` — the five inline directions on quince#461.

```yaml
storage:
  - path: /backups          # name and default optional, per the 2026-08-01 ruling
    backend: zfs            # concrete OR `auto`; see below
    zfs: {parent_dataset: rpool/quince, mode: hook, ssh_user: zfsuser, ssh_host: nas, seed: auto}
    retention: {keep_recent: 10, keep_daily: 30, keep_weekly: 12}
  - {path: /mnt/shuttle, backend: hardlink}
```
It dissolves quince#458 **by construction** — no inheritance means nothing bleeds from a global onto
a storage it was never written for — and it **deletes** rather than amends: `BackendFor`, `ZFSFor`,
the `zfs: {}` opt-out idiom with its comments and tests, `CheckStorageBackends`' remedy BRANCHING,
and **quince#468 entirely**.

**`CheckStorageBackends` itself SURVIVES, and quince#473's deletion list is wrong about it.** Read
before building, not assumed from the issue. The function does two things and only one of them is
about inheritance:

- **The zfs-with-no-parent refusal** is an incoherent *declaration*, still reachable when an entry
  writes `backend: zfs` with no `parent_dataset`. It survives, and its remedy collapses from three
  branches to **one** — *set `parent_dataset` in this entry's `zfs:` block* — because with no global
  there is no other key it could mean. That is what deletes quince#468, whose whole content is
  choosing between remedies that no longer exist.
- **The duplicate-`parent_dataset` collision** — two storages that would create the same
  `<parent>/<udid>` per device and each believe they owned it — **is not caused by inheritance at
  all.** Two flattened entries can each spell out the same `parent_dataset`. Deleting the function
  would reintroduce quince#458's actual hazard by a different route, which is the opposite of what
  the flattening is for.

**RULED (was DEFERRED): `auto` is ABSORBED, NOT REMOVED** — Operator, 2026-08-07 on quince#502:
*"I don't care as long as I'm the only user of quince, do whatever is the easiest."* Built in
`qn.6e`.

**The loader does not change.** `Resolved()` still defaults `Backend` to `auto`, so **`backend:
auto` remains legal in `config.yml`** and the one-line declaration the startup refusal itself teaches
— `storage:` / `  - path: /backups` — still works. That declaration **is** `auto`, which is why
removing it would break the shortest form in the product, taught by quince's own error message.

**What changed is that quince writes a concrete backend when it adds one.**
`POST /api/config/storage` refuses an empty or `auto` backend with a `422` rather than defaulting it,
so the add flow records the value the probe just showed and an omission cannot become an `auto` by
the back door.

**THE REACHABLE GOAL IS NARROWER THAN *"quince never writes `auto`"*, and that sentence is false.**
`replaceLocked` marshals the whole **resolved** document, so any save materialises every default for
every entry — including ones the user never touched, and including `ForgetStorage` and
`PUT /api/config`. A hand-written minimal declaration acquires `backend: auto` and `zfs.seed: auto`
the first time anything writes the config. Measured in `qn.6e`; nothing is broken, because the
materialised value means exactly what the omitted one did.

**So the guarantee is about the ADD, not about the file.** Stated this way because the broader claim
was written into the `qn.6e` spec, found false by a test, and corrected there (quince#702) — the same
sentence would have aged the same way here.

**The creation-time probe-and-refuse this block originally recommended is still NOT wanted**, and
that has not changed with the ruling.

**The measurement below is what decided it, and it is kept verbatim for that reason.**
`core/internal/storage/probe.go:42-48`:

```go
switch opts.Backend {
case BackendReflink, BackendHardlink, BackendCopy:
	// returned WITHOUT probing — "storage.backend: <x> (explicit)"
}
// auto: probe the real filesystem
name, reason, degraded := probeNamespaceDetail(opts.Backups)
```

`probeNamespaceDetail` — FICLONE **clone sharing** (`FIEMAP_EXTENT_SHARED`) plus the independence
check that rules out a hardlink, then `link()`+inode identity — runs on the **`auto` branch
only**. `degraded` is what makes the choice a WARN rather than an INFO: it is set for `copy`, and
for a `reflink` chosen on a filesystem that cannot report sharing, whose `backend_reason` then says
the space saving is unverified (quince#747). An explicit namespace backend is taken at face value. **So `auto` is not a convenience
default; it is the only thing in the product that checks a backend declaration against the medium**,
at a time when nothing creates a storage for you — that flow is quince#443, a later rung.

Without it, a wrong guess is accepted silently at startup, frozen into `quince-storage.json` where
gap 4 makes the marker the authority, and fails at **seed time, inside a backup the user just
pressed** — `ErrReflinkUnsupported` is a surfaced error and explicitly *"never a silent fallback"*
(`clonetree.go:49-52`). It also feeds quince#476, where a `backend_mismatch` clears only by
hand-deleting a checksummed file and the refusal never says so.

**Why deferral rather than a new mechanism, which is the reusable part.** Removing `auto` was chosen
as the *simpler* option — *"pick the simplest, don't accumulate debt for no reason"*. But keeping
`backend_mismatch` meaningful without it required **building** a creation-time probe-and-refuse:
more machinery, not less, and a cost the simplicity argument had not priced. Descoping is what makes
that argument actually hold. **The marker safety property then returns for free**: with `auto`, the
creation moment probes, so the marker records what the medium *is* and `Mismatch` compares a real
observation against a real observation.

**Two things quince#473 listed as undecided, measured rather than argued, and neither blocks.**

*The absent-vs-empty pointer distinction survives.* It is what G7 rests on — *no key* and *declared
none* want the same refusal for different reasons. Moving `*[]StorageEntry` up one level, onto
`storage:` itself, preserves it exactly, because `Parse` unmarshals over `Default()` and a nil
pointer stays nil:

```
PROBE absent key     → nil       PROBE declared none → empty       PROBE one storage → 1 entry
```

So `CheckStorages` keeps its shape and `Explain` changes one string: `storage.storages:` →
`storage:`.

*Unknown-key detection still reaches inside an entry.* `unknownKeys` recurses into slices of structs
and indexes them, so a typo in a flattened entry is still reported and still says which entry:

```
PROBE warning → unknown config key "storage[0].pth" (ignored)
```

**Two consequences to state in the implementing PR rather than discover.** Per-storage `retention`
with no global block means an absent `retention:` falls back to **code** defaults — D12 permits a
setting with a sane default the file need not spell out. And **the upgrade note gap 3 made a
deliverable now has two steps**, because this is the second config break in one rung.

**Whether this is `qn.6c` or its own rung was open in quince#473 and is answered by the Operator
naming it the most important piece left of `qn.6c` (2026-08-02).** Recorded because it was listed as
undecided and a later reader will look for where it went.

**Both of this block's open questions are now closed.** It read *"Not ruled here: `qn.6e`'s scope,
which quince#502 leaves open by instruction; and whether `auto` removal ultimately sits in `qn.6e` or
travels with quince#443's add-storage flow."* **`qn.6e` was scoped on 2026-08-07** (quince#502, spec
at `docs/specs/qn.6e/qn.6e.md`), and **`auto` removal happens in neither place** — it was ruled
*absorbed* rather than removed, above.

**RULED and IMPLEMENTED (was `PROPOSED (gap)`): ONE port, both protocols, routed by the first byte
of the connection, vendored rather than `cmux`. Plain HTTP gets a `301` to `https://<same host>:<same
port>` — EXCEPT when `sessions.allow_insecure_transport` is on, where quince serves it.** Operator
ruling 2026-08-02, relayed by architect session `arch1` on quince#446 — option (c) — and built in
slice 4.

`core/internal/tlsx` carries it: a per-connection peek goroutine, two channel-backed
`net.Listener`s, a `Conn` that replays the peeked byte, and an idempotent `Close` so two
`http.Server`s over one real listener cannot race into a double-close. `0x16` is a TLS
ClientHello; every HTTP request begins with an ASCII method letter.

**The redirect shipped WITH its exception rather than before it.** The sequencing note at the end of
this block permitted slice 4 to send the `301` unconditionally, *because slice 8's flag did not exist
yet and there was therefore no user it could wrong.* Slice 8 merged first (quince#540), so that
reasoning expired before slice 4 was written and the exception is in the same commit as the redirect.

**The question it answered.** With `tls.cert_file` set, does quince **(a)** add a second port for HTTPS and keep
serving the app over http on `QUINCE_LISTEN`, **(b)** listen on two ports where the http one
redirects or refuses, or **(c)** serve both protocols on the single port `QUINCE_LISTEN` already
names, routed by inspecting the first byte of each connection?

**It is not an implementation detail**, which is why it is here rather than in the spec. It decides
the URL a user bookmarks, and serving the app on both a plain and a TLS origin is two origins with
different cookie behaviour — the same `secureCookie` split that makes this rung necessary, now
inside one deployment.

**The deployment constraint that shapes it.** `deploy/compose.nas.yml` documents that Wi-Fi is
quince's primary use case, that netmuxd finds devices only by mDNS, that multicast does not cross a
bridged container network, and therefore that the answer is `network_mode: host` with the `ports:`
block deleted. **On the deployment that matters there is no port forwarding at all** — so a second
listener is a second host bind, and a second collision surface on a box where nothing can be
remapped.

**The Operator's leaning, recorded and not decided:** *"environment variable, single port for both
http and https"* — option (c), with plain-HTTP connections getting a `301` to
`https://<same host>:<same port>`.

**What (c) buys beyond a saved port.** The onboarding URL never changes: open `http://host:PORT`,
complete step 1, obtain a certificate, and the same URL keeps working, upgraded in place. There is
no *"now go to a different port"* — the worst moment in any self-hosted first run — and no saved
bookmark that starts returning a TLS error, which browsers render as *"sent an invalid response"*
and a user cannot distinguish from the app being broken.

**Budget it at ~150 lines, not ~30.** The byte-sniff is small. The working feature is a
per-connection peek goroutine with a read deadline **cleared before hand-off** (or the HTTP server
inherits it), two synthetic channel-backed listeners implementing `Accept`/`Close`/`Addr`, a `Conn`
wrapper replaying the peeked byte on first `Read`, and shutdown coordination across two
`http.Server`s over one real listener without double-closing.

**The dependency question, measured live 2026-08-02 rather than recalled.**
`github.com/soheilhy/cmux` is the obvious import and it is **dormant**: latest published version
**v0.1.5, 2021-02-05** (module proxy), 2.8k stars, 26 open issues, 10 open PRs, not archived. Its
only activity since February 2021 is one commit on **2026-06-08**, *"Modernize for current Go
toolchains"* — **untagged, so no published version contains it**; taking it means pinning a
pseudo-version. Nothing in `golang.org/x` multiplexes connections. `github.com/inetaf/tcpproxy` is
genuinely maintained (2026-05-15) but is a *proxy* — it routes to backend addresses rather than
yielding `net.Listener`s, so it is not a drop-in. **Go 1.26 adds nothing here**: `bytes.Buffer.Peek`
is a buffer method, and the standard library still has no `net.Conn` peek, no protocol detection,
and no listener-wrapping helper.

**Recommended: (c), vendored rather than `cmux`.** One byte of discrimination — `0x16` is a TLS
ClientHello, every HTTP request begins with an ASCII method letter — is a `bufio.Reader`, a
`Peek(1)`, and a `Conn` whose `Read` drains the buffered reader first. cmux buys a general matcher
framework and its own close semantics, in exchange for a dependency whose newest release predates
this decision by five years.

**A trap that belongs with the decision, because it is silent and permanent: never send HSTS while
the certificate is self-signed.** HSTS instructs the browser to refuse untrusted connections to that
origin, which removes the click-through exception the self-signed tier depends on and locks the user
out with no in-browser recovery. quince sends none today — `httpapi.securityHeaders` is CSP,
`X-Frame-Options`, `X-Content-Type-Options` and `Referrer-Policy`, measured — and must not start
while any self-signed or plain-HTTP path is reachable.

### The default listen port

*This heading is load-bearing rather than decoration.* A `PROPOSED (gap)` block is bounded by the
next heading, the next live marker, or EOF (quince#408). While the listener gap above and the port
gap below were both open, each bounded the other. Flipping the port gap alone removed that
boundary, and the still-open listener block immediately read as though its own question had been
ruled — `gap-heading-check` caught it on the first run. A heading restores the bound **correctly**,
where the gate's documented opt-out comment would only have silenced the check, and would also have
hidden any genuine future violation inside the listener block.

**RULED and IMPLEMENTED (was `PROPOSED (gap)`): the default listen port is `:8968`.** Operator
ruling 2026-08-02, relayed by architect session `arch1` on quince#446 — *"Gap B: `8968`"* — and
built in the same rung. `:8080` was close to the worst available choice, and the change was free
only until v0.1.

**What moved with it, because a default nothing follows is not a default:** `QUINCE_LISTEN`'s
fallback in `config.Bootstrap`, `deploy/Dockerfile`'s `ENV` and `EXPOSE`, both compose files,
`deploy/dev.md`, the e2e harness and its Playwright `baseURL`, `make demo`, and the dev-deploy
convention URL in `deploy/devct/` and its spec. **`docs/specs/qn.0/qn.0.md` deliberately still
says `8080`**: it records what was proven at that rung, and a past acceptance is not rewritten to
match a later decision.

The criteria and the measurement that produced the number are kept below, because *"which ports
are free"* is a live fact and whoever revisits it needs the method rather than the answer.

**Why it had to be now.** The same argument the `QUINCE_BACKUPS` retirement above turned on: there is one
instance, so changing the default is one edit today. After v0.1 it is in every README, every
screenshot, every compose file anyone copies, and every user's bookmark.

**The constraint that makes it load-bearing rather than cosmetic.** Under bridged networking a
collision costs nothing — remap `8081:8080`. Under `network_mode: host`, which Wi-Fi requires,
**nothing can be remapped**: if anything on the box already holds the port, quince fails to bind and
does not start. `8080` is close to the worst available choice for that — Synology's own stack,
Tomcat, qBittorrent, UniFi and a long tail of homelab software live there.

**Measured live against the IANA registry, 2026-08-02** — `service-names-port-numbers.csv`,
`Last-Modified: Thu, 23 Jul 2026 20:36:24 GMT`, 15,398 rows:

- **`8080` is ASSIGNED**: `http-alt`, *"HTTP Alternate (see port 80)"*.
- **`8443` is ASSIGNED**: `pcsync-https`. So the natural pair for a two-listener design is **two
  assigned and heavily squatted ports**.
- 1,582 of the 2,000 ports in 8000–9999 are unassigned for TCP; the large contiguous runs are
  8475–8499, 8504–8553, 8616–8664, 8712–8731, 8810–8872 and 8955–8979.

**Criteria, not a number** — "which ports are free" is exactly the sort of fact canon says to read
live:

1. **IANA-unassigned**, in the registered range 1024–49151.
2. **Below 32768.** A session box's `/proc/sys/net/ipv4/ip_local_port_range` reads `32768 60999`, so
   a listener above that can lose a race with an outbound socket's source port. Under host
   networking that is a real failure mode. **This criterion was not in quince#446's list**, and it
   removes a whole range.
3. **Not on Chromium's restricted-port list** (`net/base/port_util.cc`). Nothing in 8000–9999 is
   blocked; the nearest entry is **10080**, where a web UI would simply be unreachable in Chrome.
4. **Avoid 9100–9999** — the Prometheus exporter allocation band, actively curated (wiki edited
   2026-07-31).
5. **Mid-block in a large unassigned run, clear of de-facto squatters.** IANA does not record
   squatters, so this is a separate cross-check: Synology 5000/5001, Plex **32400 (officially
   registered — the only one of these that bothered)**, Jellyfin 8096, Home Assistant 8123,
   Portainer 9000/9443, Syncthing 8384, Immich 2283, Proxmox 8006, Sonarr 8989, Radarr 7878, UniFi
   8443/8843/8880. **Several sit on IANA-unassigned ports, which is precisely why the registry alone
   is not sufficient.**

**Recommended: `8968`** — unassigned, mid-block in 8955–8979, nearest known neighbours 8983 (Solr,
15 away) and 8989 (Sonarr, 21). Runners-up **`8517`** (block 8504–8553, nearest 8501 Streamlit) and
**`8486`** (block 8475–8499; weakest of the three, because 8484 is two away).

**What changing it costs:** `deploy/Dockerfile`'s `ENV QUINCE_LISTEN`, both compose files,
`deploy/dev.md`, the e2e harness and the demo.

**Two honest notes.** No unassigned port is memorable, so the real mitigation is that the listen
address is already a first-class setting rather than a good number. And under host networking a bind
failure must be a **loud named error, never a fallback to another port** — *no silent caps or
fallbacks*.

