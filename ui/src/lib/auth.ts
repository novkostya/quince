import { useQuery } from "@tanstack/react-query";
import { api, APIError } from "./api";
import { proveWithPasskey } from "./reauth";
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

// removePassword makes this install passwordless — and since qn.6n rule 2 it PROVES A PASSKEY
// FIRST, unconditionally.
//
// NO PROBE-THEN-RETRY HERE, UNLIKE changePassword, and the asymmetry is the rule rather than a
// stylistic choice. Changing a password accepts either factor, so trying the cheap one first is
// worth a round trip; removing the password accepts ONLY a passkey, because the credential being
// removed cannot vouch for what would be left behind. There is nothing to try first, so a probe
// would be one guaranteed 409 on the way to the same prompt.
//
// THE CEREMONY'S OWN FAILURE IS DELIBERATELY NOT CAUGHT. `reauth/begin` answers 409
// `last_credential` when this address holds no passkey — the same refusal this endpoint used to give
// after the fact, carrying the same sentence naming where the credentials it DID find belong.
// `messageFor` renders it, so the surface still says what to do rather than "could not remove the
// password", and the rpId rule stays implemented once, on the server.
export async function removePassword(): Promise<void> {
  const proof = await proveWithPasskey("remove_password");
  return api.del<void>("/api/auth/password", { proof });
}
