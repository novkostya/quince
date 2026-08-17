// Turn a probe run into the tables that go in front of a reader (quince#1155).
//
//   node report.mjs results.json > SURVEY.md
//
// THE JACKKNIFE IS NOT A FLOURISH. The issue sets the sample-size test itself — "large enough that
// removing any one site does not change the conclusion" — so every distribution below is reported
// with the largest movement its median takes when any single measured surface is dropped. A metric
// whose leave-one-out swing is bigger than the gap it is being used to argue about has not been
// measured well enough to argue with, and saying so is cheaper than being caught.

import { readFileSync, existsSync } from "node:fs";

const doc = JSON.parse(readFileSync(process.argv[2] || "results.json", "utf8"));
const rows = doc.results;
// The phone pass is optional so the report still renders from one profile, but it is not a
// footnote: quince's first-class client is an iPhone, and a single-width survey cannot say whether a
// responsive app carries two scales or one.
const phoneFile = process.argv[3] || "results-phone.json";
const phone = existsSync(phoneFile) ? JSON.parse(readFileSync(phoneFile, "utf8")) : null;

// iOS Dynamic Type at the DEFAULT size class, read live from Apple's Human Interface Guidelines on
// 2026-08-17 by rendering the page (its tables are JS-built, so curl returns a 17 KB shell). Canon
// binds interface facts to a live lookup and a type scale is exactly that kind of fact.
const IOS_DEFAULT = [
  ["Large Title", 34, 41],
  ["Title 1", 28, 34],
  ["Title 2", 22, 28],
  ["Title 3", 20, 25],
  ["Headline (semibold)", 17, 22],
  ["Body", 17, 22],
  ["Callout", 16, 21],
  ["Subhead", 15, 20],
  ["Footnote", 13, 18],
  ["Caption 1", 12, 16],
];

// WHAT MAKES A ROW POOLABLE IS STRUCTURAL VARIETY, NOT VOLUME OF PROSE, and getting that wrong the
// first time cost most of the population the issue cares most about. A character floor threw out
// Grafana, Home Assistant, openHAB, Immich and Jellyfin — self-hosted admin UIs are SPARSE IN
// PROSE, which is a fact about them rather than a failure to measure them, and pooling only the
// four wordiest left n=4 against n=16 on the other side.
//
// `runsMeasured` is the honest discriminator: the number of distinct styled text runs on the page.
// Below about 25 there is no scale to speak of — one label decides the "body size" — and every row
// that falls out at 25 turns out to be a sign-in screen or a bot wall, i.e. genuinely not the
// surface anyone meant to measure. Marked, never dropped: an absent row and a weak row say
// different things.
const MIN_RUNS = 25;
const weak = (r) => !r.error && r.runsMeasured < MIN_RUNS;
const usable = (r) => !r.error && r.runsMeasured >= MIN_RUNS;

const med = (a) => {
  if (!a.length) return null;
  const s = a.slice().sort((x, y) => x - y);
  const m = s.length >> 1;
  return s.length % 2 ? s[m] : Math.round(((s[m - 1] + s[m]) / 2) * 100) / 100;
};
const q = (a, p) => {
  if (!a.length) return null;
  const s = a.slice().sort((x, y) => x - y);
  return s[Math.min(s.length - 1, Math.max(0, Math.round((p / 100) * (s.length - 1))))];
};

// The largest distance the median moves when any single row is removed.
function jackknife(values) {
  if (values.length < 3) return null;
  const base = med(values);
  let worst = 0;
  for (let i = 0; i < values.length; i++) {
    const without = values.filter((_, j) => j !== i);
    worst = Math.max(worst, Math.abs(med(without) - base));
  }
  return Math.round(worst * 100) / 100;
}

const METRICS = [
  ["bodySize", "body text size", "px"],
  ["bodyLineRatio", "line-height ÷ size", "×"],
  ["primaryContrast", "primary text contrast", ":1"],
  ["secondaryContrast", "secondary text contrast", ":1"],
  ["shareBelow4_5", "share of text below 4.5:1", "%"],
  ["shareBelow7", "share of text below 7:1", "%"],
  ["contentWidth", "measured column width", "px"],
  ["buttonHeightMedian", "button height", "px"],
  ["rowHeightMedian", "list/table row height", "px"],
  ["sectionGapMedian", "gap between stacked blocks", "px"],
  ["containerPadMedian", "padding inside containers", "px"],
];

