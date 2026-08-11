import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";

import { OnboardingPasskeyPage } from "./OnboardingPasskeyPage";
import { api } from "@/lib/api";

const HERE = "quince.example.com";

// Rendered against a router with a `/` destination, so "forwards to Home" is observable rather than
// asserted through a mock.
function renderStep() {
  return render(
    <MemoryRouter initialEntries={["/onboarding/passkey"]}>
      <Routes>
        <Route path="/onboarding/passkey" element={<OnboardingPasskeyPage />} />
        <Route path="/" element={<div>HOME</div>} />
      </Routes>
    </MemoryRouter>,
  );
}

beforeEach(() => vi.restoreAllMocks());

// SKIPPING IS THE NORMAL PATH AND THE PAGE HAS TO LOOK LIKE IT. A passkey is an addition and never
// a replacement — ruled, because a lost phone must not lock the user out of their own backups. A
// step that pressed for one would be selling the wrong story on the screen where the user forms
// their idea of what quince expects.
describe("the offer", () => {
  it("says the password keeps working, and offers a first-class way out", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ rp_id: HERE, supported: true, passkeys: [] });

    renderStep();

    expect(await screen.findByRole("heading", { name: /sign in with face id/i })).toBeInTheDocument();
    expect(screen.getByText(/an addition, never a replacement/i)).toBeInTheDocument();
    // "Not now" is a BUTTON beside the offer, not a link in a corner.
    expect(screen.getByRole("button", { name: /not now/i })).toBeInTheDocument();
  });

  // The hazard is stated where the credential is created, which the ruling asks for explicitly —
  // not only in the docs.
  it("names the address the passkey would be tied to", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ rp_id: HERE, supported: true, passkeys: [] });

    renderStep();

    const note = await screen.findByText(/tied to the address you set it up on/i);
    expect(note.textContent).toContain(HERE);
  });

  it("goes to Home on Not now", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ rp_id: HERE, supported: true, passkeys: [] });

    renderStep();

    fireEvent.click(await screen.findByRole("button", { name: /not now/i }));
    expect(await screen.findByText("HOME")).toBeInTheDocument();
  });
});

// THIS STEP MUST NEVER BE SOMEWHERE A FIRST RUN GETS STUCK. It sits between setting a password and
// declaring a storage, so anything that could hold it there would strand a fresh install — which is
// why both the unsupported case and an unreachable list forward rather than render a refusal.
describe("it never becomes a dead end", () => {
  it("renders nothing and forwards where passkeys cannot work", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ rp_id: "192.0.2.10", supported: false, passkeys: [] });

    renderStep();

    expect(await screen.findByText("HOME")).toBeInTheDocument();
    // Story 4 here means SKIPPING the step, not explaining a capability the user cannot have.
    expect(screen.queryByRole("heading", { name: /sign in with face id/i })).not.toBeInTheDocument();
  });

  it("forwards when the list cannot be reached at all", async () => {
    vi.spyOn(api, "get").mockRejectedValue(new Error("network down"));

    renderStep();

    // An unreachable endpoint means "do not offer" rather than an error on the screen of somebody
    // who has just set their password.
    expect(await screen.findByText("HOME")).toBeInTheDocument();
    await waitFor(() => expect(screen.queryByText(/network down/i)).not.toBeInTheDocument());
  });
});
