import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import App from "./App";

afterEach(() => {
  vi.restoreAllMocks();
});

test("renders the shell with product name and both nav items", () => {
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new Error("no server")));
  render(<App />);
  expect(screen.getByText("quince")).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /devices/i })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: /settings/i })).toBeInTheDocument();
});

test("shows the version returned by /api/health", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ status: "ok", version: "1.2.3" }),
    }),
  );
  render(<App />);
  await waitFor(() => {
    expect(screen.getByTestId("version")).toHaveTextContent("v1.2.3");
  });
});
