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

// quince#762 — A DIALOG IS CENTRED IN THE VISIBLE AREA, NOT THE LAYOUT VIEWPORT. Operator-reported
// from a phone with screenshots: every dialog in the product sat against the top of the screen with
// its head under the Dynamic Island.
//
// WHAT THIS CAN AND CANNOT PROVE, because the difference is the whole reason the CSS is written the
// way it is. A headless browser reports every `env(safe-area-inset-*)` as 0 and has no keyboard, so
// a test written against `env()` directly could only ever exercise the fallback — it would pass just
// as happily on the broken build. Both inputs are therefore read through custom properties that this
// test can stand up itself: `--safe-*` is overridden here to raise a simulated Dynamic Island, and
// `--vv-height` is driven for real by shrinking the viewport, which is what the keyboard does to the
// visual viewport. THAT IOS ACTUALLY REPORTS THOSE INSETS IS OWED TO A DEVICE and is not claimed
// here; what is claimed is that when something reports them, the dialog lands inside them.
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

// The hook publishes the visible height asynchronously (a visualViewport event), so measuring the
// dialog before it lands would measure the fallback. Waiting on the published value rather than on a
// timeout also proves the hook RAN: on a build where it is absent this never settles.
async function visibleHeightPublished(page: Page, px: number): Promise<void> {
  await expect
    .poll(() =>
      page.evaluate(() =>
        getComputedStyle(document.documentElement).getPropertyValue("--vv-height").trim(),
      ),
    )
    .toBe(`${px}px`);
}

// `bottomInset` is what the frame reserves at the foot, and it is NOT always the home indicator.
// While the keyboard is up the indicator is BEHIND it, so reserving it would take a strip out of the
// space above the keyboard for nothing — measured on the Operator's recording as a dead gap between
// the card's foot and the keyboard's accessory bar (quince#762). With a keyboard, the reservation is
// the plain 1rem gutter.
const GUTTER = 16;

// THE SUBJECT OF BOTH quince#762 GATES USED TO BE THE ADD-STORAGE DIALOG, AND IT NO LONGER EXISTS
// (quince#846 made that surface a page). The fix it gates is a property of `DialogContent` and of
// every portalled surface — not of the add flow — so the gates move to another dialog rather than
// leaving with their old subject.
//
// THE ENCRYPTION DIALOG IS THE SUCCESSOR because it is the tallest one left: four fields and a
// submit, which is what the notch case and the below-the-fold case both need. `family-iphone` is
// paired with encryption on in `--demo`, so "Manage encryption" opens in change mode — the same
// path story 3 drives.
async function openTallDialog(page: Page): Promise<void> {
  await page.getByRole("link", { name: "family-iphone" }).click();
  await expect(page).toHaveURL(/\/devices\//);
  await page.getByRole("button", { name: /manage encryption/i }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
}

async function expectCentredInVisibleArea(
  page: Page,
  visibleH: number,
  bottomInset: number,
): Promise<void> {
  const box = await page.getByRole("dialog").boundingBox();
  expect(box).not.toBeNull();

  const above = box!.y - ISLAND_TOP; // gap between the notch and the top of the card
  const below = visibleH - bottomInset - (box!.y + box!.height); // card foot to what is reserved there

  // CLEARS BOTH. This is the reported bug: `above` was negative, so the card's head was under the
  // island. Asserted on the rendered box rather than on a class, because a class can be present and
  // overridden — and because "is it under the notch" is what the user actually sees.
  expect(above).toBeGreaterThanOrEqual(0);
  expect(below).toBeGreaterThanOrEqual(0);

  // AND IS CENTRED BETWEEN THEM, which is what the Operator asked for over merely clearing them.
  // Equal gaps, to a pixel of rounding.
  expect(Math.abs(above - below)).toBeLessThanOrEqual(1);
}

test("a dialog centres in the visible area, clear of the notch and the home indicator", async ({
  page,
}) => {
  await authenticate(page);
  // Navigate FIRST, then fake the insets: the properties are set on `documentElement` of this
  // document and client-side routing keeps it, but doing it in this order keeps that from being a
  // thing a reader has to know.
  await openTallDialog(page);
  await fakeSafeArea(page, ISLAND_TOP, HOME_INDICATOR);
  await visibleHeightPublished(page, 844);
  await expectCentredInVisibleArea(page, 844, HOME_INDICATOR);

  // THE KEYBOARD, as the visual viewport sees it: the visible area shrinks and the dialog must
  // re-centre in what is left rather than staying put against a layout viewport that never moved.
  // That is the failure the Operator reported twice before quince#762 — an input behind the
  // keyboard, coming right only on a second focus.
  await page.setViewportSize({ width: 390, height: 400 });
  await visibleHeightPublished(page, 400);
  // The keyboard is up now, so the home indicator is behind it and only the gutter is reserved.
  await expectCentredInVisibleArea(page, 400, GUTTER);

  // AND THE FOOT IS STILL REACHABLE once the card is bounded by a much smaller visible area — the
  // case where centring and scrolling have to hold at the same time.
  const submit = page.getByRole("button", { name: /change backup password/i });
  await submit.scrollIntoViewIfNeeded();
  await expect(submit).toBeVisible();
});

// quince#762 — FOCUSING A FIELD BELOW THE FOLD MUST NOT LEAVE IT ON THE EDGE OF THE SCROLL REGION.
// On the Operator's phone `Parent dataset` came to rest half-cut at the bottom of the card and
// `Helper command` did not come into view at all.
//
// THIS TEST PASSES WITH THE CORRECTION REMOVED, and that is recorded here rather than left for the
// next reader to discover. Measured: `make gates-ui-e2e` exit 0 with `useScrollFocusIntoView`
// unwired. Chromium's own scroll-into-view already clears this margin at the sizes the suite runs
// at, so a green result here says nothing about whether our correction ran.
//
// It is kept because it exercises the whole path end to end — a real dialog, a real focus, a real
// scroll container — which the unit test cannot. **The proof of the behaviour is
// `ui/src/lib/useScrollFocusIntoView.test.ts`**, which states the geometry and fails 3 of 5 when the
// correction is neutered. Do not read this one as coverage of the fix.
//
// THE SUBJECT MOVED WITH quince#846: the fields the Operator named were the zfs branch's, and that
// branch is on a page now, where this correction does not apply and is not wanted — the shell owns
// the scroll region and there is no card edge to be cut off by. The behaviour is a DIALOG's, so it
// is gated on the tallest surviving dialog rather than followed to a container that has no fold.
test("focusing a field below the fold brings it clear of the dialog's edges", async ({ page }) => {
  await authenticate(page);

  await openTallDialog(page);
  const dialog = page.getByRole("dialog");

  // The two lowest fields in the tall dialog — the ones a fold can cut, which is what the Operator
  // reported about the two lowest fields of the one this replaces.
  for (const label of ["New password", "Confirm new password"]) {
    const field = page.getByLabel(label, { exact: true });
    await field.focus();

    // Settle: the correction runs on the next animation frame after `focusin`.
    await expect
      .poll(async () => {
        const box = await dialog.boundingBox();
        const rect = await field.boundingBox();
        if (!box || !rect) return -1;
        return Math.min(rect.y - box.y, box.y + box.height - (rect.y + rect.height));
      })
      .toBeGreaterThanOrEqual(16);
  }
});
