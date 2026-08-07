import { expect, test, type Page } from "@playwright/test";

async function authenticate(page: Page): Promise<void> {
  await page.goto("/");
  // story6/story7's form, unchanged and deliberately so: NOT `(setup|login|devices)?$`, because the
  // optional group matches the bare "/" this goto lands on and waitForURL then returns before the
  // app has redirected.
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

// qn.6e G10 (the Rescan half). Operator-ruled 2026-08-07: `Rescan` moves out of the PAGE header to
// the foot of the DEVICES section, and the page header keeps only "Home".
//
// THIS IS AN E2E GATE RATHER THAN A COMPONENT TEST BY REPO CONVENTION, not by convenience:
// StorageDetailsVersions.test.tsx states it — "this pins the RULE rather than mounting the page …
// the page's own render is covered by e2e, and dragging the store/router surface in for a filter
// would test the framework instead of the decision." Placement IS the page's render, so this is the
// honest home for it.
//
// The `+ Add storage` half of G10 is NOT here and is not owed yet: it ships with the form (PR 6),
// because a button with nowhere to go is a dead affordance.
test("Rescan sits at the foot of the Devices section, and the page header carries no action", async ({
  page,
}) => {
  await authenticate(page);

  const heading = page.getByRole("heading", { name: "Home", level: 1 });
  const rescan = page.getByRole("button", { name: /rescan/i });
  await expect(rescan).toBeVisible();

  // THE HEADER BLOCK CARRIES NO BUTTON. Asserted on the h1's own container rather than on the page,
  // which is what makes it a placement claim: `rescan` is visible SOMEWHERE either way, so a
  // page-wide check would pass against the very layout this ruling replaced.
  const headerBlock = heading.locator("xpath=..");
  await expect(headerBlock.getByRole("button")).toHaveCount(0);

  // AND IT IS BELOW THE DEVICES SECTION, not above it. Compared by vertical position rather than by
  // DOM order: the ruling is about what a user sees, and a control that reads as page-level is the
  // defect, not one that happens to be a later sibling.
  const devicesHeading = page.getByRole("heading", { name: "Devices", level: 2 });
  const devicesBox = await devicesHeading.boundingBox();
  const rescanBox = await rescan.boundingBox();
  expect(devicesBox).not.toBeNull();
  expect(rescanBox).not.toBeNull();
  expect(rescanBox!.y).toBeGreaterThan(devicesBox!.y);

  // …and ABOVE Storage, so it reads as belonging to Devices rather than floating between sections.
  const storageHeading = page.getByRole("heading", { name: "Storage", level: 2 });
  const storageBox = await storageHeading.boundingBox();
  expect(storageBox).not.toBeNull();
  expect(rescanBox!.y).toBeLessThan(storageBox!.y);
});

// quince#325's hard-won behaviour must SURVIVE the move. The label stays "Rescan" throughout and the
// icon spins instead, because "Rescanning…" is wider and swapping it moved the layout. Covered as a
// unit in RescanButton.test.tsx; asserted here too because a restyle is exactly the change that
// would quietly reintroduce it, and the unit test does not see the new variant.
test("Rescan keeps its label while it is working, so the row cannot reflow", async ({ page }) => {
  await authenticate(page);

  const rescan = page.getByRole("button", { name: /rescan/i });
  const before = await rescan.boundingBox();

  await rescan.click();

  await expect(rescan).toBeDisabled();
  await expect(rescan).toHaveAccessibleName(/^\s*Rescan\s*$/);
  const during = await rescan.boundingBox();
  expect(before).not.toBeNull();
  expect(during).not.toBeNull();
  expect(Math.abs(during!.width - before!.width)).toBeLessThan(1);
});
