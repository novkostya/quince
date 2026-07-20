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

  const start = React.useCallback(
    async (transport: RequestTransport, retryOf?: string): Promise<boolean> => {
      setBusy(true);
      setError(null);
      try {
        const body: Record<string, unknown> = { udid, transport };
        if (retryOf) body.retry_of = retryOf;
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
