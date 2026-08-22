import { useQuery } from "@tanstack/react-query";
import { api, APIError } from "./api";
import { acceptsOf, proveWithPasskey } from "./reauth";
import type { AuthStatus } from "./types";

export const authStatusKey = ["auth", "status"] as const;

// useAuthStatus drives the route guards. It never retries (a 401 is a definitive answer)
// and treats an error as "needs_login" so the app always resolves to a usable screen.
export function useAuthStatus() {
  return useQuery({
    queryKey: authStatusKey,
    queryFn: () => api.get<AuthStatus>("/api/auth/status"),
  });
}


/**
 * scopeOfSession returns the device this session is confined to, or "" for an admin.
 *
 * THE TWO QUESTIONS ARE JOINED HERE ON PURPOSE (qn.13 slice 8d, ruled on quince#1443). `scope`
 * carries a claim only when `state === "authenticated"`; on `needs_login` or `needs_setup` there is
 * no principal to describe, so any value it held would be meaningless. Reading the field directly
 * lets those come apart, and the shape of that mistake is a shell that confines a visitor who is not
 * signed in.
 *
 * "" MEANS ADMIN, and it also means *we cannot tell* — a payload from an older daemon has no `scope`
 * key at all. Both land on the admin reading, which is the direction that fails safe here: the
 * server refuses every admin route to a scoped holder regardless, so over-showing costs a refusal
 * the user can see, where under-showing would hide a household member's own device from them.
 */
export function scopeOfSession(s: AuthStatus | undefined): string {
  if (!s || s.state !== "authenticated") return "";
  return typeof s.scope?.udid === "string" ? s.scope.udid : "";
}

/** isScopedSession reports whether this session is confined to one device. */
export function isScopedSession(s: AuthStatus | undefined): boolean {
  return scopeOfSession(s) !== "";
}

export function login(password: string): Promise<AuthStatus> {
  return api.post<AuthStatus>("/api/auth/login", { password });
}

export function setup(password: string): Promise<AuthStatus> {
  return api.post<AuthStatus>("/api/auth/setup", { password });
}

export function logout(): Promise<void> {
  return api.post<void>("/api/auth/logout");
}

// The password is MUTABLE since qn.6m slice 5b (contracts §1, quince#851).
//
// `current` IS OMITTED EXACTLY WHEN NO PASSWORD EXISTS — on a passwordless install "change" IS
// "set", and the server decides which case applies from its own state. Sent as an empty string
// rather than an absent key: the handler treats both identically, and one shape is fewer than two.
// AND SINCE qn.6n IT RETRIES WITH A PROOF, ONLY WHEN THE SERVER ASKS — rules 1 and 3.
//
// THE TRIGGER IS THE 401, NOT A GUESS ABOUT THE INSTALL'S STATE, and that is what makes this safe to
// ship BEFORE the server demands anything. Against today's `main` the retry branch is unreachable —
// nothing answers `reauth_required` — so this is inert; against the server after slice 4a it is
// exactly right. A client deciding for itself when to prompt would be a second copy of the rule, and
// would prompt on installs that do not need one.
//
// That asymmetry is the whole of the slice ordering: a UI that can satisfy a demand nobody makes is
// harmless, where a server making a demand no client can satisfy is a broken flow on `main`.
//
// ONE RETRY, NEVER A LOOP. A second refusal is a real one — a wrong current password, an expired
// proof, a proof for another operation — and retrying would turn a stated error into a silent hang
// behind repeated Face ID sheets.
export async function changePassword(current: string, next: string): Promise<void> {
  const body = { current_password: current, new_password: next };
  try {
    return await api.put<void>("/api/auth/password", body);
  } catch (err) {
    if (!(err instanceof APIError) || err.code !== "reauth_required") throw err;
    // The ceremony's own failure — a dismissed sheet, no credential for this address — is
    // deliberately NOT caught. "You cancelled the prompt" is more useful than the 401 that
    // preceded it, and swallowing it would report the wrong cause.
    const proof = await proveWithPasskey("set_password");
    return await api.put<void>("/api/auth/password", { ...body, proof });
  }
}

