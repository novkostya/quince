import { describe, expect, it } from "vitest";
import { formatBytes, formatDuration, formatPercent, formatSpeed } from "./format";

describe("formatBytes", () => {
  it("scales SI decimal with spaced units", () => {
    expect(formatBytes(0)).toBe("0 B");
    expect(formatBytes(999)).toBe("999 B");
    expect(formatBytes(1000)).toBe("1.0 KB");
    expect(formatBytes(2_400_000_000)).toBe("2.4 GB");
    expect(formatBytes(3_600_000_000)).toBe("3.6 GB");
  });
  it("guards bad input", () => {
    expect(formatBytes(-1)).toBe("—");
    expect(formatBytes(Number.NaN)).toBe("—");
  });
});

describe("formatSpeed", () => {
  it("appends /s", () => {
    expect(formatSpeed(7500)).toBe("7.5 KB/s");
  });
});

describe("formatPercent", () => {
  it("rounds and handles null", () => {
    expect(formatPercent(null)).toBe("—");
    expect(formatPercent(63)).toBe("63%");
    expect(formatPercent(63.4)).toBe("63%");
  });
});

describe("formatDuration", () => {
  it("humanizes", () => {
    expect(formatDuration(5)).toBe("5s");
    expect(formatDuration(65)).toBe("1m 5s");
    expect(formatDuration(3700)).toBe("1h 1m");
    expect(formatDuration(-1)).toBe("—");
  });
});
