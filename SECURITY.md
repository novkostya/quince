# Security policy

quince holds a person's entire phone — messages, photos, credentials, health data — and the
pairing records that let a machine talk to that phone as a trusted host. `docs/quince.design.md` §6
opens by saying that *"LAN-only" is context, not a defense*, and this file is written on the same
assumption. Reports are welcome and will be taken seriously.

## Reporting a vulnerability

**Use GitHub's private vulnerability reporting:
[open a draft advisory](https://github.com/novkostya/quince/security/advisories/new).** It is
private to you and the maintainer until an advisory is published.

**Please do not open a public issue for a suspected vulnerability**, and please do not put one in a
pull request, a discussion or a commit message. Public git history is permanent here — an
unfixed finding filed in the open is the exploit's own announcement, and it stays readable after
the fix lands.

If the advisory form is unavailable to you for any reason, open a public issue that says **only**
that you have a security report and asks for a private channel — no details, no reproduction, no
affected path. That is enough to start the conversation without publishing the finding.

Useful in a report, roughly in order of value:

1. What an attacker gets, and what they need to start — unauthenticated on the LAN? an
   authenticated session? a malicious file inside a backup?
2. A reproduction, or the reasoning if you have not built one.
3. The commit you looked at. There are no releases yet, so a version number will not identify it.
4. Your deployment shape, if it matters — reverse proxy, `QUINCE_TRUSTED_PROXIES`, storage backend.

## What to expect

**quince is a pre-release project with one maintainer and no on-call, so this policy sets no
response times.** A deadline nobody is rostered to meet is worse than none, and *state honesty* is
a project rule rather than an aspiration. What you can expect instead:

- an acknowledgement that a human has read it, and whether it is understood as reported;
- the assessment in plain terms — including *"this is known and accepted, here is why"*, which is a
  real outcome and is not a brush-off (see the list below);
- the fix as an ordinary reviewed pull request, referencing the finding once it is no longer
  exploitable;
- credit in the advisory and the release notes, under whatever name you choose, unless you would
  rather not be named.

If a report goes unanswered, it has been missed rather than declined. Say so again.

## Supported versions

| Version | Supported |
| --- | --- |
| `main` | yes — this is the only thing there is |
| tagged releases | none exist yet |
| published images | none exist yet |

There is no tagged release and no image on any registry, so **there is nothing deployed that
quince's maintainer has shipped**. Anyone running it built it from a commit. Fixes land on `main`;
once releases exist this table gets a real support window.

## Scope

**In scope** — the daemon and everything it exposes:

- the HTTP API and the WebSocket, authenticated and unauthenticated;
- authentication, sessions, passkeys, and the re-authentication rules in design §6;
- the storage lifecycle — anything that mutates a committed version, escapes a storage root, or
  lets one device's backup reach another's;
- path handling for file names that come **out of a backup**, which are attacker-controlled input
  by design;
- the container image as built from `deploy/Dockerfile`, including the patched libimobiledevice in
  it (see `CREDITS.md`);
- handling of the backup password, pairing records, and anything else `docs/quince.design.md` §6
  calls a secret;
- the config file and the endpoints that write it.

**Out of scope**, because quince does not ship or control them:

- **the muxer.** `usbmuxd` and `netmuxd` are not in the image — quince dials whichever one the host
  runs. Report those to their own projects.
- **upstream libimobiledevice**, unless the bug is in one of quince's four patches under
  `deploy/patches/libimobiledevice/`. Report upstream; quince will move the pin.
- **Apple's backup protocol and its encryption.** quince drives it and does not implement it.
- **your reverse proxy, your TLS termination, your network.**
- **exposing quince to the public internet.** The docs say not to. Doing it anyway is a
  configuration you chose, not a vulnerability in the software — findings that only apply in that
  shape are still interesting, but say so in the report.

## Already known, and accepted rather than overlooked

These are documented decisions with reasoning behind them. Reporting one is not wasted — if you
think the reasoning is wrong, that is worth hearing — but it is not news, and each has a citation
you can argue with.

- **A self-signed certificate is the built-in fallback.** Real TLS is the user's reverse proxy.
  Design §6, *Transport*.
- **A user may knowingly turn off `Secure` cookies on a trusted network** — off by default, an
  explicit surfaced switch. Ruled by the Operator, 2026-08-02; design §6.
- **`X-Forwarded-Proto` is believed from any peer until `QUINCE_TRUSTED_PROXIES` is set**, while
  `X-Forwarded-For` is ignored until then. The asymmetry is deliberate and design §6 gives the
  reason: disbelieving one falls back to something true, disbelieving the other falls back to
  something false.
- **The backup password reaches `idevicebackup2` over a pty, or via `BACKUP_PASSWORD` in the
  environment of a short-lived child.** Same-uid exposure is accepted; argv is forbidden outright,
  because `/proc` is world-readable.
- **Pairing records under `/data` are private-key-grade secrets** and are mode `0600`, not served
  and not logged. Anyone who can read that directory can talk to the phone.
- **A secure wipe of on-disk session scratch is not achievable** on SSD or ZFS. The compose
  examples put scratch on tmpfs; the docs say plainly that deleted plaintext may survive in lower
  storage layers.
- **Whoever holds a valid session can use quince fully.** Changing *what can authenticate* needs a
  fresh proof presented with the operation — there is deliberately no sudo window, because an
  ambient grant is exactly what a stolen session inherits.

## The public demo

`quince-demo.fly.dev` runs `--public-demo` on fixture data with **the password printed on its own
login screen**, no persistence, and no device or Apple account behind it. That it is open is the
point of it. Logging in, reading the fixtures, or resetting it is not a finding.

A way to reach something the demo is not meant to expose — the host, another instance, or anything
that survives a restart — very much is.

## If you are testing

Test against your own instance. Do not test against anyone else's, do not run automated scanners
against the public demo (it is a 256 MB machine and you will only knock it over, which helps
nobody), and do not touch iPhones that are not yours. Stay within your own data and stop at the
point where you have demonstrated the issue.

Work done within those limits is welcome, and no report made in good faith under this policy will
be met with a complaint.
