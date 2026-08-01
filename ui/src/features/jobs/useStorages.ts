import * as React from "react";
import { api } from "@/lib/api";
import type { Storage } from "@/lib/types";

// StoragesState distinguishes THREE outcomes, and the third is why this is not just a list.
//
// A failed fetch used to return an empty array, which rendered identically to "there is only one
// storage" — the selector vanished, no error, and the next backup went to the default without
// anything saying so (quince#452 review). Two different states that render the same are the
// no-silent-fallbacks rule, and canon names this family directly: "cache-dropped, truncated list".
export type StoragesState =
  | { status: "loading" }
  | { status: "loaded"; storages: Storage[] }
  | { status: "failed" };

// useStorages fetches the declared storages for ONE device (contracts §1 GET /api/storages?udid=).
//
// Device-scoped on purpose: `will_be_full` is a fact about a (device, storage) PAIR, so the list is
// fetched per device rather than shared. The device-independent form exists and is not what a
// backup selector wants — the point of the selector is telling this user, about this phone, what
// the next backup to each disk will cost.
//
// A failure does NOT take the backup button down with it: the caller renders the button either way.
// It does surface, because "we could not load your storages, so this goes to the default" is
// something the user can act on and silence is not.
export function useStorages(udid: string): StoragesState {
  const [state, setState] = React.useState<StoragesState>({ status: "loading" });

  React.useEffect(() => {
    let live = true;
    setState({ status: "loading" });
    void (async () => {
      try {
        const r = await api.get<{ storages: Storage[] }>(
          `/api/storages?udid=${encodeURIComponent(udid)}`,
        );
        if (live) setState({ status: "loaded", storages: r.storages ?? [] });
      } catch {
        if (live) setState({ status: "failed" });
      }
    })();
    return () => {
      live = false;
    };
  }, [udid]);

  return state;
}
