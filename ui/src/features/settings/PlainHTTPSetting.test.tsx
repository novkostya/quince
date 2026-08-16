import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { PlainHTTPSetting } from "./PlainHTTPSetting";
import { api } from "@/lib/api";
import { useConfig } from "@/lib/config";
import type { Health } from "@/lib/types";

const OK: Health = { status: "ok", version: "t", mode: "normal" };

function renderIn() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <PlainHTTPSetting />
    </QueryClientProvider>,
  );
}

// THIS CONTROL IS WHAT MAKES THE BANNER'S SAFE TO PRESS — quince#1069. The Operator turned plain
// HTTP off from the banner and found that nothing could turn it back on: login answers 426 at that
// address, the pre-auth route answers 409 to a caller with no session, and the first-run confirm
// does not render once the install is claimed. A signed-in admin needs a way back, and this is it.
describe("the plain-HTTP setting", () => {
  beforeEach(() => vi.restoreAllMocks());

  it("turns it back on, behind a confirm because that direction relaxes something", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: false });
    const post = vi.spyOn(api, "post").mockResolvedValue({});
    renderIn();

    expect(await screen.findByText(/Not allowed/i)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Allow plain HTTP/i }));
    // ONE PRESS MUST NOT DO IT, as everywhere else this setting is written.
    expect(post).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /^Allow it$/i }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/config/insecure-transport", { allow: true }),
    );
  });

  // AND THE TIGHTENING DIRECTION NEEDS NO CEREMONY: it is reversible from this same row, by the
  // reader who is already signed in. A confirm there would be friction charged for doing the right
  // thing.
  it("turns it off in one press", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: true });
    const post = vi.spyOn(api, "post").mockResolvedValue({});
    renderIn();

    fireEvent.click(await screen.findByRole("button", { name: /Stop allowing it/i }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/config/insecure-transport", { allow: false }),
    );
  });

  // IT STATES WHICH WAY ROUND THE INSTALL IS, rather than offering a control whose label is the only
  // clue. A reader arriving from the banner's "you can allow it again in Settings" needs to see the
  // state they are in before they change it.
  it("says what the install is doing now", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: true });
    renderIn();

    expect(await screen.findByText(/travels in the clear/i)).toBeInTheDocument();
  });
});

// THE CONFIG QUERY IS INVALIDATED TOO, AND THE STALE PANEL WAS THE SMALLER HALF — Operator,
// 2026-08-17: *"when you turn off insecure_transport via banner on Settings page, it's not
// updated."* `ConfigEditor`'s draft follows the config query, and `PUT /api/config` is a
// full-document replace — so a reader who turned the setting off and then saved an unrelated field
// on the same screen would have shipped a document still carrying `allow_insecure_transport: true`.
//
// ASSERTED AS A REFETCH rather than as a call to `invalidateQueries`, because what the user gets is
// the panel updating, and a test that spied on the client would pass against a key that nothing
// reads.
describe("the config view beside it", () => {
  it("refetches the configuration after the setting changes", async () => {
    const get = vi.spyOn(api, "get").mockImplementation((async (path: string) =>
      path.startsWith("/api/config")
        ? { config: { sessions: { allow_insecure_transport: true } }, warnings: [], source: "file" }
        : { ...OK, insecure_transport_allowed: true }) as typeof api.get);
    vi.spyOn(api, "post").mockResolvedValue({});

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <ConfigReader />
        <PlainHTTPSetting />
      </QueryClientProvider>,
    );

    await waitFor(() => expect(get).toHaveBeenCalledWith("/api/config"));
    const before = get.mock.calls.filter(([p]) => p === "/api/config").length;

    fireEvent.click(await screen.findByRole("button", { name: /Stop allowing it/i }));

    await waitFor(() => {
      const after = get.mock.calls.filter(([p]) => p === "/api/config").length;
      expect(after).toBeGreaterThan(before);
    });
  });
});

// A minimal subscriber, because `invalidateQueries` refetches ACTIVE queries only — with no
// observer the assertion above would pass against a client that had marked nothing.
function ConfigReader() {
  useConfig();
  return null;
}
