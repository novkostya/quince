# qn.6f — quince serves TLS: the listener, the certificate, and onboarding step 1

**Goal.** A person opens quince from their phone and can log in — over a connection whose
security they chose, can see, and were not silently downgraded out of.

Rung issue: quince#462. **The rulings this spec builds on live on quince#446 and are cited, not
restated** — five of them, taken 2026-08-01/02. Where this document and quince#446 disagree,
quince#446 is right. What is new here is the *how*: what the listener does, where its refusal
lives, and what the two still-open decisions cost.

**Why it runs before `qn.6e`** — quince#462, and `roadmap.md`'s *"numbers are labels, not order"*.
Highest uncertainty in `qn.6`, and onboarding is 1 → 2 → 3.

---

## Boundary

**In scope.**

| tree | what changes |
| --- | --- |
| `core/cmd/quince/` | the listener; the startup refusal on a bad certificate |
| `core/internal/config/` | the `tls:` section, its validation, and the **serve-path** certificate check |
| `core/internal/tlsx/` (new) | `GetCertificate` + rotation; self-signed generation (slice 3 only) |
| `core/internal/httpapi/` | the step-1 endpoint and its detection |
| `ui/src/` | the onboarding step-1 page; the `Config` TS type |
| `docs/` | contracts §1 and §6; design §6 and §9 |
| `deploy/` | reverse-proxy and `tailscale serve` prose; compose and `Dockerfile` if the port moves |

**Out of scope**, per quince#462 and not revisited here: a quince-managed address (quince#406 —
rendered, **disabled**, labelled *not implemented yet*, wired to nothing); onboarding steps 2 and 3;
Web Push and the PWA (`qn.12`); **HSTS**; ACME and automatic renewal.

**Also explicitly out: live config reload.** Interface fact 5 — it does not exist, it is staged to
`qn.6`, and this rung does not build it. Certificate *rotation* (G3) is a different mechanism and is
in scope; changing the certificate *paths* still needs a restart, and the spec says so rather than
letting D12's promise imply otherwise.

---

## Interface facts — measured in this checkout at `1036d15`, 2026-08-02, not recalled

**1. There is no TLS-serving code.** `grep -rn 'ListenAndServeTLS|tls\.Config|crypto/tls|cert_file'
core/ --include=*.go` excluding tests → **zero hits**. The only `crypto/tls` import in the tree is
`core/internal/auth/auth_test.go`, using `httptest`'s TLS server. This re-confirms quince#446's
measurement at a third commit. **This rung introduces a listener, not a flag.**

**2. The listen address is BOOTSTRAP ENV, and the set is closed at THREE.** `QUINCE_DATA`,
`QUINCE_CACHE`, `QUINCE_LISTEN` (`core/internal/config/bootstrap.go`). Anything else `QUINCE_*` is a
typo-guard warning. **`QUINCE_BACKUPS` was RETIRED by `qn.6c`** and its absence from
`knownBootstrapVars` is load-bearing rather than an omission — a box that still sets it now gets the
unknown-variable warning.

**This is the seam the rung has to cross, and it is the reason decision 4 is not cosmetic.** The
port is env (deployment topology); the certificate is `config.yml` (everything else). They configure
one listener. Contracts §6 draws that line as a hard binary and has never had to arbitrate a case
where both halves describe the same object.

**3. `Load()` falls back to `Default()` on a `Validate` failure, and `NewService` CONTINUES.**
Measured in `core/internal/config/service.go`: an unreadable file, invalid YAML, or any `Validate`
error returns `Loaded{Config: Default(), OK: false}`, and `NewService` logs *"config invalid at
startup — running on last-good defaults"* and carries on.

**So `Validate` is the wrong place to check a certificate, and this is the single most important
design point in this spec.** A `tls.cert_file` that cannot be read, checked there, would discard the
whole config, start the daemon on defaults, and **defaults have no TLS**. The user asked for HTTPS
and gets HTTP, with the reason in a log line nobody reads. **That is precisely the silent downgrade
G2 exists to forbid — reached by putting the check in the obvious place.**

**4. The right place already exists, with a written argument for why.** `config.CheckStorages` +
`StorageRequirement.Explain` run on the **serve path** in `main.go`: they print a remedy to stderr
and return an error that exits non-zero. `CheckStorageBackends` does the same for a zfs collision.
Contracts §6's own note says why they are not `Validate` errors — routing them there *"would start a
daemon that serves a healthy-looking UI and can back nothing up."* **The TLS refusal is the same
shape and reuses it rather than inventing one.**

