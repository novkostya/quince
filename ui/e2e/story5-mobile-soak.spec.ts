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
  // Home is `/` since qn.6d (quince#443); `/devices` redirects to it. Asserting the HEADING
  // rather than the URL keeps this stable across the next rename — this label has already
  // had one.
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
}

test("the dashboard fits a phone and lists an offline device with a disabled, explained action", async ({ page }) => {
  await authenticate(page);

  // No horizontal overflow — the page must never require sideways scrolling on a phone.
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);

  // The DOCUMENT itself must not scroll vertically — the scroll region is <main>, not the page, so
  // the top nav bar never scrolls away and there's no nested scroll. (Headless can't emulate the iOS
  // toolbar that actually triggered the bug, but this holds the shell-owns-its-scroll invariant.)
  const docVScroll = await page.evaluate(
    () => document.documentElement.scrollHeight - document.documentElement.clientHeight,
  );
  expect(docVScroll).toBeLessThanOrEqual(1);

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

// quince#649 — THE BOTTOM OF THE PAGE MUST BE REACHABLE, and the shell must not outgrow the viewport.
//
// WHAT THIS CAN AND CANNOT ANSWER, stated because the issue's own diagnosis is unconfirmed.
// It CANNOT reproduce the reported clip: that needs an iOS toolbar expanding, and headless Chromium
// has none — `100svh`, `100dvh` and `100vh` are the same number here, so this passes on the code
// this PR replaces too. Confirmation on a real iPhone is OWED to the Operator.
//
// What it DOES settle is the second contributor the architect asked about rather than assumed:
// <main> carries `pb-[max(1rem,env(safe-area-inset-bottom))]`, and the open question was whether
// that bottom padding is SCROLLABLE or CLIPPED. That is an engine behaviour, not a toolbar one, so
// Chromium answers it honestly. `env(safe-area-inset-bottom)` is 0 here, so the padding is 1rem.
test("the bottom of a long page is reachable, and <main>'s bottom padding scrolls rather than clipping", async ({
  page,
}) => {
  await authenticate(page);

  const main = page.locator("main");

  // PRECONDITION, asserted rather than assumed: if Home fits the viewport there is nothing to
  // scroll and every assertion below would pass by having nothing to do — a check whose positive
  // answer is produced by the act of asking. Fail loudly instead.
  const metrics = await main.evaluate((el) => ({
    scrollHeight: el.scrollHeight,
    clientHeight: el.clientHeight,
    padBottom: parseFloat(getComputedStyle(el).paddingBottom),
  }));
  expect(
    metrics.scrollHeight,
    "Home is not taller than the phone viewport, so this test proves nothing — give it more content",
  ).toBeGreaterThan(metrics.clientHeight);
  expect(metrics.padBottom).toBeGreaterThan(0);

  // The shell can never be taller than the visible viewport — the property that makes the clip
  // structurally impossible. (True of `dvh` here too; it is the invariant, not the discriminator.)
  const shellHeight = await page.locator("main").evaluate((el) => {
    const shell = el.parentElement as HTMLElement;
    return shell.getBoundingClientRect().height;
  });
  expect(shellHeight).toBeLessThanOrEqual((await page.evaluate(() => window.innerHeight)) + 1);

  // Scroll <main> to its end and assert it actually reached it.
  await main.evaluate((el) => el.scrollTo(0, el.scrollHeight));
  const atEnd = await main.evaluate((el) => el.scrollHeight - el.scrollTop - el.clientHeight);
  expect(Math.abs(atEnd)).toBeLessThanOrEqual(1);

  // THE ANSWER TO THE ARCHITECT'S QUESTION: the scrollable extent INCLUDES the bottom padding, so
  // the padding is scrolled past rather than clipping the last element. If Chromium excluded
  // end-edge padding from the scroll extent, scrollHeight would stop at the content box and this
  // would fail by exactly padBottom.
  const contentBottomGap = await main.evaluate((el) => {
    const last = el.lastElementChild?.lastElementChild ?? el.lastElementChild;
    if (!last) return NaN;
    return el.getBoundingClientRect().bottom - last.getBoundingClientRect().bottom;
  });
  expect(contentBottomGap).toBeGreaterThanOrEqual(metrics.padBottom - 1);

  // The document still does not scroll — the qn.6a invariant this fix must not trade away.
  const docVScroll = await page.evaluate(
    () => document.documentElement.scrollHeight - document.documentElement.clientHeight,
  );
  expect(docVScroll).toBeLessThanOrEqual(1);
});
