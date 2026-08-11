import { expect, test } from "@playwright/test";

// Story 1 (spec qn.1): fresh start → set password → shell with live demo devices
// appearing; reload keeps the session. Runs first (fresh demo server = needs_setup).
test("set password, land in the shell, devices appear, reload keeps session, sign out ends it", async ({
  page,
}) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/setup/);

  // THE CREDENTIAL ANCHOR, IN A REAL BROWSER — quince#819. quince asks for two different passwords
  // on one origin; without a username on each, a password manager files them as one entry. Asserted
  // here as well as in jsdom because the attribute is only worth anything to an actual browser's
  // autofill machinery, and because this is the surface a user meets first.
  const anchor = page.getByLabel("Username", { exact: true });
  await expect(anchor).toHaveValue("quince-admin");
  await expect(anchor).toHaveAttribute("autocomplete", "username");

  // THE ANCHOR MUST NOT TAKE THE FOCUS THE PASSWORD WANTS — quince#824. Asserted in a real browser
  // because that is where `autoFocus` actually runs; jsdom pins the attributes, this pins the
  // outcome. Measured on both engines before the fix: `focused=input#password` in Chromium and
  // WebKit, with the read-only anchor sitting at `tabindex=0` ahead of it.
  //
  // ON iOS THIS ASSERTION IS STILL TRUE AND THE KEYBOARD STILL WILL NOT OPEN, which is not a
  // regression to hunt: iOS declines to raise the keyboard for a focus that did not come from a
  // user gesture. Do not add timers or extra `.focus()` calls trying to beat it.
  await expect(page.locator("#password")).toBeFocused();
  await expect(anchor).toHaveAttribute("tabindex", "-1");

  await page.getByLabel("Password").fill("demo");
  await page.getByRole("button", { name: /set password/i }).click();

  // Home is `/` since qn.6d (quince#443); `/devices` redirects to it. Asserting the HEADING
  // rather than the URL keeps this stable across the next rename — this label has already
  // had one.
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: "family-iphone" })).toBeVisible();
  // the Wi-Fi iPad churns in on the demo's ~20s presence timer
  await expect(page.getByRole("link", { name: "studio-ipad" })).toBeVisible({ timeout: 30_000 });

  await page.reload();
  // Home is `/` since qn.6d (quince#443); `/devices` redirects to it. Asserting the HEADING
  // rather than the URL keeps this stable across the next rename — this label has already
  // had one.
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
  await expect(page.getByRole("link", { name: "family-iphone" })).toBeVisible();

  // SIGN OUT — qn.6m slice 2, G10. Here rather than in a spec of its own because this is the one
  // story that already owns the whole session lifecycle: it sets the password, lands in the shell,
  // and proves a reload KEEPS the session. Ending it by proving a click ENDS the session closes the
  // pair, and costs no extra server round trip.
  //
  // Leaving this test signed out is safe: every other spec authenticates itself against the shared
  // demo server via its own setup-or-login helper, so none of them inherits a session from here.
  await page.getByRole("button", { name: "Sign out" }).click();
  await expect(page).toHaveURL(/\/login/);

  // THE RELOAD IS THE ASSERTION THAT MATTERS, and without it this test would pass over a sign-out
  // that only changed the URL. The client cache is gone either way; only a reload asks the SERVER
  // whether the session cookie still works, which is the thing the button claims to have ended.
  await page.reload();
  await expect(page).toHaveURL(/\/login/);
  await expect(page.getByLabel("Password")).toBeVisible();
});
