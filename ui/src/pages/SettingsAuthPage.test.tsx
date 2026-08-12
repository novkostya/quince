import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { SettingsAuthPage } from "./SettingsAuthPage";
import { SettingsPage } from "./SettingsPage";
import { api } from "@/lib/api";

// qn.6m slice 6 — quince#841 ruling A: the auth surface is a PAGE linked from Settings, not a fourth
// block inside it.

function renderPage(el: React.ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{el}</MemoryRouter>
    </QueryClientProvider>,
  );
}

// A FULL config document, not a stub — and the difference is load-bearing rather than tidiness.
//
// With a partial one the page's `data` guard never yields a rendered grid, so the two tests below
// that assert on the LOADED page were passing without ever reaching it: "no longer renders the
// passkeys card" would have held against the OLD code too, because the old card lived inside that
// same guard. A test that cannot fail is worse than no test, and this one was found by probing the
// link's guard and watching the wrong tests go red.
const FULL_CONFIG = {
  config: {
    backup: { preferred_transport: "usb", require_encryption: true },
    storage: [{ name: "s" }],
    sessions: { allow_insecure_transport: false },
    reconcile: { interval_minutes: 360 },
    ui: { theme: "system" },
  },
  warnings: [],
  source: { path: "/data/config.yml", mtime: null },
};

// A passkey surface that CAN offer its button, so "the card is not here" is a real observation.
const PASSKEYS_OK = { rp_id: "quince.example.com", supported: true, passkeys: [] };

// DISPATCHED BY PATH, NOT ONE BLANKET RESOLVE — and this is the second vacuity this file had.
//
// A single `mockResolvedValue` answers `/api/config` AND `/api/auth/passkeys` with the same body, so
// the passkeys card sees `supported: undefined`, renders its unsupported state, and offers no
// button. "The card is not on this page" then holds whether the card is there or not. Measured:
// with `<Passkeys />` put BACK into SettingsPage, the blanket-mock version of these tests still
// passed. This is the same defect that bit quince#834's settings test.
function mockAPI(config: unknown = FULL_CONFIG, passkeys: unknown = PASSKEYS_OK) {
  vi.spyOn(api, "get").mockImplementation((path: string) => {
    if (path.startsWith("/api/auth/passkeys")) return Promise.resolve(passkeys);
    if (path.startsWith("/api/config")) return Promise.resolve(config);
    return Promise.reject(new Error(`unmocked GET ${path}`));
  });
}

beforeEach(() => vi.restoreAllMocks());

describe("the auth page", () => {
  it("carries the passkeys surface and a way back to Settings", async () => {
    mockAPI();
    renderPage(<SettingsAuthPage />);

    expect(screen.getByRole("heading", { name: "Sign-in", level: 1 })).toBeInTheDocument();
    // The passkeys card moved here from Settings. Asserting its ACTION renders rather than matching
    // the word "passkey", which appears several times on this page — and rather than re-asserting
    // the card's internals, which `features/settings/Passkeys.test.tsx` already owns.
    expect(await screen.findByRole("button", { name: /add a passkey/i })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /settings/i })).toHaveAttribute("href", "/settings");
  });
});

describe("Settings links to it", () => {
  it("offers the link", async () => {
    mockAPI();
    renderPage(<SettingsPage />);

    expect(await screen.findByRole("link", { name: /sign-in/i })).toHaveAttribute(
      "href",
      "/settings/auth",
    );
  });

  // THE COST quince#834 ACCEPTED, NOW PAID BACK — and this is the assertion that makes the move
  // worth anything. That card sat inside Settings' `data` guard, so a box whose config failed to
  // LOAD showed no passkey surface at all. Moving the card to its own page only fixes that if the
  // WAY TO REACH IT is not behind the same condition; a link inside the guard would leave somebody
  // with a broken config unable to reach the credentials they get in with.
  it("STILL offers the link when the config cannot be loaded", async () => {
    vi.spyOn(api, "get").mockImplementation((p: string) =>
      p.startsWith("/api/config")
        ? Promise.reject(new Error("config is unreadable"))
        : Promise.resolve(PASSKEYS_OK),
    );
    renderPage(<SettingsPage />);

    expect(await screen.findByText(/could not load configuration/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /sign-in/i })).toHaveAttribute("href", "/settings/auth");
  });

  // The card must not be in BOTH places — a duplicated surface is two things to keep in step, and
  // the one left behind is the one that rots.
  //
  // ASSERTED ON THE REQUEST, NOT ON THE RENDERED BUTTON, and that is the third vacuity this file
  // had. The obvious form — find the link, then `queryByRole` for "Add a passkey" — resolves the
  // instant the link appears, and the link is UNCONDITIONAL, so the assertion runs before the
  // passkeys query has settled and holds whether the card is mounted or not. Measured: with
  // `<Passkeys />` put back into SettingsPage, that version still passed.
  //
  // "Nothing asked for the credential list" is the same claim without the race: if the card is not
  // mounted, nobody fetches it, and `waitFor` gives the query every chance to appear before this
  // concludes it did not.
  it("no longer mounts the passkeys card — nothing here fetches the credential list", async () => {
    mockAPI();
    renderPage(<SettingsPage />);

    await screen.findByRole("link", { name: /sign-in/i });
    await waitFor(() => expect(api.get).toHaveBeenCalledWith("/api/config"));

    const paths = vi.mocked(api.get).mock.calls.map(([p]) => p);
    expect(paths).not.toContain("/api/auth/passkeys");
  });
});
