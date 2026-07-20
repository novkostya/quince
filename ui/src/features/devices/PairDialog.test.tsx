import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, act } from "@testing-library/react";
import { PairDialog } from "./PairDialog";
import { APIError } from "@/lib/api";
import { useOpsStore } from "@/stores/ops";
import type { Op } from "@/lib/types";

beforeEach(() => useOpsStore.setState({ byId: {} }));

function pushOp(op: Op) {
  act(() => useOpsStore.getState().upsert(op));
}

describe("PairDialog", () => {
  it("starts pairing and narrates op.updated through to success", async () => {
    const post = vi.fn().mockResolvedValue({ op_id: "OP1" });
    render(<PairDialog udid="DEV-1" post={post} />);

    fireEvent.click(screen.getByRole("button", { name: /^pair$/i }));
    fireEvent.click(await screen.findByRole("button", { name: /start pairing/i }));
    expect(post).toHaveBeenCalledWith("/api/devices/DEV-1/pair", undefined);

    pushOp({
      id: "OP1",
      udid: "DEV-1",
      kind: "pair",
      state: "waiting_for_user",
      message: "Tap Trust on the device.",
      error: null,
    });
    await screen.findByText(/tap trust on the device/i);

    pushOp({
      id: "OP1",
      udid: "DEV-1",
      kind: "pair",
      state: "succeeded",
      message: "Paired with this computer.",
      error: null,
    });
    await screen.findByRole("button", { name: /done/i });
  });

  it("surfaces a start error (needs USB / 409) without a dead button", async () => {
    const post = vi.fn().mockRejectedValue(new APIError(409, "conflict", "pairing needs a USB connection"));
    render(<PairDialog udid="DEV-1" post={post} />);

    fireEvent.click(screen.getByRole("button", { name: /^pair$/i }));
    fireEvent.click(await screen.findByRole("button", { name: /start pairing/i }));
    await screen.findByText(/needs a usb connection/i);
  });

  // (bq) fix: a pair intent deep-linked from the dashboard card auto-opens the dialog on arrival,
  // so the click lands IN the dialog rather than just navigating (qn.3's narrated-flow-on-details
  // decision stands — this only changes where the click delivers).
  it("auto-opens when arriving with a pair intent", () => {
    render(<PairDialog udid="DEV-1" autoOpen />);
    expect(screen.getByText(/pair this device/i)).toBeTruthy();
  });

  it("stays closed without a pair intent (the trigger button is shown, not the dialog)", () => {
    render(<PairDialog udid="DEV-1" />);
    expect(screen.queryByText(/pair this device/i)).toBeNull();
  });
});
