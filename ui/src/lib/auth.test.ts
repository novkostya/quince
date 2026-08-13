import { describe, it, expect, vi, beforeEach } from "vitest";

import { api, APIError } from "./api";
import { changePassword } from "./auth";
import * as webauthn from "./webauthn";

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
    const prove = vi.spyOn(webauthn, "proveWithPasskey");

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
    const prove = vi.spyOn(webauthn, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");

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
    const prove = vi.spyOn(webauthn, "proveWithPasskey");

    await expect(changePassword("wrong", "new")).rejects.toMatchObject({ code: "bad_password" });
    expect(prove).not.toHaveBeenCalled();
    expect(put).toHaveBeenCalledTimes(1);
  });

  // ONE RETRY, NEVER A LOOP. A second refusal is a real one, and retrying would turn a stated error
  // into a silent hang behind repeated Face ID sheets.
  it("gives up after one retry rather than looping", async () => {
    const put = vi.spyOn(api, "put").mockRejectedValue(reauthRequired());
    const prove = vi.spyOn(webauthn, "proveWithPasskey").mockResolvedValue("PROOF-TOKEN");

    await expect(changePassword("", "new")).rejects.toMatchObject({ code: "reauth_required" });
    expect(prove).toHaveBeenCalledTimes(1);
    expect(put).toHaveBeenCalledTimes(2);
  });

  // THE CEREMONY'S OWN FAILURE IS THE ONE WORTH REPORTING. A dismissed sheet, or no credential for
  // this address, is a more useful message than the 401 that preceded it — so it is not swallowed
  // and replaced by the server's error.
  it("surfaces a cancelled prompt rather than the 401 behind it", async () => {
    vi.spyOn(api, "put").mockRejectedValue(reauthRequired());
    vi.spyOn(webauthn, "proveWithPasskey").mockRejectedValue(new Error("no credential"));

    await expect(changePassword("", "new")).rejects.toThrow("no credential");
  });
});
