import * as React from "react";
import { api, APIError } from "@/lib/api";
import type { Job } from "@/lib/types";

// RequestTransport is the POST /api/jobs transport value — "auto" is request-only and the engine
// resolves it against current presence (design §4/(bp)); a Job only ever stores concrete usb|wifi.
export type RequestTransport = "auto" | "usb" | "wifi";

// useBackup wires the assisted "Back up now" / retry / cancel actions to the frozen jobs command
// surface (POST /api/jobs, POST /api/jobs/{id}/cancel). The started/cancelled job arrives via the WS
// job.updated stream into the jobs store — this hook fires the command and surfaces an honest error
// (device offline → 422, already running → 409, no engine → 503); it never fabricates job state.
export function useBackup(udid: string) {
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState<string | null>(null);

  // start takes its optional arguments as a NAMED OBJECT, and that is the fix rather than the
  // style (quince#452 regression, found on the staging stand 2026-08-02).
  //
  // It was `start(transport, storageID?, retryOf?)`. quince#452 inserted `storageID` in the MIDDLE
  // of a two-argument signature, and the two existing retry call sites still read
  // `start("auto", job.id)` — so **both Retry buttons sent a JOB id as the storage id** and the
  // daemon answered `no storage with that id is declared` for a storage that was declared and
  // reachable on the same screen.
  //
  // TYPESCRIPT COULD NOT SEE IT: both parameters are optional `string`, so a positional insert
  // between them typechecks perfectly at every call site. A named object makes the same mistake a
  // compile error, which is why this is worth a signature change rather than fixing two lines.
  const start = React.useCallback(
    async (
      transport: RequestTransport,
      opts: { storageID?: string; retryOf?: string } = {},
    ): Promise<boolean> => {
      setBusy(true);
      setError(null);
      try {
        const body: Record<string, unknown> = { udid, transport };
        // Omitted means "the default storage", which the server resolves — so an unchosen
        // selector behaves exactly as this call did before the rung (contracts §1).
        if (opts.storageID) body.storage_id = opts.storageID;
        if (opts.retryOf) body.retry_of = opts.retryOf;
        await api.post<Job>("/api/jobs", body);
        return true;
      } catch (e) {
        setError(e instanceof APIError ? e.message : "could not start the backup");
        return false;
      } finally {
        setBusy(false);
      }
    },
    [udid],
  );

  const cancel = React.useCallback(async (jobId: string): Promise<boolean> => {
    setError(null);
    try {
      await api.post<Job>(`/api/jobs/${jobId}/cancel`);
      return true;
    } catch (e) {
      setError(e instanceof APIError ? e.message : "could not cancel the backup");
      return false;
    }
  }, []);

  return { start, cancel, busy, error };
}
