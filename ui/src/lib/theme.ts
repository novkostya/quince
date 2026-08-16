// Theme (ui.design.md principle 6): system-follow by default, with a manual override the
// Settings page can set from config.ui.theme. The `.dark` class on <html> drives Tailwind.
export type Theme = "system" | "light" | "dark";

// THE KEY THE PRE-PAINT SCRIPT IN index.html READS. The two must agree, and they are in different
// languages in different files, so each names the other (quince#1074). If this changes, change the
// inline script in the same commit — a mismatch is silent: the cache simply never hits and the
// white flash comes back, with every test still green.
export const THEME_STORAGE_KEY = "quince.theme";

let current: Theme = "system";
let mql: MediaQueryList | null = null;

function resolve(theme: Theme): boolean {
  return (
    theme === "dark" ||
    (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches)
  );
}

function apply(theme: Theme): void {
  const dark = resolve(theme);
  document.documentElement.classList.toggle("dark", dark);

  // `theme-color` tints Safari's chrome around the page. It was hardcoded to the dark `--bg` in
  // index.html, so a light-theme user got dark browser chrome — and during the flash quince#1074
  // describes, the chrome was already dark while the page under it was still light, which is what
  // made a one-frame flash conspicuous rather than subtle.
  //
  // Read from the COMPUTED token rather than a literal: the class was just toggled, so `--bg` is
  // whichever value now applies, and the palette stays defined in exactly one place (tokens.css).
  const bg = getComputedStyle(document.documentElement).getPropertyValue("--bg").trim();
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta && bg) meta.setAttribute("content", bg);
}

// remember caches the PREFERENCE — "system" | "light" | "dark" — and deliberately NOT the resolved
// light/dark it produced. The inline script re-resolves `system` through `matchMedia` at boot, so a
// user who changes their system theme while quince is closed gets the right first paint; a cached
// resolution would replay the stale answer and flash.
//
// This is a RENDER CACHE, not state: `config.ui.theme` on the server stays authoritative and
// overwrites it on every load. Named because CLAUDE.md's config rule is "no UI-only state", and the
// distinction is the whole reason this is allowed to exist (quince#1074).
function remember(theme: Theme): void {
  try {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
  } catch {
    // Private browsing and disabled storage both throw on write. A missed cache costs the flash
    // this exists to prevent — it must never cost the theme itself, which `apply` has already set.
  }
}

// recall reads the cached preference. Anything that is not one of the three values — absent,
// corrupted, written by a future version — resolves to null and the caller falls back, so a bad
// cache costs the flash rather than the theme.
function recall(): Theme | null {
  try {
    const t = localStorage.getItem(THEME_STORAGE_KEY);
    return t === "system" || t === "light" || t === "dark" ? t : null;
  } catch {
    return null;
  }
}

// initTheme WITH NO ARGUMENT ADOPTS THE CACHED PREFERENCE, and that is what stops this fix causing a
// second flash (quince#1074). The pre-paint script in index.html sets the class from the cache; if
// boot then hardcoded "system", an explicit override would be painted from the cache and immediately
// overwritten — dark, light, dark — which is worse than the single flash being fixed.
//
// IT ALSO FIXES A BUG THAT WAS ALREADY THERE, and that is a consequence rather than the goal: today
// `config.ui.theme` is applied ONLY by ConfigEditor's save handler, so an explicit override does not
// survive a reload. It cannot come from the server at boot either — `GET /api/config` is behind auth
// and the login screen has to render before anyone is authenticated — so the cache is the only thing
// that can carry a preference across a reload, which is what makes it a cache of the PREFERENCE and
// not a second source of truth. A save still overwrites it from the server's answer.
export function initTheme(theme?: Theme): void {
  const resolved = theme ?? recall() ?? "system";
  current = resolved;
  apply(resolved);
  remember(resolved);
  if (!mql) {
    mql = window.matchMedia("(prefers-color-scheme: dark)");
    mql.addEventListener("change", () => apply(current));
  }
}

export function setTheme(theme: Theme): void {
  current = theme;
  apply(theme);
  remember(theme);
}
