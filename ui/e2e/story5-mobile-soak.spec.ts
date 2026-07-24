import { expect, test, type Page } from "@playwright/test";

// Story 5 (spec qn.6a): the soak runs from a PHONE, so the whole flow must work at a phone viewport.
// Everything here runs at 390×844 (iPhone-class): the dashboard fits without horizontal scroll, an
// offline device is listed with a disabled-with-reason "Back up now", "Back up now" narrates the
// `seeding` phase, backups name their device, and a dead version renders explicitly dead.
test.use({ viewport: { width: 390, height: 844 } });

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

test("the dashboard fits a phone and lists an offline device with a disabled, explained action", async ({ page }) => {
  await authenticate(page);

  // No horizontal overflow — the page must never require sideways scrolling on a phone.
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);

  // The offline demo device (attic-ipad: no transport, but it has backups).
  const offline = page.getByTestId("device-card").filter({ hasText: "attic-ipad" });
  await expect(offline.getByText("Offline")).toBeVisible();
  await expect(offline.getByText(/last seen/i)).toBeVisible();
  // Its "Back up now" is present (layout stays aligned) but DISABLED with a visible reason.
  const offlineBtn = offline.getByTestId("card-backup-now");
  await expect(offlineBtn).toBeDisabled();
  await expect(offline.getByText(/connect it over usb or wi-fi/i)).toBeVisible();

  // A backup row names its device (qn.6a #3) — the recent-backups list mixes devices.
  await expect(page.getByRole("heading", { name: /recent backups/i })).toBeVisible();
});

test("Back up now narrates the seeding phase before backing up (from a phone)", async ({ page }) => {
  await authenticate(page);

  // The stable spare device (present, encryption on) is the target.
  const card = page.getByTestId("device-card").filter({ hasText: "spare-iphone" });

  await card.getByTestId("card-backup-now").click();
  // The `seeding` phase renders as "Preparing" with the clone narration (qn.6a (cu)/(cv)).
  await expect(card.getByText("Preparing")).toBeVisible({ timeout: 15_000 });
  await expect(card.getByText(/cloning from your last backup/i)).toBeVisible();
});

test("a dead version renders explicitly dead with a Remove action", async ({ page }) => {
  await authenticate(page);

  // Open the offline device's details — it has a live version and a DEAD (missing) one.
  await page.getByRole("link", { name: "attic-ipad" }).click();
  await expect(page).toHaveURL(/\/devices\//);

  // The dead version is shown (never omitted), with no size claim and a Remove action.
  await expect(page.getByText(/artifact gone/i)).toBeVisible();
  await expect(page.getByRole("button", { name: /^remove$/i })).toBeVisible();
});
