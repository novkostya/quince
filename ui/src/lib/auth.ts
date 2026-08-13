import { useQuery } from "@tanstack/react-query";
import { api, APIError } from "./api";
import { proveWithPasskey } from "./webauthn";
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

// removePassword makes this install passwordless. The server REFUSES with 409 `last_credential`
// unless a passkey exists for THIS address — deliberately not re-checked here, because a second
// implementation of an rpId rule is a thing that drifts, and the server's refusal already names the
// addresses the credentials it found belong to.
export function removePassword(): Promise<void> {
  return api.del<void>("/api/auth/password");
}
