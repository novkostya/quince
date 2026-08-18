import type { ComponentProps } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { CertificateApply } from "./CertificateApply";
import { api } from "@/lib/api";
import type { CertificateApplied } from "@/lib/types";

// quince#908 §5, slice 5 — THE TRIAL, FROM THE USER'S SIDE.
//
// What a reviewer cannot check by reading: that "nothing is written" is said BEFORE the button, that
// the link points at the origin the SERVER named rather than wherever this page happens to be, that
// the token is in the FRAGMENT, and that the countdown comes from the absolute deadline.

const APPLIED: CertificateApplied = {
  confirm_origin: "https://quince.example:8968",
  confirm_host_covered: true,
  confirm_token: "tok-abc",
  // RELATIVE TO NOW, NOT A FIXED INSTANT. A hardcoded deadline is in the past on every run after the
  // day it was written, so every test using this fixture would silently exercise the EXPIRED trial
  // rather than the live one it means to describe. The tests that care about the boundary set their
  // own deadline explicitly.
  expires_at: futureISO(600),
  expires_seconds: 600,
  config_written: false,
};

beforeEach(() => {
  vi.restoreAllMocks();
});

// THE DEFAULT IS A NAME THE USER TYPED, so the dead-end gate stays out of the tests that are about
// something else — it fires only when the field is empty AND the address in play is uncovered.
function renderApply(props: Partial<ComponentProps<typeof CertificateApply>> = {}) {
  return render(
    <CertificateApply
      certFile="/tls/fullchain.pem"
      keyFile="/tls/privkey.pem"
      hostname="quince.example"
      currentHost="192.0.2.10"
      currentHostCovered={false}
      {...props}
    />,
  );
}

describe("the certificate trial", () => {
  // THE REASSURANCE THAT MAKES THE BUTTON SAFE TO PRESS COMES FIRST. A user who only learns that
  // nothing is written after pressing has already taken the risk they were being reassured about.
  it("says nothing is written, before anything is applied", () => {
    renderApply();
    expect(screen.getByText(/writes nothing to configuration yet/i)).toBeInTheDocument();
    // The sentence is split by a `<code>` element, so it is matched on its own clause.
    expect(screen.getByText(/was never touched/i)).toBeInTheDocument();
  });

  it("sends the pair and shows the link the SERVER named, with the token in the fragment", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue(APPLIED);
    renderApply();

    fireEvent.click(screen.getByRole("button", { name: /Try it now/i }));

    await waitFor(() => expect(screen.getByRole("link")).toBeInTheDocument());
    expect(post).toHaveBeenCalledWith("/api/onboarding/certificate/apply", {
      cert_file: "/tls/fullchain.pem",
      key_file: "/tls/privkey.pem",
      hostname: "quince.example",
    });

    // THE ORIGIN COMES FROM THE RESPONSE, NOT FROM `window.location`. The point is to send the
    // browser somewhere it is NOT — the certificate's own name, on the port quince is actually on.
    //
    // AND THE TOKEN IS AFTER A `#`, NOT A `?` (quince#979 review): a fragment never reaches a
    // server, so it stays out of access logs and out of any `Referer` a later navigation emits.
    const link = screen.getByRole("link");
    expect(link).toHaveAttribute(
      "href",
      "https://quince.example:8968/onboarding/https/certificate/confirm#t=tok-abc",
    );
    expect(link.getAttribute("href")).not.toContain("?t=");
    // A NEW TAB: a user whose https link fails needs to still be looking at the instructions.
    expect(link).toHaveAttribute("target", "_blank");
  });

  it("counts down from the absolute deadline rather than from a tick counter", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ ...APPLIED, expires_at: futureISO(125) });
    renderApply();
    fireEvent.click(screen.getByRole("button", { name: /Try it now/i }));

    // 125s → "2m 05s". A decrementing counter would show the full window here, because no second
    // has elapsed — which is exactly how a throttled background tab drifts slow and tells somebody
    // they have time left after the window has closed.
    await waitFor(() => expect(screen.getByText(/2m 0[45]s/)).toBeInTheDocument());
  });

  it("shows the daemon's own sentence when the trial is refused", async () => {
    vi.spyOn(api, "post").mockRejectedValue(new Error("tls.cert_file: that certificate expired on 2026-08-01"));
    renderApply();
    fireEvent.click(screen.getByRole("button", { name: /Try it now/i }));

    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(/that certificate expired on 2026-08-01/),
    );
  });

  it("says doing nothing loses nothing, because nothing was written", async () => {
    vi.spyOn(api, "post").mockResolvedValue(APPLIED);
    renderApply();
    fireEvent.click(screen.getByRole("button", { name: /Try it now/i }));

    await waitFor(() => expect(screen.getByText(/quince goes back by itself/i)).toBeInTheDocument());
  });

  // THE COST TO THIS PAGE, WHICH NOTHING USED TO STATE. A live trial makes the plain half redirect
  // to https at once, so the page issuing these instructions loses its own API along with everything
  // else. A user who is not told reads the silence as a failure they caused.
  it("says this page stops working, and names the restart as the fast way out", () => {
    renderApply();
    expect(screen.getByText(/This page stops working while the trial runs/i)).toBeInTheDocument();
    expect(screen.getByText(/restart quince: that\s+cancels the trial immediately/i)).toBeInTheDocument();
  });

  // THE COVERAGE CLAIM FOLLOWS THE SERVER RATHER THAN BEING ASSERTED. With the name left empty the
  // confirm link points at the address the user is on, which a certificate for somewhere else does
  // not cover — and the old copy called that "the name the certificate covers" regardless.
  it("promises coverage only when the daemon says the confirm origin is covered", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ ...APPLIED, confirm_host_covered: false });
    renderApply();
    fireEvent.click(screen.getByRole("button", { name: /Try it now/i }));

    expect(await screen.findByText(/your browser will warn you first/i)).toBeInTheDocument();
    expect(screen.queryByText(/at a name this certificate covers/i)).not.toBeInTheDocument();
  });

  it("says the address is covered when it is", async () => {
    vi.spyOn(api, "post").mockResolvedValue(APPLIED);
    renderApply();
    fireEvent.click(screen.getByRole("button", { name: /Try it now/i }));

    expect(await screen.findByText(/at a name this certificate covers/i)).toBeInTheDocument();
  });
});

