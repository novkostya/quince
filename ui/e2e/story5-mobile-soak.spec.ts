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
    await page.getByRole("button", { name: "Sign in", exact: true }).click();
  }
  // Home is `/` since qn.6d (quince#443); `/devices` redirects to it. Asserting the HEADING
  // rather than the URL keeps this stable across the next rename — this label has already
  // had one.
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
}

test("the dashboard fits a phone and lists an offline device whose action states its own condition", async ({ page }) => {
  await authenticate(page);

  // No horizontal overflow — the page must never require sideways scrolling on a phone.
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);

  // THIS ASSERTION IS INVERTED FROM WHAT IT SAID, AND THE INVERSION IS THE POINT OF quince#838.
  //
  // It read: "The DOCUMENT itself must not scroll vertically — the scroll region is <main>, not the
  // page, so the top nav bar never scrolls away." That was `qn.6a`'s invariant and the soak
  // confirmed it. Operator direction 2026-08-11 REVERSES it — let Safari scroll the document
  // natively — because the old shape is what made Back unable to restore a position (a history
  // entry records `window.scrollY`, never an element's `scrollTop`) and what left the foot of a page
  // clipped with no scroller able to reach it (quince#649).
  //
  // So this gate now asserts the OPPOSITE invariant, which is the same kind of claim rather than a
  // weaker one: the document scrolls, and nothing inside the shell owns the vertical scroll.
  //
  // MOVE IT AND SEE WHAT MOVED, rather than asserting a height. The first version of this compared
  // the document's scrollable extent against a margin and FLAKED: Home's height is not final when its
  // cards appear — the storage section and the recent-backups list arrive over their own requests —
  // so a single read caught it at 31px and the threshold was 50. Polling would have fixed the flake
  // and left an arbitrary number behind. Scrolling by a distance and reading the offset back cannot
  // be marginal: either the document is a scroller or `scrollY` stays 0.
  const NUDGE = 30;
  await expect
    .poll(() => page.evaluate(() => document.documentElement.scrollHeight - document.documentElement.clientHeight))
    .toBeGreaterThanOrEqual(NUDGE);
  await page.evaluate((to) => window.scrollTo(0, to), NUDGE);
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(NUDGE);

  // AND NOTHING INSIDE THE SHELL OWNS IT. `<main>` is named directly because it is the element that
  // used to, and a regression would put it back there first.
  const main = await page.evaluate(() => {
    const el = document.querySelector("main")!;
    return { overflowY: getComputedStyle(el).overflowY, scrollable: el.scrollHeight - el.clientHeight };
  });
  expect(main.overflowY).toBe("visible");
  expect(main.scrollable).toBeLessThanOrEqual(1);

  await page.evaluate(() => window.scrollTo(0, 0));

  // The offline demo device (attic-ipad: no transport, but it has backups).
  const offline = page.getByTestId("device-card").filter({ hasText: "attic-ipad" });
  await expect(offline.getByText("Offline")).toBeVisible();
  await expect(offline.getByText(/last seen/i)).toBeVisible();
  // Its action is present (layout stays aligned) and IS the reason — quince#1202. The caption line
  // above it is gone; the button states its own condition instead, which is what reclaimed the 25px
  // that made this card the tallest in the row.
  //
  // `toBeDisabled()` PASSES ON `aria-disabled`, MEASURED — an earlier version of this comment said
  // Playwright honours it "only on some roles" and asserted `toBeEnabled()`, which failed. It
  // resolves disabled through the accessibility tree, so the a11y-facing state is what it reports.
  //
  // WHICH IS WHY THE DOM PROPERTY IS ASSERTED SEPARATELY. The whole point of `aria-disabled` here is
  // that the control stays in the tab order — a truly `disabled` button is skipped by screen
  // readers, so putting the reason inside one would delete it for the users who cannot see the
  // styling. `toBeDisabled()` cannot tell those two apart; this can.
  const offlineBtn = offline.getByTestId("card-backup-now");
  await expect(offlineBtn).toBeDisabled(); // a11y-facing: announced as unavailable
  expect(await offlineBtn.evaluate((el: HTMLButtonElement) => el.disabled)).toBe(false); // still focusable
  await expect(offlineBtn).toHaveText(/connect to back it up/i);
  await expect(offline.getByText(/connect it over usb or wi-fi/i)).toHaveCount(0);

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

// qn.6e — ADD STORAGE ON A PHONE. Operator-reported from a live device: the surface zoomed on
// focus, and once the zfs branch opened it grew taller than the screen with no way to scroll, so
// the buttons at its foot could not be reached at all.
//
// Both were regressions against fixes this codebase already had. The zoom is quince#616 —
// `fieldBase` carries `text-base sm:text-sm` and the add form used raw `<input>`/`<select>` with
// `text-sm`, bypassing it. The height had no fix because no dialog had ever been tall enough to
// need one; `DialogContent` bounds and scrolls, which repaired every dialog rather than this one.
//
// IT IS A PAGE NOW (quince#846), and exactly half of this test moves with it. THE ZOOM HALF IS A
// PROPERTY OF THE FIELDS and is untouched — it is the half that regressed, and it would regress the
// same way on a page. The height half was a property of the CONTAINER: a page cannot overflow a
// height it does not set, so what survives of *"Save existed and could not be got to"* is that the
// foot is still reachable, measured against the shell's scroll region instead of a card's own box.
// `DialogContent`'s bound has not stopped mattering and is gated two tests below, on the encryption
// dialog — which is now this suite's tall-dialog subject.
test("the add-storage page is usable on a phone, zfs branch included", async ({ page }) => {
  await authenticate(page);

  await page.getByTestId("add-storage").click();
  await expect(page).toHaveURL(/\/storage\/new$/);
  // NOT A MODAL, asserted rather than assumed: an outside tap dismissing this surface is the whole
  // reason it moved, and quince#818 is about to make that write a keypair to disk.
  await expect(page.getByRole("dialog")).toHaveCount(0);

  // 16px MINIMUM ON EVERY FOCUSABLE FIELD. Below it, iOS Safari zooms the page to `16 / fontSize`
  // on focus — measured on the COMPUTED style, because the class that produces it is the thing that
  // regressed and asserting the class would pass on a copy of it.
  const pathField = page.getByLabel("Path");
  const pathSize = await pathField.evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
  expect(pathSize).toBeGreaterThanOrEqual(16);

  await pathField.fill("/tmp");
  await page.getByTestId("probe-check").click();
  const backend = page.getByTestId("backend-select");
  await expect(backend).toBeVisible();
  const selectSize = await backend.evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
  expect(selectSize).toBeGreaterThanOrEqual(16);

  // THE TALL CASE. zfs adds two fields, a button and a verdict — the state that broke.
  await backend.selectOption("zfs");
  await expect(page.getByTestId("zfs-fields")).toBeVisible();

  const parent = page.getByLabel("Parent dataset");
  const parentSize = await parent.evaluate((el) => parseFloat(getComputedStyle(el).fontSize));
  expect(parentSize).toBeGreaterThanOrEqual(16);

  // NO SIDEWAYS SCROLL, which is this suite's standing phone constraint and is the page's version
  // of "it fits". A dialog could overflow its own card; a page overflows the document, and that is
  // what a phone user meets as the layout sliding under their thumb.
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
  );
  expect(overflow).toBeLessThanOrEqual(1);

  // AND THE FOOT IS REACHABLE. This is the failure as the Operator met it: Save existed, and could
  // not be got to. On a page the scroll region is `<main>` (the shell owns scrolling), so this
  // asserts the same user-visible fact through the container that now owns it.
  const save = page.getByTestId("add-storage-save");
  await save.scrollIntoViewIfNeeded();
  await expect(save).toBeVisible();
  await expect(page.getByTestId("test-helper")).toBeVisible();
});

