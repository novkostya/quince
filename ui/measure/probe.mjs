// WHAT A RENDERED PAGE ACTUALLY USES — a typography/contrast/rhythm probe (quince#1155).
//
// Canon binds interface facts to a live lookup, and a type scale is exactly that kind of fact: a
// remembered number is a wrong number. This walks real pages in Chromium and reports what
// `getComputedStyle` says, so the proposal it feeds rests on measurements rather than on taste.
//
// SOURCE CSS IS NOT THE ANSWER AND THAT IS THE WHOLE REASON THIS EXISTS. A stylesheet declares
// `font-size: 0.875rem` in forty places; what a reader experiences is which of those forty covers
// most of the text on the screen. So every figure below is WEIGHTED BY RENDERED CHARACTER COUNT —
// the "body size" of a page is the size the majority of its prose is set at, not the size its
// `body` rule names, and on several measured sites those two differ.
//
// Run it through `ui/measure/run` (the pinned Playwright image, which is the same one the e2e gate
// uses). It writes one JSON document to stdout; `ui/measure/report.mjs` turns that into the table.

// `@playwright/test` rather than `playwright`: that is the package `ui/package.json` declares, and
// under pnpm's isolated store only a declared dependency is resolvable from here. It re-exports the
// same browser handles.
import { chromium, devices } from "@playwright/test";
import { readFileSync } from "node:fs";
import { pathToFileURL } from "node:url";

// `{NAME}` in a target's URL is read from the environment, and that is a PRIVACY mechanism as much
// as a convenience. quince's own address is one case; the others are admin UIs on a LAN, whose
// addresses are Operator-private and must never enter a committed file. A target whose placeholder
// is unset is dropped with a note rather than fetched against an empty string.
const targets = JSON.parse(readFileSync(new URL("./targets.json", import.meta.url), "utf8"))
  .map((t) => {
    const need = [...JSON.stringify(t).matchAll(/\{([A-Z_]+)\}/g)].map((m) => m[1]);
    const missing = need.filter((n) => !process.env[n]);
    if (missing.length) return { ...t, skip: `unset: ${[...new Set(missing)].join(", ")}` };
    let s = JSON.stringify(t);
    for (const n of new Set(need)) s = s.replaceAll(`{${n}}`, process.env[n]);
    return JSON.parse(s);
  });

// TWO PROFILES, BECAUSE ONE VIEWPORT CANNOT ANSWER THIS PRODUCT'S QUESTION. The first sweep ran at
// desktop width only and this comment said a phone pass was "a separate question" — which was wrong
// for quince specifically: `ui.design.md` makes the iPhone a first-class client, and a responsive
// app can carry two different type scales that a single-width survey averages into one wrong
// number. Several apps in the set raise their body size on a phone; measuring only the laptop hides
// that entirely.
//
// The phone profile is Playwright's own iPhone descriptor rather than a hand-set width: it carries
// the device scale factor, the touch flags and the Safari UA together, and sites serve different
// markup on the UA alone. A 390px-wide desktop Chrome is not a phone and would measure a
// desktop stylesheet at a narrow width.
const PROFILES = {
  desktop: { viewport: { width: 1440, height: 900 } },
  phone: { ...devices["iPhone 14 Pro"] },
};
const PROFILE = process.env.PROFILE || "desktop";
if (!PROFILES[PROFILE]) {
  process.stderr.write(`measure: unknown PROFILE '${PROFILE}' — expected desktop or phone\n`);
  process.exit(2);
}
const SETTLE_MS = 3500;

// ── colour maths ───────────────────────────────────────────────────────────────────────────────
// WCAG 2.x relative luminance + contrast. The ratios here are the FLOOR the issue names, not the
// goal — text can clear AA and still be the grey-on-grey being complained about — but a number is
// what makes "low contrast" arguable at all.

function parseColor(s) {
  const m = /rgba?\(([^)]+)\)/.exec(s || "");
  if (!m) return null;
  const p = m[1].split(/[,/\s]+/).filter(Boolean).map(Number);
  return { r: p[0], g: p[1], b: p[2], a: p.length > 3 ? p[3] : 1 };
}

function over(fg, bg) {
  // Composite a partly transparent colour onto an opaque one.
  const a = fg.a;
  return {
    r: fg.r * a + bg.r * (1 - a),
    g: fg.g * a + bg.g * (1 - a),
    b: fg.b * a + bg.b * (1 - a),
    a: 1,
  };
}

