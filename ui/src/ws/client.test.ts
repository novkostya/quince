import { describe, expect, it } from "vitest";
import { backoffDelay } from "./client";

describe("backoffDelay", () => {
  it("grows exponentially and caps at 30s (with jitter factor 1.0)", () => {
    const noJitter = () => 0.5; // 0.5 + 0.5 = 1.0
    expect(backoffDelay(0, noJitter)).toBe(500);
    expect(backoffDelay(1, noJitter)).toBe(1000);
    expect(backoffDelay(2, noJitter)).toBe(2000);
    expect(backoffDelay(10, noJitter)).toBe(30_000);
  });

  it("applies ±50% jitter", () => {
    expect(backoffDelay(0, () => 0)).toBe(250); // 500 * 0.5
    expect(backoffDelay(0, () => 1)).toBe(750); // 500 * 1.5
  });
});
