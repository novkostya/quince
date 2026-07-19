import { expect, test, type Page } from "@playwright/test";

declare global {
  interface Window {
    __quince?: { dropWs: () => void };
  }
}

// The password was set by story 1 (same server); this test authenticates via setup or
// login, whichever the server asks for.
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

// Story 2 (spec qn.1): the scripted job renders as a live card with a tailing log; a WS
// disconnect shows reconnecting and recovers.
test("job card shows a tailing log; WS drop reconnects and recovers", async ({ page }) => {
  await authenticate(page);

  await page.getByText("family-iphone").click();
  await expect(page).toHaveURL(/\/devices\//);

  // the scripted backup produces a live log pane (waits across the loop's pause)
  await expect(page.getByTestId("job-log")).toBeVisible({ timeout: 60_000 });
  await expect(page.getByTestId("conn-badge")).toContainText(/connected/i);

  // force a WebSocket drop via the dev-only hook
  await page.evaluate(() => window.__quince?.dropWs());
  await expect(page.getByTestId("conn-badge")).toContainText(/reconnect/i, { timeout: 10_000 });

  // it recovers and re-refreshes state
  await expect(page.getByTestId("conn-badge")).toContainText(/connected/i, { timeout: 20_000 });
  await expect(page.getByText("family-iphone")).toBeVisible();
});
