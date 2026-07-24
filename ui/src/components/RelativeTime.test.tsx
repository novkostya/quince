import { describe, it, expect, vi, afterEach } from "vitest";
import { act, render, screen } from "@testing-library/react";
import { RelativeTime } from "./RelativeTime";

describe("RelativeTime", () => {
  afterEach(() => vi.useRealTimers());

  it("renders a <time> with the exact time on hover and a relative label", () => {
    render(<RelativeTime iso="2026-07-20T11:59:30Z" />);
    const el = document.querySelector("time");
    expect(el).toBeTruthy();
    expect(el?.getAttribute("datetime")).toBe("2026-07-20T11:59:30Z");
    expect(el?.getAttribute("title")).toBeTruthy(); // exact local date-time on hover
  });

  it("advances the label live as time passes (no prop change)", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2030-01-01T00:05:00Z"));
    render(<RelativeTime iso="2030-01-01T00:00:00Z" />);
    act(() => vi.advanceTimersByTime(16_000)); // tick → now ≈ 00:05:15 → "5 minutes ago"
    const first = screen.getByText(/ago/).textContent;
    act(() => {
      vi.setSystemTime(new Date("2030-01-01T00:20:00Z"));
      vi.advanceTimersByTime(16_000); // tick → now ≈ 00:20:15 → "20 minutes ago"
    });
    expect(screen.getByText(/ago/).textContent).not.toBe(first);
  });
});
