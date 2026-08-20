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

```sh
mkdir quince && cd quince
wget https://raw.githubusercontent.com/novkostya/quince/main/deploy/compose.yml
docker compose up -d
```

Then open `http://<your-server>:8968`. quince starts by helping you set up HTTPS — browsers throw
away the login cookie on an unencrypted connection, so nothing but localhost would stay signed in —
and then asks for a password and a folder to keep backups in. Plug in your iPhone, pair it, and
back it up.

[`deploy/compose.yml`](deploy/compose.yml) is commented, including what to change if you
already run `usbmuxd` on that machine.

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
