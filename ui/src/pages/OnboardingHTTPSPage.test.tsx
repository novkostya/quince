import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OnboardingHTTPSPage } from "./OnboardingHTTPSPage";
import { api } from "@/lib/api";

// A ROUTER, BECAUSE THE CHOOSER NOW LINKS TO THE CERTIFICATE STEP'S OWN ROUTE (quince#908 §5). Without
// it `<Link>` throws and the page renders NOTHING — which is how this presented: three unrelated
// assertions failing against an empty `<body><div /></body>` rather than one failing about a link.
function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <OnboardingHTTPSPage />
      </MemoryRouter>
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

  // THE PRE-AUTH GUARANTEE, as far as a component test can carry it: everything this page asks
  // for is exempt from the auth guard. A call to /api/devices, say, would 401 for the one visitor
  // this page exists to help.
  //
  // ASSERTED AS THE GUARANTEE, NOT AS A CALL COUNT (quince#908). This read
  // `toHaveBeenCalledTimes(1)` — an exact proxy while the page asked for exactly one thing. The
  // two-mode split added `GET /api/auth/status`, which is ITSELF authExempt (`middleware.go`'s
  // list), so the guarantee held and only its proxy broke.
  //
  // The allowlist is SPELLED OUT rather than derived, so adding a call means editing this line and
  // meeting the question it asks: is that endpoint pre-auth? A count would only have said
  // "something changed", which is the same signal for a safe call and an unsafe one.
  it("calls only pre-auth endpoints", async () => {
    // `/api/health` joined the list when this page took the plain-http banner (quince#539). It is
    // the FIRST entry in `middleware.go`'s `authExempt` switch and has been pre-auth since qn.1 —
    // the login screen reads it to learn the serving mode — so the guarantee holds. Written as the
    // ANSWER to the question this line exists to ask, rather than as a list that quietly grew.
    const exempt = ["/api/onboarding/https", "/api/auth/status", "/api/health"];
    const get = vi.spyOn(api, "get").mockResolvedValue({ complete: false, detected: "none" });
    renderPage();
    await screen.findByText("Not encrypted");

    expect(get).toHaveBeenCalled();
    for (const [path] of get.mock.calls) {
      expect(exempt).toContain(path);
    }
  });
});

// quince#908 §2 — TWO MODES, not a redesign. A first-run visitor is SENT here since quince#923, so
// this page is the whole of what they can see and it must read as a decision; a returning user
// reading the same URL wants the reference material, which survives unchanged.
describe("the two modes", () => {
  function renderAs(state: "needs_setup" | "needs_login" | undefined) {
    vi.spyOn(api, "get").mockImplementation(((path: string) =>
      path === "/api/auth/status"
        ? Promise.resolve(state === undefined ? {} : { state, csrf_token: "t" })
        : Promise.resolve({ complete: false, detected: "none" })) as typeof api.get);
    return renderPage();
  }

  it("tells a first-run visitor that setup cannot be completed, not that sign-in will not work", async () => {
    renderAs("needs_setup");
    expect(await screen.findByText(/quince cannot finish setting up over it/i)).toBeInTheDocument();
    // The returning-user sentence would UNDERSTATE it: they have no account to sign in to, so it
    // would read as a problem for later rather than the thing blocking them now.
    expect(screen.queryByText(/signing in from a phone or another computer/i)).not.toBeInTheDocument();
  });

  it("drops the how-to sentences from the chooser", async () => {
    renderAs("needs_setup");
    await screen.findByText(/quince cannot finish setting up over it/i);
    // The issue's own worked example of the test: this answers "how do I do it", which the
    // chooser has not asked yet.
    expect(screen.queryByText(/serves both protocols on the same port/i)).not.toBeInTheDocument();
    // …and the badges STAY. They rank four options at a glance, which is what choosing needs.
    expect(screen.getByText("Recommended")).toBeInTheDocument();
    expect(screen.getByText("Not recommended")).toBeInTheDocument();
  });

  it("keeps the permanent cost in the chooser, because that is a decision and not a procedure", async () => {
    renderAs("needs_setup");
    await screen.findByText(/quince cannot finish setting up over it/i);
    expect(screen.getByText(/rules out notifications for good/i)).toBeInTheDocument();
  });

  it("leaves the reference page untouched for a returning user", async () => {
    renderAs("needs_login");
    expect(await screen.findByText(/signing in from a phone or another computer/i)).toBeInTheDocument();
    expect(screen.getByText(/serves both protocols on the same port/i)).toBeInTheDocument();
    expect(screen.queryByText(/quince cannot finish setting up over it/i)).not.toBeInTheDocument();
  });

  // THE SAFE DIRECTION, and it is the same one `useInsecureOrigin` takes: an unknown auth state
  // renders the version that is correct for everybody. Guessing "first run" for a visitor who has
  // an account would tell them setup is blocked when they were only trying to sign in.
  it("falls back to the reference page when the auth state is unknown", async () => {
    renderAs(undefined);
    expect(await screen.findByText(/signing in from a phone or another computer/i)).toBeInTheDocument();
    expect(screen.queryByText(/quince cannot finish setting up over it/i)).not.toBeInTheDocument();
  });
});

