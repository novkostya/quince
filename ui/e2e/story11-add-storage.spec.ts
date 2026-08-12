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

  // IT IS A LINK, NOT A DIALOG TRIGGER (quince#846) — so it has a URL, and the URL is the claim.
  await expect(add).toHaveAttribute("href", "/storage/new");
  await add.click();
  await expect(page).toHaveURL(/\/storage\/new$/);
  await expect(page.getByRole("heading", { name: /add a storage/i, level: 1 })).toBeVisible();
  // NOTHING TO DISMISS. The dismissal is why the surface moved: quince#818 writes an SSH keypair to
  // disk partway through this flow, and the dialog had no `onPointerDownOutside` or
  // `onEscapeKeyDown` guard, so an outside tap discarded it silently.
  await expect(page.getByRole("dialog")).toHaveCount(0);

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

  // BOTH FIELDS, because `Test helper` needs the whole transport (quince#818). It asked for one
  // command line until the Operator ruled SSH the only shape; quince composes the argv now, so what
  // is typed is the two things only the operator knows.
  //
  // `.invalid` IS RESERVED AND NEVER RESOLVES (RFC 2606), which is what keeps this fast. The old
  // fixture was `/nonexistent/ssh` — an exec failure, instant. Composing means `ssh` really runs, so
  // an unreachable host now costs a DNS lookup rather than nothing, and a plausible-looking hostname
  // here would spend the check's 20-second bound twice before the suite saw an answer.
  await page.getByLabel("Parent dataset").fill("pool/backups");
  await page.getByLabel("ZFS host").fill("zfshost.invalid");
  await expect(page.getByTestId("test-helper")).toBeDisabled();
  await page.getByLabel("Remote user").fill("zfsuser");
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

  // SCOPED TO THE SHELL'S CONTENT REGION, which is the job `getByRole("dialog")` was doing until
  // quince#846: bound the count to this surface, so a docs link elsewhere in the app cannot satisfy
  // it. `<main>` is the outlet the router renders into; the sidebar and top bar are outside it.
  const surface = page.locator("main");
  await expect(surface).toContainText("deploy/storage.md");

  // EVERY occurrence is inside an anchor, asserted by counting rather than by finding one — a
  // single linked instance beside an unlinked one is the exact state this test exists to catch.
  const mentions = await surface.getByText("deploy/storage.md").count();
  const links = await surface.locator('a[href*="deploy/storage.md"]').count();
  expect(links).toBe(mentions);
  expect(links).toBeGreaterThan(0);

  const href = await surface.locator('a[href*="deploy/storage.md"]').first().getAttribute("href");
  expect(href).toMatch(/^https:\/\/github\.com\/novkostya\/quince\/blob\/main\//);
});

