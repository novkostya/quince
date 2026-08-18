import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationsInstallPage } from "./NotificationsInstallPage";
import { api } from "@/lib/api";
import { routeGet } from "@/test/config";

// qn.12 — SEND TEST IS WHAT MAKES THE FEATURE INSTALLABLE BY A PERSON.
//
// Without it the next real notification is whenever a device next goes stale — three days by
// default — so the setup flow ends on "we think that worked", and a failure is indistinguishable
// from nothing having been due. These tests are about the REPORT, not the send: a per-device result
// is the only thing that tells somebody which phone to go and fix.

vi.mock("@/lib/api", () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), del: vi.fn(), getText: vi.fn() },
}));

const mockApi = vi.mocked(api);

// The base64url of 32 zero bytes — what the stubbed digest above produces.
const FP = "A".repeat(43);

function stageInstalledIOS() {
  // THE BROWSER'S OWN SUBSCRIPTION IS HALF THE ANSWER to "is this device on" — the server list is
  // the other half. Staging only the server list is what the page used to believe, and it is exactly
  // the bug: a Mac read an iPhone's subscription as its own.
  const pushManager = { getSubscription: vi.fn().mockResolvedValue({ endpoint: "https://p.example/x" }) };
  vi.stubGlobal("navigator", {
    userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_4 like Mac OS X)",
    platform: "iPhone",
    maxTouchPoints: 5,
    standalone: true,
    serviceWorker: { register: vi.fn(), ready: Promise.resolve({ pushManager }) },
  });
  vi.stubGlobal("PushManager", function PushManager() {});
  // `crypto.subtle.digest` stubbed to 32 zero bytes, so `fingerprintOf` always yields FP below.
  // Deterministic rather than real hashing: what is under test is the MATCH, not SHA-256.
  vi.stubGlobal("crypto", { subtle: { digest: vi.fn().mockResolvedValue(new ArrayBuffer(32)) } });
  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: true }));
}

function stageOneLiveSubscription() {
  mockApi.get.mockImplementation(
    routeGet({
      "/api/notifications": {
        vapid_public_key: "BFakeKey",
        subscriptions: [
          { id: "s1", label: "iPhone", state: "live", created_at: "2026-08-18T00:00:00Z", fingerprint: FP },
        ],
      },
    }) as never,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  stageInstalledIOS();
  stageOneLiveSubscription();
});

afterEach(() => {
  vi.unstubAllGlobals();
  // The remembered id is per-browser state, so it outlives a test unless cleared.
});

describe("sending a test notification", () => {
  it("offers the control once a device is subscribed, and not before", async () => {
    renderPage();
    expect(await screen.findByRole("button", { name: /send a test notification/i })).toBeInTheDocument();
  });

  // NOTHING TO SEND TO IS NOT A CONTROL TO OFFER. A button that can only ever report "nobody is
  // subscribed" is a dead end on the one screen whose job is to get somebody subscribed.
  it("does not offer it when nothing is subscribed", async () => {
    mockApi.get.mockImplementation(
      routeGet({ "/api/notifications": { vapid_public_key: "BFakeKey", subscriptions: [] } }) as never,
    );
    renderPage();

    expect(await screen.findByRole("button", { name: /turn on notifications/i })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /send a test notification/i })).not.toBeInTheDocument();
  });

  it("names the device it reached, so somebody knows where to look", async () => {
    mockApi.post.mockResolvedValue({ results: [{ label: "iPhone", state: "sent" }] });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /send a test notification/i }));

    expect(await screen.findByText(/Sent to iPhone/i)).toBeInTheDocument();
    expect(mockApi.post).toHaveBeenCalledWith("/api/notifications/test", {});
  });

  // THE THREE STATES DO NOT COLLAPSE (spec D8). `expired` and `error` have different remedies —
  // re-subscribe on that device, versus try again — and a screen that says "failed" to both sends
  // half its readers to do the wrong thing.
  it("tells an unreachable device apart from a failed send", async () => {
    mockApi.post.mockResolvedValue({
      results: [
        { label: "old iPad", state: "expired" },
        { label: "iPhone", state: "error", error: "push service returned 500" },
      ],
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /send a test notification/i }));

    expect(await screen.findByText(/old iPad is no longer reachable/i)).toBeInTheDocument();
    expect(screen.getByText(/iPhone did not receive it/i)).toBeInTheDocument();
  });

  // A SUCCESSFUL SEND IS ALSO A PROBE: a 410 expires that subscription server-side, so the device
  // list is stale the moment this returns. Not refetching would leave the page showing a phone as
  // live in the same breath as saying it is unreachable.
  it("refreshes the device list, because a test can expire a subscription", async () => {
    mockApi.post.mockResolvedValue({ results: [{ label: "iPhone", state: "expired" }] });
    renderPage();
    // COUNTED BY PATH, not by total calls. The page also reads `/api/config` for its settings
    // (quince#1212), so a bare call count answers "did anything fetch" — which would stay green if
    // the refetch this test is about were removed.
    const listCalls = () => mockApi.get.mock.calls.filter((c) => String(c[0]).startsWith("/api/notifications")).length;
    await waitFor(() => expect(listCalls()).toBe(1));

    fireEvent.click(await screen.findByRole("button", { name: /send a test notification/i }));
    await waitFor(() => expect(listCalls()).toBeGreaterThan(1));
  });

  it("says so when the send itself fails, rather than staying silent", async () => {
    mockApi.post.mockRejectedValue(new Error("network"));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /send a test notification/i }));

    expect(await screen.findByText(/could not send the test/i)).toBeInTheDocument();
  });
});

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <NotificationsInstallPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
