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

Plain text, exactly as the tool emitted it (byte-for-byte after scrubbing), one run per
file. A companion `*.meta.json` (added in qn.4) records expected terminal state, timing
hints for the replayer, and the scrub map. No such files exist yet — this rung (qn.0)
only lays down this task description.
