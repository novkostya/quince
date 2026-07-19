# qn.1 — core daemon skeleton, demo mode, UI shell

**Goal.** The daemon becomes a real app frame: config, persistence, auth, the event bus,
the WebSocket, and a `--demo` mode rich enough that the UI track can build every screen
against fixtures with no hardware.

## Boundary

In scope: `core/` (config, db, auth, bus, http, ws, demo fixtures), `ui/` (auth flow, ws
client model, Dashboard/device-details/Settings page scaffolds bound to demo data). Out
of scope:
any muxd or subprocess integration (qn.2/qn.3), real jobs (qn.4). Contracts are consumed,
not changed; gaps found here → contract-change note in the rung report.

## Design

- Config, staged per stack D12: bootstrap env (contracts §6) parsed once into a typed
  struct; startup validates dirs exist/writable; unknown `QUINCE_*` vars warn. The
  `config.yml` core: schema v0 + defaults, validation, atomic canonical write,
  `GET/PUT /api/config`, `quince config validate` CLI, restart-required semantics for
  now (hand-edits read at startup; file-watch live reload + generated doc-comments are
  qn.6).
- Web security baseline per design §6 (non-negotiable from this rung): CSRF, WS Origin
  validation, cookie flags + session rotation, CSP/frame denial, login rate limit, idle
  timeout, audit-trail table.
- SQLite via modernc, embedded migrations (plain SQL files, sequential); tables this
  rung: `settings`, `sessions_auth`. Domain tables land with their rungs.
- Auth: first-run setup flow (no password set → UI shows set-password screen; argon2id),
  cookie session, rate-limited login. `--demo` seeds password `demo`.
- Event bus: typed publish/subscribe; WS handler per contracts §3 (envelope, `hello`,
  per-client buffered fan-out, drop-on-slow).
- Demo mode: `quince serve --demo` uses an in-memory provider emitting fixture devices
  (attach/detach on a timer), a scripted fake job with progress + log chunks, and fixture
  versions — exercising every WS event type end to end. Fixture data presentable
  (screenshots come from here).
- UI: ws bridge with reconnect+refresh semantics (contracts §3) feeding Zustand feature
  stores; pages render demo data live; Tailwind v4 theme + tokens.css first pass
  (light+dark); first vendored components (stack D7).

## Stories

1. Fresh start → set password → login → shell with live demo devices appearing/vanishing;
   reload keeps session.
2. `job.updated` + `job.log` demo stream renders as a live job card with progress and a
   tailing log pane; WS disconnect (kill server) → UI shows reconnecting, recovers state
   on restart.
3. `GET /api/devices|jobs|versions` serve demo data per contracts (shapes
   golden-tested against `contracts.md` examples).
4. Unauthenticated API/WS access is rejected; login is rate-limited (test).
5. Race-clean: bus + ws fan-out under `go test -race` stress test (N publishers, M slow
   clients).
6. Config round-trip: `PUT /api/config` → file rewritten canonically; hand-edit a value
   → visible in `GET /api/config` after restart; hand-edit garbage → startup keeps
   last-good + surfaces the bad key, `quince config validate` exits nonzero (tests
   cover all three).
7. Security baseline verified by tests: CSRF token required on mutations, WS handshake
   rejected on foreign Origin, cookies carry the required flags, audit rows written for
   login.

## Gates

- `make gates` + demo click-through: `quince serve --demo` → every story demonstrable in
  a browser; Playwright covers stories 1–2 headlessly against demo mode.
- Golden contract tests fail on any wire-shape drift from `contracts.md`.

## Fixtures

`core/internal/demo/` fixture set: 2 devices (one USB+WiFi, one WiFi-only), 1 scripted
backup job (with a silent-stall phase and a recovery, mirroring lab reality), 3 versions
across backends (zfs/hardlink with honest metadata, one adopted with `job_id: null`).

## Rung-ruled decisions (qn.1 — canon within this rung's boundary)

Settled during the build; a later rung changes them only via the gap protocol.

**Operator-ruled (2026-07-19):**
- **Auth contract additions** (contracts §1): `GET /api/auth/status → {state, csrf_token}`,
  `POST /api/auth/setup {password}` (409 if already configured — setup succeeds exactly
  once, never an unauthenticated reset), CSRF via a readable `quince_csrf` cookie echoed in
  `X-CSRF-Token` (double-submit). Setup auto-logs-in.
- **`react-router-dom` adopted** (extends stack D7, which named no router): deep-linkable
  `/devices/:udid`, auth-gated route boundaries, sets up the parked qn.12 phone-first entry.

**Library choices** (all looked up live at pin time — versions in `core/go.sum` /
`ui/pnpm-lock.yaml`): core — `gopkg.in/yaml.v3`, `modernc.org/sqlite` (D8), `coder/websocket`,
`golang.org/x/crypto/argon2` (PHC hand-rolled), `oklog/ulid/v2`, hand-rolled double-submit
CSRF (no dep); UI — `zustand`, `@tanstack/react-query`, Radix primitives + `cva`/`clsx`/
`tailwind-merge` (per D7). **Deferred:** `@tanstack/react-virtual` (no unbounded list this
rung; the job log is a capped ring buffer).

**Other rung-local calls:**
- **Migrations** = `embed.FS` of numbered `.sql` + a ~40-line forward-only runner over a
  `schema_migrations` table (no goose/golang-migrate dep).
- **Secure-cookie rule** (security baseline): `Secure` unless the request is loopback-http;
  **forced off in `--demo`** so login works over the plain-http e2e/localhost address.
- **Admin-session timeouts hardcoded** (idle 12h, absolute 30d) — schema v0 has no key
  (`sessions.ttl_minutes` is the vault-unlock TTL). A future `auth:` config section is noted
  for qn.6.
- **slog** JSON by default, text on a TTY (design §10).
- **Config wire form**: exchanged as structured JSON — `GET` returns `{config, warnings[],
  source}`, `PUT` takes the bare `Config` (full-document replace); the exact-YAML-with-
  comments "current file" view is a qn.6 refinement (D12 staging).
- **Demo** uses a fresh throwaway store per run under `QUINCE_CACHE` → starts at
  `needs_setup` (exercises story 1); the canonical demo password is `demo`, entered at setup.
- **Golden contract tests** live in `core/internal/httpapi/testdata/*.json`, regenerated
  with `make gen-golden` (`UPDATE_GOLDEN=1`) and verified by eye against contracts §2.
- **Playwright e2e** runs as two containers (app `serve --demo` + the official Playwright
  runner) on a shared nerdctl network, the runner using an isolated node_modules volume so
  the alpine/musl gate install is never reused under glibc — `make gates-ui-e2e`, CI job `e2e`.
- **CSP** allows `style-src 'unsafe-inline'` (React style attributes) and `connect-src`
  `ws:`/`wss:` for the socket; `script-src` stays `'self'`.