// quince#940 §2 + quince#939 §7 — THE PAGE SAYS WHICH CAUSE, AND SAYS IT DIFFERENTLY EACH TIME.
//
// All four rendered as **Not encrypted** and nothing else before `unencrypted_code` existed. Two of
// them sent the user to a remedy that was WRONG, not merely vaguer, which is what makes this a
// defect under `CLAUDE.md`'s rule rather than a copy improvement.
describe("what quince saw behind `detected: none`", () => {
  function plain(code?: string) {
    vi.spyOn(api, "get").mockResolvedValue({
      complete: false,
      detected: "none",
      ...(code ? { unencrypted_code: code } : {}),
    });
  }

  // THE CRUEL ONE. quince used to tell an operator their proxy was broken while it was behaving
  // perfectly and reporting the truth, so the assertion is that the page now says the opposite.
  it("says the proxy is CORRECT when the proxy reports plain http", async () => {
    plain("proxy_reports_plain");
    renderPage();

    expect(await screen.findByText(/reaching quince correctly/i)).toBeInTheDocument();
    expect(screen.getByText(/quince is not the thing to change/i)).toBeInTheDocument();
  });

  it("names the trust list when the header was ignored on purpose", async () => {
    plain("proxy_untrusted");
    renderPage();

    expect(await screen.findByText(/did not believe it/i)).toBeInTheDocument();
    expect(screen.getByText("QUINCE_TRUSTED_PROXIES")).toBeInTheDocument();
  });

  // WORDED AS A HINT AND ASSERTED AS ONE (quince#939 §7). It is inferred from `X-Forwarded-For`,
  // which nginx does not set by default either — so a correctly-configured deployment can land
  // here. "Looks like" is load-bearing and this test is what stops somebody tightening it into a
  // verdict.
  it("hedges the nginx caveat rather than asserting it", async () => {
    plain("proxy_not_forwarding_scheme");
    renderPage();

    expect(await screen.findByText(/it looks like something is in front of quince/i)).toBeInTheDocument();
    expect(screen.getByText(/cannot tell whether/i)).toBeInTheDocument();
    expect(screen.getByText(/proxy_set_header X-Forwarded-Proto \$scheme;/)).toBeInTheDocument();
  });

  // NOTHING EXTRA, AND THAT IS THE ANSWER RATHER THAN A MISSING ONE. quince saw no evidence of a
  // proxy, so the remedy is the tier list the page already renders; a second copy of it here would
  // be noise, and asserting "you have no proxy" would be the over-claim §7 refuses one row up.
  it("adds nothing when there is no evidence of a proxy at all", async () => {
    plain("no_proxy_seen");
    renderPage();

    expect(await screen.findByText("Not encrypted")).toBeInTheDocument();
    expect(screen.queryByText(/looks like something is in front/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/QUINCE_TRUSTED_PROXIES/)).not.toBeInTheDocument();
    expect(screen.queryByText(/reaching quince correctly/i)).not.toBeInTheDocument();
  });

  // AN OLDER DAEMON SENDS NO CODE AT ALL, and the page must not break or invent one. `omitempty`
  // means absent is a legal wire state, not only a first-run one.
  it("renders the plain-http copy unchanged when the field is absent", async () => {
    plain();
    renderPage();

    expect(await screen.findByText("Not encrypted")).toBeInTheDocument();
    expect(screen.queryByText(/looks like something is in front/i)).not.toBeInTheDocument();
  });
});

// quince#940 §1 — A BROKEN CERTIFICATE STOPS BEING INVISIBLE, AND STOPS BEING TOLD TO TRY AGAIN.
describe("a certificate quince could not use", () => {
  function broken(code: string) {
    vi.spyOn(api, "get").mockResolvedValue({
      complete: false,
      detected: "none",
      unencrypted_code: "no_proxy_seen",
      tls_unusable_code: code,
    });
  }

  // THE RULING REQUIRES THIS: the tier card must stop being OFFERED when the pair is unusable.
  // "Point tls.cert_file and tls.key_file at a certificate" is the thing this user has already
  // done — it is why quince has a failure to report — so leaving it up is two messages
  // contradicting each other on one card.
  it("replaces the instruction the user has already followed", async () => {
    broken("mismatched");
    renderPage();

    expect(await screen.findByText(/tried your certificate and could not use it/i)).toBeInTheDocument();
    expect(screen.queryByText(/at a certificate and key, mounted/i)).not.toBeInTheDocument();
  });

  // ONE CASE PER TEST rather than a loop. The first version looped and called `restoreAllMocks`
  // between iterations, which left `api.get` unmocked for the next one — so the fifth case rendered
  // an EMPTY PAGE and failed as *"the copy is missing"* when the page had never loaded at all. A
  // green-for-the-wrong-reason in reverse, and the same class as quince-devlog#243.
  it.each([
    ["mismatched", /does not belong to that certificate/i],
    ["expired", /Renew it/i],
    ["unreadable", /still has to be mounted/i],
    ["not_yet_valid", /check this machine's clock/i],
    ["unknown", /cannot tell why/i],
  ])("names a next action specific to %s", async (code, expected) => {
    broken(code as string);
    renderPage();
    expect(await screen.findByText(expected as RegExp)).toBeInTheDocument();
  });

  // NO PATH AND NO LOADER TEXT — the server never sends them, and the page must not invent a place
  // to put them either.
  it("shows no filesystem path", async () => {
    broken("unreadable");
    const { container } = renderPage();

    await screen.findByText(/tried your certificate and could not use it/i);
    expect(container.textContent).not.toMatch(/\/etc\/|\.pem|\.key\b/);
  });

  // AND THE ORDINARY INSTRUCTION SURVIVES when there is no failure to report — the common case is a
  // user who has not configured TLS at all, and they still need to be told how.
  it("keeps the instruction when no certificate is configured", async () => {
    vi.spyOn(api, "get").mockResolvedValue({ complete: false, detected: "none" });
    renderPage();

    expect(await screen.findByText(/at a certificate and key, mounted/i)).toBeInTheDocument();
    expect(screen.queryByText(/could not use it/i)).not.toBeInTheDocument();
  });
});
