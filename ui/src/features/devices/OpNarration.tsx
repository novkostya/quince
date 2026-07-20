import { Loader2, Check, AlertCircle, Smartphone } from "lucide-react";
import type { Op } from "@/lib/types";

// OpNarration renders the live state of a pair/encryption op honestly (state honesty): a
// spinner while running, a phone icon while waiting for the on-device confirm, a check on
// success, and the plain-language error on failure. `startError` covers a POST that never
// produced an op (e.g. 409 needs-USB).
export function OpNarration({
  op,
  starting,
  startError,
}: {
  op?: Op;
  starting: boolean;
  startError: string | null;
}) {
  if (startError) {
    return (
      <p className="flex items-center gap-2 text-sm text-danger">
        <AlertCircle size={16} /> {startError}
      </p>
    );
  }
  if (!op && !starting) return null;

  const state = op?.state;
  const message = op?.message || "Working…";

  if (starting || state === "running") {
    return (
      <p className="flex items-center gap-2 text-sm text-muted">
        <Loader2 size={16} className="animate-spin" /> {message}
      </p>
    );
  }
  if (state === "waiting_for_user") {
    return (
      <p className="flex items-center gap-2 text-sm text-warn">
        <Smartphone size={16} /> {message}
      </p>
    );
  }
  if (state === "succeeded") {
    return (
      <p className="flex items-center gap-2 text-sm text-ok">
        <Check size={16} /> {message}
      </p>
    );
  }
  if (state === "failed") {
    return (
      <p className="flex items-center gap-2 text-sm text-danger">
        <AlertCircle size={16} /> {op?.error?.message || message}
      </p>
    );
  }
  return null;
}
