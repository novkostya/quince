# idevicebackup2 transcript fixtures

These are the durable, committable extract of the feasibility lab: real `idevicebackup2`
stdout/stderr captured against actual devices, replayed in tests so the backup state
machine (qn.4) is proven against genuine protocol behaviour — including the pathological
paths — without a phone in the loop (stack D9).

**Consumed by:** qn.4 (backup engine). The tests run the real state machine against a
**fake `idevicebackup2`** — a scripted binary that replays one of these transcripts on
stdout with the original timing/silences — and a **fake muxd socket**.

**Why committed here and not derived from the lab log:** the lab log
(`chatgpt-original-idea-chat.md`) is Operator-private and gitignored (it contains device
UDIDs and personal data). It never enters the public repo. These extracted transcripts,
scrubbed of identifying data, are the public durable form.

## Extraction task (performed at the start of qn.4)

From the lab log, extract each distinct `idevicebackup2 backup` run and save it as one
`*.txt` transcript here. Scrub UDIDs, device names, and any personal paths (replace with
stable placeholders, e.g. `UDID0`, `test-iphone`). Capture at least these cases — each is
a named replay fixture and, per the hard rules, every new bug found on the lab box later
becomes another one:

| Fixture | What it exercises |
| --- | --- |
| `full-usb-success.txt` | a clean full encrypted backup over USB (the happy path) |
| `wifi-incremental-success.txt` | an incremental over Wi-Fi via netmuxd (`Backup Successful`) |
| `waiting-for-passcode.txt` | the `*** Waiting for passcode ***` prompt (assisted model, D13) |
| `wifi-torn-session.txt` | a Wi-Fi session dropping mid-transfer (`-4` / connection lost) |
| `silent-stall.txt` | multi-minute silence that is NOT a failure (liveness sampler input) |
| `encryption-changed.txt` | a run right after backup-password enable/change |

## Format

Plain text, one run per file: the `idevicebackup2` stdout lines the tool emits (progress
bars, `Full backup mode.`, `Backup Successful.`, the passcode prompt, …). A companion
`*.meta.json` records the expected terminal state and the replayer's timing hints.

**Landed in qn.4a.** The six fixtures below exist now. **Honesty note (D9):** the lab's
live runs were *fragmentary* — repeated Wi-Fi tears (`Heartbeat(SleepyTime)`) meant no
single clean full-success run was captured end-to-end. So each `.txt` is a canonical
reconstruction built **only from real output lines observed in the lab log**
(`local/chatgpt-original-idea-chat.md`), not one verbatim capture. Grounding the *parser*
in the real line vocabulary is the point. **No scrubbing of PII was needed inside the
transcripts** because `idevicebackup2`'s stdout carries no UDID, device name, or path —
those appeared only in shell prompts and `netmuxd` logs, which are not part of these
captures. Tests supply a synthetic UDID out of band.

### `*.meta.json` schema (consumed by the fake replayer in `backup_test` helpers)

| field | meaning |
| --- | --- |
| `transport` | `usb` \| `wifi` — which muxer the run used |
| `terminal_state` | expected `Job.state` at the end (`succeeded` / `connection_lost` / …) |
| `exit_code` | process exit code; `-1` = the engine kills it (torn/hang) — it never exits itself |
| `encrypted`, `kind` | what the written tree's `Manifest.plist`/`Status.plist` should say |
| `tree` | `complete` (valid MobileBackup2 tree → passes `storage.Verify`) \| `torn` \| `none` |
| `line_delay_ms` | base delay between emitted lines |
| `stall_after_line` | 1-based line index after which to inject a silence (0 = none) |
| `stall_ms`, `stall_churns_tree` | the silence length, and whether the tree keeps changing during it (the `silent-stall` vs `wifi-torn-session` discriminator) |
| `hang_after_last` | after the last line, block until killed (the torn-transport freeze) |
| `note` | provenance + what the fixture exercises |

The six: `full-usb-success`, `wifi-incremental-success`, `waiting-for-passcode`,
`wifi-torn-session`, `silent-stall`, `encryption-changed`. Every new bug found on the lab
box later becomes another fixture here **before** it is fixed (hard rule).
