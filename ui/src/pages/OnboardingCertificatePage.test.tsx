import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { dirname, resolve } from "node:path";
import { describe, expect, it, vi, beforeEach } from "vitest";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { OnboardingCertificatePage } from "./OnboardingCertificatePage";
import { api } from "@/lib/api";
import type { CertificateProbe } from "@/lib/types";

function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <OnboardingCertificatePage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// THE DEFAULT FIXTURE HAS A NAME TYPED, so the "where you are standing" line stays out of the tests
// that are about something else — it renders only while the hostname field is empty, which is the
// one case where leaving it empty decides anything.
const USABLE: CertificateProbe = {
  cert_file: "/tls/fullchain.pem",
  key_file: "/tls/privkey.pem",
  hostname: "quince.example",
  outcome: "usable",
  reason: "/tls/fullchain.pem and /tls/privkey.pem load, match, and are valid until 2027-01-01T00:00:00Z",
  names: ["quince.example"],
  not_before: "2026-01-01T00:00:00Z",
  not_after: "2027-01-01T00:00:00Z",
  chain_length: 2,
  current_host: "quince.example",
  current_host_covered: true,
};

function fill(cert = "/tls/fullchain.pem", key = "/tls/privkey.pem", host = "") {
  fireEvent.change(screen.getByLabelText(/Certificate file/i), { target: { value: cert } });
  fireEvent.change(screen.getByLabelText(/Key file/i), { target: { value: key } });
  if (host !== "") {
    fireEvent.change(screen.getByLabelText(/The name you will reach quince at/i), {
      target: { value: host },
    });
  }
}

beforeEach(() => {
  vi.restoreAllMocks();
  vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("not stubbed")));
});

