import * as React from "react";
import { api, APIError } from "@/lib/api";
import type { Op } from "@/lib/types";
import { useOpsStore } from "@/stores/ops";

export type StartFn = (path: string, body?: unknown) => Promise<{ op_id: string }>;

const defaultPost: StartFn = (path, body) => api.post<{ op_id: string }>(path, body);

// useDeviceOp starts an async device op (pair/encryption) and tracks its live state: it POSTs
// to get an op_id, then reads the Op from the ops store (fed by op.updated WS events). `post`
// is injectable for tests. The returned op is undefined until the first op.updated arrives.
export function useDeviceOp(post: StartFn = defaultPost) {
  const [opId, setOpId] = React.useState<string | null>(null);
  const [starting, setStarting] = React.useState(false);
  const [startError, setStartError] = React.useState<string | null>(null);
  const op: Op | undefined = useOpsStore((s) => (opId ? s.byId[opId] : undefined));

  const start = React.useCallback(
    async (path: string, body?: unknown) => {
      setStarting(true);
      setStartError(null);
      setOpId(null);
      try {
        const { op_id } = await post(path, body);
        setOpId(op_id);
      } catch (e) {
        setStartError(
          e instanceof APIError ? e.message : "Something went wrong. Please try again.",
        );
      } finally {
        setStarting(false);
      }
    },
    [post],
  );

  const reset = React.useCallback(() => {
    setOpId(null);
    setStarting(false);
    setStartError(null);
  }, []);

  const inFlight = starting || op?.state === "running" || op?.state === "waiting_for_user";
  return { op, opId, starting, startError, start, reset, inFlight };
}
