import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationsInstallPage } from "./NotificationsInstallPage";
import { api } from "@/lib/api";
import { routeGet } from "@/test/config";

// TURNING NOTIFICATIONS ON MUST LEAVE THE PAGE SHOWING ON. Operator-reported 2026-08-20:
// *"after enabling notifications on Notification settings page it's not properly updated. You have
// to reload or go back and forth to make 'Test notification' button appear."*
//
// WHY THE OTHER FIVE NOTIFICATION TEST FILES COULD ALL BE GREEN THROUGH THIS. Every one of them
// stages a state directly and asserts what renders — `NotificationsThisDevice` stages a browser
// that is or is not subscribed, `NotificationsSendTest` stages one that already is. So each STATE
// was covered and no TRANSITION between them was, and the bug lives entirely in the transition:
// `useThisDevice` answers from two queries and `useSubscribe` invalidated only one, so the
// browser's own fingerprint stayed cached at its pre-subscription `null` and `find` matched
// nothing over a server row that was now there.
//
// SO THIS FILE ASSERTS THE CLICK, NOT THE STATE, and the assertion that matters is the one about
// NOT REMOUNTING — a remount re-runs the fingerprint query and papers over exactly this defect,
// which is why reloading was the Operator's workaround.

vi.mock("@/lib/api", () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), del: vi.fn(), getText: vi.fn() },
}));

const mockApi = vi.mocked(api);

// The base64url of 32 zero bytes — what the stubbed `crypto.subtle.digest` below produces.
const FP = "A".repeat(43);

// stageSubscribeFlow stages a browser that starts with NO subscription and gains one when the page
// asks for it, and a server that starts with no row and gains one when the POST lands.
//
// BOTH SIDES HAVE TO MOVE, which is the whole point. Staging only the server reproduces nothing:
// the fingerprint query would still be answering from a browser that never subscribed, so `find`
// would correctly match nothing and the test would assert the bug rather than the fix.
function stageSubscribeFlow({ postFails = false } = {}) {
  let browserSubscribed = false;
  let serverRow = false;

  const subscription = {
    endpoint: "https://push.example/mac-endpoint",
    toJSON: () => ({ keys: { p256dh: "p", auth: "a" } }),
  };
  const pushManager = {
    getSubscription: vi.fn().mockImplementation(() =>
      Promise.resolve(browserSubscribed ? subscription : null),
    ),
    subscribe: vi.fn().mockImplementation(() => {
      browserSubscribed = true;
      return Promise.resolve(subscription);
    }),
  };

  vi.stubGlobal("navigator", {
    userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
    platform: "MacIntel",
    maxTouchPoints: 0,
    serviceWorker: { register: vi.fn(), ready: Promise.resolve({ pushManager }) },
  });
  vi.stubGlobal("PushManager", function PushManager() {});
  // Deterministic rather than real hashing: what is under test is the MATCH, not SHA-256.
  vi.stubGlobal("crypto", { subtle: { digest: vi.fn().mockResolvedValue(new ArrayBuffer(32)) } });
  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: false }));
  vi.stubGlobal("Notification", {
    permission: "default",
    requestPermission: vi.fn().mockResolvedValue("granted"),
  });

  mockApi.get.mockImplementation((path: string) =>
    routeGet({
      "/api/notifications": {
        vapid_public_key: "BFakeKey",
        subscriptions: serverRow
          ? [
              {
                id: "mac-row",
                label: "Mac · Safari",
                fingerprint: FP,
                state: "live",
                created_at: "2026-08-20T00:00:00Z",
              },
            ]
          : [],
      },
    })(path) as never,
  );

  mockApi.post.mockImplementation((path: string) => {
    if (path === "/api/notifications/subscriptions") {
      if (postFails) return Promise.reject(new Error("network"));
      serverRow = true;
      return Promise.resolve({ id: "mac-row" }) as never;
    }
    return Promise.resolve({ results: [] }) as never;
  });

  return { pushManager };
}

beforeEach(() => {
  vi.clearAllMocks();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

// clickTurnOn presses the control only once it can actually do anything.
//
// THE BUTTON RENDERS BEFORE IT WORKS, AND CLICKING IT EARLY IS A SILENT NO-OP. It is
// `disabled={!q.data || subscribe.isPending}`, so it is on screen and inert until the notifications
// query lands — `findByRole` resolves against the inert one, `fireEvent.click` does nothing, and
// the failure reads as a broken invalidation rather than as a mistimed click. Two of the four tests
// here failed exactly that way before this helper existed, while a third passed because an
// unrelated `findByText` in front of it happened to give the query time to land.
async function clickTurnOn() {
  const button = await screen.findByRole("button", { name: /turn on notifications/i });
  await waitFor(() => expect(button).toBeEnabled());
  fireEvent.click(button);
}

describe("turning notifications on, in one page lifetime", () => {
  // THE REPORTED BUG, EXACTLY — and `renderPage` is called ONCE, deliberately. If this file ever
  // re-renders or remounts to make an assertion pass, it has stopped testing the defect.
  it("shows Send a test notification after Turn on, without a reload", async () => {
    stageSubscribeFlow();
    renderPage();

    await clickTurnOn();

    expect(
      await screen.findByRole("button", { name: /send a test notification/i }),
    ).toBeInTheDocument();
  });

  it("flips the badge from Off to On in the same lifetime", async () => {
    stageSubscribeFlow();
    renderPage();

    expect(await screen.findByText("Off")).toBeInTheDocument();

    await clickTurnOn();

    expect(await screen.findByText("On")).toBeInTheDocument();
  });

  it("withdraws the Turn on button once this device is subscribed", async () => {
    stageSubscribeFlow();
    renderPage();

    await clickTurnOn();
    await screen.findByRole("button", { name: /send a test notification/i });

    expect(
      screen.queryByRole("button", { name: /turn on notifications/i }),
    ).not.toBeInTheDocument();
  });

  // THE `onSettled` HALF, WHICH `onSuccess` WOULD NOT COVER. The browser subscribed and the POST
  // failed, so this browser holds a registration quince does not know about. The page must say Off
  // — it will receive nothing — and it must still be offering a way to try again.
  //
  // This is the state the cached fingerprint gets wrong in the other direction, and it is why the
  // refetch hangs off `onSettled` rather than off success.
  it("stays Off, and keeps offering Turn on, when the browser subscribed but quince did not record it", async () => {
    stageSubscribeFlow({ postFails: true });
    renderPage();

    await clickTurnOn();

    expect(
      await screen.findByRole("button", { name: /turn on notifications/i }),
    ).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /send a test notification/i }),
    ).not.toBeInTheDocument();
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
