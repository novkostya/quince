import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationsInstallPage } from "./NotificationsInstallPage";
import { api } from "@/lib/api";

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
  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: false }));
}

function stageServerHas(subs: Array<{ id: string; label: string }>) {
  mockApi.get.mockResolvedValue({
    vapid_public_key: "BFakeKey",
    subscriptions: subs.map((s) => ({ ...s, state: "live", created_at: "2026-08-18T00:00:00Z" })),
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  localStorage.clear();
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
    localStorage.setItem("quince.push.subscription-id", "mac-row");
    stageServerHas([{ id: "mac-row", label: "Mac · Safari" }]);
    renderPage();

    expect(await screen.findByText("On")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /turn on notifications/i })).not.toBeInTheDocument();
  });

  // THE OTHER DIRECTION, AND IT IS NOT SYMMETRIC. A browser holding a registration quince has
  // forgotten — deleted from another device — receives nothing. Saying On there is the same lie
  // pointing the other way, so BOTH halves are required.
  it("says Off when the browser is subscribed but quince has forgotten the row", async () => {
    stageBrowser(true);
    localStorage.setItem("quince.push.subscription-id", "deleted-row");
    stageServerHas([{ id: "some-other-device", label: "iPhone · Safari" }]);
    renderPage();

    expect(await screen.findByRole("button", { name: /turn on notifications/i })).toBeInTheDocument();
  });

  // TWO DEVICES OF THE SAME KIND PRODUCE THE SAME LABEL, and "Turn off" beside the wrong one is a
  // destructive misclick. The current one is marked.
  it("marks which row in the list is the current device", async () => {
    stageBrowser(true);
    localStorage.setItem("quince.push.subscription-id", "mac-b");
    stageServerHas([
      { id: "mac-a", label: "Mac · Safari" },
      { id: "mac-b", label: "Mac · Safari" },
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
