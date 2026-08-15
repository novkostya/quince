import { expect, test, type Page } from "@playwright/test";

// quince#838 — THE DOCUMENT IS THE SCROLLER, AND BACK RETURNS YOU TO WHERE YOU WERE.
//
// NOT A SPEC STORY. The `storyN` prefix is FILE ORDERING ONLY: this suite is serial
// (`fullyParallel: false`, one worker) against one `--demo` server, and `story1` must run first
// because it is the only test that meets `needs_setup`. A name sorting before it would complete
// setup and break it. There is no story 12 in any spec.
//
// WHAT IS BEING GATED. Operator direction 2026-08-11: no internal scrollable container, let Safari
// scroll the document natively. Three separate claims fall out of it and each fails differently:
//
//  1. the document scrolls and nothing in the shell does — gated in `story5` beside the invariant it
//     replaced, so the reversal is visible where the old claim was;
//  2. a Back traversal lands where you left — the reported bug;
//  3. a NEW screen opens at the top — the second symptom, which is not the same bug and would
//     survive fixing the first.
//
// AND ONE NEGATIVE, WHICH IS THE CHEAPEST GUARD HERE: `history.scrollRestoration` must still read
// `"auto"`. `<ScrollRestoration>` from react-router sets it to `"manual"` in its first effect and
// re-implements restoration over `sessionStorage` — the trap named in the field notes on quince#838
// ("if you're typing `manual`, stop"). It is the single most likely wrong turn a future session
// takes here, it looks like an improvement, and it is one property read away from being caught.
//
// A SHORTER VIEWPORT THAN THE REST OF THE PHONE SUITE, AND THE HEIGHT IS LOAD-BEARING. Claim 3 is
// only testable on a destination page TALL ENOUGH TO HOLD the offset it was navigated from: a
// browser clamps a scroll position to what is scrollable, so on a short details page `scrollY`
// arrives at 0 whether or not anything reset it, and the assertion passes on a build with the fix
// removed. That is not hypothetical — it is what the first version of this file did, measured, and
// it is the "assertion that passes because it is asking the wrong question" the field notes on
// quince#838 name as worse than no assertion at all. 500px makes both pages comfortably scrollable;
// `expectCanHold` below asserts the precondition rather than trusting it.
test.use({ viewport: { width: 390, height: 500 } });

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
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
}

// PLAYWRIGHT'S `click()` SCROLLS ITS TARGET INTO VIEW FIRST, which silently changes the offset the
// test is about to assert was preserved. That produces scroll-restoration failures that are entirely
// the test's own doing — one of the two testing traps the field notes on quince#838 name, and the
// reason this helper exists rather than a plain `.click()`.
//
// `dispatchEvent` delivers the event where the element already is. `bubbles` is required: React
// attaches its listeners at the root container, so a non-bubbling click reaches no handler at all.
async function clickWithoutScrolling(page: Page, name: string): Promise<void> {
  await page.getByRole("link", { name }).dispatchEvent("click", { bubbles: true, cancelable: true });
}

// NOTHING SCROLLS SIDEWAYS, ON ANY SCREEN THIS SHIPS TO, ON ANY PAGE.
//
// Operator-reported from a phone after the shell change, and the honest account is that the change
// REVEALED it rather than caused it: the old shell was `overflow-hidden`, so a bar too wide for the
// screen was silently CLIPPED. Removing the clip turned a hidden defect into a visible one. Both
// states are wrong, and the second is at least reportable.
//
// **THE OBVIOUS FIX IS FORBIDDEN.** `overflow-x: hidden` on `html` would end the symptom in one line
// and take scroll restoration with it — a documented interaction (blank viewport on a restored
// traversal until you touch the screen), recorded in the field notes on quince#838. The layout has to
// actually fit; it may not be clipped into fitting.
//
// WHY A SWEEP AND NOT ONE ASSERTION. `story5` has checked this at 390px on Home since `qn.6a` and it
// passed on the build that shipped the defect, because flex text WRAPS before it overflows: at 390
// the nav bar grew a second line instead of pushing past the edge, and the same content at a
// narrower width could not. One width and one route is a spot check; the property is "no page, at
// any width we ship to".
//
// THE WIDTHS ARE REAL DEVICES, not a ramp. 320 is the narrowest iPhone viewport that still runs a
// current iOS; 375 and 390 are the common modern portraits; 430 is a Pro Max. If a fifth is ever
// added, add it here rather than in a second test.
const WIDTHS = [320, 375, 390, 430];

