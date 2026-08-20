# deploy/

**Two audiences, and this list is which is which.** The distinction was implicit and got
re-derived every time somebody swept these files for internal jargon, so it is written down.

## For anyone running quince

Plain English, with none of this project's internal vocabulary.

| | |
| --- | --- |
| [`compose.yml`](compose.yml) | The example stack. Start here. |
| [`storage.md`](storage.md) | Where backups go, and what each kind of disk gives you |
| [`tls.md`](tls.md) | Getting HTTPS in front of quince |

## For people working on quince

These keep the project's own vocabulary, deliberately — their readers have the devlog open.

| | |
| --- | --- |
| [`dev.md`](dev.md) | Building, and running the gates |
| [`demo.md`](demo.md) | How the public demo is built and shipped |
| [`release.md`](release.md) | Cutting a release |

Everything else here — `Dockerfile`, the build and release scripts, `patches/`, `privacy/`,
`runner/` — is machinery rather than documentation.
