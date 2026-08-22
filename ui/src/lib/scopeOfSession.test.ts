import { describe, it, expect } from "vitest";

import { scopeOfSession, isScopedSession } from "./auth";
import type { AuthStatus } from "./types";

// qn.13 slice 8d / D8 — what the client is allowed to conclude about itself (ruled on quince#1443).
//
// THE CLAIM IS THAT TWO QUESTIONS STAY JOINED. `scope` carries a claim only when
// `state === "authenticated"`; letting them come apart produces a shell that confines a visitor who
// is not signed in, or — worse — reads a stale scope off a payload that no longer describes anybody.
//
// SYNTHETIC UDIDS. A real one is Operator-private and never enters a fixture.

const UDID = "udid-fixture-0001";

function status(over: Partial<AuthStatus>): AuthStatus {
  return { state: "authenticated", csrf_token: "t", ...over };
}

describe("what an authenticated session is told about itself", () => {
  it("reports the device a scoped session is confined to", () => {
    const s = status({ scope: { udid: UDID } });
    expect(scopeOfSession(s)).toBe(UDID);
    expect(isScopedSession(s)).toBe(true);
  });

  it("reads an explicit null as ADMIN", () => {
    const s = status({ scope: null });
    expect(scopeOfSession(s)).toBe("");
    expect(isScopedSession(s)).toBe(false);
  });

  it("reads an ABSENT key as admin too — an older daemon omits it", () => {
    // The upgrade direction, and the safe one: the server refuses every admin route to a scoped
    // holder regardless, so over-showing costs a refusal the user can see.
    const s = status({});
    expect(scopeOfSession(s)).toBe("");
    expect(isScopedSession(s)).toBe(false);
  });
});

// `state` IS THE DISAMBIGUATOR — the half that is easy to lose, because the happy path never
// exercises it. A `scope` on a non-authenticated payload describes nobody.
describe("a state that cannot carry a principal", () => {
  it("ignores a scope on needs_login", () => {
    const s = status({ state: "needs_login", scope: { udid: UDID } });
    expect(scopeOfSession(s)).toBe("");
    expect(isScopedSession(s)).toBe(false);
  });

  it("ignores a scope on needs_setup", () => {
    const s = status({ state: "needs_setup", scope: { udid: UDID } });
    expect(scopeOfSession(s)).toBe("");
  });

  it("answers admin for a payload that has not arrived yet", () => {
    // `useAuthStatus` is undefined while loading, and a shell that confined the user during that
    // window would flash a household member's view at the admin on every reload.
    expect(scopeOfSession(undefined)).toBe("");
    expect(isScopedSession(undefined)).toBe(false);
  });
});

// A SCOPE OBJECT NAMING NO DEVICE IS NOT A SCOPE. It should be unreachable — the server builds it
// from a udid — and rendering it as scoped would confine a session to a device that does not exist.
describe("a malformed scope", () => {
  it("treats an empty udid as admin rather than as a device", () => {
    expect(scopeOfSession(status({ scope: { udid: "" } }))).toBe("");
  });
});
