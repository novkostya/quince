import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { RescanButton } from "./RescanButton";
import { APIError } from "@/lib/api";

describe("RescanButton", () => {
  it("posts to the rescan endpoint and shows the in-progress note on 202", async () => {
    const post = vi.fn().mockResolvedValue({ status: "rescanning" });
    render(<RescanButton post={post} />);

    fireEvent.click(screen.getByRole("button", { name: /rescan/i }));

    expect(post).toHaveBeenCalledWith("/api/devices/rescan");
    await screen.findByText(/rescanning for devices/i);
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
});
