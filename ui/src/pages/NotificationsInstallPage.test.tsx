import { render, screen } from "@testing-library/react";
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
    render(<NotificationsInstallPage />);

    // THE GESTURE, NAMED. "Install" is not a word that appears anywhere in iOS.
    expect(screen.getByText(/Add to Home Screen/i)).toBeInTheDocument();
    expect(screen.getByText(/Share button/i)).toBeInTheDocument();

    // AND NOTHING TO PRESS. A button here would be refused by the platform.
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("confirms readiness once installed, and stops instructing", () => {
    stageBrowser({ ios: true, standalone: true, serviceWorker: true, pushManager: true });
    render(<NotificationsInstallPage />);

    expect(screen.getByText(/can receive notifications/i)).toBeInTheDocument();
    // The instruction must be GONE, not merely deprioritised — somebody who has already followed it
    // reads a repeat as "it did not work".
    expect(screen.queryByText(/Add to Home Screen/i)).not.toBeInTheDocument();
  });

  // THE ROW THAT MUST NOT COLLAPSE INTO THE ONE ABOVE (quince#510, spec D6/D7). No service worker on
  // iOS is Lockdown Mode's signature — service workers shipped in iOS 11.3 — and its remedy is
  // nothing, where "not installed" is one gesture away from working.
  it("does not tell a Lockdown Mode user to add quince to their Home Screen", () => {
    stageBrowser({ ios: true, standalone: false, serviceWorker: false, pushManager: false });
    render(<NotificationsInstallPage />);

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
    render(<NotificationsInstallPage />);

    expect(screen.queryByText(/Lockdown Mode/i)).not.toBeInTheDocument();
    expect(screen.getByText(/does not support web notifications/i)).toBeInTheDocument();
  });
});
