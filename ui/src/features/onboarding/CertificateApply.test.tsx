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
  confirm_token: "tok-abc",
  expires_at: "2026-08-14T14:10:00Z",
  expires_seconds: 600,
  config_written: false,
};

beforeEach(() => {
  vi.restoreAllMocks();
});

function renderApply() {
  return render(
    <CertificateApply certFile="/tls/fullchain.pem" keyFile="/tls/privkey.pem" hostname="quince.example" />,
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
});

function futureISO(seconds: number): string {
  return new Date(Date.now() + seconds * 1000).toISOString();
}
