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

  // NOTHING IS SAVED, AND THE PAGE SAYS SO. Slice 5 adds apply-live with a server-side revert timer;
  // until then a page that LOOKED like it had configured something would be the worst half-step —
  // the user restarts expecting HTTPS and gets whatever was already there.
  it("says plainly that nothing has been saved", async () => {
    vi.spyOn(api, "post").mockResolvedValue(USABLE);

    renderPage();
    fill();
    fireEvent.click(screen.getByRole("button", { name: /Check these files/i }));

    expect(await screen.findByText(/Nothing has been saved/i)).toBeInTheDocument();
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
});
