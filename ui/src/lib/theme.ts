// Theme (ui.design.md principle 6): system-follow by default, with a manual override the
// Settings page can set from config.ui.theme. The `.dark` class on <html> drives Tailwind.
export type Theme = "system" | "light" | "dark";

let current: Theme = "system";
let mql: MediaQueryList | null = null;

function apply(theme: Theme): void {
  const dark =
    theme === "dark" ||
    (theme === "system" && window.matchMedia("(prefers-color-scheme: dark)").matches);
  document.documentElement.classList.toggle("dark", dark);
}

export function initTheme(theme: Theme = "system"): void {
  current = theme;
  apply(theme);
  if (!mql) {
    mql = window.matchMedia("(prefers-color-scheme: dark)");
    mql.addEventListener("change", () => apply(current));
  }
}

export function setTheme(theme: Theme): void {
  current = theme;
  apply(theme);
}