// A ROUTE PER SHAPE, not per route. Home (cards + the storage list), a device page (the longest
// identifiers in the product), a storage page, Settings (the config preview, which is `<pre>` and the
// most likely thing to push), the auth page and the add-storage form.
const ROUTES = ["/", "/devices/", "/storage/internal", "/settings", "/settings/auth", "/storage/new"];

test("no page scrolls sideways at any width this ships to", async ({ page }) => {
  await authenticate(page);

  // The device path is discovered rather than hardcoded: a udid is a fixture detail and this test is
  // not about which device it is.
  await page.getByRole("link", { name: "attic-ipad" }).click();
  await expect(page).toHaveURL(/\/devices\//);
  const devicePath = new URL(page.url()).pathname;

  const offenders: string[] = [];
  for (const width of WIDTHS) {
    await page.setViewportSize({ width, height: 800 });
    for (const route of ROUTES) {
      const path = route === "/devices/" ? devicePath : route;
      await page.goto(path);

      // WAIT FOR THE SHELL BEFORE MEASURING ANYTHING, and this is not only about the crash it fixes.
      // The gap check below read `aside` the instant the navigation resolved and found `null` while
      // React was still mounting — a flake. The overflow check does not crash in that window because
      // `documentElement` always exists, which is worse: it would measure an EMPTY page and pass.
      // One `waitFor` closes a flake and a vacuous pass at the same time.
      await page.locator("aside").waitFor({ state: "visible" });
      await page.locator("main").waitFor({ state: "visible" });

      // Settle: a page that is still laying out can report a transient overflow, and a flaky gate
      // about layout is worse than none. `poll` gives it the chance to come right and still fails if
      // it does not.
      const over = await page
        .evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)
        .then(async (first) => {
          if (first <= 1) return first;
          await page.waitForTimeout(250);
          return page.evaluate(
            () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
          );
        });
      if (over > 1) offenders.push(`${width}px ${path}: overflows by ${over}px`);

      // AND THE FIXED BAR NEVER COVERS THE CONTENT. This is the other half of the same sweep and it
      // guards a contract that is easy to break and silent when broken: the bar is out of flow, so
      // the shell reserves `--bar-h` as padding, and if those two ever disagree the top of every
      // page sits under the bar. One variable feeds both, so the only way to break it is to change
      // one and not the other — which is exactly the edit a future session makes.
      //
      // Measured at scroll 0, where the reservation is the only thing holding them apart.
      const gap = await page.evaluate(() => {
        const bar = document.querySelector("aside")!.getBoundingClientRect();
        const main = document.querySelector("main")!.getBoundingClientRect();
        return Math.round(main.top - bar.bottom);
      });
      if (gap < 0) offenders.push(`${width}px ${path}: <main> starts ${-gap}px under the bar`);
    }
  }

  // ONE ASSERTION CARRYING EVERY FAILURE. Failing on the first would report one width and one route
  // and hide the rest, and the shape of the answer — "every width" vs "only 320" vs "only Settings"
  // — is what says whether the cause is a fixed-width element or a shrink that stops too early.
  expect(offenders, `horizontal overflow:\n${offenders.join("\n")}`).toEqual([]);

  // AND THE BAR IS THE SAME HEIGHT AT EVERY PHONE WIDTH. It has a fixed height now, so it cannot
  // grow a second row — it would CLIP one, and slide under content the shell no longer reserves room
  // for. What this actually catches is the content stopping fitting: at 320 the wordmark yields
  // (`min-[360px]:block`) so one row still holds, and if a future label ate that margin the overflow
  // check above fires while this one stays quiet. Both are needed; neither implies the other.
  //
  // COMPARED AGAINST ITSELF AT 430, not against a pixel count. A threshold would need updating every
  // time the bar's padding changed and would encode a number nobody could check; heights that must
  // be equal encode the actual claim.
  const barHeight = async (width: number): Promise<number> => {
    await page.setViewportSize({ width, height: 800 });
    await page.goto("/");
    const box = await page.locator("aside").boundingBox();
    expect(box).not.toBeNull();
    return box!.height;
  };
  const reference = await barHeight(430);
  for (const width of [320, 375, 390]) {
    expect(await barHeight(width), `the nav bar changed height at ${width}px`).toBe(reference);
  }
});

