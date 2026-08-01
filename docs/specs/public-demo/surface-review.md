# Public-demo surface review

**A route-by-route disposition of everything reachable with a published password.** Ruled to come
FIRST, ahead of the mode itself, on 2026-08-01 (quince#444): *"Survey every route reachable with the
published password before the mode is built around assumptions the review might invalidate … its
output should be a route-by-route disposition rather than a verdict."*

## What this is, and what it is not

It is a read of the `httpapi` surface plus a decision per route, **measured against a running
instance**. quince#444 closes by saying *"Nothing here is measured against a deployed instance"*;
everything below with a number in it now is.

It is **not** a statement that quince is safe to expose generally, and it is not a security audit of
the product. It answers one question — *what can a stranger who knows the password do to this
instance, and what does that cost the host* — for `serve --demo` as it exists at the commit this
document lands on.

**Three findings must be fixed before exposure.** Two of them are not demo-specific and one of them
is sharper on the shipping product than on the demo. That inversion is the most useful thing this
review produced, and it is the reason the ruling put the review first.

## How it was measured

`make demo` on a session box (image `quince:local-r9`, `serve --demo`, `QUINCE_DATA=QUINCE_CACHE=/tmp`),
probed over loopback. Process RSS read from `/proc/<pid>/status` where `<pid>` came from
`nerdctl inspect --format '{{.State.Pid}}'` — **not** from a `ps` grep, which on a box running several
runners' demo containers silently matches somebody else's process and reports numbers that never move.
That happened here on the first attempt and is recorded because it is the failure a reader would
otherwise repeat.

## The boundary itself holds

Worth stating plainly before the findings, because it is what makes the rest tractable. Every route
except the four deliberate exemptions refuses a request with no session, the WebSocket refuses on
*both* axes, and CSRF is enforced on mutations:

```
GET  /api/config                       no-session=401   session=200
GET  /api/devices                      no-session=401   session=200
POST /api/devices/{udid}/wifi-sync     no-session=401   session=202
POST /api/storages/{id}/recheck        no-session=401   session=404
GET  /api/ws        (Origin ok)        no-session=401
GET  /api/ws        (Origin missing)   session=403
POST /api/devices/{udid}/pair          session, no X-CSRF-Token → 403
```

So the exposure question is not *"does auth work"*. It is the one the ruling framed: **a preset public
password makes every authenticated endpoint effectively unauthenticated**, which moves the whole
weight onto (a) what the command surface does once you are through, and (b) the four routes that were
never behind it.

The four pre-auth routes are `GET /api/health`, `GET /api/auth/status`, `POST /api/auth/login` and
`POST /api/auth/setup`. **Finding 1 is in that set**, and it is the one route among them that a public
demo makes permanently useless and permanently expensive.

## Finding 1 — `POST /api/auth/setup` is an unauthenticated, un-rate-limited 64 MiB amplifier

`auth.Service.SetPassword` (`core/internal/auth/service.go:106`) derives the argon2id hash **before**
`SetSettingIfAbsent` tells it a password already exists. On a configured instance every request
therefore pays the full derivation — `memory: 64 MiB, iterations: 3, parallelism: 2`
(`core/internal/auth/argon2.go:25`) — to earn a `409` that was guaranteed before the work started.

The per-IP limiter lives in `Login`, not here. This route has **no rate limit at all**, and it is both
`authExempt` and `csrfExempt` (`core/internal/httpapi/middleware.go:75,118`).

Cost of the guaranteed-failure path, against the malformed-body path that is rejected *before* hashing:

| path | outcome | time |
| --- | --- | --- |
| `{"password":"x"}` on a configured instance | 409, hash computed and discarded | 79–99 ms |
| `{` (malformed) | 400, rejected pre-hash | 0.40–0.54 ms |

**~200× on time. On memory it is far worse.** Fresh container, one legitimate setup, then bursts of
concurrent requests that are all guaranteed 409s:

| after | RSS |
| --- | --- |
| at rest | 9 MB |
| the one legitimate setup (200) | 139 MB |
| + 4 concurrent guaranteed-409 setups | 267 MB |
| + 8 | 524 MB |
| + 16 | 1166 MB |
| + 32 | 2063 MB |
| 30 s idle afterwards | **2063 MB — not returned** |

RSS tracks **peak concurrency × 64 MiB**, which is the argon2 `memory` parameter exactly. Sixty
requests of ~100 bytes each took a 9 MB process to 2 GB. Nothing bounds the concurrency, so nothing
bounds the footprint.

**The proposed mode makes this strictly worse.** Presetting the password means `HasPassword()` is true
from startup, so **100% of traffic to this route takes the throw-away path** — the endpoint becomes a
pure remote amplifier with no legitimate caller left alive.

**And it is not a demo problem.** The same route, the same absent rate limit, and the same 64 MiB is
in the shipping product, reachable pre-auth from the LAN by anything that can open a socket. quince is
meant to fly on a low-end NAS; at 524 MB for eight concurrent requests, **a burst smaller than a
browser's default connection pool exhausts a 512 MB box**. The demo is what made this visible, not
what makes it dangerous.

- **Disposition: MUST FIX before exposure — quince#463.** Hash after the existence check, not before, and
  rate-limit the route. Both are cheap and neither is a design question. Filed separately.

## Finding 2 — the login limiter buckets on `RemoteAddr`, so behind a proxy one visitor locks out everybody

`clientIP` (`core/internal/httpapi/server.go:166`) reads `r.RemoteAddr` and nothing else. A public
HTTPS host implies a reverse proxy, and behind one **every visitor on the internet shares one bucket**.
Twelve wrong-password logins, each claiming a different client via `X-Forwarded-For`:

```
attempt 1..10   X-Forwarded-For: 203.0.113.N   → 401
attempt 11      X-Forwarded-For: 203.0.113.11  → 429
attempt 12      X-Forwarded-For: 203.0.113.12  → 429
CORRECT password, X-Forwarded-For: 198.51.100.7 → 429
```

That last line is the finding. It fails in **both** directions at once: there is no per-attacker
granularity, *and* any one visitor spending ten guesses in a minute denies the login route to every
other visitor for the rest of the window. **A demo nobody can log into is a broken demo**, and the
cost of breaking it is ten HTTP requests.

Canon already asks for the missing half and it is unbuilt — design §6: *"reverse-proxy trust headers
only from configured addresses"*. Note the asymmetry this leaves today: `secureCookie`
(`core/internal/auth/cookie.go:71`) **does** honour `X-Forwarded-Proto` from any sender, so the code
already assumes a proxy may sit in front while the limiter assumes one does not. The cookie side is
benign — that header only ever upgrades to `Secure` — but the two halves disagree about the same
deployment.

- **Disposition: MUST FIX before exposure — quince#464** *if* the instance sits behind a proxy, which the HTTPS
  requirement effectively decides. Needs a ruling, because "trust `X-Forwarded-For`" is only safe from
  configured addresses and that configuration does not exist yet — which is the §6 line above.

## Finding 3 — the three op routes have no single-flight and never evict

`POST /api/devices/{udid}/pair`, `/encryption` and `/wifi-sync` each spawn a goroutine per request
(`core/internal/demo/deviceops.go:46,97`) and insert into `Provider.ops`, a map **nothing ever deletes
from** — `delete(p.ops, …)` does not exist anywhere in the package.

Twenty pair requests against one device, back to back, contrasted with the route everybody expects to
be the problem:

```
POST /api/devices/{udid}/pair  x20 → 20/20 accepted, 20 distinct op_ids, 0 refused
POST /api/jobs                 #1  → queued
POST /api/jobs                 #2  → REFUSED conflict: a backup is already running for this device
POST /api/jobs                 #3  → REFUSED conflict: a backup is already running for this device
GET  /api/ops/{first op_id}, after all 20 finished → {"state":"succeeded","kind":"pair"}
```

`POST /api/jobs` is correctly single-flighted per UDID and is fine. **The op routes are the unbounded
ones**, and the last line shows the retention: an op from the very first request is still resident
after every op has completed. One request in, one permanent map entry plus three bus publishes — each
of which fans out to every connected WebSocket.

This is the *"unbounded job spawning"* the ruling names as unacceptable, found in the place the ruling
did not look. It is bounded only by the periodic restart, which is precisely the bound the ruling says
does not count: *"a periodic reset does not bound either"*.

- **Disposition: MUST FIX before exposure — quince#465.** Cheapest correct shape is the one already in the codebase
  next door — the per-UDID single-flight `StartBackup` uses. Filed separately.

## Route-by-route disposition

`safe` — reachable, cheap, discloses nothing an unauthenticated visitor should not see.
`accept` — the reset genuinely bounds it, and the named cost is accepted.
`MUST FIX` — the reset does not bound it.

| route | in demo | disposition |
| --- | --- | --- |
| `GET /api/health` | 200, pre-auth | **safe.** Discloses `version` and an empty muxer list. Version disclosure on a public host is a real if minor cost; it is also honest, and health is meant to be probeable. Accepted deliberately rather than by omission. |
| `GET /api/auth/status` | 200, pre-auth | **safe.** Mints a stateless double-submit CSRF token; nothing is stored server-side, so repeat calls do not grow anything. |
| `POST /api/auth/setup` | 409 once configured | **MUST FIX — finding 1** (quince#463). |
| `POST /api/auth/login` | 401/429 | **MUST FIX — finding 2** (quince#464). The limiter is correctly placed *before* the argon2 verify, which is why login is not finding 1. |
| `POST /api/auth/logout` | 204 | **safe.** Deletes one session row. |
| `GET /api/config` | 200 | **safe in demo** — serves throwaway `demo-config.yml`, and D12 forbids secrets in the file by construction. On a real deploy it discloses storage roots and filesystem layout; out of scope here, worth a line in the mode's own spec. |
| `PUT /api/config` | 200/422 | **accept.** Full-document replace, body capped at 1 MiB (`middleware.go:12`), written via `AtomicWrite` (temp + rename, no partial file, no temp litter). In demo the config drives nothing live — the demo provider never reads it — so a vandalised config is cosmetic until restart, and restart is the reset. |
| `GET /api/devices`, `GET /api/devices/{udid}` | 200/404 | **safe.** Fixture data. |
| `POST /api/devices/rescan` | 409 | **safe.** `UnmanagedMuxer` refuses; quince owns no muxer in demo. |
| `POST /api/devices/{udid}/pair` | 202 | **MUST FIX — finding 3** (quince#465). |
| `POST /api/devices/{udid}/encryption` | 202 | **MUST FIX — finding 3** (quince#465). Note it also accepts a `password` field; in demo it is validated for presence and discarded, never stored or logged. |
| `POST /api/devices/{udid}/wifi-sync` | 202 | **MUST FIX — finding 3** (quince#465). |
| `POST /api/devices/{udid}/pair/validate` | 200 | **safe.** Pure read of fixture state. |
| `POST /api/devices/{udid}/reset-working` | 503 | **safe.** No engine is wired in demo; refuses honestly. |
| `GET /api/ops/{op_id}` | 200/404 | **safe** to read; the growth is on the write side, finding 3. |
| `POST /api/jobs` | 202/409 | **accept.** Per-UDID single-flight holds (measured above), so concurrency is capped by the fixture device count. Each completed job appends a job, a version and log lines to maps that are never pruned — bounded by the restart, and small. |
| `POST /api/jobs/{id}/cancel` | 202/409/404 | **safe.** |
| `GET /api/jobs`, `/{id}`, `/{id}/log` | 200/404 | **safe.** `limit` is clamped to 200 (`handlers_read.go:13`); a garbage or negative `limit` falls back to the default rather than being honoured. |
| `GET /api/storages`, `POST /api/storages/{id}/recheck` | 200/404 | **safe.** Recheck is a stat, creates nothing, selects nothing. |
| `GET /api/versions` | 200 | **safe.** |
| `DELETE /api/versions/{id}` | 202 | **accept — and this is the one the reset exists for.** Any visitor can delete every version; the next visitor sees a gutted product until the timer fires. That is exactly the vandalism quince#444 designs the reset around, and the acceptance is deliberate. |
| `/api/…` (unmatched) | 404 | **safe.** |
| `GET /api/ws` | 101 with session + Origin | **accept, with a named cost.** Auth and strict same-origin both enforced pre-upgrade, and a slow subscriber is dropped rather than blocking the publisher. But **nothing caps concurrent connections**: each is 2 goroutines plus a 64-envelope buffer, and `Publish` walks every subscriber under a read lock, so per-event cost is O(subscribers). Not measured; called out rather than dispositioned clean. |
| `GET /` (UI) | 200 | **safe.** Embedded `embed.FS`, no filesystem path reaches it. |

## Two things the reset does not bound, beyond the findings

- **Idle connections never expire.** `main.go:194` sets `ReadHeaderTimeout` only. Checked against the
  pinned toolchain's own source rather than remembered — `golang:1.26.5-alpine3.24`,
  `net/http/server.go:3011`: *"IdleTimeout … If zero, the value of ReadTimeout is used. If negative, or
  if zero and ReadTimeout is zero or negative, there is no timeout."* Both are zero here, so there is
  no idle timeout, no whole-request read timeout and no write timeout. `ReadHeaderTimeout` covers
  classic Slowloris; a slow *body* and an idle keep-alive are uncovered. Filed as quince#466.
- **`auth_sessions` and `audit` are never pruned.** Every login and every *failed* login appends an
  audit row, and a session row is collected only if its own cookie is presented again. In `--demo`
  this is genuinely bounded — the DB is thrown away at start and exit (`removeDemoState`) — so it is
  listed as a property the mode must *keep*, not as a finding. A public-demo mode that made state
  durable would turn it into one.

## What this review did NOT establish

- **Nothing about a real deploy.** Everything was measured against `serve --demo` over loopback, with
  no reverse proxy in front. Finding 2's severity is *predicted* for a proxied deployment and
  demonstrated only in the form the code makes inevitable (`RemoteAddr` bucketing, shown above).
- **The WebSocket connection ceiling is unmeasured.** The absence of a cap is read from the source;
  what it costs at N connections is not known.
- **No fix is proposed as ruled.** Each `MUST FIX` is a defect statement with a suggested shape, filed
  as its own issue. Finding 2 in particular needs a ruling before code, because trusting
  `X-Forwarded-For` safely requires the configured-proxy-addresses mechanism design §6 asks for and
  the codebase does not have.
- **The findings are not claimed to be exhaustive.** They are what a route-by-route read plus targeted
  probing surfaced. This is a first review of a surface that has never faced the public internet.

## What was filed

No fix lands in this document. Each finding is a defect statement with a suggested shape, filed on its
own so it can be ruled, scheduled and reviewed independently of the mode:

| issue | finding |
| --- | --- |
| quince#463 | `POST /api/auth/setup` hashes before the existence check, un-rate-limited and pre-auth |
| quince#464 | the login limiter buckets on `RemoteAddr`; design §6's trusted-proxy mechanism is absent — **needs a ruling before code** |
| quince#465 | `pair` / `encryption` / `wifi-sync` have no single-flight and never evict |
| quince#466 | the HTTP server sets only `ReadHeaderTimeout` |

**quince#463 and quince#466 are not blocked on anything.** quince#464 is, and deliberately: trusting
`X-Forwarded-For` unconditionally would be *worse* than today, because any client could then mint
unlimited buckets by varying the header and delete the rate limit entirely.

**quince#465 carries an unanswered question worth more than the finding.** The real, non-demo
`deviceops.Manager` was not read here, so whether it shares the shape is unknown. If it does, the
severity is well above a demo bug.
