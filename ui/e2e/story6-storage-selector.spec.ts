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

// quince#459 — the Operator's ruling was "plug the disk in and press the button", the endpoint
// shipped in quince#445, and nothing pressed it. This drives the button against the REAL API,
// which is the only thing that proves it is wired: a unit test asserts the component calls a
// function I wrote, and the round trip is what the issue was actually about.
//
// The demo's `shuttle` is deliberately and permanently unreachable, so a successful press CANNOT
// make it appear. That is the useful case rather than a limitation: it pins that a re-check which
// changes nothing leaves the row saying exactly what it said before — not blanked, not cleared,
// not silently "fine now".
test("re-check reaches the daemon for one storage and leaves an unreachable row honest", async ({
  page,
}) => {
  await authenticate(page);
  await page.getByRole("link", { name: "spare-iphone" }).click();
  await expect(page).toHaveURL(/\/devices\//);

  await expect(page.getByTestId("storage-select")).toBeVisible();

  // ONE button, on the one unreachable row — not one per storage.
  const button = page.getByTestId("storage-recheck");
  await expect(button).toHaveCount(1);

  const recheck = page.waitForResponse(
    (r) => /\/api\/storages\/[^/]+\/recheck$/.test(r.url()) && r.request().method() === "POST",
  );
  const reload = page.waitForResponse(
    (r) => /\/api\/storages\?udid=/.test(r.url()) && r.request().method() === "GET",
  );

  await button.click();

  // The press reaches the daemon, and it answers 200 for a storage that is declared.
  expect((await recheck).status()).toBe(200);

  // AND the device-scoped list is refetched rather than the 200 {storage} being spliced in.
  // recheck is device-independent, so its will_be_full is null; splicing would drop the
  // full-transfer warning at the moment a returning disk made it true.
  expect((await reload).status()).toBe(200);

  // The disk is still out, and the row still says so — with the daemon's own sentence.
  await expect(page.getByTestId("storage-unreachable")).toContainText(/marker/);
  await expect(page.getByTestId("storage-recheck-failed")).toHaveCount(0);
  await expect(button).toBeEnabled();
});
