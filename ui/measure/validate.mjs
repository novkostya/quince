// DOES THE PROBE MEASURE THE RIGHT THING? — the question re-running cannot answer (quince#1155).
//
// A second sweep proves CONSISTENCY. A probe that is consistently wrong reproduces perfectly, so
// reproducibility is strong evidence about a harness without being evidence that it measures what it
// claims. This runs `probe.mjs` against `fixture.html`, where every number is declared, and asserts
// the reported figures against the arithmetic.
//
//   node validate.mjs        # exit 0 all assertions hold · 1 a claim is wrong · 2 could not run
//
// It imports the probe's OWN `collect` and `summarise` rather than reimplementing them. A validator
// with its own copy of the logic proves the copy correct and nothing else.

import { chromium } from "@playwright/test";
import { collect, summarise } from "./probe.mjs";

const failures = [];
const checks = [];

function eq(label, got, want, tolerance = 0) {
  const ok =
    typeof want === "number" && typeof got === "number"
      ? Math.abs(got - want) <= tolerance
      : got === want;
  checks.push(`${ok ? "ok  " : "FAIL"}  ${label.padEnd(62)} got=${got} want=${want}`);
  if (!ok) failures.push(label);
}

const browser = await chromium.launch();
const page = await browser.newPage({ viewport: { width: 1440, height: 900 }, colorScheme: "dark" });
await page.goto(new URL("./fixture.html", import.meta.url).href, { waitUntil: "load" });
await page.waitForTimeout(500);
const s = summarise(await page.evaluate(collect));
await browser.close();

// ── the central method ─────────────────────────────────────────────────────────────────────────
// 900 characters at 14px in ONE element against 144 characters at 24px across TWELVE. An
// element-counting probe reports 24px here; a character-weighting one reports 14px. This is the
// claim every figure in the survey rests on.
eq("body size is the size most CHARACTERS are set at, not most elements", s.bodySize, 14);
eq("its line-height comes with it", s.bodyLineHeight, 21);
eq("and the ratio is derived, not declared", s.bodyLineRatio, 1.5);

// ── what must not be counted ───────────────────────────────────────────────────────────────────
// If any of these leaked in, the body size would move: the off-screen block is 40px, the icon 48px.
eq("a screen-reader-only 1x1 box is not rendered text", s.scale.includes(40), false);
eq("an off-screen block is not rendered text", s.sizeShares.some((x) => x.size === 40), false);
eq("an icon glyph is not prose", s.sizeShares.some((x) => x.size === 48), false);

// ── monospace is a separate population ─────────────────────────────────────────────────────────
// The fixture sets code LARGER than prose, so pooling it would drag the body size up and show here.
eq("monospace is reported separately", s.monoSize, 20);
eq("and is excluded from the prose scale", s.scale.includes(20), false);

// ── compositing ────────────────────────────────────────────────────────────────────────────────
// #9aa1ab on rgba(255,255,255,.06) over #0b0d10. The blend is `0.06*255 + 0.94*bg` per channel:
//
//   r  15.3 + 0.94*11 = 25.64  -> 26 = 0x1a
//   g  15.3 + 0.94*13 = 27.52  -> 28 = 0x1c
//   b  15.3 + 0.94*16 = 30.34  -> 30 = 0x1e      => #1a1c1e
//
// Then WCAG relative luminance on that pair: L(#9aa1ab) = 0.35297, L(#1a1c1e) = 0.011442, so the
// ratio is (0.35297 + 0.05) / (0.011442 + 0.05) = 6.558.
//
// THE ROUNDING STEP IS WHY THIS ASSERTION EXISTS AT ALL. Its first version expected #191b1e — the
// same three sums, floored instead of rounded — and the validator failed against a probe that was
// correct. That is the check doing its job in the direction nobody plans for: it disagreed with the
// author, not with the code. The arithmetic is written out here so the expectation is DERIVED and
// can be re-checked by hand, rather than copied back out of the probe's own output, which would
// assert only that the probe agrees with itself.
const composited = s.topPairs.find((p) => p.fg === "#9aa1ab");
eq("a translucent card is composited, not read off one ancestor", composited?.bg, "#1a1c1e");
eq("so the contrast is the blended one", composited?.contrast, 6.56, 0.01);

// ── shadow DOM ─────────────────────────────────────────────────────────────────────────────────
// 120 characters inside a shadow root. Without piercing, this page measures 120 characters short —
// which is how a whole application once measured as empty.
eq("text inside a shadow root is counted", s.charsMeasured >= 1200, true);

process.stdout.write(checks.join("\n") + "\n");
if (failures.length) {
  process.stdout.write(`\nvalidate: ${failures.length} FAILED — the probe does not measure what it claims\n`);
  process.exit(1);
}
process.stdout.write(`\nvalidate: ${checks.length} assertions hold against declared ground truth\n`);