// qn.6e PR 9b — THE FIRST-RUN STORAGE STEP.
//
// The daemon half is gated in Go (quince#710): with no storage declared quince serves and refuses
// every API outside setup. What is gated HERE is the client's half — that a storageless install is
// sent to a PAGE, and that the page runs the same form as the dialog rather than a second copy.
//
// `/api/config` IS INTERCEPTED, and that is legitimate rather than a fabricated fixture: the state
// under test belongs to the CLIENT — where does it route, and what does it render.
//
// BOTH SHAPES ARE DRIVEN, and the first version of this test drove only one. `Config.storage` is a
// POINTER server-side, so an ABSENT key serialises as `null` and an emptied list as `[]`. A fresh
// install — the case this whole page exists for — is `null`, and the intercept fabricated `[]`:
// the one shape that made the test pass. It went green over a guard that ignored `null`, and an
// Operator on a real stand walked straight onto Home.
//
// That is qn.6d's rule turned on its author — *a fixture that fabricates a value the live code
// never produces makes its gate a lie* — quoted approvingly in the PR that added this test.
//
// The demo has storages by construction, so intercepting is the only way to reach the branch at
// all; the alternative is a gate that never runs.
for (const empty of [null, []]) {
  const shape = empty === null ? "null (a fresh install: no `storage:` key)" : "[] (a list emptied by hand)";
  test(`a storageless install is sent to the first-run storage page — storage: ${shape}`, async ({ page }) => {
    await authenticate(page);
  
    await page.route("**/api/config", async (route) => {
      const res = await route.fetch();
      const body = await res.json();
        body.config.storage = empty;
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
}

// A CONFIGURED INSTALL IS NOT SENT THERE, which is the assertion that stops the gate above from
// passing on a redirect that always fires.
test("an install with a storage stays on Home", async ({ page }) => {
  await authenticate(page);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
  expect(page.url()).not.toContain("/onboarding/storage");
});

// THE ADD MUST LAND YOU ON HOME, and this is the assertion that was missing when the redirect loop
// shipped. Operator-reported: "added my first storage but still on /onboarding/storage".
//
// The loop was a CACHE ordering bug, not a routing one: `invalidateQueries` marks the config stale
// and refetches, but `RequireStorage` mounts on `/` and react-query hands it the CACHED pre-add
// value synchronously — `storage: null` — so it bounced straight back. The form had already reset,
// so from the outside nothing had happened.
//
// It is gated end to end rather than at the guard, because every part in isolation was already
// correct: the POST succeeded, the guard's predicate was right, the navigate fired. Only the ORDER
// was wrong, and order is only visible in the whole flow.
test("adding the first storage lands on Home and does not bounce back", async ({ page }) => {
  await authenticate(page);

  // A first run as the daemon actually serves it: `storage: null`, and the add returns the new
  // document. The route stays installed after the POST, so a subsequent GET would still answer
  // `null` — which is what makes this a real test of the ordering rather than of a refetch.
  let added = false;
  await page.route("**/api/config", async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    if (!added) body.config.storage = null;
    await route.fulfill({ response: res, json: body });
  });
  await page.route("**/api/config/storage", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    added = true;
    // The shape the real endpoint returns: the config-endpoint body, carrying the NEW list.
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        config: { storage: [{ name: "first", path: "/tmp", default: true, backend: "copy" }] },
        warnings: [],
        source: { path: "/data/config.yml", mtime: new Date().toISOString() },
      }),
    });
  });

  await page.goto("/");
  await page.waitForURL(/\/onboarding\/storage/);

  await page.getByLabel("Path").fill("/tmp");
  await page.getByTestId("probe-check").click();
  await expect(page.getByTestId("add-storage-save")).toBeEnabled();
  await page.getByTestId("add-storage-save").click();

  // ON HOME, and STILL on Home — a bounce would show up as a URL that comes back.
  await page.waitForURL((u) => !u.pathname.startsWith("/onboarding"), { timeout: 10_000 });
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
  await page.waitForTimeout(500);
  expect(page.url()).not.toContain("/onboarding/storage");
});

