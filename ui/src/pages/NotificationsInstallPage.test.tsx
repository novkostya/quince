import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, describe, expect, it, vi } from "vitest";
import { NotificationsInstallPage } from "./NotificationsInstallPage";

// qn.12 G5 — THE INSTALL PRECONDITION IS NOT BYPASSED, and the three unsupported reasons never
// collapse into one sentence.
//
// The page's whole job is detect → instruct → confirm. Rendering an Enable control to somebody the
// platform will refuse is the *no silent caps* failure in its most literal form, and telling a
// Lockdown Mode user to Add to Home Screen sends them to do something that cannot help.

// The capability tests read `navigator` and `window`, so each case stages a browser rather than
// mocking the module — a mocked `pushSupport` would assert the page's plumbing and not the rule.
function stageBrowser(opts: {
  ios?: boolean;
  standalone?: boolean;
  serviceWorker?: boolean;
  pushManager?: boolean;
}) {
  vi.stubGlobal("navigator", {
    userAgent: opts.ios ? "Mozilla/5.0 (iPhone; CPU iPhone OS 18_4 like Mac OS X)" : "Mozilla/5.0 (X11; Linux x86_64)",
    platform: opts.ios ? "iPhone" : "Linux x86_64",
    maxTouchPoints: opts.ios ? 5 : 0,
    standalone: opts.standalone,
    ...(opts.serviceWorker === false ? {} : { serviceWorker: { register: vi.fn() } }),
  });
  // `PushManager` is read with `in window`, so it has to be present or absent on the object rather
  // than undefined — `"PushManager" in window` is true for a key set to undefined.
  if (opts.pushManager === false) {
    Reflect.deleteProperty(window as unknown as Record<string, unknown>, "PushManager");
  } else {
    vi.stubGlobal("PushManager", function PushManager() {});
  }
  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockReturnValue({ matches: Boolean(opts.standalone) }),
  );
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("the notifications install page", () => {
  it("shows the literal iOS gesture to a Safari tab, and no control", () => {
    stageBrowser({ ios: true, standalone: false, serviceWorker: true, pushManager: false });
    renderPage();

    // THE GESTURE, NAMED. "Install" is not a word that appears anywhere in iOS.
    expect(screen.getByText(/Add to Home Screen/i)).toBeInTheDocument();
    expect(screen.getByText(/Share button/i)).toBeInTheDocument();

    // AND NOTHING TO PRESS. A button here would be refused by the platform.
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("offers the control once installed, and stops instructing", () => {
    stageBrowser({ ios: true, standalone: true, serviceWorker: true, pushManager: true });
    renderPage();

    // THE READINESS CARD IS GONE AND THE CONTROL REPLACES IT. "You can receive notifications,
    // nothing else is needed here" was true and useless; the honest end state of this page is a
    // switch the person came to throw.
    expect(screen.getByRole("button", { name: /turn on notifications/i })).toBeInTheDocument();
    // The instruction must be GONE, not merely deprioritised — somebody who has already followed it
    // reads a repeat as "it did not work".
    expect(screen.queryByText(/Add to Home Screen/i)).not.toBeInTheDocument();
  });

  // THE ROW THAT MUST NOT COLLAPSE INTO THE ONE ABOVE (quince#510, spec D6/D7). No service worker on
  // iOS is Lockdown Mode's signature — service workers shipped in iOS 11.3 — and its remedy is
  // nothing, where "not installed" is one gesture away from working.
  it("does not tell a Lockdown Mode user to add quince to their Home Screen", () => {
    stageBrowser({ ios: true, standalone: false, serviceWorker: false, pushManager: false });
    renderPage();

    expect(screen.getByText(/Lockdown Mode/i)).toBeInTheDocument();
    expect(screen.queryByText(/Add to Home Screen/i)).not.toBeInTheDocument();

    // NAMED AS LIKELY, NOT ASSERTED. Detection cannot prove the cause, and a screen that states an
    // unproven one is a state-honesty failure. The heuristic is owed to hardware (spec G7).
    expect(screen.getByText(/most likely/i)).toBeInTheDocument();

    // AND IT OFFERS THE PATH THAT DOES WORK rather than leaving a dead end.
    expect(screen.getByText(/Devices list/i)).toBeInTheDocument();
  });

  it("does not blame Lockdown Mode on a browser that is not iOS", () => {
    stageBrowser({ ios: false, standalone: false, serviceWorker: false, pushManager: false });
    renderPage();

    expect(screen.queryByText(/Lockdown Mode/i)).not.toBeInTheDocument();
    expect(screen.getByText(/does not support web notifications/i)).toBeInTheDocument();
  });

  // THE ROW NOTHING REACHED, AND THE ONE THAT WAS BROKEN. A non-iOS browser WITH service workers and
  // WITHOUT the Push API — Safari on macOS before 16.1, Firefox with `dom.push.enabled=false` — used
  // to be told to install. Installing flipped `isStandalone()`, the same predicate then answered
  // `unsupported_platform`, and the page said quince cannot help: a dead end reached by following
  // quince's own instruction.
  //
  // The test above LOOKS like this case and is not — it sets `serviceWorker: false`, which returns on
  // the first line of `pushSupport` and never reaches the `PushManager` branch. That is why this row
  // exists separately rather than being folded into it.
  it("never tells a non-iOS browser to install when installing cannot help", () => {
    stageBrowser({ ios: false, standalone: false, serviceWorker: true, pushManager: false });
    renderPage();

    expect(screen.queryByText(/Add to Home Screen/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/address bar/i)).not.toBeInTheDocument();
    expect(screen.getByText(/does not support web notifications/i)).toBeInTheDocument();
  });

  // AN INSTALLED iOS APP WITH SERVICE WORKERS AND NO PUSH API IS AN OLD iOS, NOT LOCKDOWN MODE —
  // and naming Lockdown Mode here would send somebody to check a setting that cannot be the cause,
  // because Lockdown Mode removes the service worker too. quince can tell these apart, so it must.
  it("does not blame Lockdown Mode when the service worker rules it out", () => {
    stageBrowser({ ios: true, standalone: true, serviceWorker: true, pushManager: false });
    renderPage();

    expect(screen.queryByText(/Lockdown Mode/i)).not.toBeInTheDocument();
    expect(screen.getByText(/iOS 16\.4 or later/i)).toBeInTheDocument();
    // And it does not tell somebody who has already installed to install.
    expect(screen.queryByText(/Add to Home Screen/i)).not.toBeInTheDocument();
  });

  // AND THE SAME BROWSER ONCE INSTALLED SAYS THE SAME THING. The old rule changed its answer when
  // `isStandalone()` flipped; the fix discriminates on platform, so installing cannot move a user
  // between two different explanations of one unchanged fact.
  it("gives an installed non-iOS browser the same answer as a tab", () => {
    stageBrowser({ ios: false, standalone: true, serviceWorker: true, pushManager: false });
    renderPage();

    expect(screen.getByText(/does not support web notifications/i)).toBeInTheDocument();
    expect(screen.queryByText(/Add to Home Screen/i)).not.toBeInTheDocument();
  });
});

// renderPage supplies the QueryClient the controls need.
//
// THE PAGE FETCHES NOW, AND ITS TEST HARNESS HAD TO LEARN THAT. The `supported` branch used to be a
// static card; it is a control backed by `GET /api/notifications`, so a bare `render` fails with
// `No QueryClient set` — the same breakage `DeviceCard` produced when a query moved into it, and the
// same honest signal that the component's dependencies changed.
function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <NotificationsInstallPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// THE PAGE IS A CHILD OF THE SHELL AND MUST BE LAID OUT LIKE ONE (Operator-reported 2026-08-18).
//
// It was written with the pre-shell onboarding layout — `min-h-dvh`, its own safe-area padding, its
// own background, its own `mx-auto max-w-2xl` — and routed INSIDE the authed shell, which supplies
// all four. A full-page layout nested in a full-page layout: doubled horizontal inset, a dead gap
// above the title, and a column lining up with nothing else in Settings.
//
// NOTHING COULD FAIL, which is why this test exists rather than a screenshot. Both layouts render,
// both pass every behavioural assertion in this file, and jsdom computes no geometry — so the only
// checkable statement is the one below: the shell-owned classes are not repeated here.
describe("the page's own layout", () => {
  it("does not carry the full-page shell its parent already provides", () => {
    stageBrowser({ ios: true, standalone: true, serviceWorker: true, pushManager: true });
    const { container } = renderPage();

    const root = container.querySelector("section");
    expect(root).not.toBeNull();
    // `min-h-dvh` is the signature of a page that owns the viewport. Inside the shell it forces a
    // second full-height box into one that is already scrolling.
    expect(container.innerHTML).not.toContain("min-h-dvh");
    // The shell owns the safe-area inset. Repeating it doubles the gutter on a notched phone, which
    // is exactly what was reported.
    expect(container.innerHTML).not.toContain("env(safe-area-inset-left)");
  });

  // A HOME SCREEN WEB APP HAS NO BROWSER CHROME, so the back gesture is all a phone user has and
  // nothing on screen promises it. This page is reachable only from Settings, so it must return
  // there — the same reason SettingsAuthPage carries one, one degree sharper.
  it("offers a way back to Settings", () => {
    stageBrowser({ ios: true, standalone: true, serviceWorker: true, pushManager: true });
    renderPage();

    expect(screen.getByRole("link", { name: /settings/i })).toHaveAttribute("href", "/settings");
  });
});

// `text-fg-muted` IS NOT A CLASS, AND TAILWIND SAYS NOTHING WHEN YOU WRITE ONE (Operator-reported
// 2026-08-18, third round on this page).
//
// The colour roles are exposed as `--color-muted` / `--color-subtle` / `--color-fg`, so the utility
// is `text-muted`. This page was written with `text-fg-muted` in EIGHT places — every body-text block
// it had — and an unknown utility is simply dropped: no error, no warning, no build failure. So the
// text rendered at full `--fg` instead of the muted role, which is a real part of why the page read
// differently from its neighbours after the layout was already fixed.
//
// ASSERTED AS "NO UNDEFINED ROLE CLASS", because the specific typo matters less than the shape: a
// colour utility that names a CSS variable rather than a Tailwind colour is always dead.
it("uses no colour utility that Tailwind will silently drop", () => {
  stageBrowser({ ios: true, standalone: true, serviceWorker: true, pushManager: true });
  const { container } = renderPage();

  // The role names as they exist in `index.css` are `muted`, `subtle`, `fg` — never `fg-muted`.
  expect(container.innerHTML).not.toMatch(/\b(?:text|bg|border)-fg-(?:muted|subtle|placeholder)\b/);
});