**5. Config is read once at process start. No live reload, no generated doc-comments.**
`core/internal/config/schema.go`'s package comment, verbatim: ships the core *"but NOT file-watch
live reload or generated doc-comments, which are staged to qn.6."* `ConfigEditor.tsx` says `Saved ·
restart quince to apply`. **D12's "editable in the UI, needs no restart" is a promise this rung must
not claim it inherits**, and the honest statement is in the Boundary above.

**6. `PUT /api/config` is a full-document replace decoded into a ZERO-VALUED struct.**
`handleConfigPut` does `var cfg config.Config` then `decodeJSON`, and `decodeJSON` sets
`DisallowUnknownFields`. **An omitted key becomes the Go zero value, not its default.**

Today the UI is safe only because `ConfigEditor` spreads a document it *fetched*
(`setDraft({ ...draft, … })`) rather than reconstructing one — and **nothing enforces that**. The TS
`Config` type already omits a Go key: `devices.manage_muxer` exists in `schema.go` and not in
`ui/src/lib/types.ts`. That omission is currently harmless *by accident*.

**For TLS the same accident is not harmless: a client that PUTs without `tls` turns TLS off.** So
the keys land in `ui/src/lib/types.ts` in the same PR as the Go struct, and story 3 owes a test that
asserts the round-trip preserves them.

**7. No HSTS is sent.** `httpapi.securityHeaders` sets CSP, `X-Frame-Options`,
`X-Content-Type-Options` and `Referrer-Policy`, and nothing else; `grep -rin 'strict-transport|hsts'`
over `core/ ui/src/ docs/` returns no hit outside this rung's own text. **The out-of-scope HSTS
decision therefore costs nothing** — it is already absent and must stay absent while any
self-signed or plain-HTTP path is reachable, or the click-through exception is foreclosed and the
user is locked out with no in-browser recovery.

**8. There is no onboarding surface at all.** The only first-run flow is *password setup* — `GET
/api/auth/status` → `needs_setup`, `POST /api/auth/setup`, UI route `/setup` behind `SetupGate`.
There is no `/api/onboarding`, no onboarding route, store, or wire type. Canon has specified
onboarding since design §9 and stack D12 and it has never been built.

**Step 1 is therefore the FIRST onboarding surface, and it sets the shape steps 2 and 3 inherit.**
That is an argument for keeping this rung's endpoint narrow — see rung-ruled decision 3.

**9. Go 1.25.0** (`core/go.mod`).

---

## Design

### The page — ruled, and reproduced here only as the build target

Four tiers, from quince#446's 2026-08-01T21:39 ruling. **Not re-litigated:**

```
Best — quince does nothing:
    an HTTPS reverse proxy, or `tailscale serve`
    "Set it up, load this page over https, and this step completes itself."

Also recommended — quince serves TLS with YOUR certificate:
    point it at a cert and key (acme.sh, Caddy's own cert, `tailscale cert`,
    a wildcard bind-mounted read-only)

Not recommended:
    self-signed certificate — with the warning
    plain HTTP — with the warning

