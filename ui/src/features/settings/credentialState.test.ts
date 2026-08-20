import { describe, expect, it } from "vitest";

import {
  boundElsewhere,
  canRemoveHere,
  credentialState,
  type CredentialState,
} from "@/features/settings/credentialState";
import type { PasskeyList } from "@/features/settings/Passkeys";

// THE DERIVATION, TESTED WITHOUT A COMPONENT — quince#1316. It lives in its own module so the page
// and `PasswordControls` cannot disagree about which state an install is in, and the point of that
// move is that the rule becomes checkable against a plain payload: no query client, no DOM, no
// rendering. What the components then owe is that they ORDER by this answer, not that they compute
// it again.

function list(over: Partial<PasskeyList> = {}): PasskeyList {
  return {
    passkeys: [],
    rp_id: "quince.example",
    supported: true,
    has_password: true,
    ...over,
  };
}

function passkey(rpID: string) {
  return { id: rpID, name: "phone", rp_id: rpID, created_at: "", last_used_at: null };
}

describe("credentialState", () => {
  // The four rows of quince#1316's matrix, each named by what the user can actually sign in with.
  const cases: Array<[string, PasskeyList | undefined, boolean, CredentialState]> = [
    ["a password exists", list(), true, "has-password"],
    [
      "a password exists and passkeys do too",
      list({ passkeys: [passkey("quince.example")] }),
      true,
      "has-password",
    ],
    ["no password, no passkeys", list({ has_password: false }), false, "unconfigured"],
    [
      "no password, a passkey bound HERE",
      list({ has_password: false, passkeys: [passkey("quince.example")] }),
      false,
      "passwordless",
    ],
    [
      "no password, every passkey bound elsewhere",
      list({ has_password: false, passkeys: [passkey("other.example")] }),
      false,
      "elsewhere-only",
    ],
  ];

  for (const [name, data, hasPassword, want] of cases) {
    it(`${name} → ${want}`, () => {
      expect(credentialState(data, hasPassword)).toBe(want);
    });
  }

  // AN UNKNOWN rpId IS NOT AN ACCUSATION — the module's own rule, and the one a reorder could
  // quietly break: reporting `elsewhere-only` here would tell a user with a working passkey that
  // nothing can sign them in.
  it("reports plain passwordless when the server named no rpId", () => {
    expect(
      credentialState(list({ has_password: false, rp_id: "", passkeys: [passkey("x.example")] }), false),
    ).toBe("passwordless");
  });

  // The list has not arrived yet. `has-password` is the caller's fallback, and this asserts the
  // undefined payload does not throw on the way there.
  it("survives an absent payload", () => {
    expect(credentialState(undefined, true)).toBe("has-password");
    expect(credentialState(undefined, false)).toBe("unconfigured");
  });
});

describe("canRemoveHere", () => {
  // A DIFFERENT QUESTION FROM `credentialState`, which returns `has-password` the moment a password
  // exists and never looks at the passkeys. This is what decides whether the remove offer renders.
  it("is false with no passkeys at all", () => {
    expect(canRemoveHere(list())).toBe(false);
  });

  it("is false when every passkey is bound to another address", () => {
    expect(canRemoveHere(list({ passkeys: [passkey("other.example")] }))).toBe(false);
  });

  it("is true when a passkey is bound here", () => {
    expect(
      canRemoveHere(list({ passkeys: [passkey("other.example"), passkey("quince.example")] })),
    ).toBe(true);
  });

  // NO rpID IS NOT A REASON TO HIDE — the server has not said what it would bind to, so this cannot
  // prove the offer would fail, and the server's own refusal is a better answer than a missing
  // control.
  it("is true when the server named no rpId but passkeys exist", () => {
    expect(canRemoveHere(list({ rp_id: "", passkeys: [passkey("x.example")] }))).toBe(true);
  });

  it("survives an absent payload", () => {
    expect(canRemoveHere(undefined)).toBe(false);
  });
});

describe("boundElsewhere", () => {
  it("names each address once", () => {
    expect(
      boundElsewhere(list({ passkeys: [passkey("a.example"), passkey("a.example"), passkey("b.example")] })),
    ).toEqual(["a.example", "b.example"]);
  });

  it("drops an empty rp_id rather than rendering an address of nothing", () => {
    expect(boundElsewhere(list({ passkeys: [passkey(""), passkey("a.example")] }))).toEqual([
      "a.example",
    ]);
  });

  it("survives an absent payload", () => {
    expect(boundElsewhere(undefined)).toEqual([]);
  });
});
