import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { initTheme, setTheme, THEME_STORAGE_KEY } from "./theme";

// quince#1074. The flash these tests exist for happens BEFORE any of this module runs — it is the
// inline script in index.html that fixes it. What is testable here is the contract that script
// depends on: that the preference reaches localStorage under the agreed key, in the agreed shape.
//
// So the assertions are deliberately about the CACHE, not about the class. A test that only checked
// `classList` would pass unchanged if `remember()` were deleted, which is the exact regression that
// brings the flash back.

// jsdom has no matchMedia. `system` resolution reads it, so every test here needs it — and the value
// is what distinguishes "system on a dark machine" from "system on a light one", which is half the
// cases below.
function stubMatchMedia(dark: boolean): void {
  Object.defineProperty(window, "matchMedia", {
    writable: true,
    configurable: true,
    value: (query: string) => ({
      matches: dark,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }),
  });
}

describe("theme", () => {
  beforeEach(() => {
    localStorage.clear();
    document.documentElement.classList.remove("dark");
  });

  afterEach(() => {
    localStorage.clear();
  });

  it("caches the PREFERENCE, not the resolved light/dark", () => {
    stubMatchMedia(true);
    initTheme("system");
    // "system", not "dark" — even though it resolved to dark on this machine. The inline script
    // re-resolves at boot, so a system-theme change while quince is closed still paints correctly;
    // caching "dark" here would replay a stale answer and flash.
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("system");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });

  it("caches an explicit override, which is the case matchMedia cannot recover", () => {
    stubMatchMedia(false); // a LIGHT system…
    setTheme("dark"); // …with an explicit dark preference.
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("dark");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });

  it("caches an explicit light on a dark system, so the pre-paint script does not guess dark", () => {
    stubMatchMedia(true);
    setTheme("light");
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("light");
    expect(document.documentElement.classList.contains("dark")).toBe(false);
  });

  // The regression this file exists to prevent, and the reason initTheme takes no argument at boot.
  // The pre-paint script paints from the cache; if boot then forced "system", an explicit override
  // would be shown correctly and overwritten a frame later — dark, light, dark.
  it("adopts the cached preference at boot instead of forcing system", () => {
    stubMatchMedia(false); // a LIGHT system, so "system" would resolve to light…
    localStorage.setItem(THEME_STORAGE_KEY, "dark"); // …but the user chose dark.
    initTheme();
    expect(document.documentElement.classList.contains("dark")).toBe(true);
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("dark");
  });

  it("falls back to system when nothing is cached", () => {
    stubMatchMedia(true);
    initTheme();
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("system");
    expect(document.documentElement.classList.contains("dark")).toBe(true);
  });

  it("ignores a cached value that is not one of the three, rather than trusting it", () => {
    stubMatchMedia(false);
    localStorage.setItem(THEME_STORAGE_KEY, "midnight"); // a future version, or a hand-edit
    initTheme();
    expect(document.documentElement.classList.contains("dark")).toBe(false);
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("system");
  });

  it("still honours an explicit argument, which is what a save passes", () => {
    stubMatchMedia(false);
    localStorage.setItem(THEME_STORAGE_KEY, "dark");
    initTheme("light"); // an explicit caller wins over the cache
    expect(document.documentElement.classList.contains("dark")).toBe(false);
    expect(localStorage.getItem(THEME_STORAGE_KEY)).toBe("light");
  });

  it("keeps the theme when storage refuses, because the flash must never cost the theme itself", () => {
    stubMatchMedia(true);
    const setItem = Storage.prototype.setItem;
    Storage.prototype.setItem = () => {
      throw new Error("QuotaExceededError"); // what private browsing does
    };
    try {
      expect(() => initTheme("system")).not.toThrow();
      expect(document.documentElement.classList.contains("dark")).toBe(true);
    } finally {
      Storage.prototype.setItem = setItem;
    }
  });

  it("points theme-color at the resolved background instead of a hardcoded dark", () => {
    stubMatchMedia(false);
    const meta = document.createElement("meta");
    meta.setAttribute("name", "theme-color");
    meta.setAttribute("content", "#0b0d10"); // the hardcoded dark this replaces
    document.head.appendChild(meta);
    // jsdom computes no custom properties from the real stylesheet, so declare one inline: the
    // claim under test is "it reads --bg and writes it through", not what the palette contains.
    document.documentElement.style.setProperty("--bg", "#f6f7f8");
    try {
      setTheme("light");
      expect(meta.getAttribute("content")).toBe("#f6f7f8");
    } finally {
      meta.remove();
      document.documentElement.style.removeProperty("--bg");
    }
  });
});
