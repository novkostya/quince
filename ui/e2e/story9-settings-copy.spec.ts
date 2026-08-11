import { expect, test, type Page } from "@playwright/test";

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

// qn.6g G8 — the settings screen stops promising a restart.
//
// WHAT THIS GATE CAN AND CANNOT PROVE, stated first because the spec bounds it in advance
// (interface fact 10). `--public-demo` deletes its config at startup and the demo provider
// FABRICATES its storages rather than resolving them, so **this file proves the COPY and nothing
// about a setting taking effect.** That claim lives in G1–G7, in Go, against a real temp-dir
// storage. A test here that clicked Save and then asserted "the transport really changed" would be
// asserting against a fixture — `a fixture that fabricates a value the live code never produces
// makes its gate a lie` (qn.6d, inherited verbatim).
//
// It is worth a browser anyway, and for one reason: the string that was wrong lived in a `saved`
// branch that only renders AFTER a successful PUT. A grep of the source proves the literal is gone;
// only a browser proves nothing else took its place on the path a user walks.
//
// FILE ORDER. Playwright runs single-worker in file order and the demo holds shared state. This
// file sorts last, after story8 — which forgets a storage and is genuinely mutating. This one is
// NOT: the save below submits the document it was handed, unchanged. Sorting last anyway costs
// nothing and keeps the rule *"the file that touches config goes last"* readable, since a later
// edit here could easily make it true.

test("the settings intro distinguishes the two editing paths", async ({ page }) => {
  await authenticate(page);
  await page.goto("/settings");

  const intro = page.getByText(/quince is configured by one file/);
  await expect(intro).toBeVisible();

  // THE UI PATH IS IMMEDIATE, and this is the claim qn.6g earns.
  await expect(intro).toContainText(/[Cc]hanges made here apply immediately/);

  // AND THE HAND-EDIT PATH IS NOT, which is the half most likely to be dropped as a caveat. Nothing
  // watches `config.yml` — file-watch was ruled into its own rung on 2026-08-04, option (a) — so a
  // page that said only "changes apply immediately" would send a hand-editor away waiting for an
  // effect that never arrives. Overstating is this project's most-filed defect and this sentence is
  // where it would land.
  await expect(intro).toContainText(/by hand/);
  await expect(intro).toContainText(/restarts/);

  // The line it replaced said the opposite about the form below it.
  await expect(intro).not.toContainText(/changes apply on restart/i);
  await expect(intro).not.toContainText(/live reload lands later/i);
});

test("saving does not tell the user to restart quince", async ({ page }) => {
  await authenticate(page);
  await page.goto("/settings");

  // NOTHING IS EDITED FIRST, and that is the better test rather than a shortcut.
  //
  // The notice rendered on ANY successful save, so a save is all this needs — and a form submitted
  // unchanged PUTs the document it was handed, which leaves the demo exactly as it found it. That
  // makes this file non-mutating, so its position in the run stops mattering.
  //
  // THE ORIGINAL REASON FOR THIS IS NOW STALE, AND SAYING SO IS THE POINT. The first version changed
  // the theme and cost a 90s Playwright timeout, because `Field` rendered `<Label>` with no
  // `htmlFor` and `getByLabel("Theme")` matched nothing. **quince#629 fixed exactly that** — `Field`
  // now mints an id with `useId()` and the label carries `htmlFor` — and it merged as quince#672
  // while this branch was open, so `getByLabel` would work today.
  //
  // Not editing is kept anyway, on its own merits: reaching for a control couples this test to a
  // field this form has already reshuffled twice, and editing nothing is what makes the file
  // non-mutating. Recorded rather than quietly rewritten, because *"getByLabel does not work here"*
  // is a claim a later reader would otherwise carry forward, and it stopped being true.
  await page.getByRole("button", { name: /^save$/i }).click();

  // The confirmation renders only after a 200, so reaching it at all is the precondition.
  const saved = page.getByText(/^Saved$/);
  await expect(saved).toBeVisible();

  // THE STRING THAT WAS WRONG. It read "Saved · restart quince to apply", unconditionally, over a
  // form whose every field is live or unread. Asserted page-wide rather than on the span, because
  // the failure this guards against is the notice MOVING rather than surviving in place.
  await expect(page.getByText(/restart quince to apply/i)).toHaveCount(0);

  // AND NOT "Saved · applied" EITHER, which was the first draft and is a different lie:
  // `sessions.ttl_minutes` is read by nothing (quince#656), so a save of that field neither applies
  // nor fails — it lands in a file with no consumer. Trading a false restart promise for a false
  // apply promise is not a fix, and this assertion is what stops it being made later.
  await expect(page.getByText(/Saved · applied/i)).toHaveCount(0);
});
