# public-demo — a publicly reachable demo instance

**Status: SPEC, unbuilt.** Written after the surface review it was ruled to wait for
(`surface-review.md`, quince#467). Reviewed before any code exists, per the program doc.

## Goal

A stranger opens a URL, reads the password off the login screen, signs in, and clicks around a live
quince that resets itself on a schedule — with the `Secure` cookie flag ON, which `--demo` forces off.

## Boundary

**In scope**

- A public-demo mode: password preset and therefore immutable, `Secure` cookies **not** forced off,
  otherwise identical to `--demo`.
- The reset, and the UI saying it happens.
- The login screen telling the visitor the password.
- The compose/timer wiring that performs the reset on a public host.

**Out of scope, each for a stated reason**

- **Per-visitor isolation** — ruled out on quince#444; one shared instance plus a reset is the accepted
  model, and session-scoped demo state is a much larger feature.
- **Changing `--demo`** — it keeps its first-run flow and its plain-http cookies. Presetting the
  password there would delete e2e's set-password coverage (`main.go:112` records this as a ruling).
- **The domain and its certificate** — filed as the HTTPS-for-non-technical-users work (quince#446,
  quince#406), not this spec.
- **The rate-limiting and abuse fixes the review found** — quince#463, quince#464, quince#465,
  quince#466. They are **dependencies of exposure**, not of this spec; see *Dependencies*.

## Design

The minimum this settles. Canon is linked, not repeated.

**D1 — a separate mode, not a `--demo` modifier.** `--public-demo` implies `--demo`'s fixture stack and
differs in exactly two behaviours: the password is preset at startup, and `SetInsecureCookies` is not
called. Endorsed as filed on quince#444; the argument that decides it is the coverage one above.

**D2 — the password is `demo`.** Ruled on quince#444, 2026-08-01. `test` remains the fixture password,
unrelated and unchanged.

**D3 — presetting the password is what makes it immutable, and no new refusal is needed.** There is no
change-password endpoint; `auth.Service.SetPassword` has exactly one caller, `POST /api/auth/setup`,
which 409s once a password exists. Calling `SetPassword` at startup therefore leaves the instance at
`needs_login` with setup already refusing. **This is the property quince#463 makes cheap** — before
that fix, every one of those refusals derived a 64 MiB argon2 hash, and in this mode 100% of that
route's traffic is refusals.

**D4 — the reset is a process restart performed from OUTSIDE the process.** `--demo` already deletes
its DB and config at start *and* at exit (`removeDemoState`), so a restart is a complete reset with no
new code. quince gets no timer, no scheduler and no self-restart path. What quince needs is only to
*know* the interval so the UI can state it.

**D5 — the interval is configuration, never a constant.** The Operator explicitly left the value
unruled — *"trivially changed after the fact — decide it when the instance exists rather than now"*.
A spec that baked in thirty minutes would be deciding it. So the mode takes the interval as a value it
is told, renders it, and asserts nothing about what it should be.

## Stories

1. **`quince serve --public-demo` starts at `needs_login`, not `needs_setup`.** `GET /api/auth/status`
   reports `needs_login` on a cold start with no prior state.
2. **`POST /api/auth/setup` refuses on that instance** with `409 already_configured`, before deriving a
   hash (quince#463's guarantee, asserted here as a property of the mode).
3. **The session cookie carries `Secure`** when the request arrives over HTTPS or with
   `X-Forwarded-Proto: https`, where `--demo` would omit it.
4. **`--demo` is unchanged** — still starts at `needs_setup`, still omits `Secure`. The existing e2e
   first-run story still passes untouched.
5. **The login screen states the password**, and does so only in this mode.
6. **The UI states that the demo resets, and how often**, from the configured interval rather than a
   hardcoded string.
7. **A restart resets everything** — versions deleted by a visitor are back, the config edit is gone,
   and the instance is at `needs_login` again rather than `needs_setup`.

## Gates

Beyond `make gates`:

- Stories 1, 2, 4, 7 — `make gates-go`: table tests over the mode's startup wiring, asserting the
  `auth.Status` tri-state and the setup refusal for both modes. Story 7 is a start → mutate → restart →
  re-read assertion over a temp state dir.
- Story 3 — `make gates-go`: `Service.Secure` over a request with `X-Forwarded-Proto: https` in each
  mode. The existing `secureCookie` tests already cover the rule itself; this asserts only that the
  mode does not override it.
- Stories 5, 6 — `make gates-ui-e2e`: the login screen shows the password and the reset notice under
  `--public-demo`, and shows **neither** under `--demo`. The negative half is the one that matters —
  it is what stops the copy leaking into the shipping product.
- Story 7, end to end — `make demo`-style manual observation on a dev deploy, recorded in the PR.

## Fixtures

**None new.** The mode reuses `internal/demo`'s fixture stack unchanged; that is what D1 means by
"otherwise identical". The only new test data is the temp state directory story 7 needs, created and
discarded by the test.

## Dependencies — what must land before EXPOSURE, not before this spec

From the merged surface review. This spec can be built and merged with these open; the instance must
not be reachable from the public internet until they are closed.

| dependency | why it blocks exposure |
| --- | --- |
| quince#463 | `POST /api/auth/setup` is 100% refusals in this mode, and each one was a 64 MiB derivation. Measured 9 MB → 2063 MB RSS over 60 requests. |
| quince#465 | `pair`/`encryption`/`wifi-sync` accept unbounded concurrent ops; the reset does not bound a burst inside a window. |
| quince#464 | **The one that breaks the demo's only purpose**: behind a proxy, ten wrong guesses deny login to *every* visitor, and a demo nobody can log into is not a demo. **Ruled 2026-08-02, unbuilt** — trust `X-Forwarded-For` only from configured proxy addresses, empty default preserving today's behaviour exactly. A reverse proxy is where this first bites, so it lands with or before this mode. |
| quince#466 | idle keep-alives never expire on a host where connection-holding is the cheapest attack. |

## RULED (was `PROPOSED (gap)`): the interval is a reported deployment fact, carried in env

**Operator ruling, 2026-08-02, on quince#470; the decision and the reasoning that lost are in
`docs/quince.stack.md` under D12, and are deliberately NOT repeated here.**

**It is not a setting.** D12 governs settings; a value quince only renders and never branches on is a
*reported deployment fact*, outside D12's scope. The test is **does any code branch on this value** —
nothing branches on the interval, because D4 puts the restart outside the process and quince runs no
timer. Carried in env, read at startup, surfaced read-only. There is no `PUT`, so the
visitor-edits-the-promise problem does not arise.

**Story 6 is unblocked. The other six never were.** D5 stands unchanged and is now the ruling's own
shape: the mode takes the interval as a value it is told and asserts nothing about what it should be.
The Operator left the *value* open deliberately — *decide it when the instance exists* — and this
ruling is about where it lives, not what it is.

**Still open, and rung-local:** the env var's exact name and where it is read.

*(The enumeration of candidates was never repeated here, and this section is not the place to start.
An earlier draft listed them in both this spec and canon, and the two sets diverged within the hour
while both PRs were open — canon gained a possibility the spec did not have. One option set, one home,
and a builder reads the spec.)*

## Rule check

Written before building, covering every rule this touches *or comes near*.

- **Privacy is a commit-time gate.** The mode publishes a password by ruling and nothing else. The
  domain and host are deliberately absent from this document, as they were from quince#444 — an
  unregistered domain named in a public issue is how it gets taken. `make privacy-check` before push.
- **State honesty.** Every story is an observation, not a claim. The `Status: SPEC, unbuilt` header is
  part of this rule: nothing here asserts the mode works.
- **No silent caps or fallbacks.** Near-miss, and it is the sharpest one here. **The reset is a silent
  destructive fallback unless the UI says it is coming** — a visitor mid-click who is signed out and
  finds their edits gone has hit exactly the degraded mode this rule forbids leaving unsurfaced. That
  is why story 6 exists — and why *state no interval at all*, which the ruling did not take, is the
  option this rule argues hardest against.
- **Config tidiness (D12).** Directly touched, and now RULED: the interval is a *reported deployment
  fact*, outside D12's scope, because nothing branches on it. The spec raised it as a gap rather than
  asserting compliance, and canon carries the category as a result.
- **Secrets discipline.** `demo` is a published password by ruling, not a secret. It reaches
  `SetPassword` in process, never via argv or env. `test` is untouched.
- **Never mutate a committed version.** Not touched — the mode wires no storage backend, and
  `versionAdmin` is the demo provider operating on in-memory fixtures.
- **Docs are part of the diff.** This spec is the diff. It contradicts no canon; where it diverges from
  D12 it raised a gap rather than editing D12 — which is how D12 gained its third category.
- **Interface facts looked up live.** None pinned here. The build will re-check `Secure`-flag behaviour
  against the running server rather than against `cookie.go`'s comment.
- **Don't improvise architecture.** The interval's home is the one architectural question this spec
  reached; it was proposed rather than decided here, and has since been ruled. Everything else is
  either ruled on quince#444 or rung-local.
- **Coverage.** The build declares `go test -cover` plus a known-untested list. Named already as
  expected debt: the compose/timer wiring is deploy configuration and will not be covered by Go tests;
  it is proven by story 7's manual observation on a dev deploy.
