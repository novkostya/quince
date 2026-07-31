import * as React from "react";
import { RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { api, APIError } from "@/lib/api";

type Status = "idle" | "busy" | "external";

// RescanButton triggers POST /api/devices/rescan (qn.2b): restart the managed in-container muxer
// so a device the unprivileged container's absent hotplug missed re-enumerates. On 409 it explains
// that the muxer is external and disables itself — never a dead button. `post` is injectable for
// tests; it defaults to the real API client.
//
// IN PROGRESS IS ONE STATE AND THE LABEL NEVER CHANGES. The endpoint returns 202 as soon as the
// restart is ACCEPTED, so a busy state that ends when the request resolves lasts milliseconds: the
// button flashed "Rescanning…", then snapped back to an ENABLED "Rescan" beside a separate
// "Rescanning for devices…" note — an inviting button while the rescan was still running, and a
// second element appearing next to it that reflowed the header (quince#325, Operator screenshots).
//
// The spinning icon is therefore the only progress signal. The label stays "Rescan" so the button
// keeps its width, and it stays disabled throughout: swapping the label IS the layout jump, because
// "Rescanning…" is wider than "Rescan".
export function RescanButton({
  post = api.post,
}: {
  post?: (path: string) => Promise<unknown>;
}) {
  const [status, setStatus] = React.useState<Status>("idle");
  const [reason, setReason] = React.useState("");

  async function onClick() {
    setStatus("busy");
    try {
      await post("/api/devices/rescan");
      // 202 means ACCEPTED, not finished — the muxer restart and the re-enumeration happen after
      // this resolves, and a re-detected device arrives over the WS. There is no completion signal
      // to wait for: a rescan that finds nothing emits nothing. So this is a SETTLE WINDOW, not a
      // measurement, and it is the honest shape available — the button must not invite a second
      // click while the first is still working.
      window.setTimeout(() => setStatus("idle"), 2500);
    } catch (e) {
      if (e instanceof APIError && e.status === 409) {
        setReason(e.message);
        setStatus("external");
        return;
      }
      setStatus("idle"); // transient failure: let the user try again
    }
  }

  return (
    <div className="flex items-center gap-2">
      <Button
        variant="outline"
        size="sm"
        onClick={onClick}
        disabled={status === "busy" || status === "external"}
        title={
          status === "external"
            ? reason
            : "Restart the managed USB muxer to re-detect a plugged-in device"
        }
      >
        <RefreshCw size={14} className={status === "busy" ? "animate-spin" : undefined} />
        Rescan
      </Button>
      {status === "external" ? (
        <span className="text-xs text-muted">{reason}</span>
      ) : null}
    </div>
  );
}
