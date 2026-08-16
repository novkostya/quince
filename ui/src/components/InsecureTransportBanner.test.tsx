import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import type { ReactNode } from "react";
import { InsecureTransportBanner } from "./InsecureTransportBanner";
import { api } from "@/lib/api";
import type { Health } from "@/lib/types";

const OK: Health = { status: "ok", version: "t", mode: "normal" };

// A ROUTER, kept from the first attempt at this dialog even though the component no longer needs
// one: the confirm is local state (see the component for why a global banner must not impose a
// Router on every surface that renders it), and `window.location.assign` is what ends the plain-http
// session rather than a route change. Harmless here, and it keeps this file able to render the
// banner inside a shell if one is ever added.
function renderIn(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>{node}</MemoryRouter>
    </QueryClientProvider>,
  );
}

// Opening the confirm is pressing the words that name the act — the instruction IS the control
// (Operator direction, 2026-08-16), so there is no separate button to look for.
const openConfirm = () =>
  fireEvent.click(screen.getByRole("button", { name: /^Turn this off$/i }));

// The banner's own copy, asserted on the CONSEQUENCE rather than on the setting's name, because a
// user weighing whether to type a password is not reading a config key.
const CONSEQUENCE = /anyone who can see the traffic can sign in as you/i;

describe("InsecureTransportBanner", () => {
  beforeEach(() => vi.restoreAllMocks());
  afterEach(() => vi.restoreAllMocks());

  it("warns while the plain-http opt-in is on, naming what is unprotected", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: true });
    renderIn(<InsecureTransportBanner />);

    expect(await screen.findByRole("alert")).toBeInTheDocument();
    expect(screen.getByText(CONSEQUENCE)).toBeInTheDocument();
    // NAMES WHAT IS UNPROTECTED, which is quince#446's ruling — in words rather than in mechanism.
    // This asserted `/sign-in cookie and CSRF token/` until quince#1069: the ruling asks for what is
    // unprotected, and "your sign-in travels in the clear" is that, for a reader who does not know
    // what a CSRF token is. A test pinned to the mechanism makes the human version look like a
    // regression, which is how copy written for insiders survives review.
    expect(screen.getByText(/your sign-in travels in the clear/i)).toBeInTheDocument();
  });

  // NO CONFIG KEY IN FRONT OF A PERSON — Operator direction, 2026-08-16: user-facing text is written
  // for humans. Asserted as an ABSENCE so the next well-meaning "be precise, name the setting" edit
  // fails here rather than shipping. A key belongs in a doc or a config file, not in a warning
  // somebody reads while deciding whether to type their password.
  it("does not put a config key or a mechanism in front of a person", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: true });
    renderIn(<InsecureTransportBanner />);

    const alert = await screen.findByRole("alert");
    expect(alert).not.toHaveTextContent(/allow_insecure_transport/);
    expect(alert).not.toHaveTextContent(/CSRF/i);
  });

  // NON-DISMISSIBLE IS THE RULING (quince#446): a degraded mode that can be hidden stops being
  // surfaced. This asserted the ABSENCE OF ANY CONTROL until quince#1069 — stricter than the ruling
  // says, and it would have failed the control that makes the banner's own instruction followable.
  // The line the ruling draws is between HIDING THE WARNING and REMOVING THE CONDITION: the first is
  // forbidden, the second is the whole point. So this now asserts what the controls DO.
  it("offers nothing that hides it while the setting is on", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: true });
    renderIn(<InsecureTransportBanner />);

    await screen.findByRole("alert");
    expect(screen.queryByRole("button", { name: /dismiss|close|hide|not now|got it/i })).toBeNull();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();

    // Arming and cancelling is the closest thing to a dismiss this surface has, and it must leave
    // the warning exactly where it was.
    openConfirm();
    fireEvent.click(screen.getByRole("button", { name: /Cancel/i }));
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByText(CONSEQUENCE)).toBeInTheDocument();
  });

  // THE INSTRUCTION IS FOLLOWABLE — quince#1069. "Turn this off" had nothing behind it: the only
  // writer of this setting wrote `true` and only `true`, and the way back was a text editor on the
  // box.
  //
  // TWO STEPS, ASSERTED AS THE ABSENCE OF A CALL ON THE FIRST PRESS, so a later "one button is
  // tidier" fails here rather than reading as a simplification. The cost is real: from a plain-http
  // connection this ends the reader's ability to sign in at that address.
  it("turns the setting off, and not on the first press", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: true });
    const post = vi.spyOn(api, "post").mockResolvedValue({});
    renderIn(<InsecureTransportBanner />);

    await screen.findByRole("alert");
    openConfirm();
    expect(post).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole("button", { name: /^Turn it off$/i }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/config/insecure-transport", { allow: false }),
    );
  });

  // ONE SENTENCE, TRUE FOR BOTH READERS. It branched on `useInsecureOrigin()` for a day, and that
  // field is false for everybody who can see this banner — so the branch that mattered never
  // rendered. The copy now states the rule and the personal consequence together, which needs no
  // knowledge the client does not have before the write.
  it("names the cost in one sentence, whatever the reader is standing on", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      ...OK,
      insecure_origin: false,
      insecure_transport_allowed: true,
    });
    renderIn(<InsecureTransportBanner />);

    await screen.findByRole("alert");
    openConfirm();
    expect(screen.getByText(/stop accepting sign-ins over plain HTTP/i)).toBeInTheDocument();
    expect(screen.getByText(/You stay signed in here/i)).toBeInTheDocument();
    // AND WHERE THE WAY BACK IS. This sentence is only honest while `PlainHTTPSetting` exists, so it
    // is asserted here as well as there — the pair is what stops this act being a lockout.
    expect(screen.getByText(/allow it again in Settings/i)).toBeInTheDocument();
  });

  // IT DOES NOT SIGN THE READER OUT, AND THAT IS THE WHOLE LESSON OF THIS ROUND — Operator,
  // 2026-08-16, having walked a build that did: *"it redirects to `/login` (which will fail) … in
  // this case it's impossible to re-enable insecure_transport on `/onboarding/https`, so the whole
  // idea doesn't really work."*
  //
  // With the setting off and a plain-http address, login answers 426, the pre-auth route answers 409
  // without a session, and the first-run confirm does not render in `needs_login`. Signing the reader
  // out therefore left ssh as the only way back — the dead end quince#908 exists to remove, rebuilt
  // by the control meant to help. Asserted as the ABSENCE of a logout so it cannot come back as a
  // tidy-up.
  it("does not sign the reader out, because that was a lockout", async () => {
    let flipped = false;
    vi.spyOn(api, "get").mockImplementation((async () =>
      flipped
        ? { ...OK, insecure_origin: true, insecure_transport_allowed: false }
        : { ...OK, insecure_origin: false, insecure_transport_allowed: true }) as typeof api.get);
    const post = vi.spyOn(api, "post").mockImplementation((async (path: string) => {
      if (path === "/api/config/insecure-transport") flipped = true;
      return {};
    }) as typeof api.post);
    renderIn(<InsecureTransportBanner />);

    await screen.findByRole("alert");
    openConfirm();
    fireEvent.click(screen.getByRole("button", { name: /^Turn it off$/i }));

    await waitFor(() =>
      expect(post).toHaveBeenCalledWith("/api/config/insecure-transport", { allow: false }),
    );
    expect(post).not.toHaveBeenCalledWith("/api/auth/logout");
  });

  // THE SERVER'S SENTENCE IS SHOWN. A 409 here means this browser holds no session, and "sign in to
  // change this" is the fact the reader needs — "could not save" would hide it.
  it("shows the daemon's refusal rather than a generic failure", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: true });
    vi.spyOn(api, "post").mockRejectedValue(
      new Error("quince is already set up — sign in to change this"),
    );
    renderIn(<InsecureTransportBanner />);

    await screen.findByRole("alert");
    openConfirm();
    fireEvent.click(screen.getByRole("button", { name: /^Turn it off$/i }));

    expect(await screen.findByText(/sign in to change this/i)).toBeInTheDocument();
  });

  it("renders nothing on a normal install", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: false });
    const { container } = renderIn(<InsecureTransportBanner />);

    await screen.findByText(/./, {}, { timeout: 1 }).catch(() => undefined);
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(container).toBeEmptyDOMElement();
  });

  // A SERVER OLDER THAN THIS UI REPORTS NOTHING, and that must not raise a warning about a setting
  // nobody turned on. `=== true` and nothing else, per contracts §1.
  it("stays silent when the server never reported the field", async () => {
    vi.spyOn(api, "get").mockResolvedValue(OK);
    const { container } = renderIn(<InsecureTransportBanner />);

    await screen.findByText(/./, {}, { timeout: 1 }).catch(() => undefined);
    expect(container).toBeEmptyDOMElement();
  });

  // THE INVERSE TRAP, ASSERTED FROM THE CLIENT SIDE TOO (contracts §1, and the server test of the
  // same name). `insecure_origin` is FALSE on exactly this install, so a banner keyed on the
  // nearer-sounding field would be silent here. This fails if anybody rewires the hook.
  it("warns even though insecure_origin is false — they are inverses", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      ...OK,
      insecure_origin: false,
      insecure_transport_allowed: true,
    });
    renderIn(<InsecureTransportBanner />);

    expect(await screen.findByRole("alert")).toBeInTheDocument();
  });

  // AND THE OTHER DIRECTION, which is the one that would make the banner furniture: a plain-http
  // install with the opt-in OFF is the dead end quince#908 is about, not a relaxed transport. It
  // gets the HTTPS page, not this.
  it("stays silent on a plain-http dead end, where insecure_origin is the true one", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      ...OK,
      insecure_origin: true,
      insecure_transport_allowed: false,
    });
    const { container } = renderIn(<InsecureTransportBanner />);

    await screen.findByText(/./, {}, { timeout: 1 }).catch(() => undefined);
    expect(container).toBeEmptyDOMElement();
  });
});
