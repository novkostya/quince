import { expect, test, type Page } from "@playwright/test";

async function authenticate(page: Page): Promise<void> {
  await page.goto("/");
  await page.waitForURL(/\/(setup|login|devices)/);
  if (page.url().includes("/setup")) {
    await page.getByLabel("Password").fill("demo");
    await page.getByRole("button", { name: /set password/i }).click();
  } else if (page.url().includes("/login")) {
    await page.getByLabel("Password").fill("demo");
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
  }
  // Home is `/` since qn.6d (quince#443); `/devices` redirects to it. Asserting the HEADING
  // rather than the URL keeps this stable across the next rename — this label has already
  // had one.
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
}

// G8 (spec qn.6c story 9): the selector renders both storages, disables the unreachable one with
// its reason, and does not cry full transfer over a storage that already holds this device's
// backups. (This line said the test "shows the full-transfer warning"; it asserts the absence of
// one, and has since it was written.)
//
// This drives UI → API → provider, which the unit tests deliberately do not: they render the
// component against props I wrote, so they cannot catch a field the server names differently or a
// null the client reads as absent. The demo ships the spec's fixture — two storages, one
// deliberately unreachable — precisely so this path is exercisable without hardware.
test("the storage selector shows both storages, the unreachable one disabled, and the full-transfer cost", async ({
  page,
}) => {
  await authenticate(page);

  // attic-ipad, AND THE CHOICE OF DEVICE IS THE ASSERTION AT THE BOTTOM (quince#505).
  //
  // This read `spare-iphone`, which the fixture seeds with a failed job and NO version — so on a
  // fresh instance the default storage does claim a full transfer, and the last assertion in this
  // test failed. It passed in CI only because one worker runs every spec against one shared demo in
  // file order, and story4/story5 back up spare-iphone first. The test was pinning *"some earlier
  // spec has by now created a version"* while its comment claimed *"this device already holds
  // backups"* — the second is what a reader trusts, and only the first was true.
  //
  // attic-ipad is seeded with a LIVE version on `internal` (plus a dead one, which `hasVersion`
  // correctly excludes), so the claim holds on a fresh instance and holds on its own. Nothing in
  // the suite changes that: story5 and story12 only read this device, and story5's Remove is
  // asserted visible rather than pressed.
  //
  // NOT family-iphone, which looks like the better candidate and is not: the demo runs a scripted
  // backup on it, and `BackupControls` renders the cancel row instead of the selector while a job
  // is active, so `storage-select` is simply absent. Measured, not reasoned — it is what the first
  // attempt at this fix did.
  //
  // Being OFFLINE is not a problem here and is worth saying, because it looks like one: the control
  // block still renders for a device with no transport, with the button disabled and a reason. That
  // is the state qn.6a put there deliberately.
  //
  // Everything above the last assertion is device-independent — the storage list is the same for
  // any device — so this costs no coverage.
  await page.getByRole("link", { name: "attic-ipad" }).click();
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

  // THE DIAGNOSIS IS NOT ON THIS PAGE ANY MORE (quince#627), and its absence is asserted rather
  // than the assertion simply deleted.
  //
  // This used to read `expect(getByTestId("storage-unreachable")).toContainText(/marker/)` — the
  // daemon's full sentence about a disk, on a page about a phone. Worse, that block rendered one
  // line per unreachable storage in the whole configuration with no reference to the chosen one, so
  // it diagnosed disks the user was not backing up to. It now lives on the storage's own page,
  // where the re-check test below drives it.
  //
  // The device page still says something when ITS chosen storage is unavailable — a short line
  // naming it, `storage-unavailable`. Not exercised here: this device's chosen storage is the
  // default, which is reachable, so there is correctly nothing to say.
  await expect(page.getByTestId("storage-unreachable")).toHaveCount(0);
  await expect(page.getByTestId("storage-recheck")).toHaveCount(0);
  await expect(page.getByText(/carries no quince storage marker/)).toHaveCount(0);

  // And the default — reachable, and genuinely holding this device's backups in the fixture — does
  // NOT claim a full transfer. A warning that is always on trains the user to ignore it.
  //
  // THE OPPOSITE DIRECTION IS NOT ASSERTED HERE and is not missing: `will_be_full: true` rendering
  // the line is pinned by StorageSelect.test.tsx, so this cannot pass merely because the testid was
  // renamed away. What only this test can prove is the integration — that the server computes the
  // field per (storage, device) and names it what the client reads — and a device with a real
  // history is the honest way to prove the `false` half of that.
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

  // ON THE STORAGE'S OWN PAGE, not a device's (quince#627). This drove a DEVICE page until now,
  // because that is where the diagnosis and the button used to render — a disk's mount state, and a
  // button that re-probes it, on a screen about a phone. The endpoint and the round trip are
  // unchanged; the surface offering them moved.
  await page.locator('[data-testid="storage-card"][data-storage-name="shuttle"] a').first().click();
  await expect(page).toHaveURL(/\/storage\/shuttle$/);

  // ONE button, on the one row that states the problem. This page is about ONE storage, so
  // "one button per unreachable storage in the configuration" — the defect quince#627 deleted — is
  // no longer expressible here at all.
  const button = page.getByTestId("storage-recheck");
  await expect(button).toHaveCount(1);

  const recheck = page.waitForResponse(
    (r) => /\/api\/storages\/[^/]+\/recheck$/.test(r.url()) && r.request().method() === "POST",
  );
  // NO `?udid=` — this page asks for the device-INDEPENDENT list, so the refetch is the bare
  // endpoint. The device-scoped variant is the device page's, and that page no longer carries this
  // button. The reason for refetching rather than splicing the 200 in is unchanged: `recheck`
  // answers about a storage, not a pair, so its `will_be_full` is null and splicing would drop the
  // full-transfer warning exactly when a returning disk made it true.
  const reload = page.waitForResponse(
    (r) => /\/api\/storages(\?|$)/.test(r.url()) && r.request().method() === "GET",
  );

  await button.click();

  // The press reaches the daemon, and it answers 200 for a storage that is declared.
  expect((await recheck).status()).toBe(200);
  expect((await reload).status()).toBe(200);

  // The disk is still out, and the row still says so — with the daemon's own sentence.
  await expect(page.getByTestId("storage-detail-reason")).toContainText(/marker/);
  await expect(page.getByTestId("storage-recheck-failed")).toHaveCount(0);
  await expect(button).toBeEnabled();
});
