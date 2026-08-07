import { expect, test, type Page } from "@playwright/test";

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
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
}

// qn.6e G11 — the add flow end to end, and G10's `+ Add storage` half.
//
// THE PROBE RUNS AGAINST A REAL DIRECTORY INSIDE THE DEMO CONTAINER, not a fabricated response.
// qn.6d's requirement is inherited verbatim: *a fixture that fabricates a value the live code never
// produces makes its gate a lie.* `--demo` fabricates its STORAGE LIST, but `storage.Inspect` is the
// live code path here, so the report a user sees is genuinely produced by the daemon statting a
// path. `/tmp` exists in the container and is writable, which is all the probe needs.
test("Add storage sits at the foot of Storage, probes a real path, and reports the daemon's own reason", async ({
  page,
}) => {
  await authenticate(page);

  // G10's add half: the button is at the foot of its own section, below the cards.
  const add = page.getByTestId("add-storage");
  await expect(add).toBeVisible();
  const storageHeading = page.getByRole("heading", { name: "Storage", level: 2 });
  const headingBox = await storageHeading.boundingBox();
  const addBox = await add.boundingBox();
  expect(headingBox).not.toBeNull();
  expect(addBox).not.toBeNull();
  expect(addBox!.y).toBeGreaterThan(headingBox!.y);

  await add.click();
  await expect(page.getByRole("heading", { name: /add a storage/i })).toBeVisible();

  // Nothing is offered before a probe: the form is probe-first, so there is nothing to save yet.
  await expect(page.getByTestId("add-storage-save")).toBeDisabled();

  await page.getByLabel("Path").fill("/tmp");
  await page.getByTestId("probe-check").click();

  // THE RECOMMENDATION CARRIES THE DAEMON'S OWN SENTENCE, which names the path it probed
  // (quince#514). Asserted on the path appearing in the reason rather than on a fixed string,
  // because the backend the demo container's /tmp resolves to depends on its filesystem — and
  // pinning that would make this gate assert the CI box rather than the product.
  const select = page.getByTestId("backend-select");
  await expect(select).toBeVisible();
  await expect(page.getByText(/\/tmp/).first()).toBeVisible();
  await expect(page.getByTestId("add-storage-save")).toBeEnabled();
});

// A REFUSAL ARRIVES AS AN ANSWER, in the same place as a success — which is the whole reason the
// endpoint returns 200 for it (contracts §1). The user sees the daemon's sentence, beside the same
// field, and the save stays disabled.
test("a path that does not exist is answered, not errored, and cannot be saved", async ({ page }) => {
  await authenticate(page);

  await page.getByTestId("add-storage").click();
  await page.getByLabel("Path").fill("/definitely-not-here");
  await page.getByTestId("probe-check").click();

  const refusal = page.getByTestId("probe-refusal");
  await expect(refusal).toBeVisible();
  await expect(refusal).toContainText("/definitely-not-here");
  // The sentence that is the whole point of this branch: quince cannot fix a disk that was never
  // mounted in, and this is the only moment the user is looking.
  await expect(refusal).toContainText(/inside the container/i);

  await expect(page.getByTestId("backend-select")).toHaveCount(0);
  await expect(page.getByTestId("add-storage-save")).toBeDisabled();
});

// THE TIER-3 CONSTRAINT, asserted where it can actually be violated: the rendered page. `zfs: none`
// means NO SIGNAL, and in hook mode a negative reading is a guaranteed false negative for the
// supported containerised topology — so the product must never say ZFS is unavailable.
test("nothing in the add flow claims ZFS is unsupported", async ({ page }) => {
  await authenticate(page);

  await page.getByTestId("add-storage").click();
  await page.getByLabel("Path").fill("/tmp");
  await page.getByTestId("probe-check").click();
  await expect(page.getByTestId("backend-select")).toBeVisible();

  const body = (await page.textContent("body")) ?? "";
  for (const banned of [/zfs (is )?not supported/i, /zfs unsupported/i, /zfs (is )?unavailable/i]) {
    expect(body).not.toMatch(banned);
  }
});

// qn.6e PR 6b — the zfs branch and `Test helper`.
//
// THE HELPER'S FOUR OUTCOMES ARE GATED IN GO, against the REAL quince-zfs-helper script extracted
// from deploy/storage.md (G8). What is proven HERE is the branch's own claims: that the mode is not
// offered as a choice, that a zfs storage cannot be saved before the helper has answered, and that
// the daemon's sentence reaches the user.
//
// The demo container has no ZFS and no helper, so the outcome reachable here is `unreachable` — and
// that is the honest one to gate on: it is what a user sees before they have installed anything,
// which is the moment this button exists for.
test("a zfs storage cannot be saved until the helper has answered", async ({ page }) => {
  await authenticate(page);

  await page.getByTestId("add-storage").click();
  await page.getByLabel("Path").fill("/tmp");
  await page.getByTestId("probe-check").click();
  await expect(page.getByTestId("backend-select")).toBeVisible();

  await page.getByTestId("backend-select").selectOption("zfs");
  await expect(page.getByTestId("zfs-fields")).toBeVisible();
  await expect(page.getByTestId("add-storage-save")).toBeDisabled();

  // `Test helper` is itself unavailable until there is something to test — quince will not fire a
  // command at nothing.
  await expect(page.getByTestId("test-helper")).toBeDisabled();

  await page.getByLabel("Parent dataset").fill("pool/backups");
  await page.getByLabel("Helper command").fill("/nonexistent/ssh");
  await expect(page.getByTestId("test-helper")).toBeEnabled();

  // STILL not saveable: the fields are filled and the helper has not answered.
  await expect(page.getByTestId("add-storage-save")).toBeDisabled();

  await page.getByTestId("test-helper").click();
  const result = page.getByTestId("hook-result");
  await expect(result).toBeVisible();
  await expect(result).toHaveAttribute("data-outcome", "unreachable");
  // The daemon's own remedy, not a client rewording of it.
  await expect(result).toContainText(/key|forced command|host/i);

  // An unreachable helper means the storage would fail at commit time, so the save stays shut.
  await expect(page.getByTestId("add-storage-save")).toBeDisabled();
});

