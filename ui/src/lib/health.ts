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
    // `staleTime: Infinity` STOOD HERE, on the reason *"the mode cannot change without a restart"*.
    // That was true of every field this payload carried until qn.6i added `reconciling`, which
    // changes WHILE THE PROCESS RUNS — so a cached-forever query would raise the notice once at mount
    // and never take it down, or never raise it at all.
    //
    // A SECOND QUERY AGAINST THE SAME ENDPOINT was the alternative and is worse: two cache entries for
    // one document, and the next mutable field would have to pick a side.
    //
    // THE POLL RATE FOLLOWS THE ANSWER — 2 s while reconciling, so the notice clears promptly when the
    // pass ends; 30 s otherwise, so a scheduled pass beginning under an idle browser is still noticed
    // without polling hard for a state that is usually false. `undefined` (a server older than this
    // UI) takes the slow rate, because such a server never reports the state at all.
    staleTime: 1000,
    refetchInterval: (q) => (q.state.data?.reconciling ? 2000 : 30000),
    retry: false,
  });
}

// useReconciling is the question a caller actually has: is quince re-reading its storages right now,
// so a version list or a count may be SHORT (contracts §1)?
//
// FALSE WHILE LOADING AND FALSE ON A FAILED PROBE, matching useIsPublicDemo: the notice appears only
// on a POSITIVE answer from the server. That direction is deliberate — a health endpoint that cannot
// be reached must not make quince claim its own data is incomplete, which would layer a second wrong
// statement on a first.
export function useReconciling(): boolean {
  const { data } = useHealth();
  return data?.reconciling === true;
}

// useInsecureOrigin is first-run routing's question: would a session cookie earned on THIS
// connection be discarded, so that no credential can be established over it at all (contracts §1)?
//
// FALSE WHILE LOADING AND FALSE ON A FAILED PROBE, like the two hooks around it — and here the
// direction has teeth rather than being a house style. A false POSITIVE routes somebody away from
// a setup form they were about to complete successfully; a false NEGATIVE leaves them in the dead
// end they are in today, unchanged. One of those is a regression and the other is the status quo,
// so the answer that does nothing is the safe one.
export function useInsecureOrigin(): boolean {
  const { data } = useHealth();
  return data?.insecure_origin === true;
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
