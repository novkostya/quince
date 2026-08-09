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
// THE FIRST ATTEMPT AT THIS DID NOT WORK ON THE DEVICE, for two reasons, either of which was enough:
//
//  - it watched the top-left 64px square, which on an iPhone is UNDER THE STATUS BAR. iOS keeps that
//    strip for itself — a tap there means "scroll to top" — so most of those taps never reached the
//    page at all. The region is now well inside the content area;
//  - the handler touched `localStorage` UNWRAPPED. Boot-time access was already in a `try`, and the
//    gesture's was not, so a store that refuses in standalone would throw inside the listener and the
//    gesture would do nothing, silently. Everything that touches storage is wrapped now.
//
// It also no longer reloads: the overlay starts and stops in place, so turning it on cannot depend on
// the flag surviving a page load. Remembering it is now a convenience rather than the mechanism.
//
// TWO WAYS IN, because a gesture that does not work is worse than no gesture:
//
//  - THREE FINGERS ON THE SCREEN AT ONCE, anywhere. Position-independent, so nothing about safe
//    areas or overlays can swallow it.
//  - FIVE TAPS in the top-left of the CONTENT, below the status bar.
//
// Either toggles. No UI, no route, no component touched; it lives and dies with this file.
const TAPS = 5;
const WINDOW_MS = 3000;
const FINGERS = 3;
const REMEMBERED = "quince.vvdebug";

function remembered(): boolean {
  try {
    return localStorage.getItem(REMEMBERED) === "1";
  } catch {
    return false;
  }
}

function remember(on: boolean): void {
  try {
    localStorage.setItem(REMEMBERED, on ? "1" : "0");
  } catch {
    // A blocked store makes the choice forgetful, never broken.
  }
}

export function initViewportDebug(): () => void {
  let stop: (() => void) | null = null;
  const toggle = (): void => {
    if (stop) {
      stop();
      stop = null;
      remember(false);
      return;
    }
    stop = start();
    remember(true);
  };

  // Three fingers down together. `pointerdown` fires once per finger, so this counts how many are
  // currently down rather than looking for a single event.
  const down = new Set<number>();
  const onDown = (e: PointerEvent): void => {
    down.add(e.pointerId);
    if (down.size >= FINGERS) {
      down.clear();
      toggle();
    }
  };
  const onUp = (e: PointerEvent): void => {
    down.delete(e.pointerId);
  };

  // Five taps in the top-left of the CONTENT. The band starts below the status bar and is generous,
  // because the point is that it can be hit, not that it is precise.
  let taps = 0;
  let first = 0;
  const onTap = (e: PointerEvent): void => {
    const inside = e.clientX < 140 && e.clientY > 96 && e.clientY < 420;
    if (!inside) {
      taps = 0;
      return;
    }
    if (taps === 0 || e.timeStamp - first > WINDOW_MS) {
      taps = 1;
      first = e.timeStamp;
      return;
    }
    if (++taps < TAPS) return;
    taps = 0;
    toggle();
  };

  document.addEventListener("pointerdown", onDown, true);
  document.addEventListener("pointerup", onUp, true);
  document.addEventListener("pointercancel", onUp, true);
  document.addEventListener("pointerdown", onTap, true);

  const flagged =
    typeof location !== "undefined" && new URLSearchParams(location.search).has("vvdebug");
  if (flagged || remembered()) {
    stop = start();
    remember(true);
  }

  return () => {
    document.removeEventListener("pointerdown", onDown, true);
    document.removeEventListener("pointerup", onUp, true);
    document.removeEventListener("pointercancel", onUp, true);
    document.removeEventListener("pointerdown", onTap, true);
    stop?.();
    stop = null;
  };
}

function start(): () => void {
  const vv = window.visualViewport;
  if (!vv) return () => {};

  const box = document.createElement("div");
  box.setAttribute("data-viewport-debug", "");
  box.style.cssText = [
    "position:fixed",
    "left:0",
    "top:env(safe-area-inset-top,0px)",
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
