import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render as rtlRender, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement } from "react";

import { usePasskeyLogin } from "./usePasskeyLogin";
import { PasswordForm } from "./PasswordForm";

// A QUERY CLIENT, BECAUSE `AuthPage` NOW CARRIES THE PLAIN-HTTP WARNING (quince#539). Nothing in
// this suite is about that banner; the form it renders simply sits inside the shared auth primitive,
// and that primitive reads `GET /api/health`. Wrapping here rather than at each call site keeps the
// diff to the harness — and the app has always mounted these components inside a provider, so a
// test that omitted one was exercising a configuration that does not ship.
function render(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return rtlRender(<QueryClientProvider client={qc}>{ui}</QueryClientProvider>);
}

// REWRITTEN for the on-load sheet. The earlier suite asserted CONDITIONAL mediation — a non-modal
// call armed on mount, offered inside the browser's autofill dropdown. That shape was replaced after
// hardware showed it is undiscoverable: the Operator found the passkey only by tapping the key icon
// on the iOS keyboard, past a suggestion list whose first entry was a password.
//
// What replaced it is a modal on load GATED ON MEMORY, plus an unconditional button. The tests below
// are about the gate, because the gate is the whole reason an unprompted sheet is defensible.

function Harness({ onSuccess }: { onSuccess?: () => void }) {
  usePasskeyLogin(onSuccess ?? (() => {}));
  return <div>armed</div>;
}

const origCreds = navigator.credentials;

beforeEach(() => {
  vi.restoreAllMocks();
  vi.stubGlobal("fetch", vi.fn());
  localStorage.clear();
});

afterEach(() => {
  // @ts-expect-error — jsdom's navigator.credentials is not typed as assignable.
  navigator.credentials = origCreds;
  vi.unstubAllGlobals();
  localStorage.clear();
});

// THE GATE, AND IT IS THE POINT OF THE DESIGN. Credential presence is undetectable — no API will say
// whether this device holds a passkey, because that would be a fingerprinting vector. So an on-load
// modal is a guess, and it guesses wrong for everyone who has none: a fresh install, a box after
// `quince auth reset`, the public demo, a laptop the admin never set one up on.
//
// Firing only where a passkey has already been created or used IN THIS BROWSER removes every one of
// those cases without a device heuristic and without asking the server anything.
describe("the unprompted sheet is gated on memory", () => {
  it("does nothing at all on a browser that has never used a passkey", async () => {
    // @ts-expect-error — minimal stand-in for the browser global.
    window.PublicKeyCredential = {};
    // @ts-expect-error — stand-in for the credentials container.
    navigator.credentials = { get: vi.fn() };

    render(<Harness />);
    await screen.findByText("armed");

    // No ceremony begun, and — the part that matters — no sheet.
    expect(fetch).not.toHaveBeenCalled();
    expect(navigator.credentials.get).not.toHaveBeenCalled();
  });

  it("fires once a passkey has been seen in this browser", async () => {
    localStorage.setItem("quince.passkey.seen", "1");
    // @ts-expect-error — stand-in.
    window.PublicKeyCredential = {};
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ ceremony: "c", options: { publicKey: { challenge: "AAAA" } } }),
    } as Response);
    // @ts-expect-error — stand-in.
    navigator.credentials = { get: vi.fn().mockResolvedValue(null) };

    render(<Harness />);

    await waitFor(() => expect(navigator.credentials.get).toHaveBeenCalled());
  });
});

// A FAILURE ON THIS PATH IS NEVER THE USER'S PROBLEM — nobody asked for it. The password form is
// right there and works; an error message would turn an optional convenience into an apparent fault.
// The BUTTON is the opposite case and reports its failures, which is asserted below.
describe("the on-load path is silent", () => {
  it("swallows a rejected begin and leaves the form usable", async () => {
    localStorage.setItem("quince.passkey.seen", "1");
    // @ts-expect-error — stand-in.
    window.PublicKeyCredential = {};
    vi.mocked(fetch).mockRejectedValue(new Error("no such endpoint"));

    render(
      <MemoryRouter>
        <Harness />
        <PasswordForm title="Sign in" subtitle="s" cta="Sign in" passkeys onSubmit={() => Promise.resolve()} />
      </MemoryRouter>,
    );

    await waitFor(() => expect(fetch).toHaveBeenCalled());
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
    expect(screen.queryByText(/no such endpoint/i)).not.toBeInTheDocument();
  });

  it("does not sign in when the authenticator returns nothing", async () => {
    localStorage.setItem("quince.passkey.seen", "1");
    // @ts-expect-error — stand-in.
    window.PublicKeyCredential = {};
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ ceremony: "c", options: { publicKey: { challenge: "AAAA" } } }),
    } as Response);
    // @ts-expect-error — stand-in.
    navigator.credentials = { get: vi.fn().mockResolvedValue(null) };

    const onSuccess = vi.fn();
    render(<Harness onSuccess={onSuccess} />);

    await waitFor(() => expect(navigator.credentials.get).toHaveBeenCalled());
    expect(onSuccess).not.toHaveBeenCalled();
  });
});

// THE BUTTON IS UNCONDITIONAL, and that is what makes the feature findable at all. It cannot make
// the wrong-guess mistake the gate exists to prevent: the user pressed it, so "no passkey here" is a
// fine answer to a question they asked.
describe("the explicit button", () => {
  it("is offered whenever passkeys are armed, with no memory required", () => {
    render(
      <MemoryRouter>
        <PasswordForm title="Sign in" subtitle="s" cta="Sign in" passkeys onSubmit={() => Promise.resolve()} />
      </MemoryRouter>,
    );
    expect(screen.getByRole("button", { name: /sign in with a passkey/i })).toBeInTheDocument();
  });

  it("is ABSENT on the setup form, which shares this component", () => {
    render(
      <MemoryRouter>
        <PasswordForm title="Set a password" subtitle="s" cta="Set" onSubmit={() => Promise.resolve()} />
      </MemoryRouter>,
    );
    expect(screen.queryByRole("button", { name: /sign in with a passkey/i })).not.toBeInTheDocument();
  });
});
