import { expect, test } from "@playwright/test";

// Story 1 (spec qn.1): fresh start → set password → shell with live demo devices
// appearing; reload keeps the session. Runs first (fresh demo server = needs_setup).
test("set password, land in the shell, devices appear, reload keeps session", async ({ page }) => {
  await page.goto("/");
  await expect(page).toHaveURL(/\/setup/);

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
});
