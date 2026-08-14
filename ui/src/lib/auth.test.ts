import { describe, it, expect, vi, beforeEach } from "vitest";

import { api, APIError } from "./api";
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

// REGISTRATION RETRIES AT **BEGIN**, NOT AROUND THE WRITE — qn.6n slice 5b, and the asymmetry with
// `changePassword` above is the point rather than an inconsistency.
//
// A WebAuthn creation ceremony is consumed by `navigator.credentials.create()`. A client that
// learned at `finish` that a proof was required would have to run it again — a second Face ID sheet
// for a credential the user has already made. So the server refuses before a ceremony exists and the
// client retries there.
describe("adding a passkey", () => {
  // `navigator.credentials` does not exist in jsdom, and these tests never reach it: every case
  // below is decided by the FIRST `begin` call.
  it("re-authenticates at begin, then starts the ceremony with the proof", async () => {
    const post = vi
      .spyOn(api, "post")
      .mockRejectedValueOnce(reauthRequired())
      // The second `begin` resolves with a body that fails later, at the browser call — which is
      // fine: what is asserted here is the two POSTs and the proof, not the ceremony.
      .mockResolvedValueOnce({ ceremony: "C", options: { publicKey: {} } });
    const prove = vi.spyOn(reauth, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");

    await expect(webauthn.registerPasskey("phone")).rejects.toBeDefined();

    expect(prove).toHaveBeenCalledWith("add_passkey");
    expect(post).toHaveBeenNthCalledWith(1, "/api/auth/passkeys/register/begin", {});
    expect(post).toHaveBeenNthCalledWith(2, "/api/auth/passkeys/register/begin", {
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
describe("removing the password", () => {
  it("proves a passkey before asking, and sends the proof", async () => {
    const del = vi.spyOn(api, "del").mockResolvedValue(undefined);
    const prove = vi.spyOn(reauth, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");

    await removePassword();

    // THE OPERATION IS NAMED. A proof minted for anything else is refused by `Proofs.Consume`, so
    // this argument is load-bearing rather than a label.
    expect(prove).toHaveBeenCalledWith("remove_password");
    expect(del).toHaveBeenCalledTimes(1);
    expect(del).toHaveBeenCalledWith("/api/auth/password", { proof: "PROOF-TOKEN" });
  });

  // NO BARE ATTEMPT FIRST. A probe would be one guaranteed 409 on the way to the same prompt, and
  // — worse — a user watching the network would see the client ask for something it knows is
  // refused. Asserted as "del is not called before prove", which is the failure a refactor makes.
  it("never calls the endpoint without a proof", async () => {
    const del = vi.spyOn(api, "del").mockResolvedValue(undefined);
    vi.spyOn(reauth, "proveWithPasskey").mockRejectedValue(new Error("no credential"));

    await expect(removePassword()).rejects.toThrow("no credential");
    expect(del).not.toHaveBeenCalled();
  });

  // THE REFUSAL FROM `reauth/begin` IS WHAT THE USER SEES when this address holds no passkey — the
  // lockout sentence, moved ahead of the sheet. It is rethrown untouched so `messageFor` can render
  // the server's own words rather than "could not remove the password".
  it("surfaces the server's lockout sentence from the ceremony", async () => {
    const del = vi.spyOn(api, "del");
    vi.spyOn(reauth, "proveWithPasskey").mockRejectedValue(
      new APIError(409, "last_credential", "this quince holds no passkey for example.com."),
    );

    await expect(removePassword()).rejects.toMatchObject({ code: "last_credential" });
    expect(del).not.toHaveBeenCalled();
  });
});
