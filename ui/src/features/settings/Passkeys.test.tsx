import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { Passkeys } from "./Passkeys";
import { api, APIError } from "@/lib/api";
import * as reauth from "@/lib/reauth";

// Fictional domains throughout — a real one is Operator-private and the privacy gate does not catch
// a bare domain, so the fixture discipline is the control on this rung.
const HERE = "quince.example.com";
const ELSEWHERE = "quince.example.net";

function renderCard() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <Passkeys />
    </QueryClientProvider>,
  );
}

beforeEach(() => vi.restoreAllMocks());

// THE JOB THIS SCREEN HAS THAT NO OTHER DOES. A passkey is bound to a domain and nothing in the
// protocol warns when the access path changes — the phone still lists a credential that can no
// longer sign in. Comparing against the SERVER's rpId rather than `location.hostname` is what makes
// the warning agree with what would actually happen behind a proxy.
describe("the rpId hazard", () => {
  it("marks a credential registered for another address", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      rp_id: HERE,
      supported: true,
      passkeys: [
        { id: "a", name: "phone", rp_id: HERE, created_at: "2026-08-01T00:00:00Z", last_used_at: null },
        { id: "b", name: "old laptop", rp_id: ELSEWHERE, created_at: "2026-07-01T00:00:00Z", last_used_at: null },
      ],
    });

    renderCard();

    await screen.findByText("old laptop");
    // The stale one is called out by name, with both domains.
    const warn = await screen.findByText(/will not work at/i);
    expect(warn.textContent).toContain(ELSEWHERE);
    expect(warn.textContent).toContain(HERE);
    // And the one that DOES work carries no warning — a blanket banner would be useless.
    expect(screen.getAllByText(/will not work at/i)).toHaveLength(1);
  });

  it("states where a new passkey will be bound, at the point of creation", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ rp_id: HERE, supported: true, passkeys: [] });
    renderCard();
    const note = await screen.findByText(/tied to the address you set it up on/i);
    expect(note.textContent).toContain(HERE);
  });
});

// STORY 4: refuse the tier rather than offer a button that cannot work. An rpId must be a domain,
// so a bare IP cannot hold a passkey and no certificate rescues it.
describe("an address that cannot be a relying party", () => {
  it("says so and disables the button", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ rp_id: "192.0.2.10", supported: false, passkeys: [] });

    renderCard();

    expect(await screen.findByText(/need a domain name over https/i)).toBeInTheDocument();
    // THE INPUT, NOT THE BUTTON — qn.6o D9/story 9. The add affordance is a ROW now, and its button
    // is disabled on an empty name whatever `supported` says, so asserting the button would pass
    // here even if support were never consulted. The field is driven only by `supported`.
    expect(screen.getByLabelText("Passkey name")).toBeDisabled();
  });
});

// THE CRASH THIS CARD ALREADY CAUSED ONCE, PINNED. It is mounted beside the config editor, so a
// render that throws takes the WHOLE Settings page with it — the editor included, on a box whose
// only fault is a malformed passkey response or a daemon too old to have the endpoint. Found by an
// existing SettingsPage test whose shared `api.get` mock returns the config body for every call.
describe("a malformed or absent list never takes the page down", () => {
  it("renders when the response carries no passkeys array", async () => {
    // Exactly what the shared mock produced: a body with none of the expected fields.
    vi.spyOn(api, "get").mockResolvedValue({ config: {}, warnings: [] });

    renderCard();

    expect(await screen.findByText("Passkeys")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText(/no passkeys yet/i)).toBeInTheDocument());
    // And with the shape unknown, the add row stays disabled rather than starting a ceremony this
    // address may not be able to finish. Asserted on the field for the reason given above.
    expect(screen.getByLabelText("Passkey name")).toBeDisabled();
  });

  // AND WHEN `passkeys` IS PRESENT BUT NOT A LIST. `?? []` does not catch this — only a type check
  // does — so without it `.map` throws and the page goes blank. Added because bypassing the
  // `Array.isArray` guard left every other test green: the undefined case above is covered by the
  // nullish default alone, and I would have claimed a guard that nothing exercised.
  it("renders when passkeys is present but not an array", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ rp_id: HERE, supported: true, passkeys: "unexpected" });

    renderCard();

    expect(await screen.findByText("Passkeys")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText(/no passkeys yet/i)).toBeInTheDocument());
  });

  it("renders when the request fails outright", async () => {
    vi.spyOn(api, "get").mockRejectedValue(new Error("network down"));

    renderCard();

    expect(await screen.findByText("Passkeys")).toBeInTheDocument();
    expect(await screen.findByText(/could not load passkeys/i)).toBeInTheDocument();
  });
});

// "Never used" is a fact worth rendering rather than an empty cell: a credential nobody has signed
// in with is exactly the one worth removing.
describe("the row", () => {
  it("distinguishes a credential that has never been used", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      rp_id: HERE,
      supported: true,
      passkeys: [{ id: "a", name: "phone", rp_id: HERE, created_at: "2026-08-01T00:00:00Z", last_used_at: null }],
    });

    renderCard();

    expect(await screen.findByText(/never used/i)).toBeInTheDocument();
  });
});

