import { defineConfig, devices } from "@playwright/test";

// Playwright drives `quince serve --demo` (BASE_URL points at the app container in the
// gates-ui-e2e target; defaults to the dev proxy for local runs). Single worker: the demo
// server holds shared state (setup runs once), so tests stay deterministic and ordered.
export default defineConfig({
  testDir: "./e2e",
  fullyParallel: false,
  workers: 1,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: "list",
  timeout: 90_000,
  use: {
    baseURL: process.env.BASE_URL ?? "http://localhost:8080",
    trace: "on-first-retry",
  },
  projects: [{ name: "chromium", use: { ...devices["Desktop Chrome"] } }],
});
