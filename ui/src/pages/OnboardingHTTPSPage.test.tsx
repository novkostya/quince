import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OnboardingHTTPSPage } from "./OnboardingHTTPSPage";
import { api } from "@/lib/api";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <OnboardingHTTPSPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe("the onboarding HTTPS check", () => {
  // G1: an already-secure origin completes with NO buttons. The top-tier user — a reverse proxy
  // or `tailscale serve` — must meet zero friction, so the absence of the options is the
  // assertion, not an afterthought.
  it("offers nothing at all when the origin is already encrypted", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ complete: true, detected: "forwarded_proto" });
    renderPage();

    expect(await screen.findByText("Encrypted")).toBeInTheDocument();
    expect(screen.getByText(/proxy in front of quince/i)).toBeInTheDocument();

    // No tier is rendered. If this ever starts failing, the top tier has grown friction.
    expect(screen.queryByText(/Put something in front of quince/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/Not implemented/i)).not.toBeInTheDocument();
  });

  it("names TLS rather than a proxy when quince terminated it", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ complete: true, detected: "tls" });
    renderPage();
    expect(await screen.findByText(/quince is serving this connection over HTTPS/i)).toBeInTheDocument();
  });

  // Story 2: plain HTTP is offered the four tiers, with BOTH not-implemented rows rendered and
  // labelled — "not merely inert". A tier that is silently absent reads as an option nobody
  // thought of; one that is present and labelled says the decision was made.
  it("offers all four tiers on plain http, with both disabled rows labelled", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ complete: false, detected: "none" });
    renderPage();

    expect(await screen.findByText("Not encrypted")).toBeInTheDocument();

    for (const title of [
      "Put something in front of quince",
      "Give quince your own certificate",
      "Plain HTTP on a network you trust",
      "A certificate quince makes itself",
      "An address quince manages for you",
    ]) {
      expect(screen.getByText(title)).toBeInTheDocument();
    }

    // Rendered AND labelled, both of them — the story's own words.
    expect(screen.getAllByText("Not implemented")).toHaveLength(2);
  });

  // It must say what goes WRONG, not only that the connection is plain. "Your browser discards
  // the cookie" is the mechanism; "you cannot sign in from a phone" is what the user came about.
  it("explains the consequence, not just the state", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ complete: false, detected: "none" });
    renderPage();
    expect(await screen.findByText(/signing in from a phone/i)).toBeInTheDocument();
  });

  // A failed check must not claim the connection is fine, and must not hide the options — the
  // page is still useful without the server's answer, because the tiers are static.
  it("is honest when the check itself fails, and still shows the options", async () => {
    vi.spyOn(api, "get").mockRejectedValue(new Error("boom"));
    renderPage();

    expect(await screen.findByText(/Could not check this connection/i)).toBeInTheDocument();
    expect(screen.queryByText("Encrypted")).not.toBeInTheDocument();
  });

  // THE PRE-AUTH GUARANTEE, as far as a component test can carry it: the page asks for exactly
  // one endpoint, and it is the exempt one. A second call — to /api/devices, say — would be
  // behind the auth guard and would 401 for the visitor this page exists to help.
  it("calls only the pre-auth endpoint", async () => {
    const get = vi.spyOn(api, "get").mockResolvedValue({ complete: false, detected: "none" });
    renderPage();
    await screen.findByText("Not encrypted");

    expect(get).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledWith("/api/onboarding/https");
  });
});
