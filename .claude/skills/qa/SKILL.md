---
name: qa
description: Build this branch and serve it in demo mode on this box, then produce the PR's URL + what-to-click list. Uses `make demo`; the URL is fetched before it is reported.
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
make demo                                    # builds THIS branch and serves it, on this box
```

That fetches the ref onto a dev container, builds **the production image** (the same target
CI builds — QA against a different artifact is QA of something nobody ships), serves it in
`--demo` mode replacing any previous deploy, and **polls `/api/health` until it answers**
before printing anything.

If it refuses, read the refusal: it names the condition it observed, and each one has a
different fix.

**Why `make demo` and not `devct deploy`.** `devct deploy` needs a Proxmox API token, a pinned CA
and `devct.conf` under `~/.config/quince` — and `provision` places none of them on a session host, so
on the runner it exits 1 with `no config at ~/.config/quince/devct.conf`. The DoD's deploy leg
therefore required the Operator's workstation, silently, discovered at report time. quince-devlog#45
removed the reason for the round trip: the runner IS the work host, so there is no container to
select and no hypervisor to ask (quince#45).

- `could not bind a port in 10 tries` → something else is serving; say `deploy: unavailable — <reason>`.
- `/api/health never answered` → the build ran and the app did not come up. That is a FINDING, not
  an unavailable deploy — the logs are printed; read them.
- a build failure, or a demo that never answered → that is a **finding**. It goes in the PR
  as `deploy: unavailable — <reason>`, never as silence.

## What goes in the PR

The tool prints **two claims, and they are not the same claim**:

```
demo: answering on 127.0.0.1:<port> — FETCHED, not composed.

  paste this into the PR:  http://quince-runner:<port>/
```

**Paste the second line.** It is a convention name plus the allocated port, and it carries no
site information by design. The first line is what *this box* verified — the service answered
`/api/health` on the loopback — and the tool cannot verify that a name resolves for anybody
else, which is why it says so instead of calling both "verified".

**The port is allocated, not fixed, so it is part of the URL.** `make demo` takes 8080 if it is
free and the next port if it is not, because two runners serving demos on one box is the ordinary
case once parallel rungs exist. A URL without the port is a URL for somebody else's demo.

**Never paste an address.** `CLAUDE.md` is unambiguous and the rule did not change — what changed
is the tool. `quince-dev-N` no longer exists: quince-devlog#45 removed dev containers and the
runner is the work host, so the convention name is the runner's (`DEMO_HOST`, overridable when
serving from elsewhere).

**There is no `ssh -L` line any more, and that is a real loss worth naming.** `devct deploy` set up
forwarding on the container it created; `make demo` touches no sshd. A reader who has the alias but
not the LAN now forwards by hand. Not restored here, because inventing a new capability inside a
bug fix is how a bug fix stops being reviewable.

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