function luminance(c) {
  const f = (v) => {
    const x = v / 255;
    return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * f(c.r) + 0.7152 * f(c.g) + 0.0722 * f(c.b);
}

function contrast(a, b) {
  const l1 = luminance(a);
  const l2 = luminance(b);
  const [hi, lo] = l1 > l2 ? [l1, l2] : [l2, l1];
  return (hi + 0.05) / (lo + 0.05);
}

function hex(c) {
  const h = (v) => Math.round(v).toString(16).padStart(2, "0");
  return `#${h(c.r)}${h(c.g)}${h(c.b)}`;
}

// ── the in-page walk ───────────────────────────────────────────────────────────────────────────
// Runs in the browser. Returns raw per-run tallies; every ratio and percentile is computed on this
// side, where it can be read.

const collect = () => {
  // `width > 0` is not enough: a screen-reader-only label is a real 1×1 box with real computed
  // styles, and there are hundreds of them on a page like GitHub's. Eight pixels is below any
  // glyph and above every clip trick.
  const vis = (el, cs, rect) =>
    cs.display !== "none" &&
    cs.visibility !== "hidden" &&
    Number(cs.opacity) > 0.05 &&
    rect.width >= 8 &&
    rect.height >= 8 &&
    rect.left > -2000 &&
    rect.top > -5000;

  // The effective background of an element: walk up until something is opaque, compositing the
  // translucent layers on the way. A page that paints its card with `rgba(255,255,255,.04)` over a
  // near-black body is doing something a naive read of `background-color` reports as transparent.
  // A GRADIENT OR AN IMAGE IS NOT A COLOUR, AND PRETENDING OTHERWISE PRODUCES A RATIO OF EXACTLY
  // 1.0. Two sites in the first sweep reported that — text painted over a gradient hero, where
  // every ancestor's `background-color` is `transparent` and the paint comes from
  // `background-image`. There is no honest single number for that pair, so it is marked and left
  // out of the contrast tallies rather than counted as perfect or as broken.
  const bgOf = (el) => {
    const stack = [];
    let fromImage = false;
    let n = el;
    while (n && n.nodeType === 1) {
      const cs = getComputedStyle(n);
      if (cs.backgroundImage && cs.backgroundImage !== "none") fromImage = true;
      const c = cs.backgroundColor;
      const m = /rgba?\(([^)]+)\)/.exec(c);
      if (m) {
        const p = m[1].split(/[,/\s]+/).filter(Boolean).map(Number);
        const a = p.length > 3 ? p[3] : 1;
        if (a > 0) {
          stack.push({ r: p[0], g: p[1], b: p[2], a });
          if (a >= 0.999) break;
        }
      }
      n = n.parentElement;
    }
    // Nothing opaque anywhere means the canvas colour, which Chromium reports on <html>.
    if (!stack.length || stack[stack.length - 1].a < 0.999) stack.push({ r: 255, g: 255, b: 255, a: 1 });
    let out = stack.pop();
    while (stack.length) {
      const f = stack.pop();
      out = {
        r: f.r * f.a + out.r * (1 - f.a),
        g: f.g * f.a + out.g * (1 - f.a),
        b: f.b * f.a + out.b * (1 - f.a),
        a: 1,
      };
    }
    out.fromImage = fromImage;
    return out;
  };

  // SHADOW ROOTS ARE NOT OPTIONAL AND SKIPPING THEM IS NOT A SMALL ERROR. Home Assistant's whole
  // frontend is custom elements, and the first sweep reported "no rendered text found" for it — a
  // page with thousands of visible characters, measured as empty. A walker that stops at the shadow
  // boundary silently under-samples every web-component app in the set, which is a large part of the
  // self-hosted population quince is being compared against. Elements are gathered on the same pass,
  // because `document.querySelectorAll` does not pierce either.
  const textNodes = [];
  const elements = [];
  const descend = (root) => {
    const w = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT);
    for (let n = w.nextNode(); n; n = w.nextNode()) {
      if (n.nodeType === 3) textNodes.push(n);
      else {
        elements.push(n);
        if (n.shadowRoot) descend(n.shadowRoot);
      }
    }
  };
  descend(document.body);

  const runs = [];
  for (const t of textNodes) {
    const text = (t.nodeValue || "").replace(/\s+/g, " ").trim();
    if (text.length < 2) continue;
    const el = t.parentElement;
    if (!el) continue;
    const tag = el.tagName;
    if (tag === "SCRIPT" || tag === "STYLE" || tag === "NOSCRIPT" || tag === "TITLE") continue;
    const cs = getComputedStyle(el);
    const rect = el.getBoundingClientRect();
    if (!vis(el, cs, rect)) continue;
    // Icon fonts and single glyphs are not prose; they distort a character-weighted tally.
    if (/icon|material-symbols|fa-|glyphicon/i.test(cs.fontFamily) && text.length < 4) continue;
    const bg = bgOf(el);
    const lh =
      cs.lineHeight === "normal"
        ? Math.round(parseFloat(cs.fontSize) * 1.2 * 100) / 100
        : parseFloat(cs.lineHeight);
    runs.push({
      chars: text.length,
      size: Math.round(parseFloat(cs.fontSize) * 100) / 100,
      lineHeight: Math.round(lh * 100) / 100,
      weight: cs.fontWeight,
      color: cs.color,
      bg: `rgb(${Math.round(bg.r)}, ${Math.round(bg.g)}, ${Math.round(bg.b)})`,
      bgFromImage: bg.fromImage,
      tag,
      heading: /^H[1-6]$/.test(tag),
      // MONOSPACE IS A DIFFERENT POPULATION AND POOLING IT IS THE ONE MISTAKE THAT MOVES EVERY
      // NUMBER. A repository page is mostly code; a dashboard is mostly figures. Both are set
      // smaller than the prose around them on purpose, so a character-weighted "body size" that
      // includes them reports 12px for a site whose interface text is 14px.
      mono: /mono|consolas|courier|menlo|"SF Mono"|Roboto Mono/i.test(cs.fontFamily),
      family: cs.fontFamily.split(",")[0].replace(/["']/g, "").trim(),
      // The content-box width of the block this text sits in — the line length a reader gets. A
      // page's `main` is often a full-bleed wrapper, so the useful column is measured here, at the
      // text, rather than at a landmark.
      blockWidth:
        cs.display === "inline"
          ? null
          : Math.round(rect.width - parseFloat(cs.paddingLeft || 0) - parseFloat(cs.paddingRight || 0)),
      // MEASURE, IN CHARACTERS — the number typography is actually argued in, and a pixel width
      // cannot stand in for it. A 630px column is comfortable at 14px and long at 18px, so the two
      // populations' px medians say nothing on their own once a scale has moved. Counted from the
      // rendered box: how many lines this run wrapped into, divided into its characters.
      lines: cs.display === "inline" ? null : Math.max(1, Math.round(rect.height / lh)),
    });
  }

  // NON-TEXT CONTRAST, WHICH THIS PROBE DID NOT MEASURE AND SHOULD HAVE. Every ratio above is a
  // TEXT ratio, so a survey that came back clean still said nothing about whether a control can be
  // SEEN — and the first thing the Operator hit on the deployed build was a checkbox invisible
  // against the page. WCAG asks 3:1 for the boundary of a control, on the same footing as 4.5:1 for
  // body text, and nothing here was checking it.
  //
  // Sampled at the border of real interactive elements against what is actually behind them, which
  // is the boundary a user needs in order to find the control at all.
  const affordances = [];
  for (const el of elements) {
    if (!/^(INPUT|SELECT|TEXTAREA|BUTTON)$/.test(el.tagName)) continue;
    const cs = getComputedStyle(el);
    const r = el.getBoundingClientRect();
    if (!vis(el, cs, r)) continue;
    const bw = parseFloat(cs.borderTopWidth || 0);
    // `collect` runs INSIDE THE PAGE, so it can only use what it closes over — the module-scope
    // helpers above it do not exist here. Parsing the alpha inline is the fix; reaching for
    // `parseColor` was a `ReferenceError` that took out every target in a sweep.
    const fillM = /rgba?\(([^)]+)\)/.exec(cs.backgroundColor || "");
    const fillParts = fillM ? fillM[1].split(/[,/\s]+/).filter(Boolean).map(Number) : null;
    const fillAlpha = fillParts ? (fillParts.length > 3 ? fillParts[3] : 1) : 0;
    // A GHOST BUTTON HAS NO EDGE AND NO FILL, AND REPORTING 1.0:1 FOR IT IS A FALSE ALARM. Its
    // affordance is its own label, which the text tallies already cover — there is nothing here to
    // measure, so it says so rather than counting as the worst control on the page.
    if (bw === 0 && fillAlpha === 0) {
      affordances.push({ tag: el.tagName, type: el.getAttribute("type") || "", noEdge: true });
      continue;
    }
    // A UA-drawn control (`appearance: auto` with no border of ours) reports no useful colour — the
    // platform paints it. Recorded as such rather than silently skipped, because "the browser draws
    // it" is exactly the case that went wrong.
    const uaDrawn = bw === 0 && cs.appearance !== "none";
    const behind = el.parentElement ? bgOf(el.parentElement) : { r: 255, g: 255, b: 255 };
    affordances.push({
      tag: el.tagName,
      type: el.getAttribute("type") || "",
      uaDrawn,
      border: bw > 0 ? cs.borderTopColor : null,
      fill: cs.backgroundColor,
      behind: `rgb(${Math.round(behind.r)}, ${Math.round(behind.g)}, ${Math.round(behind.b)})`,
    });
  }

  // Control geometry. `height` from the box, not from a class name — a control padded to 40px by
  // its content is 40px to a thumb regardless of what the stylesheet says.
  const boxes = (sel) =>
    elements
      .filter((el) => {
        try {
          return el.matches(sel);
        } catch {
          return false;
        }
      })
      .map((el) => ({ el, cs: getComputedStyle(el), r: el.getBoundingClientRect() }))
      .filter(({ el, cs, r }) => vis(el, cs, r) && r.height > 8 && r.height < 200)
      .map(({ r }) => Math.round(r.height * 10) / 10);

  // Content column: the widest block that actually holds prose, which is what a line length is
  // measured against. Falls back to the viewport when a page has no main landmark.
  const findOne = (sel) =>
    elements.find((el) => {
      try {
        return el.matches(sel);
      } catch {
        return false;
      }
    });
  const mainEl = findOne("main") || findOne("[role=main]") || findOne("#main, .main, #content, .content") || document.body;
  const mainRect = mainEl.getBoundingClientRect();
  const mcs = getComputedStyle(mainEl);
  const contentWidth =
    Math.round(mainRect.width - parseFloat(mcs.paddingLeft || 0) - parseFloat(mcs.paddingRight || 0));

  // Vertical rhythm: the gap between consecutive top-level blocks of the main region. Margins
  // collapse and gaps do not, so this is measured from the rendered boxes rather than from the
  // declared margins — the only version of the number that is true.
  // DOCUMENT-WIDE, NOT THE LANDMARK'S CHILDREN. Reading only `main`'s direct children produced
  // n=3 across sixteen mainstream sites and n=0 for quince — most apps wrap their content in a
  // couple of full-height flex shells, so the "sections" are two levels down and differ per site.
  // Every parent holding three or more stacked blocks is a rhythm, so all of them are sampled and
  // the median is the page's answer.
  const rhythm = [];
  for (const parent of elements) {
    const kids = [...parent.children]
      .map((el) => ({ el, cs: getComputedStyle(el), r: el.getBoundingClientRect() }))
      .filter(({ el, cs, r }) => vis(el, cs, r) && r.height >= 24 && cs.display !== "inline");
    if (kids.length < 3) continue;
    // Stacked, not side by side: a row of cards has no vertical rhythm to report.
    for (let i = 1; i < kids.length; i++) {
      if (kids[i].r.top < kids[i - 1].r.bottom - 2) continue;
      const g = Math.round((kids[i].r.top - kids[i - 1].r.bottom) * 10) / 10;
      if (g >= 0 && g < 160) rhythm.push(g);
    }
  }

  // The padding a page puts inside its own containers — the "clamped together" complaint measured
  // rather than described. Sampled from every block that holds at least two stacked children, so
  // it describes cards and panels rather than the one-off outer shell.
  const pads = [];
  for (const el of elements) {
    const cs = getComputedStyle(el);
    const r = el.getBoundingClientRect();
    if (!vis(el, cs, r) || r.height < 48 || r.width < 120) continue;
    const kids = [...el.children].filter((k) => k.getBoundingClientRect().height > 8);
    if (kids.length < 2) continue;
    const pl = parseFloat(cs.paddingLeft || 0);
    const pt = parseFloat(cs.paddingTop || 0);
    if (pl > 0 && pl < 80) pads.push(Math.round(pl * 10) / 10);
    if (pt > 0 && pt < 80) pads.push(Math.round(pt * 10) / 10);
  }

  return {
    runs,
    rootFontSize: parseFloat(getComputedStyle(document.documentElement).fontSize),
    bodyFontSize: parseFloat(getComputedStyle(document.body).fontSize),
    bodyBg: getComputedStyle(document.body).backgroundColor,
    buttons: boxes("button, [role=button], a[class*=btn], .btn"),
    inputs: boxes("input:not([type=hidden]):not([type=checkbox]):not([type=radio]), select, textarea"),
    rows: boxes("tbody tr, [role=row], li[class], [class*=list-item], [class*=ListItem]"),
    contentWidth,
    mainTag: mainEl.tagName + (mainEl.id ? `#${mainEl.id}` : ""),
    rhythm,
    affordances,
    pads,
    title: document.title,
    url: location.href,
  };
};

