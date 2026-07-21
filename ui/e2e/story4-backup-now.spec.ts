import { expect, test, type Page } from "@playwright/test";

// Authenticate via setup or login, whichever the shared demo server asks for (same pattern as
// stories 1–3).
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

// Story 4 (spec qn.4b): the assisted "Back up now" flow drives the whole UI → API → engine (demo) →
// WS loop with no hardware — a backup starts and shows a live cancel + log, cancels honestly, and a
// failed backup shows a one-tap Retry that starts a fresh attempt (stack D13). The on-demand demo
// device (stable, encryption on) is the target; its seeded failed backup exercises retry.
test("back up now starts a job, cancels honestly, and retries a failed backup", async ({ page }) => {
  await authenticate(page);

  await page.getByText("spare-iphone").click();
  await expect(page).toHaveURL(/\/devices\//);

  // The seeded failed backup shows the assisted retry affordance.
  await expect(page.getByTestId("retry-backup")).toBeVisible();

  // Back up now → a job starts (the cancel control + live log exist only for a running job).
  await page.getByTestId("backup-now").click();
  await expect(page.getByTestId("cancel-backup")).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId("job-log")).toBeVisible({ timeout: 10_000 });

  // Cancel → the job ends cancelled (state honesty) and the Back up now control returns.
  await page.getByTestId("cancel-backup").click();
  await expect(page.getByText(/backup cancelled/i)).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId("backup-now")).toBeVisible({ timeout: 10_000 });

  // Retry the failed backup → a new attempt starts.
  await page.getByTestId("retry-backup").click();
  await expect(page.getByTestId("cancel-backup")).toBeVisible({ timeout: 10_000 });
});

// (bq) fix: the dashboard card's Pair deep-links a pair INTENT (router state) that auto-opens the
// pairing dialog on the details page — the click lands in the dialog, not just on the page (qn.3's
// narrated-flow-on-details decision stands). Kept a separate test so it never navigates through an
// open modal (which would obscure the back link).
test("dashboard card Pair auto-opens the pairing dialog on the details page", async ({ page }) => {
  await authenticate(page);

  // The Run()-seeded unpaired demo device is the only card showing a "Pair" link.
  await page.getByRole("link", { name: /^pair$/i }).first().click();
  await expect(page).toHaveURL(/\/devices\//);
  await expect(page.getByText(/pair this device/i)).toBeVisible({ timeout: 10_000 });
});

// qn.4c story 10 (findings (iv)+(v)): a backup started from the DASHBOARD CARD runs to success and
// the card lands on its real last-backup line — live, with no page reload. The defect this covers:
// the card sat at "Backing up 100%" through verify+commit and then said "No backups yet" even
// though the backup had committed a version. Kept separate from the cancel/retry story above,
// which deliberately never lets a job finish.
test("a card-started backup ends on the real last-backup line without a reload", async ({ page }) => {
  await authenticate(page);

  // Scope to the spare device's card body — the innermost element holding both its name and its
  // backup control. (No assertion on the STARTING text: the demo server is shared across the
  // tests in this file, and the retry above may already have given this device a backup. What
  // this story proves is the transition out of progress and onto a real last-backup line.)
  const card = page
    .locator("div")
    .filter({ has: page.getByTestId("card-backup-now") })
    .filter({ hasText: "spare-iphone" })
    .last();

  await card.getByTestId("card-backup-now").click();
  await expect(card.getByTestId("card-backup-now")).toBeHidden({ timeout: 10_000 });

  // The scripted job walks queued → … → verifying → committing → succeeded (~6s). No reload
  // anywhere in this test: everything below arrives over the WebSocket.
  await expect(card.getByTestId("card-backup-now")).toBeVisible({ timeout: 30_000 });
  await expect(card.getByText(/last backup/i)).toBeVisible();
  await expect(card.getByText(/no backups yet/i)).toHaveCount(0);
  await expect(card.getByText("Backing up")).toHaveCount(0);
});
