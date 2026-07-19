import * as React from "react";
import { RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { api, APIError } from "@/lib/api";

type Status = "idle" | "busy" | "done" | "external";

// RescanButton triggers POST /api/devices/rescan (qn.2b): restart the managed in-container muxer
// so a device the unprivileged container's absent hotplug missed re-enumerates. On 202 it shows
// a transient in-progress note (the re-detected device arrives over the WS); on 409 it explains
// that the muxer is external and disables itself — never a dead button. `post` is injectable for
// tests; it defaults to the real API client.
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
      setStatus("done");
      window.setTimeout(() => setStatus("idle"), 2500); // clear the transient note
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
        {status === "busy" ? "Rescanning…" : "Rescan"}
      </Button>
      {status === "done" ? (
        <span className="text-xs text-muted">Rescanning for devices…</span>
      ) : null}
      {status === "external" ? (
        <span className="text-xs text-muted">{reason}</span>
      ) : null}
    </div>
  );
}
