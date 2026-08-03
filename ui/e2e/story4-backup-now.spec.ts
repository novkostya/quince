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
  // Home is `/` since qn.6d (quince#443); `/devices` redirects to it. Asserting the HEADING
  // rather than the URL keeps this stable across the next rename — this label has already
  // had one.
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
}

// Story 4 (spec qn.4b): the assisted "Back up now" flow drives the whole UI → API → engine (demo) →
// WS loop with no hardware — a backup starts and shows a live cancel + log, cancels honestly, and a
// failed backup shows a one-tap Retry that starts a fresh attempt (stack D13). The on-demand demo
// device (stable, encryption on) is the target; its seeded failed backup exercises retry.
test("back up now starts a job, cancels honestly, and retries a failed backup", async ({ page }, testInfo) => {
  await authenticate(page);

  await page.getByRole("link", { name: "spare-iphone" }).click();
  await expect(page).toHaveURL(/\/devices\//);

  // JobHistory renders one summary per intent group; a cancelled intent's summary is exactly "Backup
  // cancelled" (capital B — this exact match deliberately excludes the lowercase job-log line "backup
  // cancelled", which lives only in the transient log pane). This test cancels TWO jobs (the retried
  // seed intent + the backup-now intent), so by the end two groups legitimately read this. Assert on
  // the COUNT (delta), never bare visibility: (a) tolerant of >1 cancelled group, so it never trips
  // strict mode; (b) immune to the newest-first ordering tie when both cancelled jobs share a
  // whole-second `started_at` (wire.Now() is RFC3339, second precision). — flake fix, CI 30108238903.
  const cancelledGroups = page.getByText("Backup cancelled", { exact: true });

  // The retry leg needs the demo's one-shot seeded failed backup to be spare-iphone's LATEST intent
  // (Retry renders only on the latest intent — qn.6a). The e2e demo server is SHARED and never
  // re-seeds per test, so a Playwright RETRY of this test re-runs against state the failed primary
  // attempt already advanced past: the seed is no longer the latest intent and Retry is gone. Run the
  // retry leg only on the primary attempt (retry === 0 — always a fresh container, seed pristine); a
  // Playwright retry still fully exercises the backup-now/cancel leg below. — idempotence fix.
  if (testInfo.retry === 0) {
    // The seeded failed backup is the LATEST intent, so it shows the one-tap Retry. Retry starts a
    // fresh attempt; cancel it back to a clean state (one cancelled group appears — the seed intent).
    await expect(page.getByTestId("retry-backup")).toBeVisible();
    const before = await cancelledGroups.count();
    await page.getByTestId("retry-backup").click();
    await expect(page.getByTestId("cancel-backup")).toBeVisible({ timeout: 10_000 });
    await page.getByTestId("cancel-backup").click();
    await expect(cancelledGroups).toHaveCount(before + 1, { timeout: 10_000 });
  }

  // Back up now → a job starts (the cancel control + live log exist only for a running job).
  await expect(page.getByTestId("backup-now")).toBeVisible({ timeout: 10_000 });
  const beforeBackupNow = await cancelledGroups.count();
  await page.getByTestId("backup-now").click();
  await expect(page.getByTestId("cancel-backup")).toBeVisible({ timeout: 10_000 });
  await expect(page.getByTestId("job-log")).toBeVisible({ timeout: 10_000 });

  // Cancel → the job ends cancelled (state honesty) and the Back up now control returns. Exactly one
  // more cancelled group appears — assert the delta, not bare visibility (see the note above).
  await page.getByTestId("cancel-backup").click();
  await expect(cancelledGroups).toHaveCount(beforeBackupNow + 1, { timeout: 10_000 });
  await expect(page.getByTestId("backup-now")).toBeVisible({ timeout: 10_000 });
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
  const card = page.getByTestId("device-card").filter({ hasText: "spare-iphone" });

  await card.getByTestId("card-backup-now").click();
  await expect(card.getByTestId("card-backup-now")).toBeHidden({ timeout: 10_000 });

  // The scripted job walks queued → … → verifying → committing → succeeded (~6s). No reload
  // anywhere in this test: everything below arrives over the WebSocket.
  await expect(card.getByTestId("card-backup-now")).toBeVisible({ timeout: 30_000 });
  await expect(card.getByText(/last backup/i)).toBeVisible();
  await expect(card.getByText(/no backups yet/i)).toHaveCount(0);
  await expect(card.getByText("Backing up")).toHaveCount(0);
});
