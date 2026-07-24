import { describe, expect, it } from "vitest";
import { formatBytes, formatDuration, formatPercent, formatRelativeTime, formatSpeed } from "./format";

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

describe("formatRelativeTime", () => {
  const now = new Date("2026-07-20T12:00:00Z").getTime();
  it("collapses sub-minute ages to 'just now' (no noisy seconds)", () => {
    expect(formatRelativeTime("2026-07-20T12:00:00Z", now)).toBe("just now");
    expect(formatRelativeTime("2026-07-20T11:59:30Z", now)).toBe("just now"); // 30s
  });
  it("shows minutes / hours / days", () => {
    expect(formatRelativeTime("2026-07-20T11:58:00Z", now)).toBe("2 minutes ago");
    expect(formatRelativeTime("2026-07-20T10:00:00Z", now)).toBe("2 hours ago");
    expect(formatRelativeTime("2026-07-18T12:00:00Z", now)).toBe("2 days ago");
  });
  it("guards empty / invalid", () => {
    expect(formatRelativeTime("", now)).toBe("—");
    expect(formatRelativeTime("not-a-date", now)).toBe("—");
  });
});