// quince#762 — A DIALOG CLEARS THE NOTCH AND THE HOME INDICATOR, AND IS CENTRED BETWEEN THEM.
// Operator-reported from a phone with screenshots: every dialog in the product sat against the top
// of the screen with its head under the Dynamic Island.
//
// THIS IS THE HALF OF quince#762 THAT SURVIVES. The other half — a JavaScript frame that tracked
// `visualViewport` so the card re-centred in the visible area while the keyboard was up — is gone
// (Operator direction 2026-08-15, quince#816): the correction could only ever land a frame after the
// scroll that caused it, so it jumped on every focus change. The keyboard is Safari's now. What is
// asserted here is the part that is pure CSS and has no such cost.
//
// WHAT THIS CAN AND CANNOT PROVE, because the difference is why the CSS is written the way it is. A
// headless browser reports every `env(safe-area-inset-*)` as 0, so a test written against `env()`
// directly could only ever exercise the fallback — it would pass just as happily on the broken
// build. The insets are therefore read through custom properties this test can stand up itself.
// THAT IOS ACTUALLY REPORTS THOSE INSETS IS OWED TO A DEVICE and is not claimed here; what is
// claimed is that when something reports them, the dialog lands inside them.
const ISLAND_TOP = 59; // iPhone-class portrait. The exact figure is not the claim — clearing it is.
const HOME_INDICATOR = 34;