describe("the certificate step", () => {
  // THE PLACEHOLDERS ARE THE PATHS THE SHIPPED EXAMPLES PRODUCE, read out of those examples rather
  // than copied into this test. A placeholder is instruction: it is what a user types when they
  // have no idea what to type, and one from a convention this project does not ship teaches a path
  // that exists nowhere. Found on the rig, 2026-08-18 — the field suggested `/etc/quince/tls/…`
  // while both compose examples mount `/certs`.
  //
  // IT READS THE COMPOSE FILES because they are now the artifact a user copies from. This test read
  // `deploy/tls.md`'s `tls:` block until quince#1307 retired it: mounting a certificate and letting
  // THIS SCREEN configure it is the documented route, so there is no longer a config snippet to
  // copy — which makes these placeholders the only place those paths are stated to a user.
  //
  // Both examples are checked, not one. They are separate files that can drift, and a placeholder
  // matching only the file a given reader did not take is the same defect one step removed.
  it("suggests paths under the directory the shipped compose examples mount", () => {
    const here = dirname(fileURLToPath(import.meta.url));
    for (const name of ["compose.yml", "compose.host-muxer.yml"]) {
      const example = readFileSync(resolve(here, `../../../deploy/${name}`), "utf8");
      const mount = /:(\/certs)(:ro)?\s*$/m.exec(example);
      expect(mount, `deploy/${name} no longer mounts a certificate directory`).not.toBeNull();
    }

    renderPage();
    expect(screen.getByLabelText(/Certificate file/i)).toHaveAttribute("placeholder", "/certs/quince.pem");
    expect(screen.getByLabelText(/^Key file/i)).toHaveAttribute("placeholder", "/certs/quince.key");
  });

  // THE HOSTNAME FIELD STARTS EMPTY AND IS NEVER PRE-FILLED (quince#908 §5). That is the name they
  // are LEAVING — an IP or a `.local` — and no CA issues for either. Pre-filling it would quietly
  // aim the whole step at the address the user came here to stop using.
  it("starts with an empty hostname", () => {
    renderPage();
    expect(screen.getByLabelText(/The name you will reach quince at/i)).toHaveValue("");
  });

  // THE SERVER'S SENTENCE IS SHOWN, NOT REPLACED (quince#514, quince#940). quince knows which of the
  // two files failed; a client composing prose from the enum would say "certificate problem".
  it("shows the daemon's own reason rather than composing one", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      ...USABLE,
      outcome: "mismatched",
      reason: "/tls/fullchain.pem with /tls/privkey.pem: tls: private key does not match public key",
      names: [],
      not_after: "",
    } satisfies CertificateProbe);

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    expect(await screen.findByText(/private key does not match public key/i)).toBeInTheDocument();
    expect(screen.getByText("Not usable")).toBeInTheDocument();
  });

  // `wrong_host` MUST NAME WHAT IT DOES COVER. "Does not cover quince.example" is a status; the
  // list of names is the thing a person acts on.
  it("lists the names a certificate does cover", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      ...USABLE,
      outcome: "wrong_host",
      reason: "/tls/fullchain.pem does not cover quince.other — it covers quince.example, quince.lan",
      names: ["quince.example", "quince.lan"],
    } satisfies CertificateProbe);

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    // TWICE, AND BOTH ARE WANTED: once inside the daemon's own sentence, and once on the `Covers:`
    // line, which is what somebody scanning the result reads first. `findAllByText` rather than a
    // narrower query, because asserting only one of the two would let the other be dropped.
    expect(await screen.findAllByText(/quince\.example, quince\.lan/)).toHaveLength(2);
  });

  // A LEAF WITH NO INTERMEDIATE IS REPORTED, NOT JUDGED. It validates on a machine that caches the
  // issuer and fails on a phone that does not — the hardest TLS failure to diagnose — but whether it
  // matters depends on the issuer, so the page states the consequence rather than calling it wrong.
  it("warns about a missing intermediate without calling the pair unusable", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ ...USABLE, chain_length: 1 } satisfies CertificateProbe);

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    expect(await screen.findByText("The files are usable")).toBeInTheDocument();
    expect(screen.getByText(/phones reject it while this computer accepts it/i)).toBeInTheDocument();
  });

  // THE TRIAL IS OFFERED ONLY FOR A PAIR THE SERVER CALLED `usable`.
  //
  // This test said *"says plainly that nothing has been saved"* until slice 5, which is what the
  // page had to say while there was nothing to press. The half that was load-bearing survives as the
  // case below: a pair that FAILED the check still gets that sentence, because the server refuses to
  // serve one and a button leading to a refusal would be a promise the product does not keep.
  it("offers the trial once the files check out", async () => {
    vi.spyOn(api, "post").mockResolvedValue(USABLE);

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    expect(await screen.findByRole("button", { name: /Try it now/i })).toBeInTheDocument();
    expect(screen.queryByText(/Nothing was saved/i)).not.toBeInTheDocument();
  });

  it("does not offer the trial for a pair the check refused", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      ...USABLE,
      outcome: "expired",
      reason: "/tls/fullchain.pem expired on 2026-08-01",
    });

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    expect(await screen.findByText(/Nothing was saved/i)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Try it now/i })).not.toBeInTheDocument();
  });

  // THE REACHABILITY HALF IS SKIPPED WHEN THE PAIR IS ALREADY UNUSABLE. Probing a name whose
  // certificate is known to be expired asks the user to debug DNS for something that was never going
  // to work — the same misdirection quince#940's sweep is against.
  it("does not probe the network when the files are unusable", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      ...USABLE,
      outcome: "expired",
      reason: "/tls/fullchain.pem expired on 2026-01-02T00:00:00Z",
    } satisfies CertificateProbe);

    renderPage();
    fill("/tls/fullchain.pem", "/tls/privkey.pem", "quince.example");
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    await screen.findByText(/expired on/i);
    // NOT `not.toHaveBeenCalled()`, WHICH IS TOO BROAD AND FAILED HONESTLY: the plain-http banner
    // polls `/api/health` through the same `fetch`, so the assertion has to be about the PROBE — a
    // cross-origin request to the typed name — rather than about network activity in general.
    const probed = vi.mocked(fetch).mock.calls.map(([u]) => String(u));
    expect(probed.filter((u) => u.includes("quince.example"))).toEqual([]);
  });

  // AND A STALE VERDICT IS CLEARED THE MOMENT AN INPUT CHANGES. Leaving the previous answer on
  // screen beside different paths invites acting on a result about other files.
  it("clears the verdict when a field changes", async () => {
    vi.spyOn(api, "post").mockResolvedValue(USABLE);

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));
    await screen.findByText("The files are usable");

    fireEvent.change(screen.getByLabelText(/Certificate file/i), { target: { value: "/other.pem" } });
    await waitFor(() => expect(screen.queryByText("The files are usable")).not.toBeInTheDocument());
  });

  // RETURN RUNS THE CHECK. Reported three times on this flow, once per field-and-button screen, so
  // it is asserted here rather than left to the markup: on a phone the key is labelled "go", and a
  // page that does nothing when it is pressed reads as broken.
  it("runs the check when Return is pressed in a field", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue(USABLE);
    renderPage();
    fill();

    fireEvent.submit(screen.getByLabelText(/Certificate file/i).closest("form")!);

    await waitFor(() => expect(post).toHaveBeenCalledWith("/api/onboarding/certificate", expect.anything()));
  });

  // AND DOES NOTHING WHILE A FIELD IS EMPTY, from the same rule that greys the button: a disabled
  // default button is what makes implicit submission a no-op, so there is no second condition to
  // keep in step.
  it("does nothing on Return until both files are named", () => {
    const post = vi.spyOn(api, "post").mockResolvedValue(USABLE);
    renderPage();

    fireEvent.submit(screen.getByLabelText(/Certificate file/i).closest("form")!);
    expect(post).not.toHaveBeenCalled();
  });

  // THE CHECK CANNOT RUN WITHOUT BOTH FILES — the 422 exists on the server, and asking for it is a
  // round trip that tells the user nothing they could not be told immediately.
  it("will not check until both files are named", () => {
    renderPage();
    expect(screen.getByRole("button", { name: /Check these files/i })).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/Certificate file/i), { target: { value: "/a.pem" } });
    expect(screen.getByRole("button", { name: /Check these files/i })).toBeDisabled();
    fireEvent.change(screen.getByLabelText(/Key file/i), { target: { value: "/a.key" } });
    expect(screen.getByRole("button", { name: /Check these files/i })).toBeEnabled();
  });

  // WHAT LEAVING THE NAME EMPTY WILL MEAN — the answer to the question the field's own label used to
  // dodge. Empty means *keep using the address I am on*, so whether that certificate covers that
  // address is the only thing that decides whether empty is a good idea.
  it("says the address you are on is covered, when the name is left empty", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      ...USABLE,
      hostname: "",
      current_host: "quince.example",
      current_host_covered: true,
    });

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    expect(await screen.findByText(/you can leave the name empty/i)).toBeInTheDocument();
  });

  // THE CASE THE WALK FOUND: a wildcard for somewhere else, checked from an IP, called usable with
  // nothing said about the address in play — and a browser interstitial two screens later.
  it("warns when the address you are on is not covered, and still calls the files usable", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      ...USABLE,
      hostname: "",
      current_host: "192.0.2.10",
      current_host_covered: false,
    });

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    expect(await screen.findByText(/browser will warn you every visit/i)).toBeInTheDocument();
    // NOT A REFUSAL. An IP-only LAN install is legitimate and the trial is still offered.
    expect(screen.getByText("The files are usable")).toBeInTheDocument();
  });

  // AND IT STAYS QUIET ONCE A NAME IS TYPED. The address in play is then that name, `outcome` already
  // answers coverage for it, and a note about the address they are leaving is noise.
  it("says nothing about the current address once a name is typed", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      ...USABLE,
      hostname: "quince.example",
      current_host: "192.0.2.10",
      current_host_covered: false,
    });

    renderPage();
    fill("/tls/fullchain.pem", "/tls/privkey.pem", "quince.example");
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    await screen.findByText("The files are usable");
    // ASSERTED ON THE ADDRESS ITSELF rather than on the sentence: the field's own hint says "the
    // address you are on now" whatever is typed, so a phrase match would find that instead and pass
    // for the wrong reason.
    expect(screen.queryByText("192.0.2.10")).not.toBeInTheDocument();
  });

  // THE PAGE HANDS THE TRIAL THE DAEMON'S ANSWER, which is what lets the button be gated before it
  // can be pressed. A page deriving the address from `window.location` would be a second answer to a
  // question the daemon already answered, free to disagree with the sentence right above the button.
  it("gates the trial on the address the daemon reported, not on the browser's own", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      ...USABLE,
      hostname: "",
      current_host: "192.0.2.10",
      current_host_covered: false,
    });

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    // ASSERTED ON AN UNBROKEN PHRASE: the sentence above puts <strong>not</strong> and a <code> host
    // inside itself, and the default matcher does not read across elements.
    expect(await screen.findByText(/the address you are on/i)).toBeInTheDocument();
    expect(screen.getByText(/Fix the problem above/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Try it now/i })).toBeDisabled();
  });

  // THE REACH HALF IS A REAL FINDING NOW. It probes http at the typed name — which quince is serving
  // while this page is open — so a failure means the name does not get here, rather than the
  // foregone "no TLS there yet" an https probe would always have returned.
  it("refuses the name when this browser cannot reach quince at it over http", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ ...USABLE, hostname: "quince.example" });
    vi.spyOn(api, "get").mockResolvedValue({ nonce: "n-1" });
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("nope")));

    renderPage();
    fill("/certs/quince.pem", "/certs/quince.key", "quince.example");
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    // ONCE. The card that would have repeated it points at it instead — the same sentence twice,
    // two boxes apart, reads as a rendering fault.
    expect((await screen.findAllByText(/cannot reach quince at/i)).length).toBe(1);
    expect(screen.getByText(/Check that the name points/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /Try it now/i })).toBeDisabled();
  });

  // AND A NAME THAT DOES REACH THIS QUINCE IS REPORTED AS THE PRECONDITION IT IS — not as a promise
  // that the browser will trust the certificate, which no probe can answer.
  it("confirms a name that reaches this quince, without promising the trial will pass", async () => {
    vi.spyOn(api, "post").mockResolvedValue({ ...USABLE, hostname: "quince.example" });
    vi.spyOn(api, "get").mockResolvedValue({ nonce: "n-2" });
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.resolve({ nonce: "n-2", detected: "plain" }),
      } as unknown as Response),
    );

    renderPage();
    fill("/certs/quince.pem", "/certs/quince.key", "quince.example");
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    expect(await screen.findByText(/That name reaches quince/i)).toBeInTheDocument();
    expect(screen.getByText(/move it to https/i)).toBeInTheDocument();
  });

  // THE VERDICT RENDERS EVEN IF `names` ARRIVES AS `null`. The server now sends `[]` on every
  // outcome, so this asserts the page does not depend on that being true — it reads the field
  // structurally, and this page sits under no error boundary, so one unexpected null replaces the
  // whole flow with react-router's default screen and a minified stack.
  //
  // THE CAST IS THE POINT: `CertificateProbe` declares an array, so a well-typed mock cannot express
  // the shape being defended against.
  it("renders a verdict whose names field is null rather than an array", async () => {
    vi.spyOn(api, "post").mockResolvedValue({
      ...USABLE,
      outcome: "unreadable",
      reason: "/tls/fullchain.pem with /tls/privkey.pem: no such file or directory",
      names: null,
      not_before: "",
      not_after: "",
      chain_length: 0,
    } as unknown as CertificateProbe);

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    expect(await screen.findByText(/no such file or directory/i)).toBeInTheDocument();
    expect(screen.getByText("Not usable")).toBeInTheDocument();
    expect(screen.queryByText(/^Covers:/)).not.toBeInTheDocument();
  });
});
