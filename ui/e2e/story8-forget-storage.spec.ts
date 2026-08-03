import { expect, test, type Page } from "@playwright/test";

async function authenticate(page: Page): Promise<void> {
  await page.goto("/");
  // NOT `(setup|login|devices)?$` — an optional group matches the bare "/" this goto lands on and
  // returns before the redirect, so the login branch never runs (measured, story7).
  await page.waitForURL(/\/(setup|login|devices)/);
  if (page.url().includes("/setup")) {
    await page.getByLabel("Password").fill("demo");
    await page.getByRole("button", { name: /set password/i }).click();
  } else if (page.url().includes("/login")) {
    await page.getByLabel("Password").fill("demo");
    await page.getByRole("button", { name: /sign in/i }).click();
  }
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
}

async function openStorage(page: Page, name: string): Promise<void> {
  await authenticate(page);
  await page.locator(`[data-testid="storage-card"][data-storage-name="${name}"] a`).first().click();
  await expect(page).toHaveURL(new RegExp(`/storage/${name}$`));
}

// qn.6d stories 8 and 9, END TO END — the seam neither quince#597 nor quince#599 could prove.
//
// 6a's Go gates pin the server's refusal text; 6b's vitest pins the rendering against a hand-built
// APIError. NOTHING proved the two agree on the wire, and that is exactly the gap this file exists
// to close: the message a user reads here came out of the daemon, through the 422 envelope, into
// the dialog.
//
// FILE ORDER MATTERS. Playwright runs single-worker and in file order, and the demo holds shared
// state, so the test that MUTATES the config is last in this file and this file sorts last. The
// refusal case mutates nothing and runs first.

// G6's client half. The default cannot be forgotten, and the sentence shown is the SERVER's —
// including the remedy, which is the half a client-side rewording would drop.
test("forgetting the default storage is refused, in the daemon's own words", async ({ page }) => {
  await openStorage(page, "internal");

  await page.getByTestId("storage-forget").click();
  // SCOPED TO THE DIALOG, not the page. That sentence deliberately appears TWICE — once as the
  // section's standing prose, once in the confirm — so a page-wide match is a strict-mode
  // violation rather than a passing assertion. Asserting the dialog's fuller wording also pins the
  // sentence carrying the ruling, which is the one a user reads immediately before pressing.
  await expect(
    page
      .getByRole("dialog")
      .getByText(/Forget removes it from quince\. The backups on the disk are not deleted\./),
  ).toBeVisible();

  await page.getByTestId("storage-forget-confirm").click();

  const err = page.getByTestId("storage-forget-error");
  await expect(err).toBeVisible();
  // The daemon's wording, not ours. If 6a's message is reworded and this is not, that is a real
  // disagreement between the two halves and this test is meant to catch it.
  await expect(err).toContainText("internal");
  await expect(err).toContainText(/is the default/);
  await expect(err).toContainText(/make another storage the default first/i);

  // A refusal leaves the dialog OPEN — a user just told to do something first should not have to
  // reopen the thing that told them — and changes nothing.
  await expect(page.getByTestId("storage-forgotten")).toHaveCount(0);
});

// Story 8 end to end, and the MUTATING one, so it is last.
test("forgetting a non-default storage succeeds and surfaces the restart", async ({ page }) => {
  await openStorage(page, "shuttle");

  await page.getByTestId("storage-forget").click();
  await page.getByTestId("storage-forget-confirm").click();

  const done = page.getByTestId("storage-forgotten");
  await expect(done).toBeVisible();
  await expect(done).toContainText(/no longer declared/);
  // THE RESTART IS SURFACED. Gap B ruled it is never silent, and this is the assertion that the
  // ruled behaviour reached a browser rather than only a unit test.
  await expect(done).toContainText(/still serving this disk until it restarts/i);
  await expect(done).toContainText(/[Nn]othing on the disk was deleted/);

  // AND IT DID NOT PROMISE LIVE-APPLY. quince#577 is a separate rung; story 8's copy must not
  // imply the storage is already gone from the running process.
  await expect(done).not.toContainText(/immediately|already stopped|no longer in use/i);

  // The config really changed — asserted against the API rather than the DOM, because the DOM is
  // what 6b already covers and the point of this file is the wire.
  const cfg = await page.request.get("/api/config");
  expect(cfg.ok()).toBe(true);
  const body = (await cfg.json()) as { config: { storage: { name: string }[] } };
  expect(body.config.storage.map((s) => s.name)).toEqual(["internal"]);

  // AND THE CARD IS STILL THERE, which is the ruled behaviour rather than a bug: the process keeps
  // serving the disk until it restarts, so `GET /api/storages` still lists it. A card vanishing
  // here would mean live deregistration, which this rung explicitly did not build.
  await page.goto("/");
  await expect(
    page.locator('[data-testid="storage-card"][data-storage-name="shuttle"]'),
  ).toBeVisible();
});