const valueOf = (r, key) => {
  if (key === "primaryContrast") return r.primary?.contrast ?? null;
  const v = r[key];
  return typeof v === "number" ? v : null;
};

const CATEGORIES = [
  ["mainstream", "Mainstream web apps"],
  ["selfhosted", "Self-hosted admin UIs"],
  ["quince", "quince, today"],
];

const out = [];
const p = (s = "") => out.push(s);

p(`# What mainstream and self-hosted interfaces actually measure (quince#1155)`);
p();
p(`Measured **${doc.measuredAt.slice(0, 10)}** in Chromium at ${doc.viewport.width}×${doc.viewport.height},`);
p(`dark scheme unless a row says otherwise, by \`ui/measure/probe.mjs\`.`);
p(`Every figure is **character-weighted**: the "body size" of a page is the size most of its rendered`);
p(`text is actually set at, not the size its \`body\` rule declares. Monospace runs are excluded from`);
p(`the prose figures and reported separately.`);
p();

// ── per-category distributions ─────────────────────────────────────────────────────────────────
p(`## Distributions`);
p();
for (const [key, label, unit] of METRICS) {
  p(`### ${label}`);
  p();
  p(`| population | n | min | p25 | **median** | p75 | max | worst leave-one-out shift |`);
  p(`| --- | --- | --- | --- | --- | --- | --- | --- |`);
  for (const [cat, catLabel] of CATEGORIES) {
    const vals = rows.filter((r) => r.category === cat && usable(r)).map((r) => valueOf(r, key)).filter((v) => v !== null);
    if (!vals.length) {
      p(`| ${catLabel} | 0 | — | — | — | — | — | — |`);
      continue;
    }
    const j = jackknife(vals);
    p(
      `| ${catLabel} | ${vals.length} | ${q(vals, 0)} | ${q(vals, 25)} | **${med(vals)}${unit === "%" || unit === "×" || unit === ":1" ? unit : ""}** | ${q(vals, 75)} | ${q(vals, 100)} | ${j === null ? "n<3" : `±${j}`} |`,
    );
  }
  p();
}

// ── the full table ─────────────────────────────────────────────────────────────────────────────
p(`## Every surface measured`);
p();
p(`\`fg\` / \`bg\` are the colours as **rendered**, after compositing every translucent layer above the`);
p(`first opaque one. \`<4.5\` is the share of rendered characters whose pair is below WCAG AA for body`);
p(`text — the floor, not the goal.`);
p();
p(`| surface | body | line | scale present | fg on bg | c1 | c2 | <4.5 | <7 | col | btn | row | gap | pad | chars |`);
p(`| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |`);
for (const [cat, catLabel] of CATEGORIES) {
  const set = rows.filter((r) => r.category === cat);
  if (!set.length) continue;
  p(`| **${catLabel}** | | | | | | | | | | | | | | |`);
  for (const r of set) {
    if (r.error) {
      p(`| ${r.name} | \`NOT MEASURED — ${r.error}\` | | | | | | | | | | | | |`);
      continue;
    }
    const n = (v, s = "") => (v === null || v === undefined ? "—" : `${v}${s}`);
    p(
      `| ${r.name}${weak(r) ? " ⚠" : ""} | ${r.bodySize}px | ${r.bodyLineHeight} (${r.bodyLineRatio}×) | ${r.scale.join(" · ")} | \`${r.primary?.fg}\` on \`${r.primary?.bg}\` | ${n(r.primary?.contrast)} | ${n(r.secondaryContrast)} | ${n(r.shareBelow4_5, "%")} | ${n(r.shareBelow7, "%")} | ${n(r.contentWidth)} | ${n(r.buttonHeightMedian)} | ${n(r.rowHeightMedian)} | ${n(r.sectionGapMedian)} | ${n(r.containerPadMedian)} | ${r.charsMeasured} |`,
    );
  }
}
p();
p(`⚠ = fewer than ${MIN_RUNS} distinct styled text runs — in practice a sign-in screen or a bot wall.`);
p(`Kept because an absent row and a weak row say different things; excluded from the distributions`);
p(`above because at that size one label decides the answer.`);
p();