// THE REFUSAL HAS TO LAND SOMEWHERE — quince#888 item 1. Until the server grew a lockout guard this
// mutation could not fail in a way the user was meant to act on, so nothing rendered `remove.error`.
// Adding a 409 to the endpoint without adding this would have made the button do nothing at all:
// the row stays, no message appears, and the user retries forever. That is the silent-fallback shape
// the hard rules forbid, and it is invisible to every server-side test.
describe("removal refused because it would be the last credential", () => {
  it("shows the SERVER's sentence, not generic copy", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      rp_id: HERE,
      supported: true,
      has_password: false,
      passkeys: [{ id: "a", name: "phone", rp_id: HERE, created_at: "2026-08-01T00:00:00Z", last_used_at: null }],
    });
    // THE REFUSAL MOVED IN qn.6n AND THE CLAIM DID NOT. It used to come back from `DELETE`; since
    // rule 2 the client proves a passkey first, so the server refuses at `reauth/begin` — before any
    // authenticator sheet — and this mocks it there. Mocking `del` would assert against a call this
    // flow no longer reaches.
    const refusal = new APIError(
      409,
      "last_credential",
      `removing this passkey would leave no way to sign in: this quince has no password, and no other passkey for "${HERE}". Set a password first, or add another passkey.`,
    );
    const del = vi.spyOn(api, "del");
    vi.spyOn(reauth, "proveWithPasskey").mockRejectedValue(refusal);

    renderCard();
    (await screen.findByRole("button", { name: /^remove$/i })).click();

    // The message names what to do next, which is knowledge this client does not have. Asserting on
    // the REMEDY rather than on any old error text is what makes a generic fallback fail this test.
    const shown = await screen.findByText(/set a password first/i);
    expect(shown.textContent).toContain(HERE);
    // AND NOTHING WAS DELETED — the refusal arrives before the endpoint is called at all.
    expect(del).not.toHaveBeenCalled();
    // NO PASSWORD FORM ON THIS INSTALL. `has_password` is false, so there is nothing to fall back
    // to, and offering a field would be asking for something that does not exist.
    expect(screen.queryByLabelText(/your password/i)).not.toBeInTheDocument();

    // AND THE ROW SURVIVES. A UI that optimistically dropped it would show the refusal beside an
    // empty list, which contradicts the refusal it is displaying.
    expect(screen.getByText("phone")).toBeInTheDocument();
  });
});

// RULE 2's SECOND FACTOR — qn.6n slice 6b. On an install WITH a password, "no other passkey here"
// is not a lockout: the password can prove the removal, and the server's sentence says so. The form
// appears only after that refusal, never speculatively.
describe("removal falls back to the password", () => {
  function refusalWithPassword() {
    return new APIError(
      409,
      "last_credential",
      "a passkey cannot authorise its own removal, and this quince holds no other passkey for " +
        `"${HERE}". Confirm with your password instead.`,
    );
  }

  it("offers the field only after the server refuses, then sends what was typed", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      rp_id: HERE,
      supported: true,
      has_password: true,
      passkeys: [{ id: "a", name: "phone", rp_id: HERE, created_at: "2026-08-01T00:00:00Z", last_used_at: null }],
    });
    vi.spyOn(reauth, "proveWithPasskey").mockRejectedValue(refusalWithPassword());
    const del = vi.spyOn(api, "del").mockResolvedValue(undefined);

    renderCard();
    // NOT BEFORE THE REFUSAL. The client does not decide which factor is available — that is an rpId
    // rule and it lives on the server.
    expect(screen.queryByLabelText(/your password/i)).not.toBeInTheDocument();

    (await screen.findByRole("button", { name: /^remove$/i })).click();

    const field = await screen.findByLabelText(/your password/i);
    fireEvent.change(field, { target: { value: "hunter2" } });
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));

    // THE CREDENTIAL ID IS PART OF THE ASSERTION, not just "delete was called" — this removal names
    // a target, so a call that deleted the wrong row would pass a laxer check.
    await waitFor(() =>
      expect(del).toHaveBeenCalledWith("/api/auth/passkeys/a", { current_password: "hunter2" }),
    );
  });

  // A WRONG PASSWORD MUST NOT RE-OFFER THE FORM FROM SCRATCH, which is what a fallback that fired on
  // every error would do — the user would type, be refused, and watch the field reset with no reason
  // shown. The server's sentence is what they need.
  it("shows why a typed password was refused rather than looping", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      rp_id: HERE,
      supported: true,
      has_password: true,
      passkeys: [{ id: "a", name: "phone", rp_id: HERE, created_at: "2026-08-01T00:00:00Z", last_used_at: null }],
    });
    vi.spyOn(reauth, "proveWithPasskey").mockRejectedValue(refusalWithPassword());
    vi.spyOn(api, "del").mockRejectedValue(
      new APIError(401, "bad_password", "current password is incorrect"),
    );

    renderCard();
    (await screen.findByRole("button", { name: /^remove$/i })).click();
    fireEvent.change(await screen.findByLabelText(/your password/i), {
      target: { value: "wrong" },
    });
    fireEvent.click(screen.getByRole("button", { name: /^confirm$/i }));

    expect(await screen.findByText(/current password is incorrect/i)).toBeInTheDocument();
    // The field is still there, holding what was typed, so the user can correct it.
    expect(screen.getByLabelText(/your password/i)).toHaveValue("wrong");
  });
});
