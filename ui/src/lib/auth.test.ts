import { describe, it, expect, vi, beforeEach } from "vitest";

import { api, APIError, UnauthorizedError } from "./api";
import { changePassword, removePassword } from "./auth";
import * as webauthn from "./webauthn";
import * as reauth from "./reauth";


// qn.6n slice 6 — the re-authentication prompt, and the shape that lets it land BEFORE the server
// demands a proof.
//
// THE TRIGGER IS THE SERVER'S 401, NOT A GUESS ABOUT THE INSTALL. Against today's `main` the retry
// branch is unreachable, because nothing answers `reauth_required` — so this ships inert and becomes
// correct the day slice 4a lands. A client that decided for itself when to prompt would be a second
// copy of the server's rule and would prompt on installs that do not need one.

beforeEach(() => vi.restoreAllMocks());

const reauthRequired = () =>
  new APIError(401, "reauth_required", "no proof for this operation — authenticate again");

describe("changing the password", () => {
  // THE ORDINARY CASE, AND THE ONE THAT MUST NOT GROW A PROMPT. An install with a password answers
  // 204 to the first attempt, so no ceremony runs and the user sees no Face ID sheet at all.
  it("does not re-authenticate when the server accepts the first attempt", async () => {
    const put = vi.spyOn(api, "put").mockResolvedValue(undefined);
    const prove = vi.spyOn(reauth, "proveWithPasskey");

    await changePassword("old", "new");

    expect(put).toHaveBeenCalledTimes(1);
    expect(put).toHaveBeenCalledWith("/api/auth/password", {
      current_password: "old",
      new_password: "new",
    });
    expect(prove).not.toHaveBeenCalled();
  });

  it("re-authenticates and retries with the proof when the server asks", async () => {
    const put = vi
      .spyOn(api, "put")
      .mockRejectedValueOnce(reauthRequired())
      .mockResolvedValueOnce(undefined);
    const prove = vi.spyOn(reauth, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");

    await changePassword("", "brand-new");

    // THE OPERATION IS NAMED, and it is the one the server will check the proof against. A proof
    // minted for anything else is refused, so this argument is load-bearing rather than a label.
    expect(prove).toHaveBeenCalledWith("set_password");
    expect(put).toHaveBeenCalledTimes(2);
    expect(put).toHaveBeenLastCalledWith("/api/auth/password", {
      current_password: "",
      new_password: "brand-new",
      proof: "PROOF-TOKEN",
    });
  });

  // ANY OTHER FAILURE IS RETHROWN UNTOUCHED. A wrong current password is a 401 too — `bad_password`
  // — and re-authenticating would answer a question the user did not get wrong, then fail again.
  it("does not re-authenticate on a wrong current password", async () => {
    const put = vi
      .spyOn(api, "put")
      .mockRejectedValue(new APIError(401, "bad_password", "current password is incorrect"));
    const prove = vi.spyOn(reauth, "proveWithPasskey");

    await expect(changePassword("wrong", "new")).rejects.toMatchObject({ code: "bad_password" });
    expect(prove).not.toHaveBeenCalled();
    expect(put).toHaveBeenCalledTimes(1);
  });

  // ONE RETRY, NEVER A LOOP. A second refusal is a real one, and retrying would turn a stated error
  // into a silent hang behind repeated Face ID sheets.
  it("gives up after one retry rather than looping", async () => {
    const put = vi.spyOn(api, "put").mockRejectedValue(reauthRequired());
    const prove = vi.spyOn(reauth, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");

    await expect(changePassword("", "new")).rejects.toMatchObject({ code: "reauth_required" });
    expect(prove).toHaveBeenCalledTimes(1);
    expect(put).toHaveBeenCalledTimes(2);
  });

  // THE CEREMONY'S OWN FAILURE IS THE ONE WORTH REPORTING. A dismissed sheet, or no credential for
  // this address, is a more useful message than the 401 that preceded it — so it is not swallowed
  // and replaced by the server's error.
  it("surfaces a cancelled prompt rather than the 401 behind it", async () => {
    vi.spyOn(api, "put").mockRejectedValue(reauthRequired());
    vi.spyOn(reauth, "proveWithPasskey").mockRejectedValue(new Error("no credential"));

    await expect(changePassword("", "new")).rejects.toThrow("no credential");
  });
});

// REGISTRATION IS REFUSED AT **BEGIN**, NOT AROUND THE WRITE — qn.6n slice 5b, and the asymmetry
// with `changePassword` above is the point rather than an inconsistency.
//
// A WebAuthn creation ceremony is consumed by `navigator.credentials.create()`. A client that
// learned at `finish` that a proof was required would have to run it again — a second Face ID sheet
// for a credential the user has already made. So the server refuses before a ceremony exists.
describe("adding a passkey", () => {
  // `navigator.credentials` does not exist in jsdom, and these tests never reach it: every case
  // below is decided by the FIRST `begin` call.

  // IT NO LONGER RE-AUTHENTICATES BY ITSELF — qn.6o slice 4, and this test asserted the opposite
  // until then. `registerPasskey` used to catch `reauth_required`, run `proveWithPasskey` and retry
  // `begin` with the proof, which is the chain quince#976 is filed on:
  //
  //     click → reauth/begin → credentials.get() → reauth/finish → begin(proof) → create()
  //                            ^ the user's gesture is SPENT on the proof's own sheet
  //
  // Completing an authenticator sheet grants no new activation, so `create()` arrived three awaits
  // and one sheet past the last real click. The caller now runs the challenge and comes back from a
  // FRESH CLICK (spec D1 as corrected on quince#988) — which a library cannot do for itself.
  //
  // KEPT AS AN ASSERTION RATHER THAN DELETED, inverted: the old behaviour is the bug, so what is
  // worth pinning is that it does not come back.
  it("does NOT re-authenticate by itself — the refusal reaches the caller", async () => {
    const post = vi.spyOn(api, "post").mockRejectedValueOnce(reauthRequired());
    const prove = vi.spyOn(reauth, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");

    await expect(webauthn.registerPasskey("phone")).rejects.toMatchObject({
      code: "reauth_required",
    });

    expect(prove).not.toHaveBeenCalled();
    expect(post).toHaveBeenCalledTimes(1);
  });

  // AND A PROOF THE CALLER ALREADY HAS TRAVELS ON THE FIRST `begin`, which is how the challenge
  // hands its result back — one await between the fresh click and `create()`, and no second one.
  it("sends a caller-supplied proof on the first begin", async () => {
    const post = vi
      .spyOn(api, "post")
      // Resolves with a body that fails later, at the browser call — fine: what is asserted here is
      // the single POST and its payload, not the ceremony.
      .mockResolvedValueOnce({ ceremony: "C", options: { publicKey: {} } });

    await expect(webauthn.registerPasskey("phone", { proof: "PROOF-TOKEN" })).rejects.toBeDefined();

    expect(post).toHaveBeenCalledTimes(1);
    expect(post).toHaveBeenNthCalledWith(1, "/api/auth/passkeys/register/begin", {
      proof: "PROOF-TOKEN",
    });
  });

  // FIRST RUN MUST NOT PROMPT. There is no session and no credential to present, so asking would be
  // asking the user to prove possession of something they have not created yet.
  it("never re-authenticates on the first-run pair", async () => {
    vi.spyOn(api, "post").mockRejectedValue(reauthRequired());
    const prove = vi.spyOn(reauth, "proveWithPasskey");

    await expect(webauthn.registerPasskey("phone", { firstRun: true })).rejects.toMatchObject({
      code: "reauth_required",
    });
    expect(prove).not.toHaveBeenCalled();
  });

  it("does not re-authenticate on any other refusal", async () => {
    vi.spyOn(api, "post").mockRejectedValue(
      new APIError(409, "passkeys_unsupported_here", "passkeys need a domain name"),
    );
    const prove = vi.spyOn(reauth, "proveWithPasskey");

    await expect(webauthn.registerPasskey("phone")).rejects.toMatchObject({
      code: "passkeys_unsupported_here",
    });
    expect(prove).not.toHaveBeenCalled();
  });
});

// THE FLOW THAT ACTUALLY SHIPS, AGAINST THE ANSWER THE SERVER ACTUALLY GIVES — quince#930 review,
// finding 3. Every test above mocks the first `begin` as `reauth_required`, which is what a
// PASSWORDLESS install answers. The install this flow has just created has a password and no
// credentials, so `RequirePresent` verifies an empty string against the hash and answers
// `bad_password` — and the retry, which fires only on `reauth_required`, rethrows it.
//
// The defect was invisible to CI because all three layers mocked past it: this file assumed the
// server's answer, `SetupPasswordPage.test.tsx` spies on `registerPasskey` wholesale, and the e2e
// runs over http where the passkey offer is absent. One test driving the real first response is
// worth more than three more mocks.
describe("adding the first passkey right after setting a password", () => {
  it("presents the password, so the server never answers bad_password", async () => {
    const post = vi
      .spyOn(api, "post")
      // Fails later, at the browser call — what is asserted here is the body of `begin`.
      .mockResolvedValueOnce({ ceremony: "C", options: { publicKey: {} } });
    const prove = vi.spyOn(reauth, "proveWithPasskey");

    await expect(webauthn.registerPasskey("phone", { currentPassword: "hunter2" })).rejects.toBeDefined();

    expect(post).toHaveBeenNthCalledWith(1, "/api/auth/passkeys/register/begin", {
      current_password: "hunter2",
    });
    // NO CEREMONY. The password satisfied rule 1, so nothing should have asked for a passkey the
    // user does not have yet — which is the state this whole flow exists to leave.
    expect(prove).not.toHaveBeenCalled();
  });

  // THE REGRESSION ITSELF. Without the password the server answers `bad_password`, and this asserts
  // the client does NOT paper over it with a ceremony — the fix is to send the password, not to
  // widen the retry.
  it("does not re-authenticate on bad_password", async () => {
    vi.spyOn(api, "post").mockRejectedValue(
      new APIError(401, "bad_password", "current password is incorrect"),
    );
    const prove = vi.spyOn(reauth, "proveWithPasskey");

    await expect(webauthn.registerPasskey("phone")).rejects.toMatchObject({
      code: "bad_password",
    });
    expect(prove).not.toHaveBeenCalled();
  });

  // AN EMPTY PASSWORD IS OMITTED, NOT SENT. On a passwordless install an empty string is a WRONG
  // password; an absent field is the case the server decides for itself, which is what lets the
  // passkey-first install still reach `reauth_required` and prompt.
  it("omits the field entirely when there is no password", async () => {
    const post = vi
      .spyOn(api, "post")
      .mockResolvedValueOnce({ ceremony: "C", options: { publicKey: {} } });

    await expect(webauthn.registerPasskey("phone", { currentPassword: "" })).rejects.toBeDefined();

    expect(post).toHaveBeenNthCalledWith(1, "/api/auth/passkeys/register/begin", {});
  });
});

// REMOVAL PROVES FIRST AND NEVER PROBES — qn.6n slice 6a. The third shape in this file, and the
// three are not inconsistent: they follow what the SERVER accepts.
//
//	change    either factor  → try the cheap one, retry on the server's 401
//	register  either factor  → retry at BEGIN, because a creation ceremony cannot be replayed
//	remove    a passkey ONLY → no probe at all; there is nothing cheaper to try

// quince#994 — IT ASKS THE SERVER BEFORE IT OPENS A SHEET.
//
// This block asserted the OPPOSITE until 2026-08-15: *"proves a passkey before asking"*, with a
// companion insisting the endpoint is never called without a proof, defended as *"a probe would be
// one guaranteed 409 on the way to the same prompt."*
//
// THE ARGUMENT WAS RIGHT ABOUT THE FACTOR AND WRONG ABOUT THE COST. The round trip does not buy a
// choice — rule 2 leaves only one — it buys knowing whether the ceremony can succeed at all. On an
// install whose passkeys are bound elsewhere the old path opened a sheet with nothing in it and
// produced the lockout refusal only after the user had dismissed it.
const REAUTH_NEEDS_PASSKEY = new UnauthorizedError(
  "reauth_required",
  "Confirm it is you before changing how you sign in.",
  { error: { code: "reauth_required", accepts: ["passkey"] } },
);

describe("removing the password", () => {
  it("presents nothing first, then proves with the factor the server named", async () => {
    const del = vi
      .spyOn(api, "del")
      .mockRejectedValueOnce(REAUTH_NEEDS_PASSKEY)
      .mockResolvedValueOnce(undefined);
    const prove = vi.spyOn(reauth, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");

    await removePassword();

    // THE BARE ATTEMPT IS FIRST, and it is what earns `accepts`.
    expect(del).toHaveBeenNthCalledWith(1, "/api/auth/password", {});
    // THE OPERATION IS NAMED. A proof minted for anything else is refused by `Proofs.Consume`, so
    // this argument is load-bearing rather than a label.
    expect(prove).toHaveBeenCalledWith("remove_password");
    expect(del).toHaveBeenNthCalledWith(2, "/api/auth/password", { proof: "PROOF-TOKEN" });
  });

  // THE DEAD END NEVER REACHES A SHEET, which is the whole point of the change. A refusal that does
  // not name the passkey means nothing on this install can authorise the removal, so opening an
  // authenticator prompt would ask a question with no possible answer.
  it("opens no sheet when the server does not name the passkey", async () => {
    vi.spyOn(api, "del").mockRejectedValue(
      new APIError(409, "last_credential", "this quince holds no passkey for example.com."),
    );
    const prove = vi.spyOn(reauth, "proveWithPasskey");

    await expect(removePassword()).rejects.toMatchObject({ code: "last_credential" });
    expect(prove).not.toHaveBeenCalled();
  });

  // AND THE SERVER'S SENTENCE SURVIVES, so the surface still says where the credentials it found
  // belong rather than "could not remove the password".
  it("rethrows the ceremony's own failure untouched", async () => {
    vi.spyOn(api, "del").mockRejectedValue(REAUTH_NEEDS_PASSKEY);
    vi.spyOn(reauth, "proveWithPasskey").mockRejectedValue(
      new APIError(409, "last_credential", "this quince holds no passkey for example.com."),
    );

    await expect(removePassword()).rejects.toMatchObject({ code: "last_credential" });
  });
});

// AN OLDER DAEMON ANSWERS `reauth_required` WITH NO `accepts`, and that must not open a sheet either.
//
// FOUND BY A PROBE THAT PASSED. Deleting the `accepts` guard broke nothing, because the dead-end
// test above mocks `last_credential` — a 409, rethrown before this branch is reached. So the guard
// had no coverage at all and the probe said so, which is the point of running one.
it("opens no sheet when the refusal names no factor", async () => {
  vi.spyOn(api, "del").mockRejectedValue(
    new UnauthorizedError("reauth_required", "Confirm it is you before changing how you sign in."),
  );
  const prove = vi.spyOn(reauth, "proveWithPasskey");

  await expect(removePassword()).rejects.toMatchObject({ code: "reauth_required" });
  expect(prove).not.toHaveBeenCalled();
});