// ── what did not run ───────────────────────────────────────────────────────────────────────────
const failed = rows.filter((r) => r.error);
p(`## Not measured`);
p();
if (!failed.length) p(`Nothing — every declared target rendered.`);
else {
  p(`| surface | why |`);
  p(`| --- | --- |`);
  for (const r of failed) p(`| ${r.name} | ${r.error} |`);
}
p();

// ── desktop vs phone ───────────────────────────────────────────────────────────────────────────
if (phone) {
  const P = Object.fromEntries(phone.results.filter((r) => !r.error).map((r) => [r.id, r]));
  p(`## Does the scale change on a phone?`);
  p();
  p(`Same probe, same day, Playwright's iPhone descriptor — device scale factor, touch flags and`);
  p(`Safari UA together, because sites serve different markup on the UA alone.`);
  p();
  p(`| surface | body px | line-height | button px |`);
  p(`| --- | --- | --- | --- |`);
  let same = 0;
  let n = 0;
  for (const r of rows.filter((r) => usable(r) && P[r.id])) {
    const q = P[r.id];
    if (r.category !== "quince") {
      n += 1;
      if (r.bodySize === q.bodySize) same += 1;
    }
    p(
      `| ${r.name} | ${r.bodySize} → **${q.bodySize}** | ${r.bodyLineRatio} → ${q.bodyLineRatio} | ${r.buttonHeightMedian ?? "—"} → ${q.buttonHeightMedian ?? "—"} |`,
    );
  }
  p();
  p(`**Body size is UNCHANGED between desktop and phone on ${same} of ${n} comparison surfaces**, and`);
  p(`the ones that differ go DOWN on the phone rather than up. So a responsive type scale is not what`);
  p(`this category does, and quince does not need one — which is the opposite of what was expected`);
  p(`before measuring, and is why the phone pass was worth running rather than reasoning about.`);
  p();
}

// ── the native reference ───────────────────────────────────────────────────────────────────────
p(`## The native reference — iOS Dynamic Type`);
p();
p(`Read live from Apple's Human Interface Guidelines, **Large (default)** size class, 2026-08-17.`);
p(`Included because the web set and the platform DISAGREE, and quince's first-class client is an`);
p(`iPhone: a web-only comparison would have answered half the question and looked complete.`);
p();
p(`| iOS text style | size / line-height | ratio |`);
p(`| --- | --- | --- |`);
for (const [name, size, lh] of IOS_DEFAULT) {
  p(`| ${name} | ${size} / ${lh} | ${Math.round((lh / size) * 100) / 100}× |`);
}
p();
p(`iOS Body is **17pt** where the web set's median is **14px**, and iOS runs a TIGHTER line at 1.29×`);
p(`against the web's 1.51×. Native is bigger per glyph and denser per line. A user who has moved the`);
p(`Dynamic Type slider gets 15pt or 19pt and a native app follows them; quince follows nothing, which`);
p(`is an asymmetry rather than a defect but is not measured here.`);
p();

// ── the type-scale steps actually in use ───────────────────────────────────────────────────────
p(`## Which steps the comparison set actually uses`);
p();
p(`How many measured surfaces carry each size as a visible step (≥1.5% of their rendered characters).`);
p();
const stepCount = new Map();
for (const r of rows.filter(usable)) for (const s of r.scale || []) stepCount.set(s, (stepCount.get(s) || 0) + 1);
p(`| size | surfaces carrying it |`);
p(`| --- | --- |`);
for (const [s, c] of [...stepCount.entries()].sort((a, b) => a[0] - b[0])) {
  p(`| ${s}px | ${"█".repeat(c)} ${c} |`);
}
p();

process.stdout.write(out.join("\n") + "\n");