// removePassword makes this install passwordless — and since qn.6n rule 2 only a PASSKEY can
// authorise it: the credential being removed cannot vouch for what would be left behind.
//
// IT ASKS THE SERVER BEFORE IT OPENS A SHEET — quince#994. It used to call `proveWithPasskey`
// unconditionally, and the comment defended that: *"removing the password accepts ONLY a passkey …
// there is nothing to try first, so a probe would be one guaranteed 409 on the way to the same
// prompt."*
//
// THAT ARGUMENT WAS RIGHT ABOUT THE FACTOR AND WRONG ABOUT THE COST. The round trip does not buy a
// CHOICE — there is only ever one — it buys knowing whether the ceremony can succeed at all. On an
// install whose passkeys are bound elsewhere, the old path opened an authenticator sheet with
// nothing in it and produced the `last_credential` refusal only after the user had dismissed it. A
// prompt that cannot be answered is `qn.6g`'s defect, and it was being shown to the one user who
// could not act on it.
//
// SO THE FIRST CALL PRESENTS NOTHING, which is what earns `reauth_required` carrying `accepts`
// (qn.6o slice 2). If the server names the passkey, the ceremony runs. If it does not — a dead end —
// the refusal propagates with its own sentence naming where the credentials it found belong, and no
// sheet is ever opened.
//
// NO CHALLENGE DIALOG, EVER, ON THIS PATH — qn.6o D5 as amended, and the reason quince#994 was worth
// taking at all: `accepts` here is `["passkey"]` for every install that can do this, so a chooser
// would have one choice. The ceremony runs from the press the user already made.
//
// THE CEREMONY'S OWN FAILURE IS DELIBERATELY NOT CAUGHT. *"You cancelled the prompt"* is more useful
// than the 401 that preceded it, and `messageFor` renders whatever the server said.
export async function removePassword(): Promise<void> {
  const path = "/api/auth/password";
  try {
    return await api.del<void>(path, {});
  } catch (err) {
    if (!(err instanceof APIError) || err.code !== "reauth_required") throw err;
    // NOT `onlyPasskey`, and the difference matters: this asks whether the passkey is OFFERED, not
    // whether it is the sole offer. Rule 2 makes it the sole one today; a build where that changed
    // should still run the ceremony rather than fall through to a rethrow with no explanation.
    const accepts = acceptsOf(err);
    if (!accepts?.includes("passkey")) throw err;
    const proof = await proveWithPasskey("remove_password");
    return await api.del<void>(path, { proof });
  }
}

// removePasskey deletes one credential, presenting a DIFFERENT one — qn.6n rule 2, slice 6b.
//
// TWO FACTORS QUALIFY HERE, UNLIKE `removePassword`, and that is what makes this the one removal
// with a choice to make. A passkey may be removed by the password or by another passkey; only the
// credential being removed is excluded. `removePassword` has no such choice — nothing but a passkey
// can prove it.
//
// THAT USED TO BE THE REASON THE TWO LOOKED DIFFERENT, AND IT NO LONGER IS. This sentence ended
// *"which is why that one prompts unconditionally and this one does not"* until quince#994. Both now
// ask the server first; what still differs is only WHO runs the ceremony afterwards — there, the
// library, because the answer can only ever be a passkey; here, the caller, because it can be
// either and the client must not choose.
//
// IT PRESENTS WHAT THE CALLER GIVES IT AND RUNS NO CEREMONY OF ITS OWN — qn.6o slice 5.
//
// IT USED TO FIRE A PASSKEY SHEET WHENEVER NO PASSWORD WAS PASSED: press Remove, meet a modal
// authenticator prompt, and only on ITS refusal learn that the password was the way. Two costs, and
// the second is the one that made this a slice rather than a tidy-up.
//
// A SHEET BEFORE THE SERVER HAS SAID ANYTHING is a guess about which factor applies, made on the
// client — the exact thing the comment above correctly refuses to do with the rpId rule, arrived at
// from the other direction. The server now answers `reauth_required` with `accepts`, computed for
// THIS target at THIS address with rule 2's exclusions already applied, so the caller renders the
// challenge from that and the guess disappears.
//
// AND IT COST THE USER A PROMPT THEY COULD NOT ANSWER: on an install whose only credential at this
// address is the one being removed, `accepts` omits `passkey`, and the old path opened a sheet with
// nothing in it before falling back.
//
// NO FRESH-CLICK PROBLEM HERE, UNLIKE `registerPasskey` (D1, quince#988). That one ends in
// `navigator.credentials.create()`, which needs transient activation the proof's own sheet has
// already spent. A removal ends in a `DELETE` — an ordinary request, needing no activation at all —
// so the challenge may hand its proof straight through, and this asymmetry is why the two paths
// look different rather than one of them being stale.
export async function removePasskey(
  id: string,
  present: { current_password: string } | { proof: string } | undefined = undefined,
): Promise<void> {
  const path = `/api/auth/passkeys/${encodeURIComponent(id)}`;
  // Presenting NOTHING is deliberate and is the first call: it is what earns the `reauth_required`
  // that names the acceptable factors. Since quince#978 the server answers that rather than
  // "current password is incorrect".
  return api.del<void>(path, present ?? {});
}
