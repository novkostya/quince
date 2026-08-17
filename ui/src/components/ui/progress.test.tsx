import { describe, expect, it } from "vitest";
import { render, screen } from "@testing-library/react";
import { Progress } from "./progress";

// quince#376. The component's comment promised an indeterminate track for a null percent from the
// day it was written, and the code rendered a zero-width fill — a bar sitting at 0%, which is what
// a stalled transfer looks like. There was no test either way, which is how the two disagreed
// unnoticed. These assert the distinction rather than the styling.
describe("Progress", () => {
  it("renders a determinate bar with a value when it has one", () => {
    render(<Progress percent={63} />);
    const bar = screen.getByRole("progressbar");
    expect(bar.getAttribute("aria-valuenow")).toBe("63");
    // Absent, not "false": the attribute's presence is the whole signal.
    expect(bar.getAttribute("data-indeterminate")).toBeNull();
  });

  it("renders indeterminate for a null percent, claiming no value", () => {
    render(<Progress percent={null} />);
    const bar = screen.getByRole("progressbar");
    // No aria-valuenow — an indeterminate bar reporting 0 would be a measurement, and a wrong one.
    expect(bar.getAttribute("aria-valuenow")).toBeNull();
    expect(bar.getAttribute("data-indeterminate")).toBe("true");
    // The travelling segment is what makes it read as "working" rather than "stopped".
    expect(bar.querySelector(".quince-indeterminate")).not.toBeNull();
  });

  it("clamps out-of-range values instead of overflowing the track", () => {
    render(<Progress percent={140} />);
    expect(screen.getByRole("progressbar").getAttribute("aria-valuenow")).toBe("100");
  });
});
