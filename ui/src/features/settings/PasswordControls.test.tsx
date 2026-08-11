import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { PasswordControls } from "./PasswordControls";
import { api, APIError } from "@/lib/api";

// qn.6m slice 6b — D4 and D7. The change form is ordinary; everything interesting is on the REMOVE
// side, where the decision is irreversible without console access and the server is the only thing
// that knows whether it is allowed.

// THE LIST IS NOW AN INPUT TO THIS COMPONENT (quince#855): it carries `has_password`, and the whole
// surface renders differently on the two states. Mocked per test rather than globally, because the
// PASSWORDLESS case is the one the issue was filed about and it must be reachable here.
function renderControls(hasPassword = true) {
  vi.spyOn(api, "get").mockResolvedValue({
    passkeys: [],
    rp_id: "quince.example.com",
    supported: true,
    has_password: hasPassword,
  });
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <PasswordControls />
    </QueryClientProvider>,
  );
}

beforeEach(() => vi.restoreAllMocks());

describe("changing the password", () => {
  it("sends both fields and reports success, because nothing else on screen changes", async () => {
    const put = vi.spyOn(api, "put").mockResolvedValue(undefined);
    renderControls();

    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "old" } });
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "new" } });
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByText(/password changed/i)).toBeInTheDocument();
    expect(put).toHaveBeenCalledWith("/api/auth/password", {
      current_password: "old",
      new_password: "new",
    });
    // Cleared, so the next visitor to this screen cannot read the old password out of the field.
    expect(screen.getByLabelText("Current password")).toHaveValue("");
  });

  // THE SERVER'S OWN SENTENCE, not generic copy. A 401 here means the CURRENT password was wrong,
  // which is a different mistake from every other failure on this page.
  it("shows the server's message when the current password is wrong", async () => {
    vi.spyOn(api, "put").mockRejectedValue(
      new APIError(401, "bad_password", "current password is incorrect"),
    );
    renderControls();

    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "new" } });
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByText(/current password is incorrect/i)).toBeInTheDocument();
  });
});

// D7, AND quince#841 IS EXPLICIT ABOUT IT: the cost "should be said on the screen that offers
// passwordless, not only in docs". The sentence that matters most is the third — a user can work
// out that losing the phone is bad, but not that a box they cannot get a shell on is unrecoverable.
describe("what passwordless costs is on the screen", () => {
  it("names the recovery command, what it clears, and that it needs a shell", () => {
    renderControls();

    expect(screen.getByText(/quince auth reset/)).toBeInTheDocument();
    expect(screen.getByText(/every passkey/i)).toBeInTheDocument();
    expect(screen.getByText(/console or SSH access/i)).toBeInTheDocument();
    expect(screen.getByText(/no way back in at all/i)).toBeInTheDocument();
  });

  // BEFORE the confirmation rather than inside it. A consequence read only after committing to a
  // destructive action is read too late.
  it("shows the cost before anything has been confirmed", () => {
    renderControls();

    expect(screen.getByText(/no way back in at all/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /yes, remove/i })).not.toBeInTheDocument();
  });
});

