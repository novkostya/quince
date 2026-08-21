import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";

import { signInWithPasskey } from "./webauthn";
import { rememberPasskey, passkeyHintCredentialID } from "./passkeyHint";

// qn.13 slice 7 / G8 — the ceremony half, in the browser.
//
// THREE THINGS G8 ASKS FOR, and the third is the one a happy-path test would miss:
//   1. a remembered id changes only what is OFFERED — it is sent, and it is sent as a hint;
//   2. what the browser then REMEMBERS is the credential that asserted, not the one that was offered;
//   3. a remembered credential that no longer exists FALLS BACK to the discoverable flow rather than
//      dead-ending on a page that should have worked.
//
// THE SERVER IS STUBBED, so nothing here proves quince's half — `passkey_hint_test.go` does that. What
// is proven here is the client's contract with it: what it sends, what it retries, what it stores.

function beginResponse(allowCredentials: unknown[] = []): Response {
  return new Response(
    JSON.stringify({
      ceremony: "ceremony-key",
      options: { publicKey: { challenge: "AAAA", allowCredentials } },
    }),
    { status: 200, headers: { "Content-Type": "application/json" } },
  );
}

function finishResponse(): Response {
  return new Response(JSON.stringify({ state: "authenticated", csrf_token: "t" }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

// A stand-in credential. `rawId` and the response buffers only have to be byte sources — nothing
// here verifies a signature, and a real one would be a capability against a real authenticator
// (the spec's Fixtures rule).
function fakeCredential(id: string) {
  return {
    id,
    rawId: new Uint8Array([1, 2, 3]).buffer,
    type: "public-key",
    response: {
      clientDataJSON: new Uint8Array([4]).buffer,
      authenticatorData: new Uint8Array([5]).buffer,
      signature: new Uint8Array([6]).buffer,
      userHandle: null,
    },
  };
}

const origCreds = navigator.credentials;

beforeEach(() => {
  localStorage.clear();
  vi.restoreAllMocks();
});

afterEach(() => {
  // @ts-expect-error — jsdom's navigator.credentials is not typed as assignable.
  navigator.credentials = origCreds;
  vi.unstubAllGlobals();
  localStorage.clear();
});

/** bodyOf returns the JSON body of the n-th fetch call. */
function bodyOf(n: number): unknown {
  const call = vi.mocked(fetch).mock.calls[n];
  return JSON.parse(String((call[1] as RequestInit).body));
}

describe("the hint is sent, and it is sent as a hint", () => {
  it("puts the remembered credential id in the begin request", async () => {
    rememberPasskey("cred-abc");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValueOnce(beginResponse()).mockResolvedValueOnce(finishResponse()),
    );
    // @ts-expect-error — stand-in for the credentials container.
    navigator.credentials = { get: vi.fn().mockResolvedValue(fakeCredential("cred-abc")) };

    await signInWithPasskey({ conditional: false });

    expect(bodyOf(0)).toEqual({ credential_id: "cred-abc" });
  });

  it("sends nothing at all when this browser remembers no id", async () => {
    // qn.6k's boolean, and a fresh browser, both arrive here — and both want the old request.
    localStorage.setItem("quince.passkey.seen", "1");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValueOnce(beginResponse()).mockResolvedValueOnce(finishResponse()),
    );
    // @ts-expect-error — stand-in.
    navigator.credentials = { get: vi.fn().mockResolvedValue(fakeCredential("cred-xyz")) };

    await signInWithPasskey({ conditional: false });

    expect(bodyOf(0)).toEqual({});
  });

  it("offers the server's list to the authenticator instead of an empty one", async () => {
    // THE BUG THIS PINS. The client hardcoded `allowCredentials: []`, so it would have discarded the
    // offer even once the server began sending one — silently, because an empty list is also the
    // valid discoverable case.
    rememberPasskey("cred-abc");
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(beginResponse([{ type: "public-key", id: "AQID" }]))
        .mockResolvedValueOnce(finishResponse()),
    );
    const get = vi.fn().mockResolvedValue(fakeCredential("cred-abc"));
    // @ts-expect-error — stand-in.
    navigator.credentials = { get };

    await signInWithPasskey({ conditional: false });

    const offered = get.mock.calls[0][0].publicKey.allowCredentials;
    expect(offered).toHaveLength(1);
    expect(offered[0].id).toBeInstanceOf(Uint8Array);
    expect(Array.from(offered[0].id as Uint8Array)).toEqual([1, 2, 3]);
  });
});

