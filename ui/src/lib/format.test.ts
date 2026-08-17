import { describe, expect, it } from "vitest";
import {
  formatBytes,
  formatDuration,
  formatPercent,
  formatRelativeTime,
  formatResetInterval,
  formatSpeed,
} from "./format";

// formatResetInterval feeds "This demo resets every …" (public-demo story 6). Every case here is a
// sentence a stranger reads on the login screen, so the assertions are on the whole phrase rather
// than on a number.
describe("formatResetInterval", () => {
  it("reads as English after `every`", () => {
    expect(formatResetInterval(30)).toBe("30 minutes");
    expect(formatResetInterval(1)).toBe("minute");
    expect(formatResetInterval(60)).toBe("hour");
    expect(formatResetInterval(120)).toBe("2 hours");
  });

  // NEVER ROUNDED. 90 minutes is not "1.5 hours" and not "2 hours": this line tells a visitor when
  // their work disappears, so an approximation here is a false statement rather than a nicety.
  it("converts to hours only on an exact multiple", () => {
    expect(formatResetInterval(90)).toBe("90 minutes");
    expect(formatResetInterval(45)).toBe("45 minutes");
    expect(formatResetInterval(180)).toBe("3 hours");
  });

  // "" is the signal that there is no schedule to state, and the caller keeps the reset warning and
  // drops only the interval. A value that arrives unusable must land here rather than render raw —
  // "resets every NaN minutes" on a public login screen is the failure this case exists to stop.
  it.each([undefined, 0, -5, 12.5, NaN, Infinity])("has nothing to say for %s", (v) => {
    expect(formatResetInterval(v as number | undefined)).toBe("");
  });
});

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

// Operator, 2026-08-17: "more precision to make it feel more alive". The live received figure
// updates up to twice a second, but at GB scale one decimal only changes every few seconds — so the
// number looked stuck while gigabytes were arriving.
describe("formatBytes precision", () => {
  it("keeps one decimal by default, so the figures that move once an hour stay quiet", () => {
    expect(formatBytes(3_400_000_000)).toBe("3.4 GB");
    expect(formatBytes(531_100_000_000)).toBe("531.1 GB");
  });

  it("takes two when asked, which is what makes a live figure visibly move", () => {
    expect(formatBytes(3_400_000_000, 2)).toBe("3.40 GB");
    expect(formatBytes(3_412_000_000, 2)).toBe("3.41 GB");
    // The point of the extra digit: these two differ at 2dp and are identical at 1dp.
    expect(formatBytes(3_412_000_000)).toBe(formatBytes(3_400_000_000));
  });

  it("never fractions whole bytes, whatever is asked — there is no half a byte", () => {
    expect(formatBytes(512, 2)).toBe("512 B");
  });
});
