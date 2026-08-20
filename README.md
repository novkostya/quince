<h1 align="center">quince</h1>

<p align="center">
  <b>Self-hosted iPhone and iPad backups.</b><br>
  Runs on your own server. Keeps every version, not just the latest one.
</p>

<p align="center">
  <a href="https://github.com/novkostya/quince/releases"><img src="https://img.shields.io/github/v/release/novkostya/quince?include_prereleases&label=release" alt="Latest release"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/novkostya/quince" alt="MIT licence"></a>
</p>

## [Try the live demo →](https://demo.quince.page/)

Sample data, password `demo`. Nothing you do there is kept.

> [!IMPORTANT]
> **quince is pre-release.** It backs up real iPhones over USB and Wi-Fi every day, and it is
> not finished: **it cannot restore a backup yet**, and you cannot browse what is inside one.
> Treat it as a second copy of your data, not your only one.

## What it does

- **Backs up over USB or Wi-Fi**, in your phone's own backup format. Encrypted backups are the
  default, because an unencrypted one silently leaves out Health, saved passwords and call history.
- **Keeps every version.** Each one is frozen the moment it is written, so a new backup can
  never spoil an older one.
- **Lives on the server**, next to the disk. Nothing else has to be awake.
- **Safe to copy offsite while a backup is running** — a version only appears once it has been
  verified, so a sync job can never pick up a half-written one.
- **One config file**, edited from the web UI, with no passwords in it.

iOS makes you unlock the phone and confirm every Wi-Fi backup, so quince asks — it sends a
notification when a backup is worth doing instead of pretending it can run unattended.

## Install

You need a Linux server with Docker — x86-64 or ARM, so a NAS or a Pi is fine — and your phone on
the same network.

**1. Pick your file.** Talking to an iPhone over USB needs a service called `usbmuxd`. quince can
run one for you, but if this machine already has one they will fight over the phone, so check:

```sh
ss -lx | grep usbmux
```

Nothing → [`compose.yml`](deploy/compose.yml). Something → [`compose.host-muxer.yml`](deploy/compose.host-muxer.yml),
which uses the one you have.

**2. Save it as `compose.yml` and change the backups path** to point at real storage — a big disk,
a NAS share. It is marked in the file, and it is the only line you must change before starting.

**3. Start it, and open `http://<your-server>:8968`.**

```sh
docker compose up -d
```

quince takes you through the rest — a password, where backups go, and pairing your phone.

## Docs

| | |
| --- | --- |
| [`deploy/storage.md`](deploy/storage.md) | Where backups go, and what each kind of disk gives you |
| [`deploy/tls.md`](deploy/tls.md) | Getting HTTPS in front of quince |
| [`docs/`](docs/) | How quince is built and why — architecture, API, design decisions |
| [quince-devlog](https://github.com/novkostya/quince-devlog) | What is being worked on, and every decision so far |

quince is built by AI agents, working in the open. The devlog is the whole record.

## Security

quince holds everything that is on your phone. [`SECURITY.md`](SECURITY.md) has the security
model and how to report something privately — read its *known and accepted* list first, because
some sharp edges are deliberate and it says why.

## Licence

[MIT](LICENSE). The container also ships tooling under LGPL-2.1 and GPL-2.0;
[`CREDITS.md`](CREDITS.md) lists every dependency and its licence. Not affiliated with Apple Inc.
