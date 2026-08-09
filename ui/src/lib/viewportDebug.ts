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
const LOG_LINES = 6;

export function initViewportDebug(): () => void {
  if (typeof location === "undefined" || !new URLSearchParams(location.search).has("vvdebug")) {
    return () => {};
  }
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
    box.remove();
  };
}
