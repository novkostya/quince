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
  | "unsupported_platform"
  /// An INSTALLED iOS web app that has service workers and no Push API — which is iOS older than
  /// 16.4, when Web Push shipped for home-screen web apps.
  ///
  /// IT IS ITS OWN VALUE BECAUSE THE ALTERNATIVE MISATTRIBUTES. Folded into `unsupported_platform`
  /// it would inherit that state's copy and tell this user Lockdown Mode is the likely cause — but
  /// Lockdown Mode removes the service worker too, so having one RULES IT OUT. quince can tell these
  /// apart, and D6's whole claim is that where it can, it must.
  | "unsupported_ios_version";

export function pushSupport(): PushSupport {
  if (!("serviceWorker" in navigator)) return "unsupported_platform";
  if (!("PushManager" in window)) {
    // `needs_install` MEANS INSTALLING WOULD HELP, and that is only true on iOS — where the Push API
    // is absent from a tab by design and appears once the web app is on the Home Screen. Everywhere
    // else, push works in an ordinary tab, so an absent `PushManager` is the platform's answer and
    // installing changes nothing.
    //
    // DISCRIMINATING ON `isStandalone()` ALONE IS THE BUG THIS REPLACES. A non-iOS browser with no
    // Push API was told to install; installing flipped `isStandalone()`, the same predicate then
    // answered `unsupported_platform`, and the page said quince cannot help — **after** the person
    // had spent an action on quince's own instruction. That is a dead end reached by following
    // directions, which is worse than a control that does nothing, and it is exactly the collapse
    // D6 exists to prevent.
    if (!isIOS()) return "unsupported_platform";
    if (!isStandalone()) return "needs_install";
    // Installed, on iOS, with a service worker and no Push API. Not Lockdown Mode — that removes the
    // service worker as well, so reaching here rules it out — and not an install problem, since they
    // have installed. What is left is an iOS older than 16.4.
    return "unsupported_ios_version";
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