// THE MODE IS NOT OFFERED AS A CHOICE, and the form says why rather than hiding it. `exec` runs
// `zfs` inside the container and the runtime image does not contain it (quince#697), so an `exec`
// option would be offering something that cannot work.
test("the zfs branch explains the helper instead of offering a mode nobody can use", async ({
  page,
}) => {
  await authenticate(page);

  await page.getByTestId("add-storage").click();
  await page.getByLabel("Path").fill("/tmp");
  await page.getByTestId("probe-check").click();
  await page.getByTestId("backend-select").selectOption("zfs");

  const fields = page.getByTestId("zfs-fields");
  await expect(fields).toContainText(/can't run .*zfs.* from inside its container/i);
  await expect(fields).toContainText(/that's normal/i);
  // No mode selector anywhere: the choice does not exist, so neither does the control.
  await expect(page.getByLabel(/mode/i)).toHaveCount(0);
});

// A REPO PATH PRINTED AT A USER IS A DEAD END DRESSED AS HELP. Someone running quince from a
// container image may have no checkout at all, so `deploy/storage.md` as bare text points at a file
// they cannot open. Every docs reference in the add flow is a real link.
//
// Operator-reported from a screenshot of the shipped 6a form, which printed it twice as plain text.
test("docs references in the add flow are links, not bare repo paths", async ({ page }) => {
  await authenticate(page);

  await page.getByTestId("add-storage").click();
  await page.getByLabel("Path").fill("/tmp");
  await page.getByTestId("probe-check").click();
  await page.getByTestId("backend-select").selectOption("zfs");

  const dialog = page.getByRole("dialog");
  await expect(dialog).toContainText("deploy/storage.md");

  // EVERY occurrence is inside an anchor, asserted by counting rather than by finding one — a
  // single linked instance beside an unlinked one is the exact state this test exists to catch.
  const mentions = await dialog.getByText("deploy/storage.md").count();
  const links = await dialog.locator('a[href*="deploy/storage.md"]').count();
  expect(links).toBe(mentions);
  expect(links).toBeGreaterThan(0);

  const href = await dialog.locator('a[href*="deploy/storage.md"]').first().getAttribute("href");
  expect(href).toMatch(/^https:\/\/github\.com\/novkostya\/quince\/blob\/main\//);
});

// qn.6e PR 9b — THE FIRST-RUN STORAGE STEP.
//
// The daemon half is gated in Go (quince#710): with no storage declared quince serves and refuses
// every API outside setup. What is gated HERE is the client's half — that a storageless install is
// sent to a PAGE, and that the page runs the same form as the dialog rather than a second copy.
//
// `/api/config` IS INTERCEPTED, and that is legitimate rather than a fabricated fixture: the state
// under test belongs to the CLIENT — where does it route, and what does it render — and an empty
// `storage` list is exactly what the live daemon returns on a first run. The demo has storages by
// construction, so intercepting is the only way to reach the branch at all; the alternative is a
// gate that never runs.
test("a storageless install is sent to the first-run storage page", async ({ page }) => {
  await authenticate(page);

  await page.route("**/api/config", async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    body.config.storage = [];
    await route.fulfill({ response: res, json: body });
  });

  await page.goto("/");
  await page.waitForURL(/\/onboarding\/storage/);

  await expect(page.getByRole("heading", { name: /add your first storage/i })).toBeVisible();

  // THE SAME FORM, not a second copy — the probe, its three branches and the helper check all come
  // from `AddStorageForm`. Asserted by driving it here: a divergent copy would not answer.
  await page.getByLabel("Path").fill("/tmp");
  await page.getByTestId("probe-check").click();
  await expect(page.getByTestId("backend-select")).toBeVisible();
  await expect(page.getByTestId("add-storage-save")).toBeEnabled();

  // AND THERE IS NO WAY OUT BUT FORWARD. A cancel would return the user to a Home that cannot
  // render against a daemon refusing every API outside setup.
  await expect(page.getByRole("button", { name: /^cancel$/i })).toHaveCount(0);
  // It is a page, not a modal: nothing to dismiss, and no dialog role in the tree.
  await expect(page.getByRole("dialog")).toHaveCount(0);
});

// A CONFIGURED INSTALL IS NOT SENT THERE, which is the assertion that stops the gate above from
// passing on a redirect that always fires.
test("an install with a storage stays on Home", async ({ page }) => {
  await authenticate(page);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
  expect(page.url()).not.toContain("/onboarding/storage");
});
