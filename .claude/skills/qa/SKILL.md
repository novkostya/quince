---
name: qa
description: Deploy the built image so a change can be clicked, and produce the PR's what-to-click list. PLACEHOLDER — the self-serve dev-container deploy does not exist yet; today this runs the demo locally on the container host and tells you what to write in the PR.
argument-hint: "[optional: PR number]"
disable-model-invocation: true
---

# /qa $ARGUMENTS — placeholder, and it says so

**This skill is not finished, and it will not pretend otherwise.** The ruled design is:
a PR's dev container deploys the built image in `--demo` mode and the deploy URL is posted
in the PR automatically, with real-device QA staying on the staging stand (one instance,
one pairing, the soak stays clean). That needs the dev-container tooling and the deploy
hook in `/report` — neither exists yet. Until they do, do the honest manual version below.

**Never invent a URL.** A fabricated deploy link is the exact class of lie the state-honesty
rule exists to prevent, and it is invisible to a reviewer who doesn't click it.

## Today: run the demo yourself on the container host

```sh
make image
docker run --rm -p 8080:8080 \
  -e QUINCE_LISTEN=:8080 -e QUINCE_DATA=/tmp -e QUINCE_CACHE=/tmp -e QUINCE_BACKUPS=/tmp \
  quince:local serve --demo            # nerdctl run … on a containerd host
```

(That is the same invocation `make gates-ui-e2e` uses for its Playwright run, so demo mode
is exercised by CI too.) Reach it at `http://<the container host>:8080` from the LAN, click
the change through yourself, and if the UI moved, take a screenshot for the PR.

Demo mode is also the public face of quince for anyone evaluating it without a device —
if you find it thin or broken while using it here, that is a real finding worth an issue.

## Then: write the click list

≤5 lines, imperative, each line something a reviewer can do in the demo without a device:

```
1. Open <url> → Devices; the demo iPhone is listed with a USB badge.
2. Open it → Back up now; watch the job phases advance live.
3. …
```

In the PR, pair the list with the honest status line:

- `no dev-deploy URL — tooling pending (pr.4); demo run locally on the container host`, or
- `deploy leg not applicable: no runnable change` (docs/config-only PRs).

## When the tooling lands

Replace this skill; don't extend it. The finished version provisions or reuses the PR's dev
container, deploys the image built from the PR head, posts the URL as a PR comment, and
`/report` calls it by default. Everything above becomes the fallback path for a session with
no dev container.
