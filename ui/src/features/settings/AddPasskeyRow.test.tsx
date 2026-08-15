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
describe("the passkey path creates without a second press", () => {
  // MEASURED 2026-08-14 ON HARDWARE, and it overturned a prediction. D1 (quince#988) reasoned that
  // chaining `create()` off a passkey proof MUST fail for want of transient activation, and said so
  // while labelling itself UNMEASURED. It was then run: passwordless install, Mac signed in by QR,
  // passkey added, confirmed with the iPhone by QR — and the creation prompt appeared BY ITSELF.
  //
  // So this test asserted the opposite until today. The mandatory button is gone.
  it("goes straight through, carrying the proof", async () => {
    const register = vi
      .spyOn(webauthn, "registerPasskey")
      .mockRejectedValueOnce(refusal(["passkey"]))
      .mockResolvedValueOnce(true);
    const prove = vi.spyOn(reauth, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");
    const { onAdded } = renderRow();

    typeNameAndAdd();

    // NO DIALOG AND NO SECOND PRESS — Operator ruling, 2026-08-15, D5 as amended. `["passkey"]` is
    // the only answer a passwordless install can give here, so the chooser would have one choice;
    // the ceremony runs straight from the press that opened this.
    await waitFor(() => expect(prove).toHaveBeenCalledWith("add_passkey"));

    await waitFor(() =>
      expect(register).toHaveBeenLastCalledWith("my iPhone", { proof: "PROOF-TOKEN" }),
    );
    await waitFor(() => expect(onAdded).toHaveBeenCalled());
    // Neither affordance appears: no chooser, and no intermediate button on the measured path.
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Create the passkey" })).not.toBeInTheDocument();
  });

  // THE FALLBACK, WHICH IS WHY THE BUTTON STILL EXISTS. One engine and one transport were measured;
  // a stricter one could still refuse, and `registerPasskey` answers `false` for that exactly as it
  // does for a dismissed sheet. The caller knows nobody clicked, so it offers the real click rather
  // than resetting the row and showing nothing — which is what it used to do.
  it("offers a real click when the chained attempt is refused", async () => {
    const register = vi
      .spyOn(webauthn, "registerPasskey")
      .mockRejectedValueOnce(refusal(["passkey"]))
      // `false` — the shape a lost activation and a dismissed sheet share.
      .mockResolvedValueOnce(false);
    vi.spyOn(reauth, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");
    renderRow();

    typeNameAndAdd();

    // STILL NO CHOOSER ON THE WAY IN — the skip is unconditional for `["passkey"]`, and the fallback
    // is the fresh-click button rather than the dialog. That distinction is the Operator's
    // correction: a one-option chooser is worst exactly when the user has just declined that option.
    const retry = await screen.findByRole("button", { name: "Create the passkey" });
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    // AND IT STILL CARRIES THE PROOF, so the fallback costs a click and not the ceremony.
    register.mockResolvedValueOnce(true);
    fireEvent.click(retry);
    await waitFor(() =>
      expect(register).toHaveBeenLastCalledWith("my iPhone", { proof: "PROOF-TOKEN" }),
    );
  });
});

// STORY 6 — NEVER TWO DIALOGS IN A ROW. Operator ruling: *"I don't want 2 dialogs in a row."*
//
// THIS ASSERTED **ZERO** AND THAT WAS WRONG — Operator-reported 2026-08-14, from a screenshot. I
// read the ruling as stronger than it is, the challenge shipped INLINE, and it dragged the sign-in
// screen's shell into a Settings page with it. The test passed the whole time, because asserting a
// stronger claim than the rule is still asserting something the code does.
//
// WHAT THE RULING ACTUALLY CONSTRAINS IS THE COUNT IN SEQUENCE. The name is a field on the PAGE
// (D6), so the challenge is the ONLY dialog in the flow — one, not two — which is what these two
// assertions now say: nothing modal before the refusal, exactly one after it.
describe("story 6 — one dialog, never two in a row", () => {
  it("shows none before the refusal and exactly one after", async () => {
    vi.spyOn(webauthn, "registerPasskey").mockRejectedValueOnce(refusal(["password"]));
    renderRow();

    // THE NAME IS NOT MODAL. This is the half of the ruling that survives unchanged — the field is
    // on the page, which is why a second dialog cannot follow the first.
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();

    typeNameAndAdd();
    await screen.findByLabelText("Password");

    expect(screen.getAllByRole("dialog")).toHaveLength(1);
  });

  // AND THE ROW IS STILL THERE BEHIND IT. The challenge used to replace the row outright, which is
  // defensible inline and wrong for a dialog: the backdrop exists to dim the thing being confirmed,
  // so removing that thing leaves the dialog floating over the gap it left.
  it("leaves the row it is confirming on the page", async () => {
    vi.spyOn(webauthn, "registerPasskey").mockRejectedValueOnce(refusal(["password"]));
    renderRow();

    typeNameAndAdd();
    await screen.findByLabelText("Password");

    expect(screen.getByLabelText("Passkey name")).toHaveValue("my iPhone");
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
