import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { PlainHTTPConfirm } from "./PlainHTTPConfirm";
import { api } from "@/lib/api";

function renderConfirm() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      {/* THE SUCCESS STATE LINKS ONWARD, so the component needs a router the way every other
          surface with a `Link` in this suite does. */}
      <MemoryRouter>
        <PlainHTTPConfirm />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

const arm = () => fireEvent.click(screen.getByRole("button", { name: /Allow plain HTTP/i }));
const confirm = () => fireEvent.click(screen.getByRole("button", { name: /^Turn it on$/i }));

beforeEach(() => vi.restoreAllMocks());

describe("the plain-HTTP confirm", () => {
  // ONE PRESS MUST NOT DO IT. Everything else on this page is reversible by navigating away; this
  // changes the daemon's configuration, and its whole cost is a thing that will not be visible
  // afterwards. Asserted as the ABSENCE of a call, so a later "simplify to one button" fails here
  // rather than passing review as a usability improvement.
  it("does not change anything on the first press", () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({});
    renderConfirm();

    arm();

    expect(post).not.toHaveBeenCalled();
    expect(screen.getByText(/cross this network unencrypted/i)).toBeInTheDocument();
  });

  it("turns it on when confirmed", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({});
    renderConfirm();

    arm();
    confirm();

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/config/insecure-transport", { allow: true }),
    );
  });

  // THE ASSERTION THAT WAS MISSING, AND ITS ABSENCE WAS THE BUG (quince#1064). Every failure path
  // below asserts what RENDERS; the success path asserted only that the POST fired, and the
  // component rendered nothing at all — so the one path that works was the only one a user could
  // not tell had happened.
  it("says it worked, where the button was", async () => {
    vi.spyOn(api, "post").mockResolvedValue({});
    renderConfirm();

    arm();
    confirm();

    expect(await screen.findByRole("status")).toHaveTextContent(/Plain HTTP is on/i);
    // AND THE CONFIRM IS GONE. A success that left "Turn it on" standing would invite a second
    // press, which the daemon answers 409 to once the install is claimed.
    expect(screen.queryByRole("button", { name: /^Turn it on$/i })).not.toBeInTheDocument();
  });

  // A WAY ONWARD, ASSERTED AS THE DESTINATION rather than as link text. The page this renders on
  // has no other exit — its only links are the certificate sub-page and the doc link — and after
  // the opt-in `SetupGate` stops diverting, so `/setup` is the step the user came for.
  it("offers the next step", async () => {
    vi.spyOn(api, "post").mockResolvedValue({});
    renderConfirm();

    arm();
    confirm();

    expect(await screen.findByRole("link", { name: /Set your password/i })).toHaveAttribute(
      "href",
      "/setup",
    );
  });

  // CANCEL LEAVES NO TRACE. A confirm that could be dismissed into a half-armed state would be a
  // control whose appearance no longer matches what pressing it does.
  it("goes back to the offer on cancel, having changed nothing", () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({});
    renderConfirm();

    arm();
    fireEvent.click(screen.getByRole("button", { name: /Cancel/i }));

    expect(post).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /Allow plain HTTP/i })).toBeInTheDocument();
    expect(screen.queryByText(/cross this network unencrypted/i)).not.toBeInTheDocument();
  });

  // THE COST IS RESTATED AT THE MOMENT OF DECIDING, not left three paragraphs up the card. What
  // somebody reads with their finger over a button is not what they read while browsing.
  it("names the cost in the confirm itself", () => {
    renderConfirm();
    arm();

    expect(screen.getByText(/Anyone who can see the traffic can sign in as you/i)).toBeInTheDocument();
    // AND THAT THE WARNING WILL PERSIST — quince#446's non-dismissible banner is a consequence of
    // this choice, so it is named before the choice rather than discovered after it.
    expect(screen.getByText(/keep saying so on every screen/i)).toBeInTheDocument();
  });

  // THE SERVER'S OWN SENTENCE IS SHOWN. A 409 here means the install was claimed between loading
  // the page and pressing the button — "quince is already set up" is exactly what the user needs to
  // read, and replacing it with "could not save" would hide the one useful fact.
  it("shows the daemon's refusal rather than a generic failure", async () => {
    vi.spyOn(api, "post").mockRejectedValue(new Error("quince is already set up — sign in and change this in Settings"));
    renderConfirm();

    arm();
    confirm();

    expect(await screen.findByRole("alert")).toHaveTextContent(/already set up/i);
  });

  // AND A FAILURE LEAVES THE CONFIRM STANDING, so the user can read the reason and retry or cancel
  // rather than being dropped back to an offer that looks untouched.
  it("keeps the confirm open after a refusal", async () => {
    vi.spyOn(api, "post").mockRejectedValue(new Error("nope"));
    renderConfirm();

    arm();
    confirm();

    await screen.findByRole("alert");
    expect(screen.getByRole("button", { name: /^Turn it on$/i })).toBeInTheDocument();
  });
});