// *CHANGE USER* IS THE DISCOVERABLE FLOW, deliberately not a separate mode (D2.2).
describe("change user", () => {
  it("runs the ceremony as if nothing were remembered", async () => {
    rememberPasskey("cred-abc");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValueOnce(beginResponse()).mockResolvedValueOnce(finishResponse()),
    );
    // @ts-expect-error — stand-in.
    navigator.credentials = { get: vi.fn().mockResolvedValue(fakeCredential("cred-other")) };

    await signInWithPasskey({ conditional: false, forgetHint: true });

    expect(bodyOf(0)).toEqual({});
  });
});

// WHAT IS REMEMBERED IS WHAT ASSERTED, which is how a discoverable ceremony teaches this browser an
// id it did not have — without anybody choosing an account.
describe("what the browser learns", () => {
  it("stores the credential that asserted, not the one that was offered", async () => {
    rememberPasskey("cred-abc");
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValueOnce(beginResponse()).mockResolvedValueOnce(finishResponse()),
    );
    // @ts-expect-error — stand-in.
    navigator.credentials = { get: vi.fn().mockResolvedValue(fakeCredential("cred-actually-used")) };

    await signInWithPasskey({ conditional: false, forgetHint: true });

    expect(passkeyHintCredentialID()).toBe("cred-actually-used");
  });
});

// G8's THIRD ASSERTION, and the failure mode a revocation creates.
describe("a remembered credential that no longer exists", () => {
  it("falls back to the discoverable flow instead of dead-ending", async () => {
    rememberPasskey("cred-revoked");
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(beginResponse([{ type: "public-key", id: "AQID" }]))
        .mockResolvedValueOnce(beginResponse())
        .mockResolvedValueOnce(finishResponse()),
    );
    const get = vi
      .fn()
      // The platform's answer when `allowCredentials` names something it cannot find.
      .mockRejectedValueOnce(Object.assign(new Error("no passkey"), { name: "NotAllowedError" }))
      .mockResolvedValueOnce(fakeCredential("cred-admin"));
    // @ts-expect-error — stand-in.
    navigator.credentials = { get };

    await signInWithPasskey({ conditional: false });

    // TWO FULL CEREMONIES, not a reused challenge: the first key was spent, so the retry has to
    // begin again or the server answers `no_ceremony`.
    expect(bodyOf(0)).toEqual({ credential_id: "cred-revoked" });
    expect(bodyOf(1)).toEqual({});
    expect(get).toHaveBeenCalledTimes(2);
    // And the dead memory is gone, so the next visit does not repeat the wasted ceremony.
    expect(passkeyHintCredentialID()).toBe("cred-admin");
  });

  it("does not retry when there was no hint to blame", async () => {
    // Without a hint the ceremony was already discoverable, so a retry runs the identical request
    // and turns one refusal into two sheets.
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce(beginResponse()));
    const get = vi
      .fn()
      .mockRejectedValue(Object.assign(new Error("no passkey"), { name: "NotAllowedError" }));
    // @ts-expect-error — stand-in.
    navigator.credentials = { get };

    await expect(signInWithPasskey({ conditional: false })).rejects.toThrow();
    expect(get).toHaveBeenCalledTimes(1);
  });

  it("does not retry a server REJECTION, which is an answer rather than a failure to ask", async () => {
    rememberPasskey("cred-abc");
    vi.stubGlobal(
      "fetch",
      vi
        .fn()
        .mockResolvedValueOnce(beginResponse())
        .mockResolvedValueOnce(
          new Response(
            JSON.stringify({ error: { code: "unauthorized", message: "this passkey was not accepted" } }),
            { status: 401, headers: { "Content-Type": "application/json" } },
          ),
        ),
    );
    const get = vi.fn().mockResolvedValue(fakeCredential("cred-abc"));
    // @ts-expect-error — stand-in.
    navigator.credentials = { get };

    await expect(signInWithPasskey({ conditional: false })).rejects.toThrow();
    expect(get).toHaveBeenCalledTimes(1);
    // The hint is cleared by the rejection path, so the sheet stops firing unprompted.
    expect(passkeyHintCredentialID()).toBe("");
  });
});
