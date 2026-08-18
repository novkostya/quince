import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationsInstallPage } from "./NotificationsInstallPage";
import { api } from "@/lib/api";
import { routeGet } from "@/test/config";

// "THIS DEVICE" MUST MEAN THIS DEVICE. Operator-reported 2026-08-18, with an iPhone and a Mac.
//
// The page answered it with "is ANY subscription live", so a Mac that had never subscribed read an
// iPhone's subscription as its own and said On. That is a state-honesty failure in the plainest
// form, and it was a TRAP as well: with no "Turn on" offered, the only way to enable the Mac was to
// Turn off — deleting the iPhone's working subscription — and then turn on. The iPhone then made the
// same false claim in reverse.

vi.mock("@/lib/api", () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), del: vi.fn(), getText: vi.fn() },
}));

const mockApi = vi.mocked(api);

// The base64url of 32 zero bytes — what the stubbed digest above produces.
const FP = "A".repeat(43);

// stageBrowser stages a push-capable browser that either HAS its own subscription or does not. That
// distinction is the whole subject of this file.
function stageBrowser(hasOwnSubscription: boolean) {
  const pushManager = {
    getSubscription: vi
      .fn()
      .mockResolvedValue(hasOwnSubscription ? { endpoint: "https://p.example/x" } : null),
  };
  vi.stubGlobal("navigator", {
    userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
    platform: "MacIntel",
    maxTouchPoints: 0,
    serviceWorker: { register: vi.fn(), ready: Promise.resolve({ pushManager }) },
  });
  vi.stubGlobal("PushManager", function PushManager() {});
  // `crypto.subtle.digest` stubbed to 32 zero bytes, so `fingerprintOf` always yields FP below.
  // Deterministic rather than real hashing: what is under test is the MATCH, not SHA-256.
  vi.stubGlobal("crypto", { subtle: { digest: vi.fn().mockResolvedValue(new ArrayBuffer(32)) } });
  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: false }));
}

function stageServerHas(subs: Array<{ id: string; label: string; fingerprint?: string }>) {
  mockApi.get.mockImplementation(
    routeGet({
      "/api/notifications": {
        vapid_public_key: "BFakeKey",
        subscriptions: subs.map((s) => ({
          fingerprint: "other-device",
          ...s,
          state: "live",
          created_at: "2026-08-18T00:00:00Z",
        })),
      },
    }) as never,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("what This device reports", () => {
  // THE REPORTED BUG, EXACTLY. A second device, someone else's subscription on the server.
  it("says Off on a device that never subscribed, however many others have", async () => {
    stageBrowser(false);
    stageServerHas([{ id: "iphone-row", label: "iPhone · Safari" }]);
    renderPage();

    expect(await screen.findByRole("button", { name: /turn on notifications/i })).toBeInTheDocument();
    expect(screen.getByText("Off")).toBeInTheDocument();
  });

  // AND THE TRAP IT CREATED. With no "Turn on" offered, the only control was another device's
  // "Turn off" — so enabling the Mac required deleting the iPhone's working subscription.
  it("offers a way to turn ON rather than only another device's Turn off", async () => {
    stageBrowser(false);
    stageServerHas([{ id: "iphone-row", label: "iPhone · Safari" }]);
    renderPage();

    expect(await screen.findByRole("button", { name: /turn on notifications/i })).toBeInTheDocument();
  });

  it("says On when this browser holds the subscription quince knows about", async () => {
    stageBrowser(true);
    // This browser's own row, matched by fingerprint rather than by anything stored locally.
    stageServerHas([{ id: "mac-row", label: "Mac · Safari", fingerprint: FP }]);
    renderPage();

    expect(await screen.findByText("On")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /turn on notifications/i })).not.toBeInTheDocument();
  });

  // THE OTHER DIRECTION, AND IT IS NOT SYMMETRIC. A browser holding a registration quince has
  // forgotten — deleted from another device — receives nothing. Saying On there is the same lie
  // pointing the other way, so BOTH halves are required.
  it("says Off when the browser is subscribed but quince has forgotten the row", async () => {
    stageBrowser(true);
    
    stageServerHas([{ id: "some-other-device", label: "iPhone · Safari" }]);
    renderPage();

    expect(await screen.findByRole("button", { name: /turn on notifications/i })).toBeInTheDocument();
  });

  // TWO DEVICES OF THE SAME KIND PRODUCE THE SAME LABEL, and "Turn off" beside the wrong one is a
  // destructive misclick. The current one is marked.
  it("marks which row in the list is the current device", async () => {
    stageBrowser(true);
    
    stageServerHas([
      { id: "mac-a", label: "Mac · Safari" },
      { id: "mac-b", label: "Mac · Safari", fingerprint: FP },
    ]);
    renderPage();

    expect(await screen.findByText(/this device/i)).toBeInTheDocument();
  });
});

function renderPage() {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <NotificationsInstallPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// THE BUG A LOCALLY-STORED ID PRODUCED, AND THE REASON THE ANSWER IS STATELESS.
//
// The first fix matched "this device" against a subscription id written to `localStorage` at
// subscribe time. Every subscription created BEFORE that code existed had none — so the device that
// owned it reported **Off while subscribed and receiving**, which is the worst direction for this
// page to be wrong in: it offers to turn on something that is already on. Operator-reported
// 2026-08-18, from an iPhone looking at its own row.
//
// A cleared profile and a private window fail identically. Nothing is stored now: both sides hash
// the endpoint, so recognition survives anything that clears the browser.
describe("recognising a subscription this browser did not just create", () => {
  it("says On for a subscription with nothing remembered locally", async () => {
    stageBrowser(true);
    localStorage.clear(); // whatever an older build may have written is irrelevant now
    stageServerHas([{ id: "pre-existing", label: "iPhone · Safari", fingerprint: FP }]);
    renderPage();

    expect(await screen.findByText("On")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /turn on notifications/i })).not.toBeInTheDocument();
  });

  it("marks that row as this device, so the list agrees with the badge", async () => {
    stageBrowser(true);
    localStorage.clear();
    stageServerHas([{ id: "pre-existing", label: "iPhone · Safari", fingerprint: FP }]);
    renderPage();

    expect(await screen.findByText(/this device/i)).toBeInTheDocument();
  });
});
