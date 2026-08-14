import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { OnboardingCertificateConfirmPage } from "./OnboardingCertificateConfirmPage";
import { api } from "@/lib/api";

// quince#908 §5, slice 5 — THE CONFIRMATION PAGE.
//
// It is reached at a DIFFERENT ORIGIN from every other route in this app, and the two things that
// matter are both restraint: it does not confirm on load, and it does not offer to confirm from a
// page that is not itself on https.

const realLocation = window.location;

// setLocation replaces `window.location` for one test. Both fields this page reads live there — the
// protocol and the FRAGMENT — and `MemoryRouter` cannot supply either, which is why this rather than
// `initialEntries`.
function setLocation(protocol: string, hash: string) {
  Object.defineProperty(window, "location", {
    configurable: true,
    value: { ...realLocation, protocol, hash },
  });
}

function renderPage() {
  return render(
    <MemoryRouter>
      <OnboardingCertificateConfirmPage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.restoreAllMocks();
  setLocation("https:", "#t=tok-abc");
});

afterEach(() => {
  Object.defineProperty(window, "location", { configurable: true, value: realLocation });
});

describe("the certificate confirmation page", () => {
  // A PAGE THAT POSTED AUTOMATICALLY WOULD CONFIRM THE MOMENT IT RENDERED — including in a
  // preloading tab, or after somebody clicked through a browser warning without reading it. The
  // button is what makes this a statement by a person.
  it("does not confirm on load", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({ confirmed: true });
    renderPage();

    await waitFor(() => expect(screen.getByRole("button", { name: /keep it/i })).toBeInTheDocument());
    expect(post).not.toHaveBeenCalled();
  });

  // THE TOKEN COMES FROM THE FRAGMENT (quince#979 review). A query parameter would reach the server
  // and land in its logs; a fragment never leaves the client.
  it("reads the token from the fragment and confirms with it", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue({ confirmed: true });
    renderPage();

    fireEvent.click(await screen.findByRole("button", { name: /keep it/i }));

    await waitFor(() => expect(screen.getByText(/^Kept\.$/)).toBeInTheDocument());
    expect(post).toHaveBeenCalledWith("/api/onboarding/certificate/confirm", { token: "tok-abc" });
  });

  // THE FIRST WRITE IN THE WHOLE STEP HAPPENS HERE, and the success copy says so — a user who was
  // told nothing was saved needs to be told when that stops being true.
  it("says the pair has now been written and will survive a restart", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ confirmed: true });
    renderPage();

    fireEvent.click(await screen.findByRole("button", { name: /keep it/i }));

    await waitFor(() => expect(screen.getByText(/written this pair into/i)).toBeInTheDocument());
    expect(screen.getByText(/including after\s+a?\s*restart/i)).toBeInTheDocument();
  });

  // THE SERVER REFUSES THIS WITH 426 REGARDLESS — `X-Forwarded-Proto` is not evidence for this one
  // question — but a user who followed the wrong link deserves the reason rather than a status code.
  it("refuses to offer confirmation from a page that is not on https", async () => {
    setLocation("http:", "#t=tok-abc");
    renderPage();

    expect(await screen.findByRole("alert")).toHaveTextContent(/This page is not on https/i);
    expect(screen.getByRole("button", { name: /keep it/i })).toBeDisabled();
  });

  it("says so when the link carries no token", async () => {
    setLocation("https:", "");
    renderPage();

    expect(await screen.findByRole("alert")).toHaveTextContent(/no confirmation token/i);
    expect(screen.queryByRole("button", { name: /keep it/i })).not.toBeInTheDocument();
  });

  // 409 `not_armed` is what a user sees when the window closed while they were reading. The server's
  // sentence is shown verbatim — it names both likely causes and the remedy.
  it("shows the daemon's sentence when there is nothing left to confirm", async () => {
    vi.spyOn(api, "post").mockRejectedValue(
      new Error("nothing is waiting to be confirmed — the window may have closed"),
    );
    renderPage();

    fireEvent.click(await screen.findByRole("button", { name: /keep it/i }));

    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(/nothing is waiting to be confirmed/i),
    );
  });

  it("says that doing nothing loses nothing", async () => {
    renderPage();
    expect(await screen.findByText(/nothing was written, nothing is lost/i)).toBeInTheDocument();
  });
});
