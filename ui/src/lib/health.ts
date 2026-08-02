import { useQuery } from "@tanstack/react-query";
import { api } from "./api";
import type { Health } from "./types";

export const healthKey = ["health"] as const;

// UNKNOWN_HEALTH is what a failed probe resolves to. `normal` is the SHIPPING mode, so a health
// endpoint that cannot be reached can never be the reason a real deployment starts advertising a
// demo password. The failure direction is deliberate: silence, never a false claim.
const UNKNOWN_HEALTH: Health = { status: "unknown", version: "", mode: "normal" };

// useHealth reads GET /api/health, which is authExempt — so it works on the login screen, which is
// the whole reason the serving mode lives there rather than on the frozen /api/auth/status
// (public-demo spec story 5).
//
// The queryFn SWALLOWS the error rather than letting the query enter an error state. That is not
// laziness: the only consumer is demo copy, "could not reach health" and "this is not a demo" want
// identical UI, and resolving here means the safe default is in the code rather than in a comment
// about what `undefined` happens to mean at each call site.
export function useHealth() {
  return useQuery({
    queryKey: healthKey,
    queryFn: async (): Promise<Health> => {
      try {
        return await api.get<Health>("/api/health");
      } catch {
        return UNKNOWN_HEALTH;
      }
    },
    staleTime: Infinity, // the mode cannot change without a restart
    retry: false,
  });
}

// useIsPublicDemo is the question every caller actually has. It is false while loading and false on
// a failed probe, so demo copy appears only on a POSITIVE answer from the server.
export function useIsPublicDemo(): boolean {
  const { data } = useHealth();
  return data?.mode === "public_demo";
}

// useDemoResetMinutes is story 6's half, gated on the mode for the same reason the server gates it
// (main.go `reportableResetMinutes`): an interval is only true where something actually performs the
// reset. Two gates for one fact is deliberate — the failure they prevent is a destructive promise
// rendered on the SHIPPING product's login screen, and neither side is expensive.
//
// `undefined` means "no schedule to state", and it covers three servers at once: one that was told
// nothing, one older than this UI, and one that could not be reached. The caller must still say the
// demo resets — only the schedule is conditional.
export function useDemoResetMinutes(): number | undefined {
  const { data } = useHealth();
  if (data?.mode !== "public_demo") return undefined;
  return data.demo_reset_minutes;
}
