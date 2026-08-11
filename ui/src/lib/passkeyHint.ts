// "Has a passkey ever worked in THIS browser?" — the gate on firing a sign-in sheet unprompted.
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

const KEY = "quince.passkey.seen";

/** rememberPasskey records that a passkey has been created or used in this browser. */
export function rememberPasskey(): void {
  try {
    localStorage.setItem(KEY, "1");
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
    return localStorage.getItem(KEY) === "1";
  } catch {
    return false;
  }
}
