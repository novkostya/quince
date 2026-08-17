// PWA install detection and service-worker registration (qn.12, spec D1/D2).
//
// EVERYTHING HERE IS A CLIENT-SIDE FEATURE TEST. The server never guesses at a browser's
// capabilities — it knows only whether a live subscription exists — so these answers are computed
// where the browser is and reported honestly (spec D6).

/// <reference lib="dom" />

// `navigator.standalone` is a non-standard Safari property and is the only reliable signal on older
// iOS. TypeScript's DOM lib does not declare it, so it is narrowed here rather than with a cast at
// the call site.
type SafariNavigator = Navigator & { standalone?: boolean };

/// The install state of THIS browsing context.
///
/// `installed` is the precondition for Web Push on iOS — WebKit: *"A web app that has been added to
/// the Home Screen can request permission to receive push notifications."* It holds for BOTH
/// mechanisms; Declarative Web Push (18.4+) relaxes the service-worker requirement and not this one.
export type InstallState =
  /// Running as a home-screen / standalone web app.
  | "installed"
  /// A browser tab that could be installed — the instruction applies.
  | "installable"
  /// The platform cannot receive Web Push at all, however it is launched. On iOS this is the
  /// Lockdown Mode signature; see `pushSupport`.
  | "unsupported";

/// isStandalone reports whether this context is running as an installed web app.
///
/// TWO CHECKS, BECAUSE NEITHER COVERS EVERYTHING. `display-mode: standalone` is the standard and is
/// what Android and desktop answer; `navigator.standalone` is Safari's own and is what iOS answered
/// before the media query was reliable there. Checking only the standard one reports a real
/// home-screen web app as a tab, which would show the install instruction to somebody who has
/// already followed it.
export function isStandalone(): boolean {
  const nav = navigator as SafariNavigator;
  if (nav.standalone === true) return true;
  return typeof matchMedia === "function" && matchMedia("(display-mode: standalone)").matches;
}

/// isIOS reports whether this is iOS or iPadOS.
///
/// IT EXISTS ONLY TO DECIDE WHICH INSTRUCTION TO SHOW, never to gate a capability — capability is
/// always a feature test. iPadOS reports itself as a Mac, so the touch-point check is what catches
/// an iPad; without it an iPad user is told the desktop story and never finds Add to Home Screen.
export function isIOS(): boolean {
  if (/iPad|iPhone|iPod/.test(navigator.userAgent)) return true;
  return navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1;
}

/// installState answers what the install page renders.
export function installState(): InstallState {
  if (isStandalone()) return "installed";
  // A tab on a platform with no Push API is only *installable* if installing would help. On iOS it
  // would — the API is absent in a tab by design and appears once installed. Anywhere else, an
  // absent PushManager in a tab means the platform does not have it.
  if (!("PushManager" in window)) return isIOS() ? "installable" : "unsupported";
  return "installable";
}

/// pushSupport distinguishes the two ways a platform can have no Push API, which have different
/// remedies and must never collapse into one sentence (spec D6, and quince#940's defect).
export type PushSupport =
  /// The APIs are present. Whether the user has granted permission is a separate question.
  | "supported"
  /// Not installed to the Home Screen — one gesture away from working.
  | "needs_install"
  /// No service worker at all. On iOS this is Lockdown Mode's signature: WebKit disables
  /// `ServiceWorkersEnabled` and `PushAPIEnabled` declaratively, on any certificate. Service Workers
  /// shipped in iOS 11.3, so their absence on a current iOS is not a version story.
  ///
  /// THE HEURISTIC IS UNVERIFIED ON HARDWARE (spec G7). The copy says Lockdown Mode is the LIKELY
  /// cause for that reason; detection cannot prove it, and asserting it would be a state-honesty
  /// failure on a screen.
  | "unsupported_platform";

export function pushSupport(): PushSupport {
  if (!("serviceWorker" in navigator)) return "unsupported_platform";
  if (!("PushManager" in window)) {
    return isStandalone() ? "unsupported_platform" : "needs_install";
  }
  return "supported";
}

/// registerServiceWorker registers the push worker, once, and resolves to null when the platform has
/// no service workers rather than throwing.
///
/// SCOPE `/` IS LOAD-BEARING. The worker is served from the root so its default scope covers every
/// route, which is what lets `notificationclick` focus and navigate an already-open client. A worker
/// under `/assets/` would silently control nothing.
export async function registerServiceWorker(): Promise<ServiceWorkerRegistration | null> {
  if (!("serviceWorker" in navigator)) return null;
  try {
    return await navigator.serviceWorker.register("/sw.js", { scope: "/" });
  } catch (err) {
    // A FAILED REGISTRATION IS NOT FATAL AND IS NOT SILENT. quince works without notifications, so
    // this must not take the app down — but a swallowed failure is a phone that quietly never
    // receives anything, so it goes to the console where a support conversation can find it.
    //
    // The most likely cause by far is the SPA fallback serving index.html for /sw.js, which fails
    // with a MIME error naming neither cause. There is a gate for exactly that (spec G4).
    console.error("quince: service worker registration failed; notifications will not arrive", err);
    return null;
  }
}