// quince#846 — THE SURFACE IS A PAGE, AND THESE ARE THE THREE THINGS THAT BUYS. Each was either
// impossible in a dialog or was the dialog's defect.
//
// The defect is the first one. `dialog.tsx` and the old `AddStorage.tsx` set no
// `onPointerDownOutside` and no `onEscapeKeyDown`, so Radix's defaults applied and an outside tap
// or `Escape` discarded five filled fields silently. After quince#818 the same tap would leave an
// SSH keypair under `/data/keys/` that no storage references — a different class of loss, and the
// reason this landed first.
test("the add surface cannot be dismissed, back cancels it, and the next visit is a clean sheet", async ({
  page,
}) => {
  await authenticate(page);

  await page.getByTestId("add-storage").click();
  await page.getByLabel("Path").fill("/tmp");
  await page.getByTestId("probe-check").click();
  await expect(page.getByTestId("backend-select")).toBeVisible();

  // ESCAPE DOES NOTHING, which is the whole point. On the dialog this discarded everything.
  await page.keyboard.press("Escape");
  await expect(page).toHaveURL(/\/storage\/new$/);
  await expect(page.getByTestId("backend-select")).toBeVisible();

  // BACK IS THE CANCEL — offered as a visible link, because a desktop user has no back gesture.
  // Scoped to `main`: the shell's sidebar carries its own Home link, and the claim here is that
  // THIS PAGE offers the way out, the way `StorageDetailsPage` and `DeviceDetailsPage` both do.
  await expect(page.getByRole("main").getByRole("link", { name: /^home$/i })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();

  // A CLEAN SHEET ON THE NEXT VISIT. The dialog got this from remounting on open; a page does not
  // get it for free, and a stale probe result attached to a field the user is about to retype is
  // worse than an empty form.
  await page.getByTestId("add-storage").click();
  await expect(page).toHaveURL(/\/storage\/new$/);
  await expect(page.getByLabel("Path")).toHaveValue("");
  await expect(page.getByTestId("backend-select")).toHaveCount(0);
});

// SUCCESS LANDS ON THE NEW STORAGE, not on Home — the dialog closed and left the user to find the
// new card themselves.
//
// THE NAME COMES FROM THE RESPONSE, and the fixture proves it rather than allowing it: the returned
// entry is named `shuttle` while its path is `/tmp`, so a client that assumed name-equals-path, or
// that guessed from what it typed, would route somewhere else. `name` defaults to `path` at config
// LOAD (quince#504) and this document went through one, so the field is always populated — the
// difference is only visible when the two disagree, which is why they do here.
//
// `/api/config/storage` IS INTERCEPTED for the same reason the first-run tests intercept
// `/api/config`: the state under test belongs to the CLIENT — where does it route — and `--demo`
// serves a fabricated storage list, so a real add could not be followed to a details page anyway.
// What that page RENDERS is story 7's; this asserts only where the user is put.
test("a successful add lands on the new storage's own page, named from the daemon's answer", async ({
  page,
}) => {
  await authenticate(page);

  await page.route("**/api/config/storage", async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    await route.fulfill({
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({
        config: { storage: [{ name: "shuttle", path: "/tmp", default: true, backend: "copy" }] },
        warnings: [],
        source: { path: "/data/config.yml", mtime: new Date().toISOString() },
        file_text: "",
      }),
    });
  });

  await page.getByTestId("add-storage").click();
  await page.getByLabel("Path").fill("/tmp");
  await page.getByTestId("probe-check").click();
  await expect(page.getByTestId("add-storage-save")).toBeEnabled();
  await page.getByTestId("add-storage-save").click();

  await expect(page).toHaveURL(/\/storage\/shuttle$/);

  // AND BACK FROM THERE GOES HOME, not into a re-armed form for a storage that now exists — which
  // is what the add page's `replace` is for.
  await page.goBack();
  await expect(page.getByRole("heading", { name: "Home", level: 1 })).toBeVisible();
});

// quince#849 — THE FIRST-RUN SCREEN MUST NOT TELL AN OPERATOR THEY HAVE NO STORAGE WHEN THEIR OWN
// FILE DECLARES ONE.
//
// Measured on a real container (quince#817): a `config.yml` carrying the retired `zfs.mode: exec` is
// DISCARDED at load, so quince runs on defaults with no storage, `RequireStorage` routes here, and
// this page said *"quince needs somewhere to keep backups"* — about an install whose file declares
// somewhere. `ConfigView` is the only other component that renders `warnings`, and it lives behind
// `RequireStorage`, which is the guard this state fails: the one surface that could explain the
// problem sat behind the gate the problem closes.
//
// THE HEADLINE IS INTERIM AND THE TEST SAYS SO. `GET /api/config` carries `warnings` but not
// `Loaded.Errors`, so a client cannot separate a discarded config from one that merely parsed with
// an ignored unknown key — and those want opposite headlines. The bit is ruled and pending
// (quince#849, a contracts addition). What is asserted here is the half that does not depend on it:
// the false claim is gone, and the daemon's own path, message and remedy are on screen.
test("a config quince could not use is explained, not reported as 'you have no storage'", async ({
  page,
}) => {
  await authenticate(page);

  await page.route("**/api/config", async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    body.config.storage = null;
    body.warnings = [
      { path: "storage[0].zfs.mode", message: 'invalid value: invalid value "exec"; must be one of [hook]' },
    ];
    body.file_text = "storage:\n  - path: /backups\n    backend: zfs\n    zfs:\n      mode: exec\n";
    // THE FATALITY, which is what makes this the DISCARDED case rather than a merely-warned one
    // (quince#849). Without it this fixture becomes the state in the test below, where
    // "Add your first storage" is the CORRECT headline.
    body.discarded = true;
    await route.fulfill({ response: res, json: body });
  });

  await page.goto("/");
  await page.waitForURL(/\/onboarding\/storage/);

  // THE FALSE CLAIM IS GONE. This is the assertion the issue was filed for.
  await expect(page.getByRole("heading", { name: /add your first storage/i })).toHaveCount(0);
  await expect(page.getByText(/needs somewhere to keep backups before/i)).toHaveCount(0);

  // AND THE SHARPER CLAIM THE BIT BUYS — not "there is a problem", but that nothing is being backed
  // up. That is the fact the operator came to this screen with, and the whole reason a boolean was
  // worth a contracts change.
  await expect(page.getByRole("heading", { name: /could not read your configuration/i })).toBeVisible();
  await expect(page.getByText(/no backups are being made/i)).toBeVisible();

  // AND THE DAEMON'S OWN SENTENCE IS ON SCREEN — path and message, not a client rewording.
  const warnings = page.getByTestId("config-warnings");
  await expect(warnings).toBeVisible();
  await expect(warnings).toContainText("storage[0].zfs.mode");
  await expect(warnings).toContainText("must be one of [hook]");

  // A REMEDY THE OPERATOR CAN FOLLOW (qn.6g) — the file to edit, and the restart, which is not
  // optional because there is no reload path (quince#727).
  await expect(page.getByText(/restart quince/i)).toBeVisible();

  // THE FILE ITSELF, which is where the offending line actually lives.
  await page.getByText(/show the file quince read/i).click();
  await expect(page.getByText("mode: exec")).toBeVisible();

  // AND THE FORM IS STILL THERE. It is not a trap since quince#857 refuses the write and names the
  // line; removing it would also break the ordinary first-run path, which is this same screen.
  await expect(page.getByTestId("add-storage-save")).toBeVisible();
});

