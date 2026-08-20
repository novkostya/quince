# dev-deploy — a PR you can click, without asking anyone

> Status: **SPEC — ruled on first review, awaiting re-review.** No code exists. Tracked as `pr.4` in the devlog's
> revamp sequence; feature-named because the deliverable outlives the label. Reviewed before any
> code per `CLAUDE.md`, and written this way because pr.2's spec-first round paid for itself twice
> (the token-first amendment, and the storage correction).

## Goal

A session finishes a change and the PR carries a **working demo URL and a ≤5-line click list
without anyone asking for them** — deployed onto a disposable dev container by the same scoped
token that provisions it, with `/report` doing it by default.

## Boundary

**In scope:**

- `deploy/devct/devct` — a `deploy` verb: build the image from a ref on a dev container, run it in
  `--demo` mode, report the URL.
- `.claude/skills/report/SKILL.md` — call it by default (R5's "I don't have to ask" requirement).
- `.claude/skills/qa/SKILL.md` — **replaced, not extended** (its own instruction).
- `CLAUDE.md` — the DoD line, with the honest non-URL outcomes named.
- `docs/specs/dev-deploy/` — this spec.

**Out of scope:** staging deploys (by request, unchanged — one instance, one pairing, the soak stays
clean); real-device QA; any product code (frozen); anything under `.github/workflows/`; the runner
host (pr.5).

**What a staging deploy would COST, measured 2026-07-28 so the next reader does not rediscover it.**
Operator ruling that day: staging stays out of scope *for now*, and it is on the table later. The two
things it needs do not exist:

- **`quince-staging` runs no sshd** — port 22 answers `Connection refused`.
- **The runner HOLDS a private ssh key** — `/root/.ssh/id_ed25519`, since 2026-07-31. It is what
  implementer sessions reach the lab rig with, and it is the credential `pr.6` constraint 2 named as
  the cost of a push design.

So a push-from-the-runner design **no longer costs a new private key — that cost has already been
paid**, for a different reason and without this trade being revisited. What remains is standing
sshd up on the host that runs the soak. `pr.6` constraint 2's concern is unchanged and is now one
item further along: *"the runner accumulates login + bot token + devct token + ssh key"*, and the
ssh key is no longer hypothetical. A **pull** design still costs less: the registry already exists
(`oci-registry`), the runner already builds images, and staging pulling needs a registry credential
rather than a shell on the soak host. Neither is built; the trade is recorded so the choice is made
rather than defaulted into.

**Expected contract changes: NONE.** `--demo` mode already exists and is exercised by
`make gates-ui-e2e`.

## The URL question — RULED (architect, on this spec's first review)

**A deploy URL contains an address, and addresses are Operator-private.** R5 says the URL goes in
the PR automatically; the privacy rule says LAN addresses never enter PR text. Both are binding, so
this was raised rather than decided in code.

**Ruled: the convention name, and never a bare address in PR text under any option.**
`http://quince-dev-1:8968/` carries no site information — meaningless to a stranger, resolvable for
anyone holding the binding, the same trick the allowlist and `devct`'s generated ssh aliases already
use. The privacy rule is satisfied *by construction* rather than by a reviewer catching a leak.

**Four reader paths, in the order the PR should present them:**

1. **The convention URL is the identifier** — `http://quince-dev-1:8968/`, always in the PR.
2. **`ssh -L` is the address-free path — a fallback, not a requirement** (Operator amendment,
   2026-07-26, after the first implementation): on the LAN, the address the deploy prints is the
   fastest path and needs no setup, so the tool prints it first. `ssh -L` earns its place only
   where an address is unavailable or unusable — which is exactly the PR, and a reader off the LAN.
   **It is also the one path that does not scale to parallel rungs**, which the revamp exists to
   enable: every container has its own `8968`, so N deploys are N addresses, while N tunnels
   collide on the *local* port. Fixed by hand with a different local port when it happens;
   auto-allocating one is complexity bought for a path nobody is required to use.
   ```
   ssh -L 8968:127.0.0.1:8968 quince-dev-1   # then open http://localhost:8968
   ```
   It reuses the binding `devct` already generates, needs no file editing, and **dies with the
   session** — no stale `hosts` entry left pointing at a recycled DHCP address, which is a real
   hazard when containers are disposable by design.
3. **Direct resolution where the site's DNS registers DHCP hostnames** — then the convention URL
   simply works, including in mobile Safari with nothing configured. This matters: the Operator QAs
   from a phone (qn.6a exists because mobile *is* the soak surface), and `ssh -L` is not a phone
   workflow.
4. **Fallback: no URL, with the reason printed** — when no convention name is configured. Honest
   absence, never a bare address.

A `hosts` line stays available for anyone who prefers it; `devct deploy` prints it on request.

## Design

Links canon rather than repeating it: R5 (demo-by-default, staging-by-request, the DoD),
`deploy/devct/README.md` (the token model and the pool boundary), `deploy/dev.md` (`--demo` and the
`make image` path CI already uses).

### `devct deploy [--vmid N] [--ref REF] [--create]`

1. Resolve the container: `--vmid`, or the single running dev container, or `--create` to make one.
2. `git fetch` the ref inside it and check it out (a dev container already has the repo and a warmed
   toolchain cache — this is what makes the deploy minutes, not tens of minutes).
3. `make image` inside the container. This is the **production** image path, so the deploy proves
   the same artifact CI builds, not a special dev build.
4. Run it: `--demo`, port 8968, restart-on-boot off, replacing any previous deploy container so a
   re-deploy is idempotent rather than accumulating.
5. **Verify before claiming**: poll `GET /api/health` until it answers, then report. A URL printed
   without a successful fetch is the rung-2 defect class again, and this spec names it in advance.
6. Print, in the ruled order: the **convention URL** (goes in the PR), the **`ssh -L` command** (the
   desktop click path), and the real address **for the session only** — never for PR text. A
   `hosts` line on request, for readers who want direct resolution and whose DNS does not provide
   it.

### `/report` and `/qa`

`/report` calls `devct deploy` by default and pastes the URL + click list into the PR. When it
cannot deploy, it must print **one of two named outcomes** — never an improvised sentence:

- `deploy: not applicable — no runnable change` (docs/config-only), or
- `deploy: unavailable — <reason>` (no dev container, deploy failed), with the reason.

That is devlog#1 item 4's refinement, and it is what stops "not applicable" from covering for
"couldn't be bothered". `/qa` becomes the explicit form of the same machinery plus the click list;
the placeholder text goes.

## Stories

1. `devct deploy --ref <branch>` builds and serves that ref on a dev container and reports a URL it
   has actually fetched.
2. A second `devct deploy` on the same container replaces the first — no accumulation, no port
   clash.
3. `devct deploy` on a container with no image cache still works (slower, and says so).
4. `/report` produces URL + click list with no one asking.
5. A docs-only PR gets `deploy: not applicable — no runnable change`, and a failed deploy gets
   `deploy: unavailable — <reason>`; neither can be silently omitted.
6. No PR text ever contains an address — and the PR's click path works for a desktop reader via
   `ssh -L` without any file editing on their machine.

## Gates

- **G1** — `devct deploy --ref <this branch>` → `/api/health` returns 200 from the session host →
  the demo device list renders in a browser **reached over `ssh -L`**, which proves the ruled click
  path rather than only the container's own port (stories 1, 3, 6).
- **G2** — re-deploy replaces (story 2): two deploys, one container, one listener.
- **G3** — this rung's own PR carries the URL and click list, produced by `/report` rather than by
  hand (stories 4, 6). The rung dogfoods its own deliverable or it is not done.
- **G4** — a docs-only PR shows the "not applicable" line; a deliberately broken deploy shows
  "unavailable" with its reason (story 5).
- **Privacy gate** — `make privacy-check` plus a read of the PR body: convention names only.

## Fixtures

None. `--demo` mode is the fixture, and it is already exercised by `make gates-ui-e2e`.

## Rule check

- **Privacy** — the entire open question above is a privacy question; no address enters PR text
  under any option, and the recommendation keeps the binding on the reader's machine.
- **State honesty** — a URL is reported only after a successful health fetch; "not applicable" and
  "unavailable" are distinct and both explicit. The rung's own PR must carry a real URL (G3).
- **No silent fallbacks** — a missing container, a failed build, an unreachable demo each produce a
  named outcome, never a quiet omission of the deploy line.
- **Secrets** — the deploy needs none beyond what `devct create` already injects; the demo runs on
  fixtures, with no device and no backup data.
- **Boundary** — no product code, no contracts, no workflow files. The `--demo` flag is used as-is.
- **Docs are part of the diff** — `CLAUDE.md`'s DoD, both skills, and `deploy/devct/README.md` land
  with the behaviour.
- **Frozen product code** — deploying the demo changes nothing in `core/`, `vault/`, or `ui/`.
- **Resurrection test** — a stranger with their own Proxmox box gets the same deploy verb; only the
  convention binding is local.

## PR sequence

1. **this spec** — the URL question ruled before code exists.
2. **`devct deploy`** — the verb plus its README section. Claim: a ref becomes a clickable demo on a
   disposable container (G1, G2).
3. **skills + DoD** — `/report` by default, `/qa` replaced, `CLAUDE.md`'s DoD with the two named
   outcomes. Claim: a PR arrives with the URL nobody asked for (G3, G4).
