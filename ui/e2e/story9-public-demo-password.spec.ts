import { expect, test } from "@playwright/test";

// quince#534: the public demo's password exists as TWO independent constants —
// `core/cmd/quince/main.go`'s `demoPassword` and `ui/src/pages/LoginPage.tsx`'s `DEMO_PASSWORD` —
// and nothing connected them. Change one and the login screen confidently states a credential that
// does not work, with every gate green. The visitor who finds out is a stranger who concludes the
// product is broken.
//
// THE DUPLICATION IS CORRECT AND STAYS. Serving the password from `/api/health` would put a
// credential on an `authExempt` endpoint, on the one instance deliberately exposed to the internet
// — worse than the drift it would prevent (quince#532's ruling). Only the mode crosses the wire.
//
// SO THE GUARD IS BEHAVIOURAL, NOT STRING EQUALITY. This reads the password off the RENDERED
// screen and logs in with exactly that string. An equality assertion between the two constants
// would be two declarations agreeing with each other and neither agreeing with the product — the
// failure mode this repo hit twice on 2026-08-02 (quince#521, quince#525).
//
// It runs against a SECOND app container in `--public-demo` mode, because the shared e2e server
// runs `--demo` and story 4 exists to keep it that way.

const PUBLIC_DEMO_URL = process.env.PUBLIC_DEMO_URL;

// REFUSE rather than skip. A `test.skip()` here would render green while asserting nothing, which
// is exactly the shape quince#41 removed from the privacy gate and quince#531 from the ladder: an
// unrun check must be loud, never a silent pass. The Makefile always supplies this.
test.beforeAll(() => {
  expect(
    PUBLIC_DEMO_URL,
    "PUBLIC_DEMO_URL is unset — the --public-demo fixture did not start, so this guard did NOT run. " +
      "It refuses instead of skipping, because a green tick over an unrun check is worse than a failure.",
  ).toBeTruthy();
});

test("the password shown on the public demo's login screen actually works", async ({ page }) => {
  await page.goto(`${PUBLIC_DEMO_URL}/`);

  // The mode itself: `--public-demo` presets the password, so setup is already closed and a visitor
  // lands on login. Landing on /setup would mean the preset never happened — a different defect,
  // worth failing distinctly rather than being swallowed by the password assertion below.
  await page.waitForURL(/\/(setup|login|devices|$)/);
  expect(page.url(), "public-demo must start at login, not setup — the password preset failed").not.toContain(
    "/setup",
  );

  // Read it OFF THE SCREEN. This is the whole point: not the TS constant, not the Go constant, but
  // the string a visitor can actually see and type.
  const shown = (await page.getByTestId("demo-password").innerText()).trim();
  expect(shown, "the login screen states no password in public-demo mode").not.toEqual("");

  await page.getByLabel("Password").fill(shown);
  await page.getByRole("button", { name: /sign in/i }).click();

  // Asserting the HEADING rather than the URL, matching stories 1–4: this label has been renamed
  // once already (`/devices` → `/`, quince#443) and the heading survived it.
  await expect(
    page.getByRole("heading", { name: "Home", level: 1 }),
    `the login screen shows "${shown}" and that password did not work — the two constants have drifted`,
  ).toBeVisible();
});
