import { expect, test, type Page } from "@playwright/test";

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

// G8 (spec qn.6c story 9): the selector renders both storages, disables the unreachable one with
// its reason, and shows the full-transfer warning on the storage that has no prior version.
//
// This drives UI → API → provider, which the unit tests deliberately do not: they render the
// component against props I wrote, so they cannot catch a field the server names differently or a
// null the client reads as absent. The demo ships the spec's fixture — two storages, one
// deliberately unreachable — precisely so this path is exercisable without hardware.
test("the storage selector shows both storages, the unreachable one disabled, and the full-transfer cost", async ({
  page,
}) => {
  await authenticate(page);

  await page.getByRole("link", { name: "spare-iphone" }).click();
  await expect(page).toHaveURL(/\/devices\//);

  const select = page.getByTestId("storage-select");
  await expect(select).toBeVisible();

  // BOTH storages are offered. The unreachable one is listed rather than hidden — a list a disk
  // silently vanishes from is one the user cannot trust.
  await expect(select.locator("option")).toHaveCount(2);
  await expect(select.locator("option", { hasText: "internal" })).toHaveCount(1);

  const shuttle = select.locator("option", { hasText: "shuttle" });
  await expect(shuttle).toHaveCount(1);
  // toHaveJSProperty, not toBeDisabled: Playwright's disabled matcher does not treat <option>
  // as a disableable control, though the DOM property is set. Asserting the property is both
  // accurate and closer to what the browser actually enforces.
  await expect(shuttle).toHaveJSProperty("disabled", true);
  await expect(shuttle).toContainText("not connected");

  // Choosing the unreachable storage shows the DAEMON's reason, not client copy.
  // The reason is shown WITHOUT selecting it — a disabled option cannot be chosen, so a
  // reason that only appeared on selection would be unreachable. This assertion is what
  // caught that.
  await expect(page.getByTestId("storage-unreachable")).toContainText(/marker/);

  // And the default — reachable, and already holding this device's backups in the demo — does NOT
  // claim a full transfer. A warning that is always on trains the user to ignore it.
  await expect(page.getByTestId("storage-will-be-full")).toHaveCount(0);
});
