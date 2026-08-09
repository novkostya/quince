// A READOUT OF WHAT iOS IS ACTUALLY REPORTING, BECAUSE FOUR EXPLANATIONS HAVE NOW BEEN PRODUCED
// FROM SOURCE AND THREE WERE WRONG.
//
// quince#762 has cost: an overflow theory (withdrawn), an unexplained-position theory (it was
// `top-4`, two days old), a clamp measured against a height iOS also shrinks, and a one-frame
// "keyboard closed" transient inferred from a scrollbar that turned out to be the Operator
// scrolling. Every one of them was a plausible reading of real evidence, and every one was checked
// against a browser that has no keyboard and no notch.
//
// This is the instrument that ends that. It is OFF unless the URL carries `?vvdebug`, it renders
// outside React with `pointer-events: none` so it cannot influence what it measures, and it keeps a
// rolling log of the last few viewport events — so ONE screenshot taken after a jump carries the
// sequence that produced it, rather than a single frozen value.
//
// TEMPORARY. This is a diagnostic, not a feature: it exists to answer one question on one device and
// should be dropped once quince#762 is closed, in its own commit, so removing it cannot disturb the
// fix. It ships inert either way — the flag is absent from every link quince produces.
const LOG_LINES = 9;

// HOW YOU TURN IT ON INSIDE A HOME-SCREEN APP, WHICH HAS NO ADDRESS BAR.
//
// The URL flag is fine in Safari and useless in the installed PWA: there is nowhere to type, and the
// manifest's `start_url` is `/`, so iOS launches the icon at `/` and drops any query string that was
// present when it was added. Safari also reports every `env(safe-area-inset-*)` as 0, so the
// questions that are actually left — what `innerHeight` and `scrollY` do in standalone, and what the
// real insets are — CANNOT be answered from Safari at all. The instrument has to be reachable from
// the installed app or it cannot measure the thing it was built for.
//
// So: FIVE TAPS IN THE TOP-LEFT CORNER, within three seconds. No UI, no route, no component touched,
// and it lives and dies with this file. It is also remembered, so a reload or a navigation inside the
// app does not lose it — tap five times again to turn it off.
const CORNER = 64; // px square in the top-left
const TAPS = 5;
const WINDOW_MS = 3000;
const REMEMBERED = "quince.vvdebug";

export function initViewportDebug(): () => void {
  const armCorner = (): void => {
    let count = 0;
    let first = 0;
    document.addEventListener(
      "pointerdown",
      (e) => {
        if (e.clientX > CORNER || e.clientY > CORNER) {
          count = 0;
          return;
        }
        const now = e.timeStamp;
        if (count === 0 || now - first > WINDOW_MS) {
          count = 1;
          first = now;
          return;
        }
        if (++count < TAPS) return;
        count = 0;
        const on = localStorage.getItem(REMEMBERED) === "1";
        localStorage.setItem(REMEMBERED, on ? "0" : "1");
        location.reload();
      },
      true,
    );
  };
  armCorner();

  const flagged =
    typeof location !== "undefined" && new URLSearchParams(location.search).has("vvdebug");
  let remembered = false;
  try {
    remembered = localStorage.getItem(REMEMBERED) === "1";
  } catch {
    // Private mode or a blocked store — the URL flag still works.
  }
  if (flagged) {
    try {
      localStorage.setItem(REMEMBERED, "1");
    } catch {
      // Not being able to remember it is not a reason to refuse to show it.
    }
  }
  if (!flagged && !remembered) return () => {};

  const vv = window.visualViewport;
  if (!vv) return () => {};

  const box = document.createElement("div");
  box.setAttribute("data-viewport-debug", "");
  box.style.cssText = [
    "position:fixed",
    "left:0",
    "top:0",
    "z-index:2147483647",
    "pointer-events:none",
    "font:9px/1.25 ui-monospace,monospace",
    "white-space:pre",
    "color:#7CFF9B",
    "background:rgba(0,0,0,.78)",
    "padding:2px 4px",
    "max-width:100vw",
  ].join(";");
  document.body.appendChild(box);

  const started = performance.now();
  const log: string[] = [];
  const note = (kind: string): void => {
    const t = Math.round(performance.now() - started);
    log.push(`${String(t).padStart(5)} ${kind} h=${Math.round(vv.height)} top=${Math.round(vv.offsetTop)}`);
    if (log.length > LOG_LINES) log.shift();
  };

  const css = (name: string): string =>
    getComputedStyle(document.documentElement).getPropertyValue(name).trim() || "-";

  const paint = (): void => {
    const card = document.querySelector('[role="dialog"]');
    const r = card?.getBoundingClientRect();
    box.textContent = [
      `vv   h=${Math.round(vv.height)} top=${Math.round(vv.offsetTop)} scale=${vv.scale.toFixed(2)}`,
      `win  inner=${window.innerHeight} client=${document.documentElement.clientHeight} scrollY=${Math.round(window.scrollY)}`,
      `css  --vv-top=${css("--vv-top")} --vv-height=${css("--vv-height")}`,
      `safe top=${css("--safe-top")} bottom=${css("--safe-bottom")}`,
      `card ${r ? `y=${Math.round(r.top)} h=${Math.round(r.height)}` : "none"}`,
      ...log,
    ].join("\n");
  };

  const onResize = (): void => {
    note("resize");
    paint();
  };
  const onScroll = (): void => {
    note("scroll");
    paint();
  };

  // FOCUS IS LOGGED TOO, because the keyboard's next/previous chevrons stopped moving between fields
  // and nothing in the source says why (quince#762). The taps register — the chevrons highlight — and
  // focus stays put. This distinguishes the three possibilities that look identical from outside: iOS
  // never attempts the move (no events at all), it attempts it and something sends focus back (a
  // focusin on the new field followed immediately by one on the old), or it lands and the field is
  // simply not where the eye expects.
  const name = (t: EventTarget | null): string => {
    if (!(t instanceof HTMLElement)) return "-";
    return t.id || `${t.tagName.toLowerCase()}${t.getAttribute("data-testid") ? `#${t.getAttribute("data-testid")}` : ""}`;
  };
  const onFocusIn = (e: FocusEvent): void => {
    note(`focin ${name(e.target)}`);
    paint();
  };
  const onFocusOut = (e: FocusEvent): void => {
    note(`focout ${name(e.target)}`);
    paint();
  };
  document.addEventListener("focusin", onFocusIn, true);
  document.addEventListener("focusout", onFocusOut, true);

  // Also repaint every frame: the interesting values change between events (the browser paints its
  // own scroll before it reports one), and a per-frame readout is what makes a recording legible.
  let frame = requestAnimationFrame(function tick() {
    paint();
    frame = requestAnimationFrame(tick);
  });

  vv.addEventListener("resize", onResize);
  vv.addEventListener("scroll", onScroll);
  note("start");
  paint();

  return () => {
    cancelAnimationFrame(frame);
    vv.removeEventListener("resize", onResize);
    vv.removeEventListener("scroll", onScroll);
    document.removeEventListener("focusin", onFocusIn, true);
    document.removeEventListener("focusout", onFocusOut, true);
    box.remove();
  };
}
