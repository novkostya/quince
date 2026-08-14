import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ReauthChallenge } from "./ReauthChallenge";
import { PasswordForm } from "./PasswordForm";
import * as reauth from "@/lib/reauth";

// qn.6o slice 3 gates G5 and G6.

// The provider is needed because `PasswordForm` renders `InsecureTransportBanner`, which reads
// `useHealth`. Same wrapper `PasswordForm.test.tsx` uses, for the same reason.
function wrap(node: React.ReactNode) {
  return (
    <MemoryRouter>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        {node}
      </QueryClientProvider>
    </MemoryRouter>
  );
}

function renderChallenge(props: Partial<Parameters<typeof ReauthChallenge>[0]> = {}) {
  const onProved = vi.fn().mockResolvedValue(undefined);
  const onCancel = vi.fn();
  render(
    wrap(
      <ReauthChallenge
        operation="add_passkey"
        accepts={["password", "passkey"]}
        onProved={onProved}
        onCancel={onCancel}
        {...props}
      />,
    ),
  );
  return { onProved, onCancel };
}

beforeEach(() => {
  vi.restoreAllMocks();
});

// G5 — THE CHALLENGE RENDERS FROM `accepts` ALONE.
//
// All three shapes the server can send, asserted on what is on screen rather than on the prop —
// the whole point is that a client which re-derived acceptability would pass a prop test and put
// the wrong control in front of a user.
describe("G5 — it renders what the server said would work", () => {
  it("password only: a password field and no passkey button", () => {
    renderChallenge({ accepts: ["password"] });

    expect(screen.getByLabelText("Password")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /passkey/i })).not.toBeInTheDocument();
  });

  it("passkey only: a passkey button and NO password field", () => {
    renderChallenge({ accepts: ["passkey"] });

    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Use a passkey" })).toBeInTheDocument();
  });

  it("both: both", () => {
    renderChallenge({ accepts: ["password", "passkey"] });

    expect(screen.getByLabelText("Password")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Use a passkey" })).toBeInTheDocument();
  });

  // THE DEAD PRIMARY ACTION. With no password there is nothing to submit, and the button is
  // disabled on an empty password — so leaving it would put a permanently unpressable primary
  // button beside the one control that works.
  it("passkey only: no submit button either", () => {
    renderChallenge({ accepts: ["passkey"] });

    expect(screen.queryByRole("button", { name: "Confirm" })).not.toBeInTheDocument();
  });
});

// G6 — `passkeys` IS NOT PASSED TO `PasswordForm`.
//
// ASSERTED ON THE PROP, because the failure is invisible in jsdom: `passkeys` arms CONDITIONAL
// MEDIATION, and what that produces is a browser autofill prompt on load. Nothing in a DOM
// assertion can see it, and a challenge that quietly armed it would pass every test above.
describe("G6 — the challenge is modal, never conditional", () => {
  it("never arms conditional mediation", () => {
    renderChallenge({ accepts: ["password", "passkey"] });

    // READ THE EFFECT, NOT THE PROP. `passkeys` shows up in jsdom as the `webauthn` token on the
    // two autocomplete attributes — that token IS the conditional-mediation request. Asserting it
    // is stronger than asserting the prop, because it would also catch a future edit that armed
    // mediation some other way.
    expect(screen.getByLabelText("Username")).toHaveAttribute("autocomplete", "username");
    expect(screen.getByLabelText("Password")).toHaveAttribute("autocomplete", "current-password");
  });

  // The counterpart, so the assertion above is known to discriminate: a form that DOES arm it
  // renders the other value. Without this, `autocomplete="username"` could be true of everything.
  it("and the login form, which does arm it, is visibly different", () => {
    render(
      wrap(
        <PasswordForm title="Sign in" subtitle="" cta="Sign in" passkeys onSubmit={async () => {}} />,
      ),
    );

    expect(screen.getByLabelText("Username")).toHaveAttribute("autocomplete", "username webauthn");
  });
});

// THE PASSKEY BUTTON PROVES, IT DOES NOT SIGN IN. The two ceremonies hit different endpoint
// families, and the one this button must not call is the pre-auth login pair.
describe("what the two controls actually do", () => {
  it("the passkey button mints a PROOF and hands it back", async () => {
    const prove = vi.spyOn(reauth, "proveWithPasskey").mockResolvedValue("proof-token");
    const { onProved } = renderChallenge({ accepts: ["passkey"], operation: "remove_passkey", target: "cre1" });

    fireEvent.click(screen.getByRole("button", { name: "Use a passkey" }));

    // THE TARGET TRAVELS. Without it `reauth/begin` cannot exclude the credential being removed
    // from its own allow-list, and rule 2 would be left to the subject check alone.
    await waitFor(() => expect(prove).toHaveBeenCalledWith("remove_passkey", "cre1"));
    await waitFor(() => expect(onProved).toHaveBeenCalledWith({ proof: "proof-token" }));
  });

  it("the password field hands back the password, not a proof", async () => {
    const { onProved } = renderChallenge({ accepts: ["password"] });

    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "old-one" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));

    await waitFor(() => expect(onProved).toHaveBeenCalledWith({ current_password: "old-one" }));
  });
});

// THE DEFECT THE OPERATOR CAUGHT IN A SCREENSHOT, PINNED — 2026-08-14.
//
// The challenge reused `PasswordForm`, which wraps itself in `AuthPage` — the SIGN-IN SCREEN's
// shell. So a Settings page rendered the quince wordmark and a `min-h-dvh` wrapper mid-scroll. Every
// test passed while the screen was wrong, because jsdom computes no layout and nothing here looked
// at what the shell contributed.
//
// ASSERTED ON THE WORDMARK AND THE WRAPPER rather than on a screenshot, because those are the two
// things the shell actually adds and both are visible to a DOM query. This is the cheap check that
// existed all along and that nobody wrote.
describe("it does not drag the sign-in shell into the page", () => {
  it("renders no wordmark and no full-viewport wrapper", () => {
    const { baseElement } = render(
      wrap(
        <ReauthChallenge
          operation="add_passkey"
          accepts={["password", "passkey"]}
          onProved={async () => {}}
          onCancel={() => {}}
        />,
      ),
    );

    // The wordmark is the login screen announcing itself. Inside a dialog it is somebody else's
    // branding on a confirmation.
    expect(screen.queryByText("quince")).not.toBeInTheDocument();
    // And nothing claims the viewport — the dialog owns that.
    expect(baseElement.querySelectorAll(".min-h-dvh")).toHaveLength(0);
  });

  // IT IS A REAL DIALOG, consistent with every other confirmation in the product — which is what the
  // Operator asked for, and what `role="dialog"` is the machine-checkable half of.
  it("is a dialog", () => {
    renderChallenge();
    expect(screen.getAllByRole("dialog")).toHaveLength(1);
  });

  // DISMISSAL REACHES THE CALLER. Escape is a path the old inline Cancel button did not have, so a
  // caller that only handled its own button would have been left holding a challenge the user
  // believed they had closed.
  it("cancels on Escape", async () => {
    const { onCancel } = renderChallenge();

    fireEvent.keyDown(document.activeElement ?? document.body, { key: "Escape", code: "Escape" });

    await waitFor(() => expect(onCancel).toHaveBeenCalled());
  });
});
