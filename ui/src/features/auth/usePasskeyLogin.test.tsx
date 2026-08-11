import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";

import { usePasskeyLogin } from "./usePasskeyLogin";
import { PasswordForm } from "./PasswordForm";
import { MemoryRouter } from "react-router-dom";

// The whole of qn.6k slice 4's risk is in the NEGATIVE paths. Conditional mediation is an optional
// convenience layered onto a login form that must keep working — so what these assert is mostly
// what does NOT happen: no call where unsupported, no error surfaced, no interference.

function Harness({ onSuccess }: { onSuccess?: () => void }) {
  usePasskeyLogin(onSuccess ?? (() => {}));
  return <div>armed</div>;
}

const origCreds = navigator.credentials;

beforeEach(() => {
  vi.restoreAllMocks();
  vi.stubGlobal("fetch", vi.fn());
});

afterEach(() => {
  // `window.PublicKeyCredential` is restored by `unstubAllGlobals` where it was stubbed, and jsdom
  // does not define it in the first place — so only `navigator.credentials`, which these tests
  // assign directly, has to be put back by hand.
  // @ts-expect-error — jsdom's navigator.credentials is not typed as assignable.
  navigator.credentials = origCreds;
  vi.unstubAllGlobals();
});

// THE GATE, AND THE REASON IT EXISTS. Calling `get({mediation: "conditional"})` unguarded produces
// a user-visible error on browsers without it — on a page the user has not asked to do anything on.
// jsdom has no PublicKeyCredential at all, which is exactly the unsupported case.
describe("conditional mediation is gated", () => {
  it("makes no request at all where the API is absent", async () => {
    // @ts-expect-error — jsdom has none; make the absence explicit.
    window.PublicKeyCredential = undefined;

    render(<Harness />);
    await screen.findByText("armed");

    expect(fetch).not.toHaveBeenCalled();
  });

  // THE GUARD THAT IS ACTUALLY LOAD-BEARING, and the assertion that discriminates it: with
  // conditional mediation unavailable, `navigator.credentials.get()` must NOT be called. That call
  // is what produces the user-visible error on such a browser — and unlike the `typeof` check
  // above, deleting this one changes behaviour rather than being absorbed by the catch.
  it("never calls credentials.get where conditional mediation is unavailable", async () => {
    // @ts-expect-error — minimal stand-in for the browser global.
    window.PublicKeyCredential = { isConditionalMediationAvailable: vi.fn().mockResolvedValue(false) };
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ ceremony: "c", options: { publicKey: { challenge: "AAAA" } } }),
    } as Response);
    // @ts-expect-error — stand-in for the credentials container.
    navigator.credentials = { get: vi.fn().mockResolvedValue(null) };

    render(<Harness />);
    await waitFor(() =>
      expect(window.PublicKeyCredential.isConditionalMediationAvailable).toHaveBeenCalled(),
    );

    expect(navigator.credentials.get).not.toHaveBeenCalled();
    expect(fetch).not.toHaveBeenCalled();
  });
});

// A FAILURE HERE IS NEVER THE USER'S PROBLEM. Every one of them — no passkey, no endpoint, sheet
// dismissed — is indistinguishable from the others by design, and the user is looking at a password
// form that works. An error on the login screen would turn an optional convenience into an apparent
// fault.
describe("failures are silent", () => {
  it("swallows a rejected begin and leaves the form usable", async () => {
    // @ts-expect-error — stand-in.
    window.PublicKeyCredential = { isConditionalMediationAvailable: vi.fn().mockResolvedValue(true) };
    vi.mocked(fetch).mockRejectedValue(new Error("no such endpoint"));

    render(
      <MemoryRouter>
        <Harness />
        <PasswordForm title="Sign in" subtitle="s" cta="Sign in" passkeys onSubmit={() => Promise.resolve()} />
      </MemoryRouter>,
    );

    await waitFor(() => expect(fetch).toHaveBeenCalled());

    // The password path is untouched: the field is there, and nothing rendered an error.
    expect(screen.getByLabelText("Password")).toBeInTheDocument();
    expect(screen.queryByText(/no such endpoint/i)).not.toBeInTheDocument();
  });

  it("does not call onSuccess when the authenticator returns nothing", async () => {
    // @ts-expect-error — stand-in.
    window.PublicKeyCredential = { isConditionalMediationAvailable: vi.fn().mockResolvedValue(true) };
    vi.mocked(fetch).mockResolvedValue({
      ok: true,
      status: 200,
      json: async () => ({ ceremony: "c", options: { publicKey: { challenge: "AAAA" } } }),
    } as Response);
    // @ts-expect-error — stand-in for the credentials container.
    navigator.credentials = { get: vi.fn().mockResolvedValue(null) };

    const onSuccess = vi.fn();
    render(<Harness onSuccess={onSuccess} />);

    await waitFor(() => expect(navigator.credentials.get).toHaveBeenCalled());
    expect(onSuccess).not.toHaveBeenCalled();
  });
});

// THE TOKEN THE BROWSER READS. Conditional mediation offers the credential against a field carrying
// `webauthn` in its autocomplete; without it the dropdown shows saved passwords and no passkey.
// And it must NOT appear on the setup form, which shares this component and has nothing to sign
// in to.
describe("the webauthn autocomplete token", () => {
  it("is present on both fields when passkeys are armed", () => {
    render(
      <MemoryRouter>
        <PasswordForm title="Sign in" subtitle="s" cta="Sign in" passkeys onSubmit={() => Promise.resolve()} />
      </MemoryRouter>,
    );
    expect(screen.getByLabelText("Username")).toHaveAttribute("autocomplete", "username webauthn");
    expect(screen.getByLabelText("Password")).toHaveAttribute("autocomplete", "current-password webauthn");
  });

  it("is ABSENT by default, which is the setup page's case", () => {
    render(
      <MemoryRouter>
        <PasswordForm title="Set a password" subtitle="s" cta="Set" onSubmit={() => Promise.resolve()} />
      </MemoryRouter>,
    );
    expect(screen.getByLabelText("Username")).toHaveAttribute("autocomplete", "username");
    expect(screen.getByLabelText("Password")).toHaveAttribute("autocomplete", "current-password");
  });
});
