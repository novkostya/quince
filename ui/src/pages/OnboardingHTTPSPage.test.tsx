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

    // The docs reference is a LINK, not a filename. This page is read on a phone more than
    // anywhere else, and a phone cannot open `deploy/tls.md` — printing a path at somebody who
    // is stuck is a dead end dressed as help (Operator, 2026-08-02, on a screenshot).
    //
    // THREE, not `toBeGreaterThan(0)`, which is what this said first. Three tiers carry the
    // link, so a floor of one passes while two of them lose it — and the comment above claimed
    // the assertion existed to stop exactly that tidy-up. An assertion weaker than the sentence
    // describing it is the day's recurring defect (review on quince#560).
    const docLinks = screen.getAllByRole("link", { name: "deploy/tls.md" });
    expect(docLinks).toHaveLength(3);
    for (const a of docLinks) {
      expect(a).toHaveAttribute("href", "https://github.com/novkostya/quince/blob/main/deploy/tls.md");
    }

    // Plain http forecloses web push the same way a click-through certificate does, and the
    // page said so for only one of the two until the Operator asked.
    expect(screen.getByText(/only allow web push on an encrypted origin/i)).toBeInTheDocument();

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
  // page is still useful without the server's answer, because the tiers are static prose.
  //
  // THE SECOND HALF OF THAT NAME WAS UNASSERTED IN THE FIRST VERSION, and the code did not do it:
  // every tier lived inside the not-complete branch, so a failed check printed "the setup options
  // below are still correct" above nothing. The test passed. It is the quince#556 shape — a name
  // that documents a behaviour the body never checks — and a test name is documentation nobody
  // runs: when it drifts from the body it does not fail, it reassures (review on quince#559).
  it("is honest when the check itself fails, and still shows the options", async () => {
    vi.spyOn(api, "get").mockRejectedValue(new Error("boom"));
    renderPage();

    expect(await screen.findByText(/Could not check this connection/i)).toBeInTheDocument();
    expect(screen.queryByText("Encrypted")).not.toBeInTheDocument();

    // The half the name promises. All five, because the copy says "the setup options below".
    for (const title of [
      "Put something in front of quince",
      "Give quince your own certificate",
      "Plain HTTP on a network you trust",
      "A certificate quince makes itself",
      "An address quince manages for you",
    ]) {
      expect(screen.getByText(title)).toBeInTheDocument();
    }
  });

  // The other side of the same coin: a SUCCESSFUL check still offers nothing. Without this, a
  // future fix to the error path could lift the tiers to always-render and quietly break G1.
  it("still offers nothing when the check succeeds, even though the error path shows tiers", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ complete: true, detected: "tls" });
    renderPage();

    await screen.findByText("Encrypted");
    expect(screen.queryByText("Put something in front of quince")).not.toBeInTheDocument();
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
