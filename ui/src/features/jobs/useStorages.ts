import * as React from "react";
import { api } from "@/lib/api";
import type { Storage } from "@/lib/types";

// useStorages fetches the declared storages for ONE device (contracts §1 GET /api/storages?udid=).
//
// Device-scoped on purpose: `will_be_full` is a fact about a (device, storage) PAIR, so the list is
// fetched per device rather than shared. The device-independent form exists and is not what a
// backup selector wants — the whole point of the selector is telling this user, about this phone,
// what the next backup to each disk will cost.
//
// A failed fetch yields an EMPTY list rather than a thrown error: the selector then renders nothing
// and "Back up now" keeps working against the default, which is what it did before this rung. A
// storage list that cannot load must not take the backup button down with it.
export function useStorages(udid: string) {
  const [storages, setStorages] = React.useState<Storage[]>([]);

  React.useEffect(() => {
    let live = true;
    void (async () => {
      try {
        const r = await api.get<{ storages: Storage[] }>(
          `/api/storages?udid=${encodeURIComponent(udid)}`,
        );
        if (live) setStorages(r.storages ?? []);
      } catch {
        if (live) setStorages([]);
      }
    })();
    return () => {
      live = false;
    };
  }, [udid]);

  return storages;
}