// THE BAR STAYS PUT, AND IT KEEPS CLEARING THE NOTCH WHILE IT DOES.
//
// `position: fixed` now, not `sticky` — the assertion is unchanged and deliberately so. It asks
// whether the bar moved when the page did, which is the user-visible property, and stays true across
// a change of mechanism. A test written against `position: sticky` specifically would have had to be
// rewritten here and would have proved nothing extra.
//
// Two claims that only look like one. Sticking is easy to get and easy to lose — any ancestor
// growing an `overflow` value silently kills `position: sticky`, with no error and no visual clue
// until you scroll. The notch clearance is the half that a naive sticky BREAKS: the inset used to
// live on the shell, and shell padding scrolls away, so a bar pinned to `top: 0` lands under the
// status bar the moment the page moves. It is invisible in CI, where every inset reports 0 — which
// is why `--safe-top` is stood up here the way `story5` does it, rather than trusted from `env()`.
//
// AND THE HEADER MUST NOT MOVE, which is the field-notes lesson from quince#838: assertions that
// compared a header's top and height "passed happily while the header was vanishing entirely". So
// this compares the bar's whole box before and after a scroll, not one edge of it.
const ISLAND = 59;

test("the phone nav bar stays put, and stays clear of the notch while it does", async ({ page }) => {
  await authenticate(page);
  await page.evaluate((t) => {
    document.documentElement.style.setProperty("--safe-top", `${t}px`);
  }, ISLAND);

  const bar = page.locator("aside");
  const before = await bar.boundingBox();
  expect(before).not.toBeNull();

  await expectCanHold(page, "Home");
  await page.evaluate((to) => window.scrollTo(0, to), OFFSET);
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(OFFSET);

  const after = await bar.boundingBox();
  expect(after).not.toBeNull();

  // THE WHOLE BOX, to a pixel. `y` unchanged is stickiness; `height` unchanged is "it did not
  // collapse or reflow to achieve it"; `x`/`width` unchanged is "it did not become a different
  // element". Any one of these alone can pass over a bar that is visibly wrong.
  expect(after).toEqual(before);

  // AND ITS CONTENT STILL CLEARS THE SIMULATED ISLAND once the page has scrolled under it. The bar's
  // own top is 0 — that is what sticky means — so what is asserted is the PADDING: the first control
  // inside it must sit below the inset, which is the thing that moving the inset off the shell was
  // for.
  const link = await page.getByRole("link", { name: "Home" }).boundingBox();
  expect(link).not.toBeNull();
  expect(link!.y).toBeGreaterThanOrEqual(ISLAND);
});