// THE ORDINARY FIRST RUN IS UNCHANGED, which is what stops the test above from passing on a screen
// that has simply lost its heading. No warnings — the overwhelmingly common case — still reads as
// the first-run step it is.
test("a clean first run still says 'Add your first storage'", async ({ page }) => {
  await authenticate(page);

  await page.route("**/api/config", async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    body.config.storage = null;
    body.warnings = [];
    await route.fulfill({ response: res, json: body });
  });

  await page.goto("/");
  await page.waitForURL(/\/onboarding\/storage/);

  await expect(page.getByRole("heading", { name: /add your first storage/i })).toBeVisible();
  await expect(page.getByTestId("config-warnings")).toHaveCount(0);
});

// quince#849 — THE STATE THE BIT EXISTS TO SEPARATE, and the one the interim headline got wrong.
//
// A config that PARSED with a warning — an unknown key, which contracts §6 makes a warning and never
// an error — keeps its storage through the load. On a fresh install that is somebody who has never
// added a storage AND has a typo, and the correct headline is the first-run one.
//
// Before `discarded` this was indistinguishable on the wire from a discarded config, so the screen
// said something true of both. This is the assertion that the distinction is real: same non-empty
// `warnings`, opposite headline, and the warning still rendered rather than hidden behind the
// headline's branch.
test("a config that merely carries warnings still reads as first run, with the warning shown", async ({
  page,
}) => {
  await authenticate(page);

  await page.route("**/api/config", async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    body.config.storage = null;
    body.warnings = [{ path: "totally_unknown_key", message: 'unknown config key "totally_unknown_key" (ignored)' }];
    body.file_text = "totally_unknown_key: 1\n";
    // NOT discarded — quince read the file, kept it, and ignored one key.
    body.discarded = false;
    await route.fulfill({ response: res, json: body });
  });

  await page.goto("/");
  await page.waitForURL(/\/onboarding\/storage/);

  // THE FIRST-RUN HEADLINE IS CORRECT HERE, and claiming the config could not be read would be the
  // same state-honesty defect pointed the other way.
  await expect(page.getByRole("heading", { name: /add your first storage/i })).toBeVisible();
  await expect(page.getByRole("heading", { name: /could not read your configuration/i })).toHaveCount(0);
  await expect(page.getByText(/no backups are being made/i)).toHaveCount(0);

  // AND THE WARNING IS STILL SHOWN. The headline branches on the fatality; the detail renders
  // either way. Hiding it here would be a second place for the same signal to go unseen, which the
  // ruling names explicitly.
  const warnings = page.getByTestId("config-warnings");
  await expect(warnings).toBeVisible();
  await expect(warnings).toContainText("totally_unknown_key");

  // The form is the point of this page in this state, so it is there and unqualified.
  await expect(page.getByTestId("add-storage-save")).toBeVisible();
});