// THE BUTTON IS NOT OFFERED WHEN THE LINK COULD ONLY POINT SOMEWHERE UNCOVERED — Operator
// direction 2026-08-18. With the name left empty the confirm origin is the address this page is on,
// so a pair that does not cover it produces a browser warning on every visit, for as long as the
// install stands. The remedy is one field away, so the control stays visible and says which.
// THE WINDOW CLOSING IS A STATE, NOT A NUMBER REACHING ZERO. Found on the rig, 2026-08-18: a page
// left open past ten minutes read *"quince is serving it now. Confirm within no time left to keep
// it."* — the pair had rolled back, there was nothing to confirm, and the advice to wait described
// something that had already happened.
describe("when the trial window has closed", () => {
  it("says the trial is over and that nothing was written", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ ...APPLIED, expires_at: futureISO(-5) });
    renderApply();
    fireEvent.click(screen.getByRole("button", { name: /Try it now/i }));

    expect(await screen.findByText(/gone back to what it was serving/i)).toBeInTheDocument();
    expect(screen.getByText(/is exactly as it was/i)).toBeInTheDocument();
    // AND STOPS ASSERTING A LIVE TRIAL — the three claims that were false at zero.
    expect(screen.queryByText(/quince is serving it now/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/no time left/i)).not.toBeInTheDocument();
    // THE DEAD LINK IS GONE TOO. Offering it invites a 409 the user did nothing to deserve.
    expect(screen.queryByRole("link")).not.toBeInTheDocument();
  });

  // A WAY BACK, because the alternative is reloading the page and retyping two paths. Pressing it
  // returns to the pre-apply card with the same files still in hand.
  it("offers another attempt, which returns to the trial offer", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ ...APPLIED, expires_at: futureISO(-5) });
    renderApply();
    fireEvent.click(screen.getByRole("button", { name: /Try it now/i }));

    fireEvent.click(await screen.findByRole("button", { name: /Try again/i }));
    expect(screen.getByRole("button", { name: /Try it now/i })).toBeInTheDocument();
    expect(screen.queryByText(/gone back to what it was serving/i)).not.toBeInTheDocument();
  });

  // AND A LIVE TRIAL IS UNTOUCHED — the countdown still renders while there is time on it.
  it("still counts down while the window is open", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ ...APPLIED, expires_at: futureISO(125) });
    renderApply();
    fireEvent.click(screen.getByRole("button", { name: /Try it now/i }));

    expect(await screen.findByText(/quince is serving it now/i)).toBeInTheDocument();
    expect(screen.queryByText(/gone back to what it was serving/i)).not.toBeInTheDocument();
  });
});

describe("the dead-end guard", () => {
  it("refuses to offer a trial that would point at an uncovered address", () => {
    renderApply({ hostname: "", currentHost: "192.0.2.10", currentHostCovered: false });

    expect(screen.getByRole("button", { name: /Try it now/i })).toBeDisabled();
    expect(screen.getByText(/Not from this address/i)).toBeInTheDocument();
    expect(screen.getByText(/192\.0\.2\.10/)).toBeInTheDocument();
  });

  // THE JOURNEY THE OPERATOR RULED VALID IS UNAFFECTED — a certificate that DOES cover where you
  // are, which is what a self-signed pair or an internal CA gives you. The browser warns about the
  // issuer, trusting it once ends the warnings, and quince must not stand in the way.
  it("offers the trial when the address you are on is covered, name or no name", () => {
    renderApply({ hostname: "", currentHost: "quince.lan", currentHostCovered: true });

    expect(screen.getByRole("button", { name: /Try it now/i })).toBeEnabled();
    expect(screen.queryByText(/Not from this address/i)).not.toBeInTheDocument();
  });

  // AND TYPING A NAME CLEARS IT. The address in play becomes that name, whose coverage `outcome`
  // has already answered — the trial is not offered at all for anything but `usable`.
  it("offers the trial once a name is typed, whatever the current address is", () => {
    renderApply({ hostname: "quince.example", currentHost: "192.0.2.10", currentHostCovered: false });

    expect(screen.getByRole("button", { name: /Try it now/i })).toBeEnabled();
  });
});

function futureISO(seconds: number): string {
  return new Date(Date.now() + seconds * 1000).toISOString();
}