describe("removing the password", () => {
  it("takes two deliberate steps", async () => {
    const del = vi.spyOn(api, "del").mockResolvedValue(undefined);
    renderControls();

    fireEvent.click(screen.getByRole("button", { name: "Remove password" }));
    expect(del).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /yes, remove my password/i }));
    await waitFor(() => expect(del).toHaveBeenCalledWith("/api/auth/password"));
  });

  it("can be backed out of", () => {
    const del = vi.spyOn(api, "del").mockResolvedValue(undefined);
    renderControls();

    fireEvent.click(screen.getByRole("button", { name: "Remove password" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));

    expect(screen.queryByRole("button", { name: /yes, remove/i })).not.toBeInTheDocument();
    expect(del).not.toHaveBeenCalled();
  });

  // THE WHOLE REASON THE BUTTON IS NOT DISABLED. The server refuses with 409 unless a passkey exists
  // for THIS address, and its message names the addresses the credentials it found belong to.
  // Re-deriving that rule client-side would be a second implementation of an rpId check — the shape
  // `RequireStorage` is commented against — and a disabled button explains nothing.
  it("surfaces the server's refusal verbatim, because it names what this client cannot know", async () => {
    vi.spyOn(api, "del").mockRejectedValue(
      new APIError(
        409,
        "last_credential",
        'removing the password would leave no way to sign in at "quince.example.com": the passkeys ' +
          'this quince holds are registered for quince.example.net',
      ),
    );
    renderControls();

    fireEvent.click(screen.getByRole("button", { name: "Remove password" }));
    fireEvent.click(screen.getByRole("button", { name: /yes, remove my password/i }));

    expect(await screen.findByText(/registered for quince\.example\.net/)).toBeInTheDocument();
  });

  // THE DEMO CARVE-OUT REACHING THE USER. `no silent caps or fallbacks` wants the control present
  // and explaining itself, so the 503's stated reason has to arrive on screen rather than becoming
  // "something went wrong".
  it("surfaces the demo's 503 reason rather than generic copy", async () => {
    vi.spyOn(api, "del").mockRejectedValue(
      new APIError(
        503,
        "unavailable",
        "the admin password cannot be changed here: this is the public demo, and its password is " +
          "shared with every visitor",
      ),
    );
    renderControls();

    fireEvent.click(screen.getByRole("button", { name: "Remove password" }));
    fireEvent.click(screen.getByRole("button", { name: /yes, remove my password/i }));

    expect(await screen.findByText(/shared with every visitor/i)).toBeInTheDocument();
  });
});

// quince#855 — THE SCREEN USED TO LIE QUIETLY. On a passwordless install it said "Change your
// password / Current password", where the field had to be left blank and nothing said so.
// `PUT /api/auth/password` handled that case correctly all along, so the defect was entirely in
// what this surface CLAIMED.
describe("a passwordless install", () => {
  it("asks to SET a password, with no current-password field", async () => {
    renderControls(false);

    expect(await screen.findByRole("heading", { name: "Set a password" })).toBeInTheDocument();
    expect(screen.queryByLabelText("Current password")).not.toBeInTheDocument();
    expect(screen.getByLabelText("New password")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Set password" })).toBeInTheDocument();
  });

  it("says why there is no current password to give", async () => {
    renderControls(false);
    expect(await screen.findByText(/no password — you sign in with a passkey/i)).toBeInTheDocument();
  });

  // NOTHING TO REMOVE. Offering a destructive action against a thing that does not exist, with a
  // cost list describing the state the user is ALREADY IN, is the same class of lie one section up.
  it("does not offer to remove a password that is not there", async () => {
    renderControls(false);

    await screen.findByRole("heading", { name: "Set a password" });
    expect(screen.queryByRole("button", { name: "Remove password" })).not.toBeInTheDocument();
    // Replaced rather than hidden: "you are already here" beats a missing section.
    expect(screen.getByRole("heading", { name: /you sign in with a passkey only/i })).toBeInTheDocument();
  });

  // THE COST STILL HAS TO BE ON SCREEN. The user is living with it now, so the console-access fact
  // is MORE relevant here, not less — it is what they need if the device is ever lost.
  it("still states that recovery needs a shell on the machine", async () => {
    renderControls(false);
    expect(await screen.findByText(/console or SSH access/i)).toBeInTheDocument();
  });
});

describe("an install with a password", () => {
  it("asks to CHANGE it, and offers removal", async () => {
    renderControls(true);

    expect(await screen.findByRole("heading", { name: "Change your password" })).toBeInTheDocument();
    expect(screen.getByLabelText("Current password")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove password" })).toBeInTheDocument();
  });

  // THE FALLBACK WHILE THE LIST IS IN FLIGHT IS "a password exists", which is the safe guess of the
  // two: a Current field that turns out to be unnecessary costs one ignored input, where hiding one
  // that IS required costs a 401 the user cannot act on.
  it("assumes a password exists before the list has answered", () => {
    vi.spyOn(api, "get").mockReturnValue(new Promise(() => {})); // never resolves
    render(
      <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
        <PasswordControls />
      </QueryClientProvider>,
    );

    expect(screen.getByRole("heading", { name: "Change your password" })).toBeInTheDocument();
    expect(screen.getByLabelText("Current password")).toBeInTheDocument();
  });
});
