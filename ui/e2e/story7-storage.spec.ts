import { expect, test, type Page } from "@playwright/test";

async function authenticate(page: Page): Promise<void> {
  await page.goto("/");
  // NOT `(setup|login|devices)?$` — the optional group matches the bare "/" this goto lands on,
  // so waitForURL returns BEFORE the app has redirected to /login, the login branch below never
  // runs, and every assertion then fails on a heading that was never going to appear. Measured;
  // it cost a full e2e cycle. This is story6's form, unchanged.
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

// G1b — the RENDERING half of G1. The wire claim (two storages on one filesystem report the same
// figures) is G1a, a Go test, because the demo FABRICATES both storages and its two paths share no
// filesystem — so a green run here would prove the card renders two numbers and nothing about the
// claim. That split is spec'd; this half is what e2e can honestly answer.
test("both storages are cards on Home, with free-of-total, a bar and counts", async ({ page }) => {
  await authenticate(page);

  const cards = page.getByTestId("storage-card");
  await expect(cards).toHaveCount(2);

  const internal = page.locator('[data-testid="storage-card"][data-storage-name="internal"]');
  await expect(internal).toBeVisible();

  // Free-of-total, in the plain form gap A ruled — never "on this filesystem". The wording is
  // asserted, not just the presence of two numbers, because the conditional phrasing this rung
  // first proposed is the thing that was ruled OUT and a regression would read as an improvement.
  const space = internal.getByTestId("storage-space");
  await expect(space).toContainText("free of");
  await expect(space).not.toContainText(/filesystem/i);

  // The bar. Its percentage is USED rather than free (quince#586) — 1.2 TB free of 3.6 TB is 67%
  // used, and a bar that filled as a disk emptied was this rung's own defect.
  await expect(internal.getByRole("progressbar")).toBeVisible();
  await expect(internal).toContainText("67%");

  await expect(internal.getByTestId("storage-counts")).toContainText("14 backups");
  await expect(internal.getByTestId("storage-counts")).toContainText("2 devices");

  // `Default` is a distinction, so it renders only where there is something to distinguish from —
  // and with two storages declared, there is.
  await expect(internal.getByText("Default", { exact: true })).toBeVisible();
});

// G2 — the unreachable storage is LISTED, states why, and claims no size.
//
// THE GATE'S OWN TEXT ASKED FOR MORE THAN THIS, and the extra half is deliberately not asserted:
// it said the card "DATES its counts", which quince#588 struck from story 4 and quince#589/#594
// removed from the wire. The counts are a COUNT(*) over `versions` and the DB is reachable whether
// or not the disk is, so there is nothing to date and no timestamp to assert. The Gates table is
// corrected in this same diff rather than left asking for behaviour the rung removed on purpose.
test("the unreachable storage is listed, says why, and claims no size", async ({ page }) => {
  await authenticate(page);

  const shuttle = page.locator('[data-testid="storage-card"][data-storage-name="shuttle"]');
  await expect(shuttle).toBeVisible();

  // LISTED, never hidden. A list a disk silently vanishes from is one the user cannot trust, and
  // this is the case removable media exists for.
  await expect(shuttle.getByTestId("storage-unreachable-reason")).toBeVisible();
  await expect(shuttle.getByTestId("storage-unreachable-reason")).toContainText(/not mounted|marker/);

  // NO SIZE CLAIM. Capacity is null rather than 0 on the wire precisely so this cannot render
  // "0 bytes free", which would read as a full disk rather than an absent one.
  await expect(shuttle.getByTestId("storage-space")).toHaveCount(0);
  await expect(shuttle.getByRole("progressbar")).toHaveCount(0);

  // Its counts ARE still shown — they are the DB's answer and the DB is reachable. This is the
  // asymmetry the capacity/counts fields exist to carry.
  await expect(shuttle.getByTestId("storage-counts")).toContainText("3 backups");

  // THE REACHABLE ONE IS UNAFFECTED, which the gate asks for by name: a page that degraded both
  // cards because one disk was out would pass every assertion above.
  const internal = page.locator('[data-testid="storage-card"][data-storage-name="internal"]');
  await expect(internal.getByTestId("storage-space")).toBeVisible();
});

// G3 — on a storage details page, the device list, the version list and `Back up now` are scoped
// to THAT storage, ASSERTED ON THE JOB THE BUTTON CREATES rather than on what is rendered.
//
// The job assertion is the whole gate. A page can render a correctly filtered list and still post
// a job with no storage_id, or with the default's — and that failure is invisible from the DOM,
// which is exactly why the spec calls this one out.
test("a storage page scopes its lists, and Back up now targets THAT storage", async ({ page }) => {
  await authenticate(page);

  await page.locator('[data-testid="storage-card"][data-storage-name="internal"] a').first().click();
  await expect(page).toHaveURL(/\/storage\/internal$/);

  await expect(page.getByTestId("storage-detail-space")).toContainText("free of");

  const rows = page.getByTestId("storage-device-row");
  await expect(rows.first()).toBeVisible();

  // The REQUEST is the subject. Captured before the click, so the assertion cannot pass on a
  // request that never happened.
  const createJob = page.waitForRequest(
    (r) => r.url().includes("/api/jobs") && r.method() === "POST",
  );
  // The testid IS the button, not a wrapper around one — `.getByRole("button")` beneath it
  // matches nothing and times out.
  await page.getByTestId("storage-device-backup").first().click();
  const req = await createJob;

  const body = req.postDataJSON() as { udid?: string; storage_id?: string };
  expect(body.udid, "the job must name a device").toBeTruthy();
  // qn.6c story 9's selector, answered by CONTEXT instead of a dropdown: the page you are on IS
  // the destination. An empty or absent storage_id would silently mean "the default", which is
  // the same value this fixture's storage happens to have — so the assertion is on the id being
  // PRESENT and correct, not merely on the job being created.
  expect(body.storage_id, "the job must carry THIS storage's id, not the default by omission")
    .toBe("01JSTORAGEDEMOINTERNAL00");
});

// G4 — a device with no versions on this storage is SHOWN there, with the full-transfer warning.
//
// The shuttle holds nothing for any device in the demo, so every device on its page is in this
// state. Shown rather than filtered is the point: the action this page exists to make easy is
// "start backing that device up here too", which is the whole 3-2-1 argument and is invisible if
// those devices are hidden.
test("a device with nothing on this storage is shown, with the full-transfer warning", async ({
  page,
}) => {
  await authenticate(page);

  await page.locator('[data-testid="storage-card"][data-storage-name="shuttle"] a').first().click();
  await expect(page).toHaveURL(/\/storage\/shuttle$/);

  const rows = page.getByTestId("storage-device-row");
  await expect(rows.first()).toBeVisible();

  const warnings = page.getByTestId("storage-device-will-be-full");
  await expect(warnings.first()).toBeVisible();
  await expect(warnings.first()).toContainText("first will be a full transfer");

  // EVERY device row on this page carries it, because none of them has anything here — a warning
  // on only the first row would pass a `.first()` assertion and be wrong.
  await expect(warnings).toHaveCount(await rows.count());
});
