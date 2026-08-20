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
| `core/internal/tlsx/` (new) | `GetCertificate` + rotation. ~~self-signed generation~~ — **slice 7, DROPPED 2026-08-02** |
| `core/internal/httpapi/` | the step-1 endpoint and its detection — **pre-auth: a fifth `authExempt` route, BY EXACT PATH** (ruled 2026-08-02, design §6) |
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

**And it is PRE-AUTH — ruled 2026-08-02, design §6.** `authExempt` is exactly four routes today
(`middleware.go:73-79`), so anything this rung adds is behind `authGuard` unless deliberately
exempted. **This spec was silent on that and the silence was the defect**: the chicken-and-egg is
that on plain HTTP to a LAN address, login returns `200`, the browser discards the `Secure` cookie,
and the page explaining exactly that sits behind the door the defect locks.

**Two constraints on the implementation, both from the ruling.** The exemption is **by exact path**
— `authExempt` switches on `r.Method + " " + r.URL.Path` with no prefix support, so
`/api/onboarding/*` would mean changing the matcher, not just the set. And **step 1 only**: every
future onboarding step will cite this as precedent.

**This rung's to settle, and SETTLED — rung-ruled decision 6.** It asked whether the exemption
covers the UI route as well as the endpoint, and what the page renders to an unauthenticated
visitor, worrying that the *"already encrypted ✓"* complete-state implies knowing whose step 1 it
is. **The answers: yes it covers the route, the page renders identically to everyone, and there is
no whose** — step 1 is a property of the deployment's transport rather than of a user. The full
reasoning, including a consequence for first-run that nothing had recorded, is with the decision.

**quince#497 is not subsumed by this.** A user who reaches `/login` first never sees step 1 either
way, so the login refusal naming the cause is still the only thing that helps them.

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

**CORRECTION — the top tier's *"this step completes itself"* is TRUE OF A PROXY THAT FORWARDS THE
SCHEME AND FALSE OF THE ONE MOST PEOPLE BUILD.** Operator ruling 2026-08-14 on quince#939, reversing
quince#908 §4's decision that this card gets no affordance. Recorded here rather than only on the
issue, because the block above is the build target and that is the line which was wrong.

**What it missed.** Caddy and Traefik set `X-Forwarded-Proto` for you; **nginx does not**, and the
widely-copied `proxy_pass` block sets `X-Forwarded-For` and omits `Proto`. That user has a genuinely
working HTTPS site, loads quince through it, and sees **Not encrypted** — with nothing saying why, and
no way to tell it from a broken proxy. **The step does not complete itself. It fails silently, on the
tier carrying the Recommended badge.**

**So the top tier gets a probe:** a name to check, a nonce-gated cross-origin request proving the
client can reach *this* quince there, and `detected` reporting what quince saw on that connection.
`detected: none` behind a working https proxy is the nginx caveat, and the remedy is one line of
configuration.

**The tier ORDER and the badges are untouched** — this corrects what the top tier *does*, not where it
sits. quince#446's ruling above stands in every other respect.

### Detection is the whole of the top tier, and it is a state rather than a button

`r.TLS != nil` **or** `X-Forwarded-Proto: https` → step 1 is **complete**, no buttons. Otherwise the
offers. The header can only ever *upgrade*, **and since quince#555 it is believed only from an
address in `QUINCE_TRUSTED_PROXIES`** — an unset list believes anyone, which is the default and the
old behaviour. This reuses `auth.SecureOrigin` rather than re-deriving it, which is the point: the
gating arrived in one place and every consumer got it.

*The header must NOT be trusted unconditionally here. `auth.secureCookie` can trust it; the two
consumers that INVERT the predicate — the `426` refusal and this very check — cannot, because both
fail toward "everything is fine" on an injected header.*

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

### ~~Self-signed generation writes to quince's OWN state directory~~ — NOT BUILT

