import { defineConfig, devices } from "@playwright/test";

// Playwright drives `quince serve --demo` (BASE_URL points at the app container in the
// gates-ui-e2e target; defaults to the dev proxy for local runs). Single worker: the demo
// server holds shared state (setup runs once), so tests stay deterministic and ordered.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  // NO RETRIES, BECAUSE A RETRY HERE CANNOT PASS AND ITS OUTPUT IS WORSE THAN SILENCE (quince#948).
  //
  // A retry is only meaningful against a test that starts from the state it started from the first
  // time. These do not: one worker drives ONE shared `serve --demo` container, in file order, and
  // the specs mutate it — story1 completes first-run setup, story4/story5 start backups that create
  // versions. Playwright re-runs the spec; it does not re-run the world.
  //
  // So story1's retry is not a second attempt at the same thing. The first attempt claimed the
  // install, so the retry starts at `/login` and can never reach `/setup`:
  //
  //   Retry #1: Expected pattern: /\/setup/
  //             Received string:  "http://…:8968/login?next=%2F"
  //
  // That is a DIFFERENT and more misleading error than the original — it names the last thing that
  // failed rather than the thing that broke, and reading the run bottom-up sends you at first-run
  // setup when the actual failure was a device that never appeared. It cost exactly that on
  // quince#945, which changes nothing near either.
  //
  // The cost of removing it is real and is accepted: a genuine flake now reddens a PR instead of
  // being absorbed. That is the trade this project already takes everywhere else — canon says to
  // classify a red as infrastructure, a known flake WITH AN ISSUE, or real, and a retry that
  // silently converts the second into a green run is the one outcome that leaves nothing to
  // classify.
  retries: 0,
  reporter: "list",
  timeout: 90_000,
  use: {
    baseURL: process.env.BASE_URL ?? "http://localhost:8968",
    // `on-first-retry` cannot fire when there is no retry, so it would be config that is
    // structurally dead rather than merely rarely reached. NOTE, because this is not the
    // improvement it looks like: `gates-ui-e2e` mounts only node_modules and the pnpm store, and
    // removes both containers when the run ends, so a trace written here goes with them. This makes
    // the setting honest, not the artifact reachable — retrieving one is separate work.
    trace: "retain-on-failure",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
