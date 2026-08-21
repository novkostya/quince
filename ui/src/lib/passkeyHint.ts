// "Which passkey worked in THIS browser?" — the gate on firing a sign-in sheet unprompted, and
// since qn.13 also the credential that sheet should offer.
//
// THE PROBLEM IT SOLVES. Credential presence is undetectable by design: no API will say whether this
// device holds a passkey, because that would be a fingerprinting vector. So an on-load modal is a
// guess, and it guesses wrong for everyone who has none — a fresh install, a box after
// `quince auth reset`, the public demo, or simply a laptop the admin has never set one up on.
//
// WHY MEMORY BEATS A DEVICE HEURISTIC. The obvious alternative is "phones only" —
// `matchMedia("(pointer: coarse)")` — on the reasoning that typing a password on a phone is
// expensive and on a desktop it is not. That reasoning is sound and the proxy is not: an iPad with a
// keyboard is coarse, a touchscreen laptop is coarse, and neither matches the intent. This records
// what actually happened instead of inferring from hardware.
//
// WHY NOT A SERVER FLAG. quince can say whether ANY credential is registered, which would rule out
// the empty cases — but not "registered on my phone, now on a laptop that has none", which is the
// case that remains. The browser is the right granularity because the credential lives there.
//
// It is a HINT and nothing hangs on it: wrong-positive costs one dismissible sheet, wrong-negative
// costs one button press. Nothing here is a credential, a secret, or an authorisation.
//
// qn.13 D2.2 — IT NOW REMEMBERS *WHICH*, AND THAT IS STILL NOT AN AUTHORISATION. The stored value
// went from the boolean `"1"` to a credential id, so quince can send `allowCredentials` and the
// platform stops choosing for the user. What it selects is what is OFFERED; authority resolves from
// the assertion on the server, so a browser that lies about what it remembers narrows itself and
// gains nothing.
//
// A CREDENTIAL ID, NEVER A NAME. The id is already public in `allowCredentials` and names exactly
// one credential; a household member's device name is personal data and would be a second copy of
// it sitting in `localStorage` for no gain.

const KEY = "quince.passkey.seen";

// qn.6k's value, which the boolean version wrote and some browsers still hold. It is a valid HINT
// — a passkey did work here — and it is NOT a credential id, so it selects nothing and the sheet
// falls back to the discoverable flow. Named rather than compared inline: this is a compatibility
// value with a reason, not a magic string.
const LEGACY_SEEN = "1";

/**
 * rememberPasskey records that a passkey has been created or used in this browser, and — when the
 * caller knows it — WHICH one.
 *
 * The argument is optional because one caller genuinely does not know: registration remembers that
 * a ceremony succeeded here before the credential id is in hand at every call site. An id-less
 * memory is qn.6k's behaviour exactly, which is the correct degradation rather than a gap.
 */
export function rememberPasskey(credentialID?: string): void {
  try {
    localStorage.setItem(KEY, credentialID && credentialID !== "" ? credentialID : LEGACY_SEEN);
  } catch {
    // Private mode, disabled storage, quota — all mean "do not remember", which degrades to the
    // button. Never a thrown error on a login page for a convenience feature.
  }
}

/** forgetPasskey clears the hint, so the sheet stops firing where there is nothing left to offer. */
export function forgetPasskey(): void {
  try {
    localStorage.removeItem(KEY);
  } catch {
    // As above.
  }
}

/** hasPasskeyHint reports whether a passkey has been created or used in this browser before. */
export function hasPasskeyHint(): boolean {
  try {
    const v = localStorage.getItem(KEY);
    return v !== null && v !== "";
  } catch {
    return false;
  }
}

/**
 * passkeyHintCredentialID returns the credential id to offer, or "" when this browser remembers
 * that a passkey worked but not which one.
 *
 * "" IS A COMPLETE ANSWER, not a failure: it is what the server reads as "no hint", which is the
 * discoverable flow. Both the legacy boolean and a browser with storage disabled arrive here, and
 * both want the same thing.
 */
export function passkeyHintCredentialID(): string {
  try {
    const v = localStorage.getItem(KEY);
    if (v === null || v === "" || v === LEGACY_SEEN) return "";
    return v;
  } catch {
    return "";
  }
}
