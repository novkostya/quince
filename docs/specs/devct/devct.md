# devct — disposable dev containers, generated from a public script

> Status: **SPEC — amended after architect review
> ([#10](https://github.com/novkostya/quince/pull/10)), awaiting re-review.** No code exists.
> Tracked as `pr.2` in the devlog's revamp sequence; the deliverable is feature-named (`devct`)
> because it outlives the sequence label. Reviewed before any code per `CLAUDE.md` ("a rung starts
> from a spec") and the program doc's spec-first loop.
>
> **Amendment (blocking review finding, folded in): the template build is TOKEN-FIRST, with
> supervised root demoted to a named fallback.** The first draft had it inverted — it assumed image
> download and container conversion needed root, and planned an Operator root session. Under the
> standing authorization for the architect to extend the devct user's own ACLs self-service, both
> steps are plausibly grantable privileges, so the plan is now: attempt the full token path, and take
> root only for a step the API *demonstrably* refuses — naming that step, with the refusal as
> evidence, in the PR. Ceremonies rewritten accordingly (§Ceremonies). Non-blocking and also folded
> in: the `bin/gh-bot` wrapper joins PR 4.

## Goal

A public `deploy/devct/` toolkit that builds a dev-container template on any Proxmox host and
then creates, provisions and destroys disposable per-unit-of-work dev containers through a
**scoped API token** — so a session gets a gate-ready box with one command, and nobody hand-builds
a container or reaches for root.

## Boundary

**In scope:**

- `deploy/devct/**` — the two entrypoints (`devct-template`, `devct`), their docs, the committed
  container-option baseline.
- `Makefile` — one new target, `gates-sh` (shell lint), wired into `gates` so CI picks it up
  without touching CI.
- `.claude/settings.json` — replace the deliberately fragile Proxmox `curl` allowlist entry with
  the wrapper script, as `.claude/README.md` already says is the honest fix; add the vendor
  documentation domain the *pins-and-interface-facts-are-looked-up-live* rule sends this work to.
- `.claude/README.md`, `deploy/dev.md` — the conventions they already promise this tooling defines.
- `bin/gh-bot` — a wrapper that reads the bot token from its file and `exec`s `gh`, allowlisted as a
  script. Accepted from the pr.2 kickoff's friction notes
  ([devlog#1](https://github.com/novkostya/quince-devlog/issues/1)): `Bash(gh pr *)` is allowlisted
  and yet every bot-authored `gh` call still prompts, because an allow rule never matches past a
  leading `VAR=value` assignment — so the documented way to act as the bot is unallowlistable. Same
  allowlist-the-script move as the Proxmox `curl` entry, and the same shape as the git credential
  helper that already keeps the token out of the environment.
- `docs/specs/devct/` — this spec.

**Out of scope:**

- **pr.4** — the `/report` deploy hook, the `/qa` rewrite, deploy URLs in PRs. This rung makes
  those *possible*; it does not build them, and `/qa` keeps saying it is a placeholder.
- **pr.5** — the session/runner host. `devct` provisions boxes that run gates, not boxes that run
  Claude sessions.
- **Product code** (`core/`, `vault/`, `ui/`) — frozen; this rung compiles nothing of it beyond
  running the existing gate ladder inside a container it made.
- **Anything under `.github/workflows/`** — the bot token has no `workflow` scope, and none is
  needed: CI calls
  only `make`, so a new gate target is picked up by the existing workflow.
- Staging/lab deployment and the soak. Untouched.

**Expected contract changes: NONE.** No REST/WS surface, no vault RPC, no storage semantics. If
review finds a contract touch is needed, STOP and propose it before building.

## Design

Canon this rests on rather than repeats: `deploy/dev.md` (a dev host is a *container host*, never a
toolchain host — every gate runs in a pinned toolchain container), `.claude/README.md` (the
`quince-pve` / `quince-dev-N` convention and the two-layer permission model), `versions.env` (the
single source of pins), and the devlog's R3 ruling (thin template generated from scratch by a public
script; lifecycle via a scoped API token, not a forced-command helper; secrets live on the session
host and are injected at provision; supervised root is a rare, expiring exception).

### Two entrypoints, split by the privilege they need

| Script | Runs as | When | Why separate |
| --- | --- | --- | --- |
| `devct-template` | **the scoped token** wherever the API allows; a supervised, expiring root session only as a *named fallback* | once per `versions.env` change | rare, slow, and rebuilds a shared artifact — a different blast radius and a different cadence, not a different privilege class |
| `devct` | the scoped API token, from the session host | many times a day | the everyday path must never need root — that is the whole point of the ruling |

`devct` verbs: `doctor`, `onboard`, `create`, `list`, `destroy`.

### The template build is token-first; root is a fallback that must justify itself

The split above is about cadence, **not** about root. Each build step names the privilege it expects,
and the ladder is attempted in order on the token alone:

1. **ACL top-ups** — `doctor` reports the delta; the architect grants it self-service on the devct
   user under the standing authorization. Not an Operator ceremony.
2. **Appliance image download** to the template storage — expected to be `Datastore.AllocateTemplate`,
   a grantable privilege rather than a root-only act.
3. **Create the container in the pool** from that image, with the authorized key baked in — the same
   create path `devct create` uses, so it is proven twice.
4. **Provision over ssh** — install the toolset, write the registry config, warm the toolchain cache.
   No hypervisor privilege at all: this is a session-host-to-container ssh, exactly like `create`'s
   secret injection.
5. **Stop, then convert to a template** — expected to fall under the granted container privileges for
   an *in-pool* vmid.
6. **Stamp and verify.**

**Root is taken only for a step the API demonstrably refuses**, and that step is named in the PR with
the API's own refusal as the evidence — not asserted in advance, which is what the first draft did.
If no step refuses, the root session dissolves and the template build joins everything else on the
token.

### ANSWERED: the root need is real, and it is one flag wide

Step 3 refused, with the reason that settles the amendment's open question:

> HTTP 403 — *"Permission check failed (changing feature flags (except nesting) is only allowed for
> root@pam)"*

`keyctl` is **root@pam by design**: PVE permits a non-root user to set `nesting` and nothing else, so
this is not a missing grant and no ACL can ever cover it. The first draft's instinct — that the
template build needs something a pool-scoped token cannot do — was right for exactly one reason, and
wrong about which one; the review's demand that the wall be *measured* rather than assumed is what
produced a fix this narrow instead of a standing root session.

**The consequence is favourable, because features are inherited by clones:**

- the create runs on the token with `nesting` alone;
- one named root command adds the flag — `pct set <vmid> -features nesting=1,keyctl=1` — announced
  before it runs, with the refusal class quoted as its justification;
- it happens **once per template rebuild** (`versions.env` cadence, i.e. rarely), and
- **`devct create` is pure-token forever**, which is the property the whole ruling exists to protect.

**Still open, and cheap to settle:** whether the container toolchain needs `keyctl` at all.
`--skip-keyctl` builds without it; if step 4 warms the cache and the gates run green, the last root
step disappears entirely. Reported either way — a negative result here is worth as much as a
positive one.

**The one root micro-step that survives regardless:** a single read-only config read of the
known-good container, to derive the committed option baseline. It is read-only, one-off, and fine
inside any supervised moment.

### Site facts are parameters, secrets stay on the session host

Nothing site-shaped is committed. `devct` reads `~/.config/quince/devct.conf` (0600, written by
`devct onboard`): API endpoint, node, pool, storage, bridge, template name, ssh key path, registry.
The API token secret stays in its own file (`~/.config/quince/proxmox-devct.token`, already placed);
the bot token and registry credentials stay in `~/.config/quince/` and are **injected into a CT over
ssh at provision**, so nothing accumulates on the hypervisor and rotation is one file plus a
`devct destroy`.

**Secrets never enter argv.** `curl` is driven with `-K -` (options, including the token header,
read from stdin), which is the same stdin-only discipline the backup password already follows.
A script that would put a secret on a command line fails review.

### TLS is pinned, never disabled

The API presents a self-signed certificate. `devct onboard` captures the server certificate once,
prints its SHA-256 fingerprint for out-of-band confirmation, and stores it; every later call
validates against that pin (`curl --cacert`). **`curl -k` / `--insecure` is forbidden in this tree**
and the shell gate greps for it. A missing pin is a hard failure with the onboarding command named —
never a silent downgrade.

### The template

Thin Alpine LXC carrying exactly what a gate needs — `git`, `make`, a container runtime
(containerd + nerdctl + buildkit), `gh`, `openssh` — plus:

- the **authorized key** baked at build time (passed in as a parameter; the API cannot exec into a
  CT, and does not need to, because the box is born reachable);
- the **registry client config** (`hosts.toml` shape committed, registry name injected) so a CT can
  `make image push` unattended — the infra half of R3(4);
- a **pre-warmed buildkit/toolchain cache** (`make toolchains` run once at build time) — the reason
  a fresh CT reaches a green gate in minutes instead of re-pulling every toolchain;
- a **stamp** recording the `versions.env` digest and build date it was built from.

**Container options are committed with their reasons, not copied blindly.** Running nerdctl inside
an LXC needs specific options (nesting, keyctl, and the unprivileged-vs-privileged call). PR 2's
first input is one supervised read of the known-good dev container's config; the resulting option
set is committed as a documented baseline, each line stating what breaks without it. Values are read
from the live box — not recalled — and the sanitized set is what ships.

### Staleness is surfaced, never silently used

`devct create` compares the template stamp against the repo's `versions.env`. A mismatch prints a
loud, named warning (`run devct-template build`) and `devct list` marks the CT stale. A pinned
toolchain that silently drifts from the template is precisely the "no silent fallbacks" failure
mode.

### The ssh alias is the binding, and the tool owns its own file

CTs take their address from the network (static addressing is supported via `devct.conf` for sites
that prefer it), so `devct` maintains a **generated ssh include file it owns**
(`~/.ssh/quince-devct.conf`) and rewrites it on every `create`/`destroy`. `devct onboard` **prints**
the single `Include` line to add to `~/.ssh/config` and the bindings it wrote — it never edits a
user's ssh config silently. After onboarding, `ssh quince-dev-1 'make gates'` matches the committed
allowlist with no prompt, which is the contract `.claude/README.md` already documents.

### Interface facts — looked up, and re-proven empirically

Verified live while writing this spec (2026-07-25), because a remembered permission model is how
this class of work burns a ceremony round-trip — or, as the review caught, invents a privilege wall
that was never there:

- **`SDN.Use` on the bridge** (`/sdn/zones/<zone>/<bridge>`) is required to create a container with
  a network interface on PVE 8+ — `PVEVMAdmin` alone is not enough, and the failure is a
  `Permission check failed (…, SDN.Use)` at create time.
  ([forum thread](https://forum.proxmox.com/threads/token-permissions-and-creating-lxc-container.173227/))
- **`Datastore.AllocateTemplate`** (template download/upload) and **`VM.Clone`** are the other two
  privileges automation roles of this shape carry beside `Datastore.AllocateSpace`.
  ([role examples](https://github.com/trfore/packer-proxmox-templates/))

Attempted and **not** settled by lookup: the privilege required by the container-to-template
conversion endpoint. Recorded as unsettled rather than guessed — it is verified at build time
against the live API viewer, and step 5 above is where it proves itself either way.

**Measured since, by the first live `doctor` run:** the hypothesis held where it mattered. The pool
already carries the container privileges the ladder needs (allocate, clone, network config, power,
audit) and the CT storage carries space + audit; the only gaps are the two this section predicted —
template allocation on the storage (ladder step 2) and bridge use (step 3). Both are **grants, not
root operations**, which is the first real evidence for the amendment's premise. **One fact no
lookup predicted, found only by running it:** an address in `api_host` fails certificate
verification — curl verifies the URL's host against the certificate, and PVE's self-signed
certificate names the node. `api_host` must be a name the certificate carries; `api_addr` binds it
via `--resolve` where DNS doesn't. Verification stays on either way, which is the whole point of
banning `-k`.

**Corrected by the generator's first run — the two storages are not one storage.** This spec said
"the CT storage" throughout, and both the grant and `doctor`'s check followed it onto the zfspool
that holds container rootfs. A zfspool cannot hold `vztmpl` content at all, so
`Datastore.AllocateTemplate` there authorises nothing: the appliance download needs it on the
storage that actually holds templates. `template_storage` is therefore a **required** config key
with no fallback to `storage` — a default that reports a privilege as satisfied where it can never
apply is exactly the silent-success this project's rules exist to prevent. The measurement stands as
the strongest evidence yet for the token-first amendment: the ladder's first blocking gap is again a
*grant*, not a root operation.

Not remembered, and not trusted either: **`devct doctor` asks the API what the token can actually
do** (its own permissions endpoint) and reports the delta against the required set. The privilege
list above is the hypothesis; `doctor` is the proof, and a missing privilege comes back as a named
ACL top-up rather than a mysterious 403 mid-provision. The exact token header spelling, the
`curl -K -` form, the appliance-download and template-conversion endpoints, the Alpine package
names, and the current Alpine CT template tag are all verified at build time and cited in the PR's
evidence.

## Stories

Each is independently checkable.

1. **`devct doctor` tells the truth about readiness without changing anything.** It reports, item by
   item: conf present, token file present, API reachable, TLS pin valid, each required privilege
   present or missing, template present and fresh, ssh include wired. Missing items name the exact
   fix — an ACL top-up, a binding, a build — and who can perform it. Non-zero exit when not ready.
2. **`devct-template build` produces the golden template from scratch, on the token path**, on a
   Proxmox host with no prior template: current Alpine CT image, the toolset, the baked key, the
   registry config, the pre-warmed cache, the stamp, joined to the pool so the scoped token can
   clone it. Any step the API refuses is reported with its refusal, and only such a step is done
   under supervised root.
3. **`devct create` makes a gate-ready box with the token alone.** Clone → start → wait for the
   network → inject the session host's secrets over ssh → rewrite the ssh include → print the alias
   and the exact gate command. No root anywhere in the path.
4. **`devct destroy N` removes a container and refuses anything outside the pool.** The pool-membership
   check is the tool's own guard; the token's scope is the second line of defence.
5. **`devct list` shows the pool** — id, alias, status, template stamp, and a stale marker.
6. **`devct onboard` binds a fresh machine in one command** — writes `devct.conf`, pins the TLS
   certificate, writes `.claude/settings.local.json` bindings, prints (never applies) the ssh
   `Include` line.
7. **A CT created by story 3 runs `make gates` green** on a fresh clone of this repo, with no
   toolchain installed on it.
8. **A CT created by story 3 pushes to the registry unattended** (`make image push REGISTRY=…`).
9. **A stale template is surfaced** — a template whose stamp predates the repo's `versions.env` is
   reported by both `create` and `list`, and never silently used.
10. **`make gates-sh` lints every script in the tree** (shellcheck, pinned container, POSIX `sh` —
    the dev CT's shell is busybox) and is wired into `make gates`, so CI runs it with no workflow
    change.

## Gates

Beyond `make gates` / `make image` / `make gates-ui-e2e` in CI on every PR:

**CI-provable (owner: the implementer session):**

- `make gates-sh` green; it fails on a planted `curl -k` and on a bashism (story 10, and the
  TLS-pin rule with teeth).
- `devct doctor` run in a clone with no `devct.conf` exits non-zero and lists every missing item
  with its fix (story 1's negative half needs no hypervisor).

**Live legs (owner: this session, against the reference Proxmox; any surviving root step is
Operator-supervised):**

- **G1 — template build on the token path.** `devct-template build` end to end on a host with no
  prior template; the container config of the result matches the committed baseline; the stamp is
  present and correct (story 2). **The PR states, step by step, which of the six ladder steps ran on
  the token and which — if any — needed root, quoting the API refusal that forced it.** "No root was
  needed" is a result worth reporting; "root was used because it was easier" is not an outcome this
  gate accepts.
- **G2 — lifecycle on the token alone.** With no root key in the agent's ssh path: `devct create` →
  `ssh quince-dev-N 'git clone … && make gates'` green → `devct list` shows it → `devct destroy`
  removes it → `devct list` shows the pool empty (stories 3, 4, 5, 7). Wall-clock for
  create-to-green is recorded in the PR — the pre-warmed cache is the claim being tested.
- **G3 — the guard holds.** `devct destroy` aimed at an id outside the pool refuses locally, and a
  read against a non-pool container returns 403, proving the token's scope. **A destroy is never
  attempted against a container outside the pool** — the guard is proven by the refusal and the
  read, not by aiming a delete at live infrastructure.
- **G4 — registry push.** `make image push REGISTRY=…` from inside a created CT, unattended
  (story 8).
- **G5 — staleness.** A template stamped against a modified `versions.env` is reported stale by both
  `create` and `list` (story 9).

**Declared untested (accepted debt, per the coverage rule):** the scripts have no automated test
coverage beyond shellcheck — shell logic here is proven by running it (G1–G5), which is the same
standard the lab gates meet. Error paths not exercised by G1–G5 (API 5xx mid-clone, ssh timeout
during injection) are handled and logged but unproven; they are named in the PR rather than implied
to work.

## Fixtures

None in the product test corpus. The tree adds no test data; `deploy/devct/` carries only scripts,
a committed container-option baseline, and docs.

## Rule check

- **Privacy is a commit-time gate.** Every site fact — host, node, pool, storage, bridge, registry,
  addresses, container ids — is a parameter read from an uncommitted conf. Committed text uses
  convention names only. `make privacy-check` on every commit, whole-branch re-sweep before merge.
  Near-miss named: the container-option baseline is derived from a live box, so it is sanitized to
  the option set and its rationale, never a config dump.
- **Secrets discipline.** Token secret, bot token and registry credentials stay in files on the
  session host, are read at point of use, travel over ssh/stdin, and never appear in argv, env in a
  committed script, or logs. `curl -K -` is the mechanism, and the shell gate enforces the ban on
  `-k`.
- **State honesty.** `doctor` reports what it probed, not what it assumes; `create` claims a box is
  ready only after the ssh round-trip succeeds; a stale template says so. No step reports success it
  did not observe.
- **No silent caps or fallbacks.** Missing privilege, missing pin, stale template, absent binding —
  each fails loudly and names the fix. The template build's root fallback is the one fallback in the
  design, and it is the opposite of silent: it is taken only after the API refuses a step, and the
  refusal is quoted in the PR.
- **Interface facts and pins are looked up live.** The privilege names above were verified while
  writing this spec and are cited; the Alpine template tag, package names, `curl` flags and the
  token header spelling are verified at build time with the lookup in the PR's evidence. `doctor`
  re-proves the permission model empirically on every run, so a decayed scope surfaces as output
  rather than as a stale belief.
- **Docs are part of the diff.** `.claude/README.md` promises this tooling and calls the current
  `curl` allowlist entry the fragile placeholder; `deploy/dev.md` describes the dev host. Both are
  updated in the PR that makes them true.
- **The resurrection test.** Nothing in `deploy/devct/` requires the private layer: a stranger with
  a Proxmox box, this repo, and their own credentials runs `devct onboard` and gets the same
  environment. The private layer holds bindings only.
- **Root is a fallback that must justify itself, not a plan.** The build attempts every step on the
  token; root is reached for only when the API demonstrably refuses, and any such session is an
  Operator-added authorized_keys entry carrying `expiry-time` (R3(2b)) so a forgotten cleanup cannot
  leave a standing hole. The privilege wall the first draft assumed was never tested — that is
  exactly the "don't build on an assumption you never wrote down" failure, caught at spec review,
  which is where it is cheap.
- **Boundary.** No product code, no contracts, no storage code, no `.github/workflows/**`. The
  `Makefile` edit is one additive target.
- **Gap protocol.** R3 rules the shape; the choices this spec settles (two entrypoints, conf home,
  ssh include ownership, stamp-based staleness) are implementation detail inside that ruling and are
  recorded here rather than improvised in code. Anything that turns out to need a different
  *authority* model — a privilege the token cannot hold, a step that genuinely needs standing root —
  stops and gets proposed, not worked around.

## Ceremonies

**Architect, self-service (not Operator ceremonies):**

- **ACL top-ups as `doctor` finds them** — granted on the devct user under the standing
  authorization, each one logged in the PR so the final ACL set is a written artifact rather than a
  box's accumulated state. **Measured, no longer predicted:** the delta is `SDN.Use` on the bridge
  and `Datastore.AllocateTemplate` on the CT storage; everything else the ladder needs is already
  held. The template-conversion privilege remains unmeasured until step 5 runs.

**Operator, likely the only one:**

- **Registry credentials** for the container, if the session host does not already hold them.

**Operator — measured, and now known to be exactly one thing:**

- **One root command per template rebuild**: `pct set <vmid> -features nesting=1,keyctl=1`. Requested
  with the refusal in hand, as an authorized_keys entry with `expiry-time` (R3(2b)), or run by the
  Operator directly — the build stops and prints the command with a `--vmid` to resume from when no
  root path is configured. It does not dissolve (the flag is root@pam by design), but it does not
  recur outside `versions.env` cadence, and it never touches `devct create`.
  **Possibly zero** if the `--skip-keyctl` measurement shows the toolchain does not need the flag.
- **One read-only container-config read** of the known-good box, to derive the committed option
  baseline. Survives regardless of the above, is read-only, and fits inside any supervised moment.

## PR sequence

**Re-sliced after the amendment** (the original 4 became 5): `doctor` moves ahead of the template
generator. The amendment makes ladder step 1 *"`doctor` reports the ACL delta"*, so the generator
cannot be written honestly — nor its root question answered — until the probe exists and has run.
`doctor` also carries the config/TLS/API core both scripts share, so building it first duplicates
nothing.

1. **the spec** — reviewed before any code existed. ✔ landed
2. **API core + `doctor`** — `lib.sh`, `devct doctor`, `make gates-sh`. Claim: what the token can do
   is a *measurement*, not a belief (story 1, story 10).
3. **template generator** — `devct-template` + the committed option baseline. Claim: a template is
   buildable from scratch by a public script, on the token path, with any root step named and
   justified (G1, story 2).
4. **lifecycle** — `devct create|list|destroy`. Claim: a disposable CT is created and destroyed with
   the scoped token, no root (G2–G5).
5. **onboarding + allowlist** — `devct onboard`, `bin/gh-bot`, the `.claude/settings.json` swap
   (Proxmox `curl` entry → the wrapper scripts), `.claude/README.md` and `deploy/dev.md`. Claim: a
   fresh machine binds the conventions in one command, and acting as the bot stops prompting
   (story 6).

3 and 4 may land as one PR if the reviewer prefers fewer sibling re-approvals
([devlog#1](https://github.com/novkostya/quince-devlog/issues/1) item 8).
