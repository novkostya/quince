---
name: qa
description: Deploy a branch to a disposable dev container in demo mode and produce the PR's URL + what-to-click list. Uses `devct deploy`; the URL is fetched before it is reported.
argument-hint: "[optional: branch or ref; defaults to the current branch]"
disable-model-invocation: true
---

# /qa $ARGUMENTS

Deploy the change so somebody can click it, and write the ≤5 lines telling them what to
click. `/report` does this by default — reach for `/qa` on its own when you are iterating
and want the demo refreshed.

**Never invent a URL.** A fabricated deploy link is the exact class of lie the state-honesty
rule exists to prevent, and it is invisible to a reviewer who doesn't click it. Nothing
below asks you to type a URL: the tool prints one it has already fetched.

## Deploy

```sh
deploy/devct/devct deploy --ref <branch>      # --create if nothing is running
```

That fetches the ref onto a dev container, builds **the production image** (the same target
CI builds — QA against a different artifact is QA of something nobody ships), serves it in
`--demo` mode replacing any previous deploy, and **polls `/api/health` until it answers**
before printing anything.

If it refuses, read the refusal: it names the condition it observed, and each one has a
different fix.

- `no running dev container` → add `--create`.
- `more than one running dev container (116,117)` → choose with `--vmid N`.
- a build failure, or a demo that never answered → that is a **finding**. It goes in the PR
  as `deploy: unavailable — <reason>`, never as silence.

## What goes in the PR

The tool prints three lines. **Paste the convention URL** — `http://quince-dev-N:8080/` —
and, under it, the `ssh -L` line for a reader who has the alias but not the LAN.

**Never paste the address.** It is Operator-private, the tool labels it session-only, and
the convention name carries no site information by design.

On the LAN the address is the fastest way for *you* to click it. The tunnel is for the
reader, and it collides on the local port when two deploys run at once (use a different
local port) — which is why it is a fallback, not the path.

## The click list

≤5 imperative lines, each doable in the demo without a device:

```
1. Open <url> → set an admin password (demo asks on first load).
2. Devices → the demo iPhone is listed with a USB badge.
3. Open it → Back up now; watch the job phases advance live.
```

Walk them yourself before pasting them. A click list nobody has walked is a claim, and a
documented path nobody has walked is how this rung found a broken one.

## When the demo is not the right surface

Real-device QA stays on the staging stand — one instance, one pairing, so the soak stays
clean. Staging deploys are by request, never by default.

Demo mode is also the public face of quince for anyone evaluating it without a device: if
you find it thin or broken while using it here, that is a real finding worth an issue.
