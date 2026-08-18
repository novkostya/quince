import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationsInstallPage } from "./NotificationsInstallPage";
import { api } from "@/lib/api";

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

function stageInstalledIOS() {
  vi.stubGlobal("navigator", {
    userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_4 like Mac OS X)",
    platform: "iPhone",
    maxTouchPoints: 5,
    standalone: true,
    serviceWorker: { register: vi.fn() },
  });
  vi.stubGlobal("PushManager", function PushManager() {});
  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: true }));
}

function stageOneLiveSubscription() {
  mockApi.get.mockResolvedValue({
    vapid_public_key: "BFakeKey",
    subscriptions: [{ id: "s1", label: "iPhone", state: "live", created_at: "2026-08-18T00:00:00Z" }],
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  stageInstalledIOS();
  stageOneLiveSubscription();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("sending a test notification", () => {
  it("offers the control once a device is subscribed, and not before", async () => {
    renderPage();
    expect(await screen.findByRole("button", { name: /send a test notification/i })).toBeInTheDocument();
  });

  // NOTHING TO SEND TO IS NOT A CONTROL TO OFFER. A button that can only ever report "nobody is
  // subscribed" is a dead end on the one screen whose job is to get somebody subscribed.
  it("does not offer it when nothing is subscribed", async () => {
    mockApi.get.mockResolvedValue({ vapid_public_key: "BFakeKey", subscriptions: [] });
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
    await waitFor(() => expect(mockApi.get).toHaveBeenCalledTimes(1));

    fireEvent.click(await screen.findByRole("button", { name: /send a test notification/i }));
    await waitFor(() => expect(mockApi.get.mock.calls.length).toBeGreaterThan(1));
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
      <NotificationsInstallPage />
    </QueryClientProvider>,
  );
}