// ── aggregation, on this side where it is readable ─────────────────────────────────────────────

const pct = (sorted, p) => {
  if (!sorted.length) return null;
  const i = Math.min(sorted.length - 1, Math.max(0, Math.round((p / 100) * (sorted.length - 1))));
  return sorted[i];
};

function summarise(raw) {
  const all = raw.runs;
  const total = all.reduce((a, r) => a + r.chars, 0);
  if (!total) return { error: "no rendered text found" };

  // Prose is what the complaint is about. Monospace is measured too, and separately.
  const prose = all.filter((r) => !r.mono);
  const mono = all.filter((r) => r.mono);
  const runs = prose.length ? prose : all;

  // Character-weighted size histogram. Headings are separated out: they are a real part of the
  // scale but they are 1% of the characters, so pooling them hides both.
  const bySize = new Map();
  for (const r of runs) {
    const k = r.size;
    const e = bySize.get(k) || { size: k, chars: 0, runs: 0, lh: 0, headingChars: 0 };
    e.chars += r.chars;
    e.runs += 1;
    e.lh += r.lineHeight * r.chars;
    if (r.heading) e.headingChars += r.chars;
    bySize.set(k, e);
  }
  const proseTotal = runs.reduce((a, r) => a + r.chars, 0);
  const sizes = [...bySize.values()]
    .map((e) => ({
      size: e.size,
      share: Math.round((e.chars / proseTotal) * 1000) / 10,
      lineHeight: Math.round((e.lh / e.chars) * 100) / 100,
      ratio: Math.round((e.lh / e.chars / e.size) * 100) / 100,
    }))
    .sort((a, b) => b.share - a.share);

  const bodySize = sizes[0].size;
  const bodyLine = sizes[0].lineHeight;

  // The scale a reader can actually see: any step carrying ≥1.5% of the page's characters. The
  // threshold exists so that one stray 11px legal line does not become "a step in the scale".
  const scale = sizes
    .filter((s) => s.share >= 1.5)
    .map((s) => s.size)
    .sort((a, b) => a - b);

  // Colour pairs, character-weighted. The primary is the pair carrying the most text; the "muted"
  // is the next distinct foreground on the same background — which is precisely the grey-on-grey
  // the complaint is about, so it gets its own ratio rather than being averaged in.
  const colourable = all.filter((r) => !r.bgFromImage);
  const colourTotal = colourable.reduce((a, r) => a + r.chars, 0) || 1;
  const byPair = new Map();
  for (const r of colourable) {
    const k = `${r.color}|${r.bg}`;
    const e = byPair.get(k) || { color: r.color, bg: r.bg, chars: 0, sizes: [] };
    e.chars += r.chars;
    e.sizes.push(r.size);
    byPair.set(k, e);
  }
  const pairs = [...byPair.values()]
    .map((e) => {
      const fg = parseColor(e.color);
      const bg = parseColor(e.bg);
      if (!fg || !bg) return null;
      const flat = fg.a < 1 ? over(fg, bg) : fg;
      return {
        fg: hex(flat),
        bg: hex(bg),
        share: Math.round((e.chars / colourTotal) * 1000) / 10,
        chars: e.chars,
        contrast: Math.round(contrast(flat, bg) * 100) / 100,
        medianSize: pct(e.sizes.slice().sort((a, b) => a - b), 50),
      };
    })
    .filter(Boolean)
    .sort((a, b) => b.share - a.share);

  const primary = pairs[0];
  // The SECOND foreground role — quince calls it `--fg-muted` — is the grey-on-grey the complaint
  // names, so it gets its own ratio rather than being averaged into a page mean. Same background
  // where one exists, because a different surface is a different question.
  const secondary =
    pairs.find((p) => p !== primary && p.bg === primary.bg && p.fg !== primary.fg && p.share >= 1) ||
    pairs.find((p) => p !== primary && p.fg !== primary.fg && p.share >= 1) ||
    null;

  // THE DISTRIBUTION, WHICH IS WHAT THE ISSUE ASKED FOR AND WHAT A SINGLE RATIO HIDES. A page can
  // have a 17:1 headline and set two thirds of its text at 3.9:1; reporting only the primary pair
  // would call that page high-contrast. `shareBelow` is the fraction of rendered characters under
  // each threshold — 4.5 is WCAG AA for body text, 7 is AAA, and the issue is explicit that AA is
  // the floor rather than the goal.
  const bandShare = (limit) =>
    Math.round((pairs.filter((p) => p.contrast < limit).reduce((a, p) => a + p.chars, 0) / colourTotal) * 1000) / 10;
  const weightedContrast =
    Math.round((pairs.reduce((a, p) => a + p.contrast * p.chars, 0) / colourTotal) * 100) / 100;
  // The worst ratio that still carries real text. A 2% floor keeps one disabled placeholder from
  // standing in for the page.
  const worstSignificant = pairs
    .filter((p) => p.share >= 2)
    .reduce((w, p) => (w === null || p.contrast < w.contrast ? p : w), null);

  const sortNum = (a) => a.slice().sort((x, y) => x - y);
  const btn = sortNum(raw.buttons);
  const inp = sortNum(raw.inputs);
  const row = sortNum(raw.rows);
  const rhy = sortNum(raw.rhythm.filter((g) => g > 0));
  // The measured line length: the width of the blocks that actually hold sentences.
  const cols = sortNum(runs.filter((r) => r.chars >= 80 && r.blockWidth > 40).map((r) => r.blockWidth));
  // Characters per line, over blocks that actually wrapped — a run that fits on one line says
  // nothing about the measure, because it stopped short of the column rather than filling it.
  const perLine = sortNum(
    runs.filter((r) => r.lines >= 2 && r.chars >= 60).map((r) => Math.round(r.chars / r.lines)),
  );

  // MONOSPACE BESIDE PROSE IS AN OPTICAL PROBLEM, NOT A METRIC ONE. At one nominal size a
  // monospace face reads smaller and lighter than the prose it is set into, which is why most
  // design systems step it down explicitly or not at all — deliberately, either way. Reported as a
  // RATIO to the page's own body size, so it is comparable across sites that disagree about the
  // body size itself.
  const monoInline = mono.filter((r) => r.chars < 40);
  const monoRatio = monoInline.length
    ? Math.round((pct(sortNum(monoInline.map((r) => r.size)), 50) / bodySize) * 100) / 100
    : null;

  const monoSizes = mono.length
    ? (() => {
        const m = new Map();
        for (const r of mono) m.set(r.size, (m.get(r.size) || 0) + r.chars);
        return [...m.entries()].sort((a, b) => b[1] - a[1])[0][0];
      })()
    : null;

  return {
    title: raw.title,
    finalUrl: raw.url,
    rootFontSize: raw.rootFontSize,
    declaredBodyFontSize: raw.bodyFontSize,
    bodySize,
    bodyLineHeight: bodyLine,
    bodyLineRatio: Math.round((bodyLine / bodySize) * 100) / 100,
    scale,
    sizeShares: sizes.slice(0, 10),
    charsPerLineMedian: pct(perLine, 50),
    charsPerLineP90: pct(perLine, 90),
    monoToBodyRatio: monoRatio,
    // Controls whose boundary is below WCAG's 3:1 for non-text, and controls the PLATFORM draws
    // (where our tokens reach nothing and the answer depends on the engine — quince#645's shape).
    affordances: (raw.affordances || []).map((a) => {
      const control = `${a.tag}${a.type ? `[${a.type}]` : ""}`;
      if (a.noEdge) return { control, noEdge: true, contrast: null };
      const behind = parseColor(a.behind);
      // TWO MECHANISMS, MEASURED SEPARATELY, BECAUSE THEY ARE ALTERNATIVES RATHER THAN A PAIR. A
      // control can be found either by a visible EDGE or by a FILL that differs from the surface it
      // sits on, and WCAG 1.4.11 is satisfied by whichever one is doing the work. Collapsing them
      // into one number — as this did, with `border || fill` — cannot tell an app that draws strong
      // borders from one that draws none and tints the field instead, which is exactly the choice
      // quince has to make.
      const rate = (c) => {
        const p = parseColor(c);
        if (!p || !behind) return null;
        const flat = p.a < 1 ? over(p, behind) : p;
        return Math.round(contrast(flat, behind) * 100) / 100;
      };
      const borderContrast = rate(a.border);
      const fillContrast = rate(a.fill);
      return {
        control,
        uaDrawn: a.uaDrawn,
        borderContrast,
        fillContrast,
        // What the control is actually found BY: the stronger of the two.
        contrast: Math.max(borderContrast ?? 0, fillContrast ?? 0) || null,
        foundBy:
          (borderContrast ?? 0) >= (fillContrast ?? 0) ? (borderContrast ? "edge" : null) : "fill",
      };
    }),
    monoSize: monoSizes,
    monoShare: Math.round((mono.reduce((a, r) => a + r.chars, 0) / total) * 1000) / 10,
    primary,
    secondary,
    secondaryContrast: secondary ? secondary.contrast : null,
    contrastWeightedMean: weightedContrast,
    contrastWorstSignificant: worstSignificant,
    shareBelow3: bandShare(3),
    shareBelow4_5: bandShare(4.5),
    shareBelow7: bandShare(7),
    topPairs: pairs.slice(0, 6).map(({ chars, ...p }) => p),
    contentWidth: pct(cols, 50) ?? raw.contentWidth,
    contentWidthP90: pct(cols, 90),
    landmarkWidth: raw.contentWidth,
    mainTag: raw.mainTag,
    buttonHeightMedian: pct(btn, 50),
    buttonHeightP90: pct(btn, 90),
    buttonCount: btn.length,
    inputHeightMedian: pct(inp, 50),
    inputCount: inp.length,
    rowHeightMedian: pct(row, 50),
    rowCount: row.length,
    sectionGapMedian: pct(rhy, 50),
    sectionGapP75: pct(rhy, 75),
    containerPadMedian: pct(sortNum(raw.pads || []), 50),
    containerPadP75: pct(sortNum(raw.pads || []), 75),
    charsMeasured: total,
    runsMeasured: raw.runs.length,
    // WHETHER A ROW IS STRONG ENOUGH TO POOL IS THE REPORT'S CALL, NOT THIS FILE'S. The probe
    // records how much it saw — `charsMeasured` and `runsMeasured` above — and stops there. The
    // first version of this decided here, on a character floor, and that floor threw out most of
    // the self-hosted population: an admin UI is genuinely sparse in prose, which is a fact about
    // that population rather than a defect in the measurement of it.
    bgUnresolvedShare:
      Math.round((all.filter((r) => r.bgFromImage).reduce((a, r) => a + r.chars, 0) / total) * 1000) / 10,
  };
}

