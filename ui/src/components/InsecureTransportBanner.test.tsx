import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import type { ReactNode } from "react";
import { InsecureTransportBanner } from "./InsecureTransportBanner";
import { api } from "@/lib/api";
import type { Health } from "@/lib/types";

const OK: Health = { status: "ok", version: "t", mode: "normal" };

function renderIn(node: ReactNode) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}>{node}</QueryClientProvider>);
}

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
  // surfaced. Asserted as the ABSENCE of any control, so a later "just add a close button" fails
  // here rather than passing review as a usability improvement.
  it("offers nothing to dismiss it with", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ ...OK, insecure_transport_allowed: true });
    renderIn(<InsecureTransportBanner />);

    await screen.findByRole("alert");
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
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
