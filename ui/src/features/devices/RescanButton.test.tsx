import { describe, it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, act, waitFor } from "@testing-library/react";
import { RescanButton } from "./RescanButton";
import { APIError } from "@/lib/api";

afterEach(() => {
  vi.useRealTimers();
});

describe("RescanButton", () => {
  it("posts to the rescan endpoint", async () => {
    const post = vi.fn().mockResolvedValue({ status: "rescanning" });
    render(<RescanButton post={post} />);

    fireEvent.click(screen.getByRole("button", { name: /rescan/i }));

    expect(post).toHaveBeenCalledWith("/api/devices/rescan");
    await act(async () => {});
  });

  // THE REGRESSION THIS FILE EXISTS FOR. `POST /api/devices/rescan` answers 202 the moment the
  // restart is ACCEPTED, so the button used to re-enable itself milliseconds after the click while
  // the rescan was still running — inviting a second click, and reflowing the header as a separate
  // "Rescanning for devices…" note appeared beside it (quince#325, from Operator screenshots).
  it("stays DISABLED after the 202 resolves, rather than snapping back to a clickable button", async () => {
    const post = vi.fn().mockResolvedValue({ status: "rescanning" });
    render(<RescanButton post={post} />);
    const button = screen.getByRole("button", { name: /rescan/i });

    fireEvent.click(button);
    await act(async () => {}); // let the 202 resolve — the moment it used to re-enable

    expect(button).toBeDisabled();
    expect(screen.queryByText(/rescanning for devices/i)).toBeNull();
  });

  // The label IS the layout: "Rescanning…" is wider than "Rescan", so swapping it is what made the
  // header jump. The icon carries the progress instead.
  it("keeps the label 'Rescan' throughout and spins the icon instead", async () => {
    const post = vi.fn().mockResolvedValue({ status: "rescanning" });
    render(<RescanButton post={post} />);
    const button = screen.getByRole("button", { name: /rescan/i });

    expect(button.querySelector("svg")?.getAttribute("class") ?? "").not.toContain("animate-spin");

    fireEvent.click(button);
    await act(async () => {});

    expect(button.textContent).toContain("Rescan");
    expect(button.textContent).not.toContain("Rescanning");
    expect(button.querySelector("svg")?.getAttribute("class") ?? "").toContain("animate-spin");
  });

  it("returns to idle after the settle window, so the button is usable again", async () => {
    vi.useFakeTimers();
    const post = vi.fn().mockResolvedValue({ status: "rescanning" });
    render(<RescanButton post={post} />);
    const button = screen.getByRole("button", { name: /rescan/i });

    fireEvent.click(button);
    await act(async () => {});
    expect(button).toBeDisabled();

    await act(async () => {
      vi.advanceTimersByTime(2500);
    });
    expect(button).not.toBeDisabled();
  });

  it("explains and disables when the muxer is external (409)", async () => {
    const post = vi
      .fn()
      .mockRejectedValue(new APIError(409, "muxer_external", "muxer is external"));
    render(<RescanButton post={post} />);

    fireEvent.click(screen.getByRole("button", { name: /rescan/i }));

    await screen.findByText(/muxer is external/i);
    expect(screen.getByRole("button", { name: /rescan/i })).toBeDisabled();
  });

  // A transient failure must NOT leave the button disabled — that would be the dead control the
  // 409 branch is carefully avoiding.
  it("re-enables after a transient failure so the user can try again", async () => {
    const post = vi.fn().mockRejectedValue(new APIError(503, "unavailable", "nope"));
    render(<RescanButton post={post} />);
    const button = screen.getByRole("button", { name: /rescan/i });

    fireEvent.click(button);

    await waitFor(() => expect(button).not.toBeDisabled());
  });
});
