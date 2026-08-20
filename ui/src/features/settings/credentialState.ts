import { rpIDOf, worksHere, type PasskeyList } from "@/features/settings/Passkeys";

// WHAT THIS INSTALL CAN SIGN IN WITH — the derivation, lifted out of `PasswordControls` so the PAGE
// can read it too (quince#1316).
//
// IT MOVED BECAUSE THE ORDER FOLLOWS THE STATE. `/settings/auth` leads with the credential the user
// actually signs in with, which means the page orders two siblings — `Passkeys` and the password
// sections — by a fact only `PasswordControls` used to hold. A component cannot order its own
// parent, so either the page re-derives this (two implementations of one rule, drifting apart the
// first time a state is added) or the derivation sits where both can reach it. This is the second.
//
// A MODULE OF ITS OWN RATHER THAN A THIRD EXPORT FROM `Passkeys`. That file already carries the
// query and its `rp_id` predicates — the seam its own comment calls "ONE QUERY, TWO CONSUMERS" — and
// these functions are consumers of that seam rather than part of it. Keeping them separate is what
// lets this be unit-tested against a plain payload with no component, no query client and no DOM.
//
// NO NEW FETCH. Every caller passes the payload from `usePasskeyList`, so the page becomes the third
// consumer of one query rather than a second request for the same bytes.


// `has_password: false` MEANS THREE DIFFERENT THINGS AND THIS SURFACE RENDERED ONE — quince#888
// item 2. It said *"This quince has no password — you sign in with a passkey"* for all of them,
// which is a confident description of a configuration the user may not have:
//
//	passwordless        a passkey works at THIS address. The sentence is true.
//	elsewhere-only      passkeys exist, none bound here. NOTHING can sign in at this address.
//	unconfigured        no credentials at all.
//
// ELSEWHERE-ONLY IS THE REACHABLE ONE, and it survives quince#888 item 1. That guard refuses to
// remove the last credential that works HERE, so it cannot be emptied — but an install can arrive in
// this state by being reached at a second address, which is qn.6k D2's whole hazard. The user is then
// told they sign in with a passkey while standing at an address where no passkey of theirs works.
//
// UNCONFIGURED is now hard to reach through the UI and is still rendered honestly, because *"assume
// the safe one"* is what produced this bug: the surface picked the reassuring reading of an ambiguous
// field rather than the one it could actually establish. `quince auth reset` clears sessions too, so
// nobody sees this screen after one — but a hand-edited DB, or a future path nobody has thought of,
// should not be met with an inviting sentence about a passkey that does not exist.
//
// AN UNKNOWN rpId IS NOT AN ACCUSATION. If the payload carries no `rp_id`, this cannot judge which
// credentials work here, so it reports plain `passwordless` rather than claiming the user is locked
// out. A wrong lockout warning would send someone to the console for nothing.
export type CredentialState = "has-password" | "passwordless" | "elsewhere-only" | "unconfigured";

export function credentialState(data: PasskeyList | undefined, hasPassword: boolean): CredentialState {
  if (hasPassword) return "has-password";
  const rows = Array.isArray(data?.passkeys) ? data.passkeys : [];
  if (rows.length === 0) return "unconfigured";
  const rpID = rpIDOf(data);
  if (!rpID) return "passwordless";
  return rows.some((p) => worksHere(rpID, p.rp_id)) ? "passwordless" : "elsewhere-only";
}


// CAN A PASSKEY CONFIRM A REMOVAL *HERE* — the condition row 4 renders on (quince#1077, Operator
// 2026-08-19: the remove offer appears "only when ≥1 passkey").
//
// READ AS "≥1 PASSKEY THAT WORKS AT THIS ADDRESS", NOT "≥1 PASSKEY", and that is a reading rather
// than a quotation. Rule 2 excludes the password from authorising its own removal, so the only
// factor left is a passkey — and `auth.Accepts` resolves it through `allowedForRemoval`, which is
// bound to the rpId the request arrived on. A credential registered elsewhere cannot assert here,
// so a bare count would put the offer back in front of exactly the user it cannot serve: the one
// whose passkeys are all bound to another address.
//
// That is this issue's own defect — a control offered where nothing can satisfy it — so the
// stricter test is the one that honours the ruling's purpose. Hiding is also the conservative
// direction here, which is what the ruling chose for this row over saying the precondition.
//
// `credentialState` cannot answer it: it returns `has-password` the moment a password exists and
// never looks at the passkeys, which is correct for the four-state matrix and blind for this
// question.
export function canRemoveHere(data: PasskeyList | undefined): boolean {
  const rows = Array.isArray(data?.passkeys) ? data.passkeys : [];
  if (rows.length === 0) return false;
  const rpID = rpIDOf(data);
  // NO rpID IS NOT A REASON TO HIDE. The server has not told us the address it would bind to — an
  // IP-only install is the case — so this cannot prove the offer would fail. Showing it leaves the
  // server's own refusal as the answer, which is a worse screen than hiding a control that works.
  if (!rpID) return true;
  return rows.some((p) => worksHere(rpID, p.rp_id));
}

// The rpIds the credentials DO belong to, so the warning can name them. Same reasoning as the
// server's `last_credential` message: "your passkeys do not work here" at a box that visibly lists
// some reads as quince being broken, where naming the address it wants is an instruction.
export function boundElsewhere(data: PasskeyList | undefined): string[] {
  const rows = Array.isArray(data?.passkeys) ? data.passkeys : [];
  return [...new Set(rows.map((p) => p.rp_id).filter(Boolean))];
}
