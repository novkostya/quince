import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { forgetStorage } from "@/lib/config";
import { configKey } from "@/lib/config";
import { APIError } from "@/lib/api";
import type { ConfigFieldError, Storage } from "@/lib/types";

// firstError pulls the server's own sentence out of a 422. The refusal names the storage AND the
// remedy — "make another storage the default first" — and re-wording it client-side would drop
// the half that tells the user what to do. On anything else the API message is already the
// server's; only a truly opaque failure gets generic copy.
function firstError(err: unknown): string {
  if (err instanceof APIError) {
    const details = err.details as { errors?: ConfigFieldError[] } | undefined;
    const errs = details?.errors;
    if (errs !== undefined && errs.length > 0) return errs[0].message;
    return err.message;
  }
  return "could not forget this storage";
}

// ForgetStorage is qn.6d stories 8 and 9 — detach-and-forget, at the bottom of the details page,
// destructive styling.
//
// THE CONFIRM SENTENCE IS THE FEATURE, not decoration. Ruled on quince#443: the peer-entity frame
// is that you do not delete a device, you unplug it and its backups stay. Without the sentence
// spelled out a user assumes the button wiped their backups, and the fear is the thing that stops
// them tidying a stale disk out of their config.
//
// It does NOT promise live-apply. The restart is real and stays until quince#577's rung lands, so
// the copy says so rather than implying the storage is gone from the running process.
export function ForgetStorage({ storage }: { storage: Storage }) {
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [done, setDone] = useState(false);
  const queryClient = useQueryClient();
  const navigate = useNavigate();

  async function onConfirm() {
    setBusy(true);
    setError(null);
    try {
      await forgetStorage(storage.name);
      // The config is what changed, so the config query is what is stale. The STORAGE list is
      // deliberately NOT invalidated: it still lists this storage, correctly — the process is
      // still serving it — and refetching would only re-render the same runtime truth.
      await queryClient.invalidateQueries({ queryKey: configKey });
      setDone(true);
    } catch (err) {
      setError(firstError(err));
    } finally {
      setBusy(false);
    }
  }

  if (done) {
    return (
      <div data-testid="storage-forgotten" className="text-sm">
        <div className="font-medium">{storage.name} is no longer declared.</div>
        {/* THE RESTART IS SURFACED, never silent — gap B ruled it, and `no silent caps or
            fallbacks` requires it. Without this line the card stays on Home with no explanation
            for why a storage the user just forgot is still there. */}
        <div className="mt-1 text-muted">
          quince is still serving this disk until it restarts. Nothing on the disk was deleted — the
          backups that were there are still there.
        </div>
        <Button variant="outline" size="sm" className="mt-3" onClick={() => void navigate("/")}>
          Back to Home
        </Button>
      </div>
    );
  }

  return (
    <div>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogTrigger asChild>
          <Button variant="destructive" size="sm" data-testid="storage-forget">
            Forget this storage
          </Button>
        </DialogTrigger>
        <DialogContent>
          <DialogTitle>Forget {storage.name}?</DialogTitle>
          <DialogDescription>
            Forget removes it from quince. The backups on the disk are not deleted.
          </DialogDescription>
          <div className="mt-3 text-sm text-muted">
            quince will keep serving this disk until it restarts.
          </div>
          {error !== null ? (
            <div
              role="alert"
              data-testid="storage-forget-error"
              className="mt-3 flex gap-2 text-sm text-danger"
            >
              <AlertTriangle size={16} className="mt-0.5 shrink-0" />
              <span>{error}</span>
            </div>
          ) : null}
          {/* Controls only in the action row; prose sits above it (quince#325). */}
          <div className="mt-5 flex justify-end gap-2">
            <Button variant="outline" size="sm" onClick={() => setOpen(false)} disabled={busy}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              size="sm"
              data-testid="storage-forget-confirm"
              disabled={busy}
              onClick={() => void onConfirm()}
            >
              {busy ? "Forgetting…" : "Forget"}
            </Button>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
