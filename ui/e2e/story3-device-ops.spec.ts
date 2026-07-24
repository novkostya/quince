import { expect, test, type Page } from "@playwright/test";

// Authenticate via setup or login, whichever the shared demo server asks for (same pattern as
// stories 1–2).
async function authenticate(page: Page): Promise<void> {
  await page.goto("/");
  await page.waitForURL(/\/(setup|login|devices)/);
  if (page.url().includes("/setup")) {
    await page.getByLabel("Password").fill("demo");
    await page.getByRole("button", { name: /set password/i }).click();
  } else if (page.url().includes("/login")) {
    await page.getByLabel("Password").fill("demo");
    await page.getByRole("button", { name: /sign in/i }).click();
  }
  await expect(page).toHaveURL(/\/devices/);
}

// Story 3 (spec qn.3): a device op (encryption change) runs through the assisted lifecycle in
// the UI — POST → op.updated narration (waiting for the on-device confirm) → succeeded — with
// no hardware, proving the pair/encryption op wiring end to end against --demo.
test("encryption op narrates the assisted flow to success", async ({ page }) => {
  await authenticate(page);

  // The demo phone is paired with encryption on → "Manage encryption" opens in change mode.
  await page.getByRole("link", { name: "family-iphone" }).click();
  await expect(page).toHaveURL(/\/devices\//);

  await page.getByRole("button", { name: /manage encryption/i }).click();
  await page.getByLabel("Current password", { exact: true }).fill("demo-current");
  await page.getByLabel("New password", { exact: true }).fill("demo-next");
  await page.getByLabel("Confirm new password", { exact: true }).fill("demo-next");
  await page.getByRole("button", { name: /change backup password/i }).click();

  // The op narrates the on-device passcode confirm, then reaches success.
  await expect(page.getByText(/confirm the change on the device/i)).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole("button", { name: /done/i })).toBeVisible({ timeout: 10_000 });
});