**RULED 2026-08-02 (quince#462): slice 7 is DROPPED — not deferred, not owed.** The architect
executed the conditional this spec itself pre-authorised, on the corrected spike report; check 1 came
back confirming the hypothesis.

**Chromium never consults the click-through at all.** `content/browser/service_worker/service_worker_loader_helpers.cc`
admits exactly two escapes — the DevTools bypass and `--ignore-certificate-errors` — and
`SSLHostStateDelegate`, where the user's *proceed* decision is stored, does not appear in the
expression. Not a bug pending a fix: an absent code path, and it runs on update checks too. **So
self-signed does not cost push sometimes; it forecloses it structurally.** Against
bring-your-own-cert — same listener, same two config keys, a real certificate and a genuine secure
context — the tier is dominated.

**The design below is what a future self-signed path would have to satisfy**, and it was reviewed:
never beside a supplied certificate (that path is `:ro` in the
deployment the option exists for, and a generator assuming a writable output directory fails on
exactly it — G4); output under `Bootstrap.Data` alongside `config.yml` and `quince.db`; `0600` on
the key.

**The page's self-signed row is RULED — it STAYS, disabled, *not implemented*.** Operator,
2026-08-02 (quince#446, eleventh ruling), in the shape the ruled page already contains for the
managed-address row. **Slice 2 builds it**, which makes slice 2 one item larger than *detection plus
the page* implies.

**Provisional by intent, and that is recorded rather than smoothed over.** The Operator's words:

> *"OK let's do that for now (although I'm thinking of dropping it entirely, but it's cheap to drop
> later)"*

**So two instructions follow, and they point in opposite directions on purpose.** A session that
meets this row and finds it pointless must **not** quietly remove it — it is there by ruling, its
removal is under active consideration, and *"cheap to drop later"* is why it is still there rather
than an oversight for a tidy reader to correct. Equally, **nobody builds toward it**: no config key,
no generation path, no test beyond rendering. The row is a statement that the option exists and
quince does not offer it.

### What does NOT change

`auth.secureCookie` is untouched by this rung. It already returns `true` for both detection
signals; the plain-HTTP tier is the only thing that would change it, and that is behind the gap
block filed as quince#487 — no code until it is ruled.

---

## Contract and design changes

**The `secureCookie` gap is RULED** — Operator, 2026-08-02, relayed on quince#446 at `05:58:57Z`. The
block landed in `docs/quince.design.md` §6 with quince#487, and the ruling is **option (b) as
recommended**: an explicit, off-by-default switch relaxing the **fallback only**, `trusted` as the
user's **blanket assertion** — one boolean, not a host/CIDR allowlist — and a **non-dismissible**
banner. Option (c), quince detecting plain-HTTP LAN access and relaxing on its own, is **rejected as
part of the ruling** rather than merely unchosen. It gated slice 8; it gates nothing now.

**Slice 8's PR carries a constraint this spec did not anticipate.** The block must be flipped to
decided text **and its heading narrowed in the same diff as the code** — `bin/gap-heading-check` fails
`gates-sh` on a live `PROPOSED (gap):` lead whose body says `RULED`. And `docs/quince.design.md` is
code-owner owned, so that PR needs `@novkostya`'s approval as well as an architect review.

**One interaction with gap A is NOT settled and belongs to gap A's ruling.** If plain-HTTP
connections get a `301` to `https://<same host>:<same port>`, then **with a certificate configured
this opt-in is unreachable** — every plain connection is bounced before the cookie question arises.
So the ruling governs the no-certificate case in practice, and the residue is exactly the deployment
design §6 calls the one that inverts the obvious answer: a VPN user who has a certificate and would
rather not terminate TLS inside an already-encrypted tunnel. **Gap A should say which wins.**

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
9. ~~**Self-signed generation** writes to quince's own state directory, `0600` on the key.~~
   **DROPPED — ruled 2026-08-02** (quince#462); check 1 came back confirming that a click-through
   certificate forecloses service workers. This list is cited by number — do not renumber it.
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

  **Slice 7's removal does not remove the self-signed FIXTURE.** quince no longer *mints* one; the
  listener must still be tested against one, because a self-signed certificate is what a test
  server has. The dropped slice was the product feature, not the test material.
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
- **Never mutate a committed version.** *Near-miss, and it is a real one:* G4 — quince **never**
  writes to the supplied certificate directory, which is `:ro` in the deployment bring-your-own-cert
  exists for. **G4 survives slice 7's removal and is not weakened by it**: it constrains the
  *listener*, which reads that directory on every handshake for rotation, not the generator that is
  no longer built. No storage tree is touched by this rung at all.
- **Subprocesses.** None. **Test** certificates are minted in-process with `crypto/x509`; nothing
  shells out to `openssl`. The fixtures need certificates whether or not the product mints any, and
  reaching for `openssl` is the obvious wrong turn — it would put a key path in argv.
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
6. **The `/onboarding/https` UI ROUTE is pre-auth too, and the page renders the same thing to
   everyone.** This is the question interface fact 8 left open for this rung. Settled below rather
   than in one line, because half of it changes what first-run means.

### Rung-ruled 6, in full: the step-1 page is outside every guard

**The route is `/onboarding/https`**, partnering the endpoint, and it is a **top-level entry** in
`ui/src/routes/router.tsx` — a sibling of `/setup` and `/login`, not a child of anything.

**Outside `RequireAuth`, outside `SetupGate`, outside `LoginGate`.** `router.tsx` has exactly those
three shapes plus a catch-all that `Navigate`s to `/`, which is itself behind `RequireAuth` — so a
route added anywhere but the top level bounces an unauthenticated visitor to `/login`. Naming the
literal path is what makes the rest of this decision checkable rather than a direction of travel.

**Exempting the endpoint but not the route would have bought nothing.** `GET
/api/onboarding/https` has no human-visible surface of its own. The ruling that made it pre-auth
(quince#501) was about the deadlock — *the page explaining why login fails must not sit behind
login* — and the page is the half a user meets.

**IT IS A PREREQUISITE OF SETUP, NOT A SUCCESSOR, AND THAT IS NEW.** Design §9 orders onboarding
after first-run password setup. Since quince#530 that ordering is impossible over plain HTTP to a
LAN address: `POST /api/auth/setup` answers `426 insecure_origin` **before storing the password**,
so a fresh install cannot complete setup at all until the transport is fixed. **Step 1 must
therefore be reachable with no password in existence**, which `SetupGate` would prevent. The `426`
refusals are what turn this from a preference into a requirement, and nothing said so until now.

**The page renders identically authenticated or not**, and the worry interface fact 8 records —
*"the 'already encrypted ✓' complete-state implies knowing whose step 1 it is"* — dissolves on
inspection: **there is no whose.** Step 1 is a property of the deployment's transport, not of a
user or a session. Its only two inputs are the connection the visitor themselves opened and static
prose about the four tiers, so `complete: true` tells them nothing they did not establish by
connecting.

**That argument is specific to step 1 and must be re-asked for steps 2 and 3**, which concern this
quince's devices and storages and will not survive it.

**What the page must not do**, stated because this is what would erode quietly: render any device,
storage, version or job data; echo back a hostname the client did not send; or vary its content by
session state. A future edit needing any of those means the exemption is wrong for that edit — not
that the rule is negotiable.

**Deliberately NOT settled:** whether an unauthenticated visitor on plain HTTP should be
*redirected* to step 1 instead of `/login`, and whether the `426 insecure_origin` message should
link to it. Both look right and both are about the login flow rather than this page, so they belong
with whoever builds it and can see it working. Named rather than quietly left.

**RULED 2026-08-16 — YES, REDIRECT** (Operator, quince#1069, from the rig). `LoginGate` sends a
`needs_login` visitor on an insecure origin to `/onboarding/https`, with `replace`, after the
auth-state checks. quince#923 had already done this for first run; this is the returning-user half,
and it was left open here because it could not be judged without seeing it.

**THE ARGUMENT IS NOT THAT THE FORM WOULD FAIL — IT IS WHERE THE PASSWORD GOES.**
`refuseInsecureOrigin` answers `426` *before* the credential is examined, but the browser has
already put it on the wire in clear by then. The form as it stood invited somebody to hand their
admin password to the network in order to be told they could not sign in here. A redirect means the
keystroke is never sent, which is a stronger reason than the convenience one this question was
originally framed around.

**§3 IS UNTOUCHED.** The plain-HTTP confirm on step 1 is `firstRun`-only, so a `needs_login` visitor
lands on the *instructional* page and reaches no control — the split §2 asks for, arrived at by
routing rather than by a link. **Loopback still gets the form**: `insecure_origin` is false there,
so the admin at the machine is never sent away from the one place they can sign in.

**The second half stays unsettled and is now smaller.** The `426` body still names the setting rather
than linking to step 1 — it is a server string served to several callers, and with this redirect in
place the login form is not where that audience ends up anyway.

---

## Known gaps and open questions

**Blocking, and none of it is this spec's to decide.**

1. ~~**Gap A — one listener or two** (contracts §6). Blocks slice 2.~~ **RULED 2026-08-02**
   (quince#446): **one port, both protocols, routed by the first byte, VENDORED not `cmux`** — whose
   newest published version predates the decision by five years. Plain HTTP gets a `301` to the same
   host and port, **except when `sessions.allow_insecure_transport` is set, where the opt-in wins**.
   Slice 4 may ship the redirect **unconditional**, since the flag arrives in slice 8 and cannot be
   enabled before it is built.
2. ~~**Gap B — the default port**~~ **RULED 2026-08-02: `8968`.** Verified against the live IANA
   registry at the ruling — zero occurrences, mid-block in the 8955–8979 unassigned run, below the
   32768 ephemeral floor, clear of Chromium's restricted list and the Prometheus band. **Gap A's
   ruling removes the pair problem entirely**: one port means `8443` never enters the picture.
3. ~~**The `secureCookie` gap** (design §6, quince#487). Blocks slice 4 entirely.~~ **RULED
   2026-08-02** (quince#446) — option (b). Slice 4 is unblocked; see *Contract and design changes*
   for what its PR owes. This list is cited by number — do not renumber it.

**Three live checks**, per *interface facts are looked up live*. Reported separately on quince#462
rather than asserted here.

1. ~~**Does a click-through self-signed certificate block service-worker registration?**~~
   **RESOLVED 2026-08-02 — YES, in Chromium, and the answer dropped slice 7.** Read from shipping
   `main`: the guard admits only the DevTools bypass and `--ignore-certificate-errors`, and
   `SSLHostStateDelegate` — where the click-through is stored — is not in the expression at all.
   Reported on quince#462; **cite the CORRECTED comment, which supersedes two wrong claims about
   Apple's fatal/recoverable split in the original.**
2. ~~**Does iOS Safari persist a self-signed exception** across restarts?~~ **MOOT.** It only ever
   decided how slice 7 was *presented*, and slice 7 is not built. Genuinely unresolved when it was
   dropped, and recorded as unresolved rather than quietly closed — **not building the tier retires
   the question without a lab day**, which is worth more here than answering it.
3. **Let's Encrypt's current rate limits.** Reported; bears on quince#406, never on this rung's code.

**A third finding came out of the spike and is NOT this rung's**: WebKit's `disableInLockdownMode`
covers `ServiceWorkersEnabled`, `PushAPIEnabled` and `NotificationsEnabled`, so **`qn.12` push is
unavailable to a Lockdown Mode user on a real certificate too.** Filed as quince#510 rather than left
in a comment.

**One claim in the spike is third-party and must not enter canon as measured:** that a Safari
click-through does not cover the **WebSocket** upgrade. It is code-server's iPad documentation, not
Apple's. **The slice-7 ruling deliberately does not rest on it** — it is recorded as why the
asymmetry is total, not as a premise.

---

## PR slicing

Each PR carries one reviewable claim.

| # | claim | stories | blocked on |
| --- | --- | --- | --- |
| **1** | *This spec.* Gaps A and B are a companion PR. | — | nothing |
| **2** | Detection + the four-tier page + **two** disabled rows: managed-address *and* self-signed, both *not implemented*. | 1, 2 | nothing |
| **3** | The `tls:` config keys, their validation, and the TS type. | 3 | nothing |
| **4** | The listener + `CheckTLS` + **G2** + **G3**. One port, first-byte routed, vendored; the `301` unconditional. Flips gap A's block. | 4, 5, 6 | **nothing — RULED** |
| **5** | Wildcard + read-only assertions. | 7, 8 | PR 4 |
| **6** | The default port moves to **`8968`**. Flips gap B's block. | — | **nothing — RULED** |
| **7** | ~~Self-signed generation.~~ **DROPPED — ruled 2026-08-02**, check 1 confirmed. | ~~9~~ | — |
| **8** | Plain HTTP — the opt-in switch, and the `301` exception that honours it. | — | **nothing — RULED** |
| **9** | `deploy/` prose. | 10 | nothing |

**NOTHING IN THIS RUNG IS BLOCKED ANY LONGER.** All three gaps were ruled on 2026-08-02, the day they
were filed, and slice 7 is dropped rather than owed. **Filing the `secureCookie` gap before the spec
— because an Operator ruling has the longest lead time of anything in a rung — is what started that**:
it was ruled within three hours, and slice 8 joined the unblocked set before slice 4 existed.

**Slices 4, 6 and 8 each flip a `PROPOSED (gap)` block in the same diff as their code**, heading
narrowed, or `bin/gap-heading-check` fails `gates-sh`. `docs/contracts.md` is **not** an owned path
(quince#953), so slices 4 and 6 take an architect review and nothing more. **Slice 8's block is
already flipped** (quince#507, merged) — it must not be flipped twice.

**The unblocked piece worth doing FIRST is not in this table**, because it is not this rung's:
**quince#497** — login over plain HTTP returns `200` with a cookie the browser discards, and the
server already knows at `handlers_auth.go:83`. The ruling separated it deliberately. It is smaller
than any slice here, needs no listener and no ruling, and it is needed **under** the ruling too,
since the opt-in is off by default and every user who has not set it still meets the loop.

**PR 4 is the one to review hardest.** It is where a mistake fails in the direction where the user
believes they are encrypted.
