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

// RecheckState is per-storage, not per-hook: the user plugged in ONE disk and pressed ONE button,
// so a second row's pending spinner or error would be a lie about what they did. `failed` carries
// no reason because a re-check that could not be performed has nothing to report ABOUT THE DISK —
// the storage's own `unreachable_reason` is still the answer to "why is it not there".
export type RecheckState = "idle" | "pending" | "failed";

export interface Storages {
  state: StoragesState;
  // recheck asks the server to look at ONE storage again, then RELOADS THE DEVICE-SCOPED LIST.
  //
  // IT DELIBERATELY DOES NOT SPLICE THE 200 {storage} RESPONSE INTO THE LIST, and this is the
  // whole subtlety of the endpoint. `POST /api/storages/{id}/recheck` is device-INDEPENDENT —
  // `RecheckStorage(id)` takes no udid — so its `will_be_full` is always null. Splicing it would
  // silently drop "First backup to X — this transfers everything" at exactly the moment the disk
  // came back and that warning became true, which is story 8's claim disappearing on success.
  // Re-fetching `?udid=` costs one request and keeps the pair fact the server owns.
  recheck: (id: string) => void;
  rechecking: Record<string, RecheckState>;

  // reload refetches the device-scoped list. The caller drives it, because the event that
  // invalidates this list is a JOB COMPLETING and this hook does not watch jobs.
  //
  // WITHOUT IT THE UI MAKES A FALSE STATEMENT, which is worse than the missing surfaces around it.
  // `will_be_full` is a fact about a (device, storage) pair at a moment: true before the first
  // backup to a storage, false forever after. The hook fetched on `udid` alone, so after a backup
  // COMPLETED the page still read "First backup to shuttle — this transfers everything" about a
  // cost that had just been paid. Reported from the staging stand during G9, minutes after the
  // transfer it was describing finished (Operator, 2026-08-02).
  reload: () => void;
}

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
//
// REACHABILITY CHANGES WITHOUT A RESTART and this hook used to have no way to notice (quince#459).
// It refetched on `udid` alone, so a disk plugged in while Back up now was open stayed listed as
// not connected until the page was remounted — the ruling behind the endpoint is "plug the disk in
// and press the button", and there was no button.
export function useStorages(udid: string): Storages {
  const [state, setState] = React.useState<StoragesState>({ status: "loading" });
  const [rechecking, setRechecking] = React.useState<Record<string, RecheckState>>({});

  // `live` guards every setState against a udid change or an unmount mid-flight. It is a ref rather
  // than an effect-local because `recheck` is called from an event handler, outside any effect.
  const live = React.useRef(true);
  React.useEffect(() => {
    live.current = true;
    return () => {
      live.current = false;
    };
  }, []);

  const load = React.useCallback(async (forUdid: string) => {
    try {
      const r = await api.get<{ storages: Storage[] }>(
        `/api/storages?udid=${encodeURIComponent(forUdid)}`,
      );
      if (live.current) setState({ status: "loaded", storages: r.storages ?? [] });
    } catch {
      if (live.current) setState({ status: "failed" });
    }
  }, []);

  React.useEffect(() => {
    setState({ status: "loading" });
    setRechecking({});
    void load(udid);
  }, [udid, load]);

  const recheck = React.useCallback(
    (id: string) => {
      setRechecking((m) => ({ ...m, [id]: "pending" }));
      void (async () => {
        try {
          await api.post(`/api/storages/${encodeURIComponent(id)}/recheck`);
        } catch {
          // A 404 (the storage is no longer declared) and a dropped connection land here alike.
          // Both mean "the press did nothing", which is what the row says — and neither is a
          // reason to blank a list that is still the last thing the server told us.
          if (live.current) setRechecking((m) => ({ ...m, [id]: "failed" }));
          return;
        }
        await load(udid);
        if (live.current) setRechecking((m) => ({ ...m, [id]: "idle" }));
      })();
    },
    [udid, load],
  );

  const reload = React.useCallback(() => void load(udid), [load, udid]);

  return { state, recheck, rechecking, reload };
}
