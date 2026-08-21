import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";

import {
  forgetPasskey,
  hasPasskeyHint,
  passkeyHintCredentialID,
  rememberPasskey,
} from "./passkeyHint";

// qn.13 slice 7 / G8 — the hint SELECTS, it never grants (spec D2.2).
//
// THE BROWSER HALF. What the server does with a hint is `passkey_hint_test.go`'s; this is about what
// gets remembered, what an OLD browser's memory means, and the fact that neither is an authorisation.

const KEY = "quince.passkey.seen";

beforeEach(() => localStorage.clear());
afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

describe("what the browser remembers", () => {
  it("stores the credential id it is given", () => {
    rememberPasskey("cred-abc");
    expect(passkeyHintCredentialID()).toBe("cred-abc");
    expect(hasPasskeyHint()).toBe(true);
  });

  it("still records a hint when the caller does not know which credential", () => {
    // qn.6k's shape, and one call site genuinely still has it: a memory with no id is a valid
    // "a passkey worked here" and must not become "no passkey ever worked here".
    rememberPasskey();
    expect(hasPasskeyHint()).toBe(true);
    expect(passkeyHintCredentialID()).toBe("");
  });

  it("treats an empty string as no id rather than storing one", () => {
    rememberPasskey("");
    expect(hasPasskeyHint()).toBe(true);
    expect(passkeyHintCredentialID()).toBe("");
  });

  it("forgets both halves at once", () => {
    rememberPasskey("cred-abc");
    forgetPasskey();
    expect(hasPasskeyHint()).toBe(false);
    expect(passkeyHintCredentialID()).toBe("");
  });
});

// THE UPGRADE PATH, AND IT IS THE CASE MOST LIKELY TO BE MISSED. Every browser that used a passkey
// before this rung holds the literal `"1"`. It is a valid HINT — the sheet must still fire — and it
// is NOT a credential id, so it must select nothing.
describe("a browser holding qn.6k's boolean", () => {
  it("still fires the sheet", () => {
    localStorage.setItem(KEY, "1");
    expect(hasPasskeyHint()).toBe(true);
  });

  it("offers no credential, which is the discoverable flow", () => {
    localStorage.setItem(KEY, "1");
    expect(passkeyHintCredentialID()).toBe("");
  });
});

// STORAGE CAN THROW, and on a login page that must never be an error the visitor sees. Private mode,
// a disabled store and quota all arrive here, and the answer to all three is "remember nothing",
// which degrades to the button.
describe("when localStorage is unavailable", () => {
  it("reads as no hint rather than throwing", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(hasPasskeyHint()).toBe(false);
    expect(passkeyHintCredentialID()).toBe("");
  });

  it("swallows a failed write", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(() => rememberPasskey("cred-abc")).not.toThrow();
  });

  it("swallows a failed clear", () => {
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(() => forgetPasskey()).not.toThrow();
  });
});
