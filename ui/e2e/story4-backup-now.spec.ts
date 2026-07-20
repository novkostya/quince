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
