import { beforeEach, describe, expect, it } from "vitest";
import { useConnectionStore } from "./connection";

// Measured on a real phone, 2026-08-17: "Set Automatically" was off, the device had drifted 26 s
// ahead of the server, and a backup that had just started rendered "26s" the instant it appeared —
// every attempt, the same number, which is the signature of an offset rather than of elapsed time.
describe("serverOffsetMs", () => {
  beforeEach(() => useConnectionStore.setState({ serverOffsetMs: 0 }));

  it("starts at zero — no correction rather than a guessed one", () => {
    expect(useConnectionStore.getState().serverOffsetMs).toBe(0);
  });

  it("records a drift big enough to change what anyone reads", () => {
    useConnectionStore.getState().setServerOffset(-26_000);
    expect(useConnectionStore.getState().serverOffsetMs).toBe(-26_000);
  });

  it("ignores sub-2s noise, so live labels do not re-render on every frame", () => {
    // These timestamps have 1 s resolution and arrive over a network; small values are not signal.
    useConnectionStore.getState().setServerOffset(900);
    expect(useConnectionStore.getState().serverOffsetMs).toBe(0);
  });

  it("tracks a drift that grows past the threshold from an existing offset", () => {
    useConnectionStore.getState().setServerOffset(-26_000);
    useConnectionStore.getState().setServerOffset(-26_500);
    expect(useConnectionStore.getState().serverOffsetMs).toBe(-26_000);
    useConnectionStore.getState().setServerOffset(-40_000);
    expect(useConnectionStore.getState().serverOffsetMs).toBe(-40_000);
  });
});
