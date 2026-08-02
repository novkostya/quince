import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider, QueryCache } from "@tanstack/react-query";
import { LoginPage } from "./LoginPage";
import type { Health } from "@/lib/types";

const get = vi.fn();
vi.mock("@/lib/api", () => ({
  api: { get: (p: string) => get(p), post: () => Promise.resolve({}) },
}));

beforeEach(() => get.mockReset());

function renderLogin() {
  // A rejecting query is EXPECTED in one test; swallow it at the cache so vitest does not report
  // the deliberate failure as an unhandled rejection.
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
    queryCache: new QueryCache({ onError: () => {} }),
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <LoginPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function health(mode: Health["mode"]): Health {
  return { status: "ok", version: "0.0.0-dev", mode };
}

describe("LoginPage demo copy", () => {
  // Story 5: a visitor to the public demo has no way to learn the password unless the login screen
  // says it. Asserted on the rendered password rather than on the mode, because the mode being
  // right is not the story — the visitor being able to sign in is.
  it("prints the password when the server reports public_demo", async () => {
    get.mockResolvedValue(health("public_demo"));
    renderLogin();
    await waitFor(() => expect(screen.getByText("demo")).toBeInTheDocument());
    expect(screen.getByText(/public demo/i)).toBeInTheDocument();
  });

  // The half that protects the SHIPPING product. Demo copy leaking into a real deployment would
  // print a password that is not the operator's, on the screen where they are trying to sign in.
  it.each(["normal", "demo"] as const)("prints nothing extra in %s mode", async (mode) => {
    get.mockResolvedValue(health(mode));
    renderLogin();
    await waitFor(() => expect(screen.getByText("Enter your admin password.")).toBeInTheDocument());
    expect(screen.queryByText("demo")).not.toBeInTheDocument();
    expect(screen.queryByText(/public demo/i)).not.toBeInTheDocument();
  });

  // SILENCE ON ANYTHING THAT IS NOT A POSITIVE public_demo. The risk is one-directional:
  // demo copy in a real deployment prints a password that is not the operator's, on the
  // screen where they are trying to sign in. An unrecognised mode is the general case of
  // that, and it also covers a server newer than this UI.
  it("prints nothing for an unrecognised mode", async () => {
    get.mockResolvedValue({ status: "ok", version: "0.0.0-dev", mode: "something-new" });
    renderLogin();
    await waitFor(() => expect(screen.getByText("Enter your admin password.")).toBeInTheDocument());
    expect(screen.queryByText("demo")).not.toBeInTheDocument();
  });
});
