import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { ReconcilingNotice } from "./ReconcilingNotice";
import { api } from "@/lib/api";

function renderNotice() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ReconcilingNotice />
    </QueryClientProvider>,
  );
}

describe("ReconcilingNotice", () => {
  beforeEach(() => vi.restoreAllMocks());
  afterEach(() => vi.restoreAllMocks());

  // THE POSITIVE CASE, and the assertion is on the SENTENCE a user can act on rather than on the
  // status. "some backups may not be listed yet" is what stops somebody concluding a disk is empty;
  // "reconciling" alone is a word about the daemon.
  it("says a list may be short while quince is reconciling", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      status: "ok",
      version: "t",
      mode: "normal",
      reconciling: true,
    });
    renderNotice();
    expect(await screen.findByText(/some backups may not be listed yet/i)).toBeInTheDocument();
  });

  // NOTHING AT ALL WHEN FALSE — not a quieter variant. A permanent element that merely changes
  // wording trains a user to stop reading it, and this sentence is worth something only when it is
  // rare.
  it("renders nothing when a pass is not running", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      status: "ok",
      version: "t",
      mode: "normal",
      reconciling: false,
    });
    renderNotice();
    // SETTLE THE QUERY BEFORE ASSERTING ABSENCE. Without this the test passes on the loading state —
    // the notice is absent because nothing has answered yet, not because the answer was `false`, and
    // it would keep passing with `useReconciling` returning `true` unconditionally.
    await vi.waitFor(() => expect(api.get).toHaveBeenCalled());
    expect(screen.queryByRole("status")).toBeNull();
  });

  // A SERVER OLDER THAN THIS UI omits the field, and absent must read as "not reconciling" rather
  // than as unknown-so-warn: such a server reconciled fully before it served anything, so there is no
  // provisional state to declare.
  it("renders nothing when the server does not report the field", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ status: "ok", version: "t", mode: "normal" });
    renderNotice();
    await vi.waitFor(() => expect(api.get).toHaveBeenCalled());
    expect(screen.queryByRole("status")).toBeNull();
  });

  // A HEALTH PROBE THAT FAILS MUST NOT MAKE QUINCE CLAIM ITS OWN DATA IS INCOMPLETE. The hook
  // swallows the error into UNKNOWN_HEALTH, and the notice stays down — one wrong statement is not
  // improved by layering a second on it.
  it("renders nothing when health cannot be reached", async () => {
    vi.spyOn(api, "get").mockRejectedValue(new Error("network"));
    renderNotice();
    await vi.waitFor(() => expect(api.get).toHaveBeenCalled());
    expect(screen.queryByRole("status")).toBeNull();
  });
});