// THE BAR IS IDENTICAL ON BOTH SIDES OF A PUSH AND A POP.
//
// Prescribed by `springback`'s `docs/ios-spa-notes.md` §4 after the Operator sent a screen recording
// of quince's header "blinking": *"serialise the whole bar — position, height, a title's x, and
// which item is selected — and assert it is byte-identical across a traversal. If it is not, that is
// your flicker, and no amount of positioning work will touch it."*
//
// IT WAS NOT, AND TWO BUILDS OF POSITIONING WORK WENT PAST IT. `Home` carried `end`, so a detail
// route lit no nav item at all and the selected pill vanished on push and returned on pop. §5 of the
// same notes is the reason it survived: the checks that existed compared the bar's top and height,
// which never changed.
//
// SO THE SELECTION IS IN THE SERIALISATION, not just the box. That is the whole lesson — an
// assertion that passes because it is asking the wrong question is worse than none, because it ends
// the investigation.
//
// The connection badge is deliberately EXCLUDED: its dot is live state that can legitimately change
// mid-test, and a gate that fails when the WebSocket reconnects is a gate nobody trusts.
async function serialiseBar(page: Page): Promise<unknown> {
  const box = await page.locator("aside").boundingBox();
  const nav = await page.evaluate(() =>
    Array.from(document.querySelectorAll("aside nav a")).map((a) => ({
      href: a.getAttribute("href"),
      text: a.textContent,
      current: a.getAttribute("aria-current"),
    })),
  );
  return { box, nav };
}