// EXPORTED SO `validate.mjs` CAN ASSERT THEM AGAINST DECLARED GROUND TRUTH, and imported rather
// than copied on purpose: a validator carrying its own reimplementation of this logic proves the
// copy correct and nothing else.
export { collect, summarise };

// ── driver ─────────────────────────────────────────────────────────────────────────────────────
// Guarded, so importing this module does not launch a browser and start sweeping the open web.

// Only when RUN, never when imported. `process.argv[1]` is the entry script; comparing it to this
// module's own URL is the ESM form of a main-module check.
const RUN_AS_SCRIPT = import.meta.url === pathToFileURL(process.argv[1]).href;
if (RUN_AS_SCRIPT) {
const only = process.argv.slice(2).filter((a) => !a.startsWith("-"));

const browser = await chromium.launch({ args: ["--ignore-certificate-errors"] });
const out = {
  measuredAt: new Date().toISOString(),
  profile: PROFILE,
  viewport: PROFILES[PROFILE].viewport,
  results: [],
};

for (const t of targets) {
  if (only.length && !only.some((o) => t.id.includes(o))) continue;
  if (t.skip) {
    out.results.push({ id: t.id, category: t.category, name: t.name, error: `not run — ${t.skip}` });
    process.stderr.write(`skip  ${t.id.padEnd(28)} ${t.skip}\n`);
    continue;
  }
  const started = Date.now();
  const ctx = await browser.newContext({
    ...PROFILES[PROFILE],
    colorScheme: t.scheme || "dark",
    ignoreHTTPSErrors: true,
    // The desktop profile needs a real UA string of its own: several of these sites serve a
    // stripped page to an obvious bot, and a stripped page is a measurement of the wrong document.
    // The phone profile brings its own Safari UA, which is the whole point of using the descriptor.
    ...(PROFILE === "desktop"
      ? {
          userAgent:
            "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
        }
      : {}),
    locale: "en-US",
  });
  const page = await ctx.newPage();
  const rec = { id: t.id, category: t.category, name: t.name, url: t.url, scheme: t.scheme || "dark" };
  const runSteps = async (steps) => {
    for (const step of steps || []) {
      try {
        // `.first()` ON EVERY SELECTOR, DELIBERATELY. Playwright is strict by default and a
        // selector matching two elements raises the SAME timeout as one matching none — so a login
        // form with two text inputs read as "the field is not there", and four demo instances were
        // recorded as thin pages when they had simply never been signed into. Driving a page onto
        // the surface being measured is not the assertion here; the measurement is.
        if (step.goto) await page.goto(step.goto, { waitUntil: "domcontentloaded", timeout: 45000 });
        if (step.click) await page.locator(step.click).first().click({ timeout: 8000 });
        if (step.fill) await page.locator(step.fill[0]).first().fill(step.fill[1], { timeout: 8000 });
        if (step.press) await page.locator(step.press[0]).first().press(step.press[1], { timeout: 8000 });
        if (step.wait) await page.waitForTimeout(step.wait);
        if (step.waitFor) await page.waitForSelector(step.waitFor, { timeout: 20000 });
      } catch (e) {
        rec.stepWarnings = [...(rec.stepWarnings || []), `${JSON.stringify(step)}: ${e.message.split("\n")[0]}`];
      }
    }
  };

  try {
    // `pre` is the sign-in leg: quince's own surfaces sit behind a session, and a login page is not
    // the surface being complained about. Every other target has none and skips it.
    if (t.pre) {
      await page.goto(t.pre.url, { waitUntil: "domcontentloaded", timeout: 45000 });
      await runSteps(t.pre.steps);
    }
    await page.goto(t.url, { waitUntil: "domcontentloaded", timeout: 45000 });
    await runSteps(t.steps);
    await page.waitForTimeout(t.settle ?? SETTLE_MS);
    // A SCROLL, THEN BACK. Half the comparison set renders below-the-fold content only once it has
    // been scrolled to, and the first sweep measured 150 characters of a page that holds thousands.
    // Back to the top afterwards, so the geometry below is read from the same layout every time.
    await page.evaluate(async () => {
      const step = Math.round(window.innerHeight * 0.9);
      for (let y = step; y < Math.min(document.body.scrollHeight, step * 6); y += step) {
        window.scrollTo(0, y);
        await new Promise((r) => setTimeout(r, 400));
      }
      window.scrollTo(0, 0);
      await new Promise((r) => setTimeout(r, 800));
    });
    await page.waitForTimeout(1500);
    const raw = await page.evaluate(collect);
    Object.assign(rec, summarise(raw));
    rec.elapsedMs = Date.now() - started;
  } catch (e) {
    // A target that did not render is recorded as unmeasured, never dropped: an absent row and a
    // failed row say different things, and only one of them is honest.
    rec.error = e.message.split("\n")[0];
  }
  await ctx.close();
  process.stderr.write(
    `${rec.error ? "FAIL" : " ok "}  ${t.id.padEnd(24)} ${
      rec.error ||
      `body=${rec.bodySize}px lh=${rec.bodyLineHeight} scale=[${rec.scale}] c1=${rec.primary?.contrast} c2=${rec.secondaryContrast} <4.5=${rec.shareBelow4_5}% col=${rec.contentWidth} btn=${rec.buttonHeightMedian} chars=${rec.charsMeasured}`
    }\n`,
  );
  out.results.push(rec);
}

await browser.close();

// THE RESULT FILE IS COMMITTED, SO THE SUBSTITUTION IS UNDONE BEFORE IT IS WRITTEN. A target that
// came from `{PVE_URL}` records a LAN address in `url` and again in `finalUrl` after redirects, and
// privacy is a commit-time gate rather than a docs rule — putting the placeholder back is the only
// version of this that cannot be forgotten by whoever runs it next.
let text = JSON.stringify(out, null, 2);
for (const name of ["QUINCE_BASE", "PVE_URL", "LUCI_URL", "KUMA_URL", "COCKPIT_URL"]) {
  const v = process.env[name];
  if (v) text = text.replaceAll(v, `{${name}}`);
}
process.stdout.write(text);
}
