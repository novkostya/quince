import { test, expect } from "@playwright/test";

// qn.10 slice 7d, story 4 — CAN A BROWSER ACTUALLY DRAW WHAT THE FILE ROUTE SERVES?
//
// WHY THIS IS A GATE AND NOT A UNIT TEST. `handleSessionFile` serves every file as
// `application/octet-stream`, with `Content-Disposition: attachment` and, from `securityHeaders`,
// `X-Content-Type-Options: nosniff`. Pointing an <img> at that asks a browser to decode bytes
// declared as a non-image type by a server that has said "do not guess". Whether it obliges is a
// fact about browsers, and jsdom cannot answer it: it never fetches `src`, never implements
// nosniff, and never fires `onError` for this reason. So `Attachment.test.tsx` proves quince
// CHOOSES the <img> branch and can prove nothing about what happens next.
//
// AND THE FAILURE WOULD BE INVISIBLE. `Attachment`'s `onError` fallback swaps a failed image for a
// named link — right for HEIC, and identical to what a TOTAL failure looks like: every attachment
// silently becomes a link, every unit test still passes, and no screen or log says the inline half
// never worked. That is the degraded mode *no silent caps* forbids, so it gets a gate that fails
// loudly instead (quince#1521 review).
//
// MEASURED 2026-08-23 across chromium, firefox and webkit: all three decode it. This gate runs
// chromium only, because that is the engine the suite is configured for; the other two were a
// one-off measurement recorded in `qn.10` D6 rather than a standing check.
//
// ITS PREMISE IS THOSE THREE HEADERS. If `handleSessionFile` ever changes them, this test is
// asserting something the product no longer does — the Go side pins the content type
// (`handlers_vault_test.go`), and this pins what a browser makes of it.

// A 1×1 PNG. Small enough to inline, real enough to decode.
const PNG = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
  "base64",
);

async function decodes(page: import("@playwright/test").Page, headers: Record<string, string>) {
  await page.route("**/probe-image", (route) => route.fulfill({ status: 200, headers, body: PNG }));
  await page.setContent(`<img id="p" src="/probe-image">`);
  await page.waitForTimeout(1000);
  return page.evaluate(() => {
    const i = document.getElementById("p") as HTMLImageElement;
    return i.complete && i.naturalWidth > 0;
  });
}

test("an image served exactly as the file route serves it still decodes in an <img>", async ({ page }) => {
  await page.goto("/");

  // THE CONTROL FIRST. Without it, a `false` below is indistinguishable from a broken probe —
  // and this whole test exists because a plausible-looking negative is easy to get here.
  expect(await decodes(page, { "Content-Type": "image/png" })).toBe(true);

  // The production triple, copied from handleSessionFile + securityHeaders.
  const asServed = await decodes(page, {
    "Content-Type": "application/octet-stream",
    "X-Content-Type-Options": "nosniff",
    "Content-Disposition": 'attachment; filename="IMG_0001.JPG"',
  });
  expect(
    asServed,
    "an <img> no longer decodes what handleSessionFile serves — inline attachments are silently " +
      "falling back to links for every format, not just HEIC (qn.10 D6)",
  ).toBe(true);
});
