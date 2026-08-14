import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AddPasskeyRow } from "./AddPasskeyRow";
import { UnauthorizedError } from "@/lib/api";
import * as webauthn from "@/lib/webauthn";
import * as reauth from "@/lib/reauth";

// qn.6o slice 4 — stories 1, 6, 7 and 8, plus D1's corrected activation rule.

// The refusal slice 2 sends: `reauth_required` with `accepts` inside the error body. Built as the
// real `UnauthorizedError` rather than a bare object, because `accepts` is read off `details` — the
// whole parsed body — and a hand-made shape would let the reader's narrowing pass on something the
// client will never actually receive.
function refusal(accepts?: string[]) {
  return new UnauthorizedError("reauth_required", "authenticate again", {
    error: { code: "reauth_required", message: "authenticate again", ...(accepts ? { accepts } : {}) },
  });
}

function renderRow(supported = true) {
  const onAdded = vi.fn();
  render(
    <MemoryRouter>
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <AddPasskeyRow supported={supported} onAdded={onAdded} />
      </QueryClientProvider>
    </MemoryRouter>,
  );
  return { onAdded };
}

function typeNameAndAdd(name = "my iPhone") {
  fireEvent.change(screen.getByLabelText("Passkey name"), { target: { value: name } });
  fireEvent.click(screen.getByRole("button", { name: "Add" }));
}

beforeEach(() => {
  vi.restoreAllMocks();
});

// STORY 1 — THE REGRESSION, CLOSED. A password-only install adds its first passkey: type the name,
// press Add, meet the challenge, type the password, and the passkey is created.
//
// This is the flow that has been IMPOSSIBLE since qn.6n rule 1 landed.
describe("story 1 — a password-only install can add its first passkey", () => {
  it("asks for the password the server named, then creates with it", async () => {
    const register = vi
      .spyOn(webauthn, "registerPasskey")
      .mockRejectedValueOnce(refusal(["password"]))
      .mockResolvedValueOnce(true);
    const { onAdded } = renderRow();

    typeNameAndAdd();

    // The challenge appears, offering exactly what `accepts` listed — a password field and no
    // passkey button, because this install holds no credential that could assert.
    expect(await screen.findByLabelText("Password")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Use a passkey" })).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Password"), { target: { value: "old-one" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));

    // THE NAME SURVIVES THE CHALLENGE. It was typed before the refusal, and it has to reach
    // `register/finish` afterwards — losing it would make the user type it twice.
    await waitFor(() =>
      expect(register).toHaveBeenLastCalledWith("my iPhone", { currentPassword: "old-one" }),
    );
    await waitFor(() => expect(onAdded).toHaveBeenCalled());
  });
});

// D1 AS CORRECTED ON quince#988 — THE SHARPEST CLAIM IN THIS SLICE.
//
// Proving with a PASSKEY spends the user's gesture on the proof's own authenticator sheet, and
// completing a sheet grants no new activation. So the proof must NOT chain into `create()`; a fresh
// click has to come between them. This is quince#976's mechanism, and the test is what stops the
// obvious "helpful" refactor from reintroducing it.
describe("the passkey path waits for a fresh click before creating", () => {
  it("parks the proof and asks for one more press", async () => {
    const register = vi.spyOn(webauthn, "registerPasskey").mockRejectedValueOnce(refusal(["passkey"]));
    const prove = vi.spyOn(reauth, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");
    renderRow();

    typeNameAndAdd();

    fireEvent.click(await screen.findByRole("button", { name: "Use a passkey" }));
    await waitFor(() => expect(prove).toHaveBeenCalledWith("add_passkey", undefined));

    // THE ASSERTION THAT MATTERS: the proof is in hand and NOTHING has been created. One
    // `registerPasskey` call — the refused one — and no second.
    await screen.findByRole("button", { name: "Create the passkey" });
    expect(register).toHaveBeenCalledTimes(1);

    // And the fresh click is what runs the ceremony, carrying the proof.
    register.mockResolvedValueOnce(true);
    fireEvent.click(screen.getByRole("button", { name: "Create the passkey" }));

    await waitFor(() =>
      expect(register).toHaveBeenLastCalledWith("my iPhone", { proof: "PROOF-TOKEN" }),
    );
  });
});

// STORY 6 — ONLY ONE DIALOG APPEARS IN THE ADD FLOW, EVER. Operator ruling: *"I don't want 2
// dialogs in a row."*
//
// ASSERTED AS ZERO, which is stronger than the ruling and is what the design actually delivers: the
// name is a field on the page and the challenge replaces the row in place, so no `role="dialog"`
// exists at any point in the flow.
describe("story 6 — no dialogs", () => {
  it("shows none, before or after the refusal", async () => {
    vi.spyOn(webauthn, "registerPasskey").mockRejectedValueOnce(refusal(["password"]));
    renderRow();

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    typeNameAndAdd();
    await screen.findByLabelText("Password");
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });
});

// STORY 8 — A SCREEN READER NAMES THE FIELD even after the placeholder is gone. D8: the placeholder
// is a HINT, it disappears the moment the user types, and an unnamed field is announced as nothing.
describe("story 8 — the field is named, not just hinted", () => {
  it("keeps its label after the placeholder has gone", () => {
    renderRow();

    const input = screen.getByLabelText("Passkey name");
    expect(input).toHaveAttribute("placeholder", "my iPhone");

    fireEvent.change(input, { target: { value: "x" } });
    // The label still resolves it, which is the whole point: the hint is gone and the name is not.
    expect(screen.getByLabelText("Passkey name")).toBe(input);
  });
});

// STORY 7 / D7 — THE ROW IS LEGIBLE AS AN ACTION, NOT AS A BROKEN PASSKEY.
//
// In this product `border-dashed` means ABSENT or BROKEN, across five sites. A dashed add-row on
// this page would read as *a passkey that is broken*, on the one screen that warns when a credential
// has stopped working at this address.
describe("story 7 — it does not borrow the broken-passkey signal", () => {
  it("uses no dashed border", () => {
    const { container } = render(
      <MemoryRouter>
        <QueryClientProvider client={new QueryClient()}>
          <AddPasskeyRow supported onAdded={() => {}} />
        </QueryClientProvider>
      </MemoryRouter>,
    );

    expect(container.querySelectorAll(".border-dashed")).toHaveLength(0);
  });
});

// A DEAD END IS A SENTENCE, NEVER AN EMPTY CHALLENGE (D4). The server omits `accepts` only where
// nothing could satisfy the operation — so an absent list means an older daemon, and rendering a
// prompt with no controls in it would be the empty challenge D4 exists to prevent.
describe("a refusal with no accepts", () => {
  it("says so instead of rendering an empty challenge", async () => {
    vi.spyOn(webauthn, "registerPasskey").mockRejectedValueOnce(refusal());
    renderRow();

    typeNameAndAdd();

    expect(await screen.findByText(/did not say which/i)).toBeInTheDocument();
    expect(screen.queryByLabelText("Password")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Use a passkey" })).not.toBeInTheDocument();
  });
});
