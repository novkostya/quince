import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { probeTargetURL, runProbe } from "./probe";
import { api } from "./api";

// THE FIVE OUTCOMES (quince#939 §4). They are asserted as a table because the copy for each is
// different and ACTIONABLE — "your proxy is working and is not telling quince" is a different
// sentence from "a different quince answered" — so a classification that collapses two of them ships
// a page confidently telling somebody the wrong thing to fix.

const NONCE = "nonce-abc";

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, "get").mockResolvedValue({ nonce: NONCE });
});
afterEach(() => vi.restoreAllMocks());

function answers(body: unknown, ok = true) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue({ ok, json: () => Promise.resolve(body) } as unknown as Response),
  );
}

describe("probeTargetURL", () => {
  it.each([
    ["quince.example.com", "https://quince.example.com/api/onboarding/probe"],
    // FORGIVING ABOUT THE SCHEME, because the field asks for a name and people paste addresses.
    ["https://quince.example.com", "https://quince.example.com/api/onboarding/probe"],
    // AND IT FORCES https EVEN WHEN http WAS TYPED. The whole point is to check the encrypted door;
    // probing plain http would answer a question nobody asked and could report success for a setup
    // that is not encrypted at all.
    ["http://quince.example.com", "https://quince.example.com/api/onboarding/probe"],
    // A PORT SURVIVES. A proxy on 8443 is ordinary, and silently dropping it would report
    // "unreachable" about a working deployment.
    ["quince.example.com:8443", "https://quince.example.com:8443/api/onboarding/probe"],
    // A pasted path is replaced rather than appended to — the probe has one path.
    ["https://quince.example.com/some/page", "https://quince.example.com/api/onboarding/probe"],
  ])("turns %o into %o", (input, want) => {
    expect(probeTargetURL(input)?.toString()).toBe(want);
  });

  it.each([["", "  ", "https://", "::::"]].flat())("refuses %o", (input) => {
    expect(probeTargetURL(input)).toBeNull();
  });
});

describe("runProbe", () => {
  it("reports READY when the proxy forwarded the scheme", async () => {
    answers({ nonce: NONCE, detected: "forwarded_proto" });
    const got = await runProbe(probeTargetURL("quince.example.com")!);
    expect(got).toEqual({ kind: "ready", url: "https://quince.example.com" });
  });

  // THE NGINX CAVEAT, and the reason this issue exists: a working HTTPS site whose proxy does not
  // forward the scheme. It must NOT read as "unreachable" or as a broken proxy.
  it("reports NO-FORWARDED-PROTO when quince saw plain http", async () => {
    answers({ nonce: NONCE, detected: "none" });
    const got = await runProbe(probeTargetURL("quince.example.com")!);
    expect(got.kind).toBe("no-forwarded-proto");
  });

  it("reports QUINCE-TLS when the name reaches quince's own certificate", async () => {
    answers({ nonce: NONCE, detected: "tls" });
    const got = await runProbe(probeTargetURL("quince.example.com")!);
    expect(got.kind).toBe("quince-tls");
  });

  // THE NONCE IS THE WHOLE REASON A SUCCESS MEANS ANYTHING. Without this branch, a name pointing at
  // a DIFFERENT quince on the LAN passes the check and the user is sent somewhere else entirely.
  it("reports OTHER-QUINCE when the echo does not match", async () => {
    answers({ nonce: "a-different-nonce", detected: "forwarded_proto" });
    const got = await runProbe(probeTargetURL("quince.example.com")!);
    expect(got.kind).toBe("other-quince");
  });

  // AND THE MISMATCH OUTRANKS `detected`. A stranger's transport is not this deployment's, so
  // believing its answer would report another box's state as ours — asserted with the MOST
  // reassuring `detected` value, which is the one that would do the damage.
  it("does not believe a stranger's detected", async () => {
    answers({ nonce: "someone-else", detected: "forwarded_proto" });
    expect((await runProbe(probeTargetURL("quince.example.com")!)).kind).toBe("other-quince");
  });

  it.each([
    ["a rejected fetch — DNS, refused connection, untrusted cert, or CORS", () => {
      vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
    }],
    ["a non-2xx answer", () => answers({}, false)],
    ["a body that is not JSON", () => {
      vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
        ok: true,
        json: () => Promise.reject(new SyntaxError("bad json")),
      } as unknown as Response));
    }],
  ])("reports UNREACHABLE for %s", async (_name, arrange) => {
    arrange();
    const got = await runProbe(probeTargetURL("quince.example.com")!);
    expect(got).toEqual({ kind: "unreachable", url: "https://quince.example.com" });
  });

  // THE NONCE IS MINTED SAME-ORIGIN AND TRAVELS IN THE QUERY STRING — both are ruled (contracts §1).
  // A custom header would trigger a CORS preflight, and the OPTIONS preflight does not carry the
  // header's value, so the server's gate would have nothing to gate on.
  it("mints same-origin and presents the nonce in the query string", async () => {
    answers({ nonce: NONCE, detected: "forwarded_proto" });
    await runProbe(probeTargetURL("quince.example.com")!);

    expect(api.get).toHaveBeenCalledWith("/api/onboarding/probe/nonce");

    const [url, init] = vi.mocked(fetch).mock.calls[0];
    expect(new URL(String(url)).searchParams.get("nonce")).toBe(NONCE);
    expect(init).toMatchObject({ mode: "cors", credentials: "omit" });
    // NOT `no-cors`: an opaque response resolves successfully while being unreadable, which restores
    // the exact ambiguity the nonce exists to remove.
    expect(init?.mode).not.toBe("no-cors");
    // NO CUSTOM HEADERS — the request must stay a SIMPLE one or it preflights.
    expect(init?.headers).toBeUndefined();
  });
});
