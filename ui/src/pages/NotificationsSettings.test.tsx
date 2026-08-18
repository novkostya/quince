import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationsInstallPage } from "./NotificationsInstallPage";
import { api } from "@/lib/api";
import { APIError } from "@/lib/api";
import { routeGet, testConfigResponse } from "@/test/config";

// EIGHT SETTINGS REACHED THE API AND NONE REACHED A SCREEN (quince#1212).
//
// Every layer was tested against its own contract — the config round trip proved the keys survive a
// PUT, the page tests proved what the page rendered — and NOTHING ASSERTED THAT A SETTING WHICH
// EXISTS IS REACHABLE BY A PERSON. That is the same shape as two other qn.12 defects found on
// hardware the same day: an endpoint with no caller, and a page with no link. This file is the
// missing assertion for the third.

vi.mock("@/lib/api", async (orig) => {
  const actual = await orig<typeof import("@/lib/api")>();
  return {
    ...actual,
    api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), del: vi.fn(), getText: vi.fn() },
  };
});

const mockApi = vi.mocked(api);

// A push-capable browser with one live subscription, so the controls above the settings render too —
// the settings must sit correctly BELOW them, not merely exist.
function stageSupportedBrowser() {
  vi.stubGlobal("navigator", {
    userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)",
    platform: "MacIntel",
    maxTouchPoints: 0,
    serviceWorker: {
      register: vi.fn(),
      ready: Promise.resolve({ pushManager: { getSubscription: vi.fn().mockResolvedValue(null) } }),
    },
  });
  vi.stubGlobal("PushManager", function PushManager() {});
  vi.stubGlobal("crypto", { subtle: { digest: vi.fn().mockResolvedValue(new ArrayBuffer(32)) } });
  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: false }));
}

// NO SERVICE WORKER AND NO PUSH API — the Lockdown Mode signature (spec D7). The settings must still
// render: they govern what quince SENDS to every subscribed device, and are not a property of the
// browser reading them.
function stageUnsupportedBrowser() {
  vi.stubGlobal("navigator", {
    userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)",
    platform: "iPhone",
    maxTouchPoints: 5,
  });
  vi.stubGlobal("crypto", { subtle: { digest: vi.fn().mockResolvedValue(new ArrayBuffer(32)) } });
  vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({ matches: false }));
}

function stageServer(over = {}) {
  mockApi.get.mockImplementation(
    routeGet(
      { "/api/notifications": { vapid_public_key: "BFakeKey", subscriptions: [] } },
      over,
    ) as never,
  );
}

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

beforeEach(() => {
  vi.clearAllMocks();
  stageSupportedBrowser();
  stageServer();
});

afterEach(() => vi.unstubAllGlobals());

describe("the notifications settings reach a screen", () => {
  // THE HEADLINE DEFECT. `backup_completed` defaults to false, so a whole kind of notification was
  // off with the only route to it being a hand-edit of `config.yml` — which inverts D12's promise
  // that the UI edits the file.
  it("offers the category that is off by default, and can turn it on", async () => {
    mockApi.put.mockResolvedValue(testConfigResponse({}));
    renderPage();

    const finished = await screen.findByRole("checkbox", { name: /a backup finished/i });
    expect(finished).not.toBeChecked();

    fireEvent.click(finished);
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(mockApi.put).toHaveBeenCalled());
    const sent = mockApi.put.mock.calls[0][1] as { notifications: Record<string, unknown> };
    expect(sent.notifications.backup_completed).toBe(true);
  });

  // ALL EIGHT, BY NAME. The gap was never "one control is missing" — it was that the section had no
  // surface at all, so an inventory is the honest assertion.
  it("renders every key of the notifications section", async () => {
    renderPage();

    for (const name of [
      /a backup is due/i,
      /a backup is badly overdue/i,
      /a backup needs you/i,
      /a backup failed/i,
      /a backup finished/i,
    ]) {
      expect(await screen.findByRole("checkbox", { name })).toBeInTheDocument();
    }
    for (const name of [/^due after \(days\)$/i, /^overdue after \(days\)$/i, /^wait between reminders \(hours\)$/i]) {
      expect(screen.getByRole("spinbutton", { name })).toBeInTheDocument();
    }
  });

  // A PUT REPLACES THE WHOLE DOCUMENT, so a form that ships only its own section zeroes every key it
  // does not render — `tls` to two empty strings, which is TLS OFF (quince#493). This asserts the
  // form spreads the document it fetched rather than rebuilding one.
  it("sends the whole document, not just the section it edits", async () => {
    mockApi.put.mockResolvedValue(testConfigResponse({}));
    renderPage();

    fireEvent.change(await screen.findByRole("spinbutton", { name: /^due after \(days\)$/i }), {
      target: { value: "7" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    await waitFor(() => expect(mockApi.put).toHaveBeenCalled());
    const sent = mockApi.put.mock.calls[0][1] as Record<string, Record<string, unknown>>;
    expect(sent.notifications.staleness_days).toBe(7);
    expect(sent.backup.preferred_transport).toBe("usb");
    expect(sent.tls).toEqual({ cert_file: "", key_file: "" });
    expect(sent.reconcile.interval_minutes).toBe(360);
    expect(sent.ui.theme).toBe("system");
  });

  // THE CROSS-FIELD RULE IS THE SERVER'S. `Validate` refuses `overdue_days < staleness_days`, and the
  // refusal has to land on the field it names rather than as a banner — a 422 with nowhere to render
  // is indistinguishable from a save that did nothing.
  it("puts a server refusal on the field it names", async () => {
    mockApi.put.mockRejectedValue(
      new APIError(422, "invalid", "invalid configuration", {
        errors: [
          {
            path: "notifications.overdue_days",
            message: "must be >= notifications.staleness_days (3)",
          },
        ],
      }),
    );
    renderPage();

    fireEvent.change(await screen.findByRole("spinbutton", { name: /^overdue after \(days\)$/i }), {
      target: { value: "1" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^save$/i }));

    expect(await screen.findByText(/must be >= notifications.staleness_days/i)).toBeInTheDocument();
  });

  // THE FILE BESIDE THE FORM (Operator ruling 2026-08-18). Editing a setting and seeing how it is
  // mirrored in `config.yml` is the whole reason the scope is wider than eight controls.
  it("shows config.yml beside the controls", async () => {
    renderPage();

    expect(await screen.findByText(/current configuration/i)).toBeInTheDocument();
    expect(screen.getByText(/staleness_days: 3/)).toBeInTheDocument();
    expect(screen.getByText(/\/data\/config\.yml/)).toBeInTheDocument();
  });

  // THESE KEYS ARE NOT A PROPERTY OF THE BROWSER READING THEM. An iPhone in Lockdown Mode cannot
  // receive a push and is still a perfectly good place to configure the phone that can — so the
  // settings must sit OUTSIDE the support branch that guards the subscribe controls.
  it("renders on a browser that cannot receive notifications at all", async () => {
    vi.unstubAllGlobals();
    stageUnsupportedBrowser();
    stageServer();
    renderPage();

    expect(await screen.findByText(/notifications are unavailable on this browser/i)).toBeInTheDocument();
    expect(await screen.findByRole("checkbox", { name: /a backup is due/i })).toBeInTheDocument();
    expect(screen.getByRole("spinbutton", { name: /^due after \(days\)$/i })).toBeInTheDocument();
  });
});