test("the nav bar is identical across a push and a pop", async ({ page }) => {
  await authenticate(page);
  const onHome = await serialiseBar(page);

  await page.getByRole("link", { name: "attic-ipad" }).click();
  await expect(page).toHaveURL(/\/devices\//);
  await expect(page.getByRole("heading", { name: "attic-ipad" })).toBeVisible();

  // A DETAIL SCREEN OPENED FROM HOME IS STILL HOME. Before the fix this was `current: null` on both
  // links, which is the pill going out.
  expect(await serialiseBar(page)).toEqual(onHome);

  await page.goBack();
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
  expect(await serialiseBar(page)).toEqual(onHome);

  // AND THE SAME HOLDS FOR A SECTION THAT DOES HAVE ITS OWN ITEM — the bar must change here, once,
  // and change back. Without this the test above would also pass on a build that lit nothing
  // anywhere, which is a bar that is stable and wrong.
  await page.goto("/settings");
  const onSettings = await serialiseBar(page);
  expect(onSettings).not.toEqual(onHome);
  await page.goto("/settings/auth");
  expect(await serialiseBar(page)).toEqual(onSettings);
});

// THE DESKTOP SIDEBAR MUST NOT BE TALLER THAN THE SCREEN IT IS PINNED TO.
//
// Operator-reported from an iPad, and NOT a regression — it predates this whole change and was found
// because the shell was under the microscope. The panel could be scrolled up by exactly the status
// bar's height, taking the wordmark under the status bar and back.
//
// The arithmetic, which is the whole bug: at `sm:` the shell reserves the safe-area inset as
// `padding-top`, and the sidebar inside it was `h-svh` — a FULL viewport height, placed after that
// padding. So the shell needed `inset + svh`, which is `inset` more than the screen, and the
// document became scrollable by exactly that much. Reported as "scrollable for the status bar
// height" because that is literally the quantity.
//
// WHY THIS IS SAFE FOR THE TWO SURFACES THAT NOW WORK, which is the Operator's condition on the fix
// and is provable rather than hopeful:
//
//  - iPHONE is below `sm`, so none of these utilities apply to it at all — the bar there is `fixed`
//    with its own height, untouched;
//  - a MAC reports every inset as 0, so the fix reduces to `top: calc(0px)` and
//    `height: calc(100svh - 0px)` — character for character the behaviour it replaces.
//
// The change can therefore only alter a surface with a NON-ZERO top inset, which is the iPad.
//
// `--safe-top` IS STOOD UP HERE because headless reports 0 and the defect is invisible at 0 — the
// same reason `story5` stands up a Dynamic Island rather than trusting `env()`.
const IPAD_INSET = 24;
// The home indicator, which the panel must also reach past with its background and stop short of
// with its contents. Distinct from the top inset so a fix that confuses the two cannot pass.
const IPAD_BOTTOM = 20;

test("the desktop sidebar fits the viewport it is pinned to, inset and all", async ({ page }) => {
  await authenticate(page);
  await page.setViewportSize({ width: 900, height: 700 });

  // The sidebar only exists as a column at `sm:`; assert we are actually on that branch, so this
  // cannot quietly become a test about the phone bar.
  await expect(page.locator("aside")).toHaveCSS("position", "sticky");

  const measure = () =>
    page.evaluate(() => {
      const bar = document.querySelector("aside")!.getBoundingClientRect();
      return { top: Math.round(bar.top), bottom: Math.round(bar.bottom), viewport: window.innerHeight };
    });

  // THE NO-OP HALF, ASSERTED RATHER THAN ARGUED. With a zero inset — every desktop, which is the
  // surface the Operator asked not to disturb — the column must fill the viewport EXACTLY: not
  // shorter, which is a strip of background at the foot, and not longer, which is the scroll-by-the-
  // status-bar-height defect. `toBe` rather than a bound, because at zero this is meant to be
  // indistinguishable from what it replaces.
  const flat = await measure();
  expect(flat.top).toBe(0);
  expect(flat.bottom).toBe(flat.viewport);

  // NOW WITH INSETS. Stood up after the measurement above, deliberately: the zero-inset claim is
  // about the default state and would be untestable once the properties are overridden.
  await page.evaluate(
    ([t, b]) => {
      document.documentElement.style.setProperty("--safe-top", `${t}px`);
      document.documentElement.style.setProperty("--safe-bottom", `${b}px`);
    },
    [IPAD_INSET, IPAD_BOTTOM],
  );
  const fit = await measure();

  // THE PANEL SPANS THE WHOLE VIEWPORT, INSETS INCLUDED — the PWA request, and note that the earlier
  // version of this test asserted only `top >= 0` and `bottom <= viewport`. That was satisfied by a
  // column INSET from both edges, which is exactly the state the Operator then photographed as "no
  // visible gaps" being wrong. A bound admitted the defect; equality does not.
  expect(fit.top, "the sidebar starts below the top of the screen").toBe(0);
  expect(
    fit.bottom,
    `the sidebar stops ${fit.viewport - fit.bottom}px short of the bottom of a ${fit.viewport}px viewport`,
  ).toBe(fit.viewport);

  // AND ITS CONTENTS STILL CLEAR THE INSETS — "elements should stay where they are though". The
  // panel reaching the screen edge is only right if the background is what got there and not the
  // wordmark: without this, a passing test above is equally satisfied by content sitting under the
  // status bar and the home indicator.
  const inner = await page.evaluate(() => {
    const first = document.querySelector("aside")!.firstElementChild!.getBoundingClientRect();
    const last = document.querySelector("aside")!.lastElementChild!.getBoundingClientRect();
    return { firstTop: Math.round(first.top), lastBottom: Math.round(last.bottom) };
  });
  expect(inner.firstTop, "the wordmark is under the status bar").toBeGreaterThanOrEqual(IPAD_INSET);
  expect(inner.lastBottom, "Sign out is under the home indicator").toBeLessThanOrEqual(
    fit.viewport - IPAD_BOTTOM,
  );

  // AND `<main>` KEPT ITS OWN CLEARANCE. The inset moved off the shell onto both children; if only
  // the aside had picked it up, a heading would sit under the status bar on an iPad in portrait.
  const mainTop = await page.evaluate(() =>
    Math.round(document.querySelector("main")!.getBoundingClientRect().top),
  );
  expect(mainTop, "<main> lost the top inset when the shell stopped holding it").toBe(0);
  const headingTop = await page
    .getByRole("heading", { name: "Home", level: 1 })
    .boundingBox()
    .then((b) => Math.round(b!.y));
  expect(headingTop, "the page heading is under the status bar").toBeGreaterThanOrEqual(IPAD_INSET);
});

// THE IN-PAGE BACK LINK RESTORES THE SCROLL POSITION, LIKE THE GESTURE DOES.
//
// Operator-reported as the last item on quince#838: "scroll position is not preserved when you tap
// on back button, e.g. `< Home`". A plain `<Link>` PUSHES, and a pushed entry has no saved offset —
// so there was nothing to restore and `useScrollReset` correctly sent it to the top. The control now
// performs a real traversal when the previous entry IS the destination it names.
//
// THE SAME `dispatchEvent` DODGE AS ABOVE: Playwright's `click()` would scroll the link into view
// first, which on a page scrolled to a known offset is the test changing the very number it is about
// to assert.
test("the in-page back link restores the scroll position", async ({ page }) => {
  await authenticate(page);
  await expectCanHold(page, "Home");
  await page.evaluate((to) => window.scrollTo(0, to), OFFSET);
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(OFFSET);

  await clickWithoutScrolling(page, "attic-ipad");
  await expect(page).toHaveURL(/\/devices\//);
  await expect(page.getByRole("heading", { name: "attic-ipad" })).toBeVisible();
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(0);

  // TAP `< Home` — the IN-PAGE control, scoped to `main` because the nav bar has a `Home` link too
  // and they are deliberately different acts: the nav item is a destination and pushes, this one is
  // a back control and traverses. An unscoped locator matches both, which is how this test first
  // failed, and it would have been the wrong element to prove anything about.
  await page
    .getByRole("main")
    .getByRole("link", { name: "Home" })
    .dispatchEvent("click", { bubbles: true, cancelable: true });
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(OFFSET);

  // AND IT LEFT NO ENTRY BEHIND, which is the half that says it was a traversal rather than a push
  // that happened to land well. If it had pushed, going back once would return to the DEVICE page;
  // it must return to whatever preceded Home instead — here, nothing further in this app, so the
  // check is that we are not on the device page.
  await page.goBack();
  await expect(page).not.toHaveURL(/\/devices\//);
});

test("the browser still owns scroll restoration — nothing has set it to manual", async ({ page }) => {
  await authenticate(page);
  expect(await page.evaluate(() => history.scrollRestoration)).toBe("auto");
});

// THE OFFSET THIS TEST USES. Fixed rather than derived from the page, so that "the destination can
// hold it" is a claim the test states and checks instead of a number it quietly adjusts to.
const OFFSET = 200;

// THE PRECONDITION THAT MAKES `scrollY === 0` MEAN ANYTHING. A browser clamps a scroll position to
// what is scrollable, so on a page that cannot reach `OFFSET` the answer is 0 for reasons that have
// nothing to do with the code under test. Asserted with the measurement in the failure message,
// because the way this goes wrong is a fixture quietly getting shorter, and the next reader needs to
// see "the details page can only scroll 40px" rather than "expected 0, got 0".
// POLLED, NOT READ ONCE — and this cost a flake before it was. A page's height is not final when its
// heading appears: the version list and the storages arrive over their own requests, so a single
// measurement taken at the wrong moment reports a page that is briefly too short and fails a test
// about something else entirely. Measured: this test passed only on the retry until the read below
// became a poll. `poll` also keeps the failure honest — it reports the height it settled on.
async function expectCanHold(page: Page, where: string): Promise<void> {
  await expect
    .poll(
      () => page.evaluate(() => document.documentElement.scrollHeight - window.innerHeight),
      { message: `${where} must be scrollable past ${OFFSET}px or this test proves nothing` },
    )
    .toBeGreaterThanOrEqual(OFFSET);
}

test("a new screen opens at the top, and Back returns to where you were", async ({ page }) => {
  await authenticate(page);
  await expectCanHold(page, "Home");

  // Read back rather than assumed: `scrollTo` is clamped silently, and asserting against the number
  // we asked for rather than the one we got is how a test passes about a page that never moved.
  await page.evaluate((to) => window.scrollTo(0, to), OFFSET);
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(OFFSET);

  // INTO the details page, from a scrolled position and without Playwright moving it for us.
  await clickWithoutScrolling(page, "attic-ipad");
  await expect(page).toHaveURL(/\/devices\//);
  await expect(page.getByRole("heading", { name: "attic-ipad" })).toBeVisible();
  await expectCanHold(page, "the device details page");

  // A NEW SCREEN STARTS AT THE TOP. Without `useScrollReset` the offset simply survives the
  // navigation — an SPA never loads a new document to start one at zero — and the details page opens
  // part-way down, which is the second symptom reported on quince#838.
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(0);

  // AND BACK LANDS WHERE WE LEFT. This is the browser's own restoration, which is the whole reason
  // the shell stopped owning the scroll: a history entry records `window.scrollY` and has no way to
  // record an element's `scrollTop`.
  await page.goBack();
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(OFFSET);
});

// A DIALOG IS A NAVIGATION, AND CLOSING ONE PUTS THE PAGE BACK WHERE IT WAS — quince#931.
//
// THE REPORT, from a phone, 2026-08-15: open a dialog at the top of a device page, let Safari scroll
// the document to clear the on-screen keyboard, close the dialog, and the page underneath is left
// wherever Safari put it — three screenshots, ending on Backup history when it started at the top.
// The document moving is CORRECT and is what quince#838 asked for; what was missing is that leaving
// the page was never recorded as going anywhere, so there was nothing to come back to.
//
// WHAT THIS TEST CAN AND CANNOT REPRODUCE. There is no on-screen keyboard in a headless browser, so
// the scroll is performed here rather than provoked. That is honest about the mechanism: the claim
// is not "we handle the keyboard" — nothing does, deliberately — it is that ANY scroll that happens
// while a dialog is open is undone by closing it, because closing is a history pop and the browser
// restores the offset. The keyboard is just the most common way for that scroll to occur.
test("opening a dialog is a push, and closing it restores the page behind it", async ({ page }) => {
  await authenticate(page);
  await page.getByRole("link", { name: "family-iphone" }).click();
  await expect(page).toHaveURL(/\/devices\//);
  await expectCanHold(page, "the device details page");

  await page.evaluate((to) => window.scrollTo(0, to), OFFSET);
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(OFFSET);

  // OPENING IS A NAVIGATION, and the address says which dialog. A query param rather than a path
  // segment on purpose: `useScrollReset` sends a new PATHNAME to the top, so a path-shaped dialog
  // route would scroll this page to 0 on open — the defect class, reintroduced by its own fix.
  await page.getByRole("button", { name: /manage encryption/i }).click();
  await expect(page.getByRole("dialog")).toBeVisible();
  await expect(page).toHaveURL(/[?&]dialog=encryption/);
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(OFFSET);

  // WHAT THE KEYBOARD DOES ON A DEVICE, performed by hand: the document moves under the open dialog.
  await page.evaluate(() => window.scrollTo(0, 0));
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(0);

  // CLOSING IS A POP, so the browser puts the offset back. Nothing in quince restores it.
  await page.getByRole("button", { name: /^cancel$/i }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page).not.toHaveURL(/dialog=/);
  await expect.poll(() => page.evaluate(() => Math.round(window.scrollY))).toBe(OFFSET);
});

// AND BACK CLOSES IT, which is what the gesture on a phone actually is. Without a history entry the
// edge-swipe leaves the page entirely with the dialog still up in memory.
test("the Back gesture closes an open dialog rather than leaving the page", async ({ page }) => {
  await authenticate(page);
  await page.getByRole("link", { name: "family-iphone" }).click();
  await expect(page).toHaveURL(/\/devices\//);

  await page.getByRole("button", { name: /manage encryption/i }).click();
  await expect(page.getByRole("dialog")).toBeVisible();

  await page.goBack();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  // STILL ON THE DEVICE PAGE. The dialog consumed the Back; it did not pass it through.
  await expect(page).toHaveURL(/\/devices\//);
  await expect(page.getByRole("heading", { name: "family-iphone" })).toBeVisible();
});
