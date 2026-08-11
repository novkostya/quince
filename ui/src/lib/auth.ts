import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
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
export function changePassword(current: string, next: string): Promise<void> {
  return api.put<void>("/api/auth/password", { current_password: current, new_password: next });
}

// removePassword makes this install passwordless. The server REFUSES with 409 `last_credential`
// unless a passkey exists for THIS address — deliberately not re-checked here, because a second
// implementation of an rpId rule is a thing that drifts, and the server's refusal already names the
// addresses the credentials it found belong to.
export function removePassword(): Promise<void> {
  return api.del<void>("/api/auth/password");
}