async function fakeSafeArea(page: Page, top: number, bottom: number): Promise<void> {
  await page.evaluate(
    ([t, b]) => {
      document.documentElement.style.setProperty("--safe-top", `${t}px`);
      document.documentElement.style.setProperty("--safe-bottom", `${b}px`);
    },
    [top, bottom],
  );
}

// THE SUBJECT OF THIS GATE USED TO BE THE ADD-STORAGE DIALOG, AND IT NO LONGER EXISTS (quince#846
// made that surface a page). The fix it gates is a property of `DialogContent` and of every
// portalled surface — not of the add flow — so the gate moves to another dialog rather than leaving
// with its old subject.
//
// THE ENCRYPTION DIALOG IS THE SUCCESSOR because it is the tallest one left: four fields and a
// submit. `family-iphone` is paired with encryption on in `--demo`, so "Manage encryption" opens in
// change mode — the same path story 3 drives.
async function openTallDialog(page: Page): Promise<void> {
  await page.getByRole("link", { name: "family-iphone" }).click();
  await expect(page).toHaveURL(/\/devices\//);
  await page.getByRole("button", { name: /manage encryption/i }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
}

test("a dialog centres in the viewport, clear of the notch and the home indicator", async ({
  page,
}) => {
  await authenticate(page);
  // Navigate FIRST, then fake the insets: the properties are set on `documentElement` of this
  // document and client-side routing keeps it, but doing it in this order keeps that from being a
  // thing a reader has to know.
  await openTallDialog(page);
  await fakeSafeArea(page, ISLAND_TOP, HOME_INDICATOR);

  const box = await page.getByRole("dialog").boundingBox();
  expect(box).not.toBeNull();

  const above = box!.y - ISLAND_TOP; // gap between the notch and the top of the card
  const below = 844 - HOME_INDICATOR - (box!.y + box!.height); // card foot to the home indicator

  // CLEARS BOTH. This is the reported bug: `above` was negative, so the card's head was under the
  // island. Asserted on the rendered box rather than on a class, because a class can be present and
  // overridden — and because "is it under the notch" is what the user actually sees.
  expect(above).toBeGreaterThanOrEqual(0);
  expect(below).toBeGreaterThanOrEqual(0);

  // AND IS CENTRED BETWEEN THEM, which is what the Operator asked for over merely clearing them.
  // Equal gaps, to a pixel of rounding.
  expect(Math.abs(above - below)).toBeLessThanOrEqual(1);
});

// A DIALOG TALLER THAN THE SCREEN MUST BE REACHABLE AT BOTH ENDS — Operator-reported 2026-08-15
// from a phone in LANDSCAPE, with the encryption dialog's head above the top edge and its buttons
// below the bottom one. Nothing scrolled, because at that moment nothing was a scroller.
//
// THE RISK THIS GATE EXISTS FOR IS NOT THE CSS, IT IS THE SCROLL LOCK. Radix wraps `Content` in
// `react-remove-scroll`, which blocks scrolling everywhere except the content it is given. The
// scroller here is the OVERLAY — an ancestor of that content, not the content itself — so whether it
// can scroll at all is a question about a third-party library's internals, and the only honest way
// to answer it is to scroll it. A landscape viewport is what makes the tallest dialog overflow.
test("a dialog taller than the screen scrolls, and both its ends are reachable", async ({ page }) => {
  await page.setViewportSize({ width: 844, height: 390 }); // landscape phone — the reported case
  await authenticate(page);
  await openTallDialog(page);

  const dialog = page.getByRole("dialog");
  const box = await dialog.boundingBox();
  expect(box).not.toBeNull();
  // The premise of the test: this dialog really does overflow this viewport. If a copy change ever
  // makes it fit, the assertions below would pass without exercising anything.
  expect(box!.height).toBeGreaterThan(390);

  // THE FOOT: the submit button is below the fold and must come into view.
  const submit = page.getByRole("button", { name: /change backup password/i });
  await submit.scrollIntoViewIfNeeded();
  await expect(submit).toBeVisible();
  expect((await submit.boundingBox())!.y).toBeGreaterThanOrEqual(0);

  // THE HEAD: and going back up must still reach the title, which is what was cut off in the report.
  // The dialog title and its submit share a name, so this is scoped to the heading role.
  const title = page.getByRole("heading", { name: /change backup password/i });
  await title.scrollIntoViewIfNeeded();
  await expect(title).toBeVisible();
  expect((await title.boundingBox())!.y).toBeGreaterThanOrEqual(0);
});
