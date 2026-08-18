import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NotificationsInstallPage } from "./NotificationsInstallPage";
import { api } from "@/lib/api";
import { routeGet, testConfig } from "@/test/config";

// `category_off` — THE FIFTH STATUS CAUSE, WHICH NOTHING COULD REPORT (quince#1212).
//
// The spec's status table lists six rows for five causes and this was the one with no client: the
// cause is a fact about `notifications:`, and until the settings reached a screen no client held
// that section. `category_off` appeared in the codebase exactly once, in a comment.

vi.mock("@/lib/api", () => ({
  api: { get: vi.fn(), post: vi.fn(), put: vi.fn(), del: vi.fn(), getText: vi.fn() },
}));

const mockApi = vi.mocked(api);

function stage(notifications: Partial<ReturnType<typeof testConfig>["notifications"]>) {
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
  mockApi.get.mockImplementation(
    routeGet(
      { "/api/notifications": { vapid_public_key: "BFakeKey", subscriptions: [] } },
      { notifications: { ...testConfig().notifications, ...notifications } },
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

beforeEach(() => vi.clearAllMocks());
afterEach(() => vi.unstubAllGlobals());

describe("what quince says when a category is off", () => {
  // THE DEFAULT CONFIGURATION IS NOT A FAULT. `backup_completed` is off by default and four kinds
  // are on, so a notice here would fire on every healthy install — which is how a warning stops
  // being read.
  it("says nothing about the default configuration", async () => {
    stage({});
    renderPage();

    expect(await screen.findByRole("checkbox", { name: /a backup is due/i })).toBeInTheDocument();
    expect(screen.queryByText(/will not notify you about anything/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/never remind you a backup is due/i)).not.toBeInTheDocument();
  });

  // EVERY KIND OFF — a subscription that is live and can never receive anything. This is the honest
  // `category_off`, and its remedy is the switches directly below it.
  it("says nothing will arrive when every kind is off", async () => {
    stage({
      backup_available: false,
      backup_overdue: false,
      action_required: false,
      backup_failed: false,
      backup_completed: false,
    });
    renderPage();

    expect(await screen.findByText(/will not notify you about anything/i)).toBeInTheDocument();
  });

  // BOTH REMINDERS OFF IS THE WORSE STATE, not the milder one: failures still arrive, so
  // notifications visibly "work", and the one thing the rung exists for silently never happens.
  it("names the reminders specifically when only they are off", async () => {
    stage({ backup_available: false, backup_overdue: false });
    renderPage();

    expect(await screen.findByText(/never remind you a backup is due/i)).toBeInTheDocument();
    expect(screen.queryByText(/will not notify you about anything/i)).not.toBeInTheDocument();
  });

  // IT READS THE DRAFT, NOT THE SAVED DOCUMENT. Unticking the last category has to say what that
  // will mean BEFORE Save — a warning that appears only after the document is written is a report
  // rather than a warning.
  it("warns as the last switch is turned off, before anything is saved", async () => {
    stage({ backup_overdue: false, action_required: false, backup_failed: false });
    renderPage();

    const due = await screen.findByRole("checkbox", { name: /a backup is due/i });
    expect(screen.queryByText(/will not notify you about anything/i)).not.toBeInTheDocument();

    fireEvent.click(due);

    expect(screen.getByText(/will not notify you about anything/i)).toBeInTheDocument();
    expect(mockApi.put).not.toHaveBeenCalled();
  });
});