(disabled) a quince-managed address — not implemented, stay tuned (quince#406)
```

The line between the tiers is *does quince terminate TLS*; the line above the warnings is *is the
certificate real*. Neither fallback carries a "recommended" badge.

### Detection is the whole of the top tier, and it is a state rather than a button

`r.TLS != nil` **or** `X-Forwarded-Proto: https` → step 1 is **complete**, no buttons. Otherwise the
offers. The header can only ever *upgrade*, so trusting it cannot weaken anything —
`auth.secureCookie` already trusts it on exactly that reasoning, and this reuses the predicate
rather than re-deriving it.

**Consequence worth stating: the one configuration already in production use meets zero friction.**
The Operator terminates TLS in a reverse proxy, so the running deployment never sees this page. That
is also why this rung **blocks the release and not the soak**.

### The listener owes three things, and only the third is new work

1. **Refuse on a bad certificate at startup — never fall back to http.** Ruled. See *Where the
   refusal lives*, below; the mechanism is the interesting part, not the policy.
2. **Serve a wildcard certificate whose SAN does not match the configured host.** quince serves what
   it is given; nothing validates CN or SAN. A bind-mounted wildcard is the *normal* case for the
   tier this option exists for, so a hostname check would break it (G5).
3. **Rotate under a running process** — `tls.Config.GetCertificate`, re-reading when the file's
   mtime or size changes. Per-handshake, no watcher, no signal. `acme.sh` renews on its own schedule
   and rewrites in place; `tls.LoadX509KeyPair` at boot serves the **old** certificate until
   restart, so it serves an expired one, with a browser error and no server-side log, on a
   long-lived process nobody thinks to restart (G3).

### Where the refusal lives — the load-bearing mechanism

**A new `config.CheckTLS`, on the serve path in `main.go`, beside `CheckStorages`.** It:

- returns OK when `tls.cert_file` and `tls.key_file` are **both empty** — TLS is off, which is a
  configuration and not a failure;
- **refuses when exactly one is set** — a half-configured pair is a typo, not an intention;
- **refuses when the pair cannot be loaded** as an X.509 keypair, naming the file and the parse
  error, with the remedy printed to stderr in `Explain`'s existing style;
- **exits non-zero and serves nothing.**

**It is NOT a `Validate` error** (interface fact 3) and **NOT a `Replace` 422 alone.** Well-formedness
of the *paths* — absolute, non-empty, both-or-neither — belongs in `Validate` and in `Replace`,
because those are cheap string checks and a 422 is the right answer for a bad edit. **Whether the
bytes on disk are a usable keypair is a serve-path question**, checked where it can stop the process.

**`--demo` is unaffected**, mirroring the storage precedent exactly: the check sits inside the live
branch, so it refuses no demo and no `ui-e2e` run over a subsystem they do not use.

### Self-signed generation writes to quince's OWN state directory

Never beside a supplied certificate. The supplied path is `:ro` in the deployment this option
exists for, and a generator that assumes its output directory is writable fails on exactly that
deployment (G4). Output goes under the data dir — `Bootstrap.Data`, alongside `config.yml` and
`quince.db` — with `0600` on the key.

**Whether self-signed is built at all is conditional on check 1** (see *Still open*).

### What does NOT change

`auth.secureCookie` is untouched by this rung. It already returns `true` for both detection
signals; the plain-HTTP tier is the only thing that would change it, and that is behind the gap
block filed as quince#487 — no code until it is ruled.

---

## Contract and design changes

**The `secureCookie` gap is already filed** — `docs/quince.design.md` §6, PR quince#487. It gates
slice 4 and nothing else in this rung.

**Two more gaps are proposed by this rung**, written into `docs/contracts.md` §6 in a **companion
PR**, not this one. They are quince#446's open decisions 3 and 4. **Neither is this spec's to
decide** — both change user-visible behaviour beyond the rung and both touch a contract surface,
which is the gap protocol's definition of architectural.

*(They were planned for this PR. They are split out because each rests on a fact that must be read
live rather than recalled — the IANA registry for gap B, `cmux`'s maintenance state for gap A — and
holding a finished spec behind a lookup buys nothing. The split is a deviation from the plan posted
on quince#462 and is recorded rather than quietly taken.)*

- **Gap A — one listener or two, and what plain HTTP does** (contracts §6). The Operator's recorded
  *leaning* is a single port serving both protocols, routed by peeking the first byte. Budgeted at
  ~150 lines, not ~30, per quince#446: a per-connection peek goroutine with a cleared deadline, two
  synthetic channel-backed listeners, a `Conn` that replays the peeked byte, and shutdown across two
  servers over one real listener.
- **Gap B — the default port** (contracts §6). Criteria rather than a number, read from the live
  IANA registry. **`qn.6c`'s retirement of `QUINCE_BACKUPS` is the precedent that decides the
  *shape* of this argument**: canon's own words are that an env var with a built-in default is *"a
  permanent implicit path: cheap now, expensive later, load-bearing by the time anyone wants it
  gone."* A default port is the same object.

The full text of both blocks lands in `docs/contracts.md`; they are summarised here rather than
duplicated, so there is one place for them to go stale.

---

## Stories

Each is independently checkable. Slice membership in *PR slicing*, below.

1. **A request over an already-secure origin completes step 1 with no buttons.** `r.TLS != nil` or
   `X-Forwarded-Proto: https` → step 1 reports complete. No configuration, no click.
2. **A request over plain HTTP is offered the four tiers**, with the managed-address row **rendered,
   disabled, and labelled** *not implemented yet* — not merely inert.
3. **`tls.cert_file` / `tls.key_file` exist as config**, with defaults, validation, a `Validate`
   error on a half-set pair, and the keys present in `ui/src/lib/types.ts`. A test asserts a
   `GET → PUT` round-trip preserves them (interface fact 6).
4. **quince serves HTTPS** from a supplied certificate and key.
5. **An unusable certificate refuses at startup**: non-zero exit, nothing served, the file and the
   reason named on stderr. Asserted for **all three** of unreadable, malformed, and mismatched
   key/cert.
6. **The certificate rotates under a running process**: files replaced, next handshake serves the
   new one, no restart and no signal.
7. **A wildcard certificate whose SAN does not equal the configured host is served**, not rejected.
8. **The supplied certificate directory is never written to**, asserted with it mounted read-only.
9. **Self-signed generation** writes to quince's own state directory, `0600` on the key.
   *Conditional on check 1.*
10. **`deploy/` prose** for the reverse-proxy and `tailscale serve` setups the top tier links to,
    distinguishing `tailscale serve` (tier 1, zero build) from `tailscale cert` (tier 2, needs the
    listener).

---

## Gates

Beyond `make gates` / `make image` / `make gates-ui-e2e`.

| id | proves | story | where |
| --- | --- | --- | --- |
| **G1** | `X-Forwarded-Proto: https` completes step 1 with **no buttons** — the top-tier user meets zero friction | 1 | ui-e2e |
| **G2** | An unreadable **or** invalid **or** mismatched certificate at startup **refuses**: non-zero exit, nothing served, the reason named. **The gate I would not ship without** — a silent downgrade here fails in the direction where the user believes they are encrypted | 5 | CI |
| **G3** | **Rotation.** Files replaced under a running process; the next handshake serves the new certificate, no restart, no signal | 6 | CI |
| **G4** | The supplied certificate directory is **never written to** — asserted with it mounted **read-only**, which is the deployment this option exists for | 8 | CI |
| **G5** | A **wildcard** certificate whose SAN does not equal the configured host is **served** | 7 | CI |
| **G6** | `make privacy-check REF=origin/main...HEAD TEXT=<file>` | all | host |
| **G7** | **OWED — hardware, Operator.** A real phone completes onboarding and logs in over at least the reverse-proxy tier and one quince-served tier | — | lab |

**G7 is declared owed with its owner named, and no PR claims it until it has run.** CI proves the
listener; only a device proves a browser accepts it.

**G2 is asserted on all three failure modes deliberately.** Two of them — malformed PEM and a key
that does not match its certificate — fail at different layers of `tls.LoadX509KeyPair`, and a check
that only covers "file missing" passes while the interesting cases still downgrade.

---

## Fixtures

- **Certificates generated at test time, never committed.** A self-signed pair, a wildcard pair
  (`*.example.invalid`, SAN deliberately unequal to the test host), a malformed PEM, and a
  mismatched key/cert pair. Generated in-test via `crypto/x509` so nothing expires in the
  repository and nothing looks like a real key in a diff.
- **`.invalid` is used throughout** (RFC 2606), so no fixture can resolve to anything real.
- **No private key of any kind is committed**, which is both the secrets rule and the reason
  generation-at-test-time is not merely convenient.
- **No new transcript fixtures.** This rung touches no device path.

---

## Rule check

Every hard rule this rung touches *or comes near*, one line each. Near-misses included.

- **Privacy is a commit-time gate.** G6 per PR, `TEXT=` a **per-runner** path under
  `$HOME/scratch/<runner>/`, never a fixed `/tmp` one. An exit `2` is declared as a sweep owed with
  the head named, never ticked. *Near-miss named:* `deploy/` prose about reverse proxies is the
  natural place for a hostname to slip in; every example uses `.invalid`.
- **State honesty.** G7 is owed with its owner named. Checks 1 and 2 are **unmeasured** and the spec
  says so wherever it relies on them. Step 1 reports *complete* only on a signal quince can observe
  — never on a user's assertion that they set a proxy up.
- **No silent caps or fallbacks.** This rung's sharpest instance, and G2 is it. Interface fact 3 is
  the trap: the obvious place for the check silently produces the forbidden outcome. Also: TLS on
  vs off is visible in the log line at startup and on the step-1 page, never inferred.
- **Secrets discipline.** `tls.key_file` is a **path**; a key body never enters `config.yml`, a log,
  an error message, argv, or the API. Generated keys are `0600`. *Near-miss named:* an error from
  `tls.LoadX509KeyPair` on a malformed key must be surfaced without echoing file contents — G2's
  assertion checks the message names the **file**, not the bytes.
- **Config tidiness (D12).** `tls.cert_file` / `tls.key_file` in `config.yml` with defaults and UI
  editing. *Near-miss stated honestly:* the **generated doc-comment** mechanism does not exist
  (interface fact 5) and this rung does not build it — the keys are documented in contracts §6's
  YAML block like every other key, and they inherit the generator when `qn.6` lands it. **Changing
  a certificate path needs a restart; rotating the file behind it does not.**
- **Don't improvise architecture.** Decisions 3 and 4 become gap blocks and **stop**. No code is
  written behind either, and the five rulings already taken on quince#446 are not re-litigated.
- **Interface facts are looked up live.** Facts 1–9 measured in this checkout at `1036d15`. The IANA
  registry, `cmux`'s maintenance state and the three browser checks are read live and cited in the
  PR that uses them, not recalled.
- **Docs are part of the diff.** Contracts §1 (the step-1 endpoint) and §6 (the `tls:` keys) change
  in the PR that implements them; design §6 and §9 likewise. Coverage is declared per PR with an
  explicit known-untested list.
- **Never mutate a committed version.** *Near-miss, and it is a real one:* G4. Self-signed
  generation writes into quince's state directory and **never** beside a supplied certificate,
  because the supplied directory is `:ro` in the deployment that option exists for. No storage tree
  is touched by this rung at all.
- **Subprocesses.** None. Certificate generation is in-process `crypto/x509`; nothing shells out to
  `openssl`. Named because reaching for `openssl` is the obvious wrong turn and it would put a key
  path in argv.
- **Every bug found on hardware becomes a replay fixture.** None found yet; G7 is where one would
  come from.

---

## Rung-ruled decisions

Rung-local by the gap protocol — inside this rung's boundary, changing no contract surface, no
storage semantics, and no user-visible behaviour beyond it. Recorded here, and one line each in the
decisions log.

1. **The certificate check is `config.CheckTLS` on the serve path, not a `Validate` error.**
   Interface fact 3 makes the alternative produce the outcome G2 forbids. Reuses `CheckStorages`'
   shape and `Explain`'s remedy style rather than inventing a second refusal idiom.
2. **Path well-formedness stays in `Validate`/`Replace` (422); loadability is the serve-path check.**
   Two checks, different questions — a bad *edit* deserves a 422, a bad *keypair* must stop the
   process.
3. **Step 1's endpoint is narrow: it reports state, it does not orchestrate onboarding.** Interface
   fact 8 — this is the first onboarding surface, so a general onboarding framework invented here
   would be invented on one data point. Steps 2 and 3 may generalise it; this rung does not.
4. **Fixture certificates are generated at test time, never committed.** Nothing expires in the
   repository, and no artifact in a diff looks like a private key.
5. **`crypto/x509` in-process, never `openssl(1)`.** No subprocess, and no key path in argv.

---

## Known gaps and open questions

**Blocking, and none of it is this spec's to decide.**

1. **Gap A — one listener or two** (contracts §6, this PR). Blocks slice 2.
2. **Gap B — the default port** (contracts §6, this PR). Blocks slice 2's deploy half; the listener
   can be built against the current default and the number changed once.
3. **The `secureCookie` gap** (design §6, quince#487). Blocks slice 4 entirely.

**Three live checks**, per *interface facts are looked up live*. Reported separately on quince#462
rather than asserted here.

1. **Does a click-through self-signed certificate block service-worker registration** in current
   Safari/iOS and Chrome? **Decides whether slice 3 is built at all** — if it does, self-signed
   ends in the same place as plain HTTP on push while costing a full-page interstitial, and
   bring-your-own-cert dominates it.
2. **Does iOS Safari persist a self-signed exception** across restarts — one tap ever, or one tap
   per session? Decides how slice 3 is *presented* if it is built.
3. **Let's Encrypt's current rate limits.** Bears on quince#406, not on this rung's code.

**Neither check 1 nor check 2 can be settled from a session.** Both are browser behaviour on a real
device. What is reportable is what primary sources state; the rest is owed to G7's hardware run.

---

## PR slicing

Each PR carries one reviewable claim.

| # | claim | stories | blocked on |
| --- | --- | --- | --- |
| **1** | *This spec.* Gaps A and B are a companion PR. | — | nothing |
| **2** | Detection + the four-tier page + the disabled managed-address row. | 1, 2 | nothing |
| **3** | The `tls:` config keys, their validation, and the TS type. | 3 | nothing |
| **4** | The listener + `CheckTLS` + **G2** + **G3**. | 4, 5, 6 | **gap A** |
| **5** | Wildcard + read-only assertions. | 7, 8 | PR 4 |
| **6** | The default port moves. | — | **gap B** |
| **7** | Self-signed generation. | 9 | **check 1** |
| **8** | Plain HTTP. | — | **quince#487's ruling** |
| **9** | `deploy/` prose. | 10 | nothing |

**PRs 1, 2, 3 and 9 are unblocked today.** The `secureCookie` gap block (quince#487) was filed
*before* this spec precisely because it has the longest lead time of anything in the rung.

**PR 4 is the one to review hardest.** It is where a mistake fails in the direction where the user
believes they are encrypted.
