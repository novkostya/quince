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
// IT NO LONGER PROMISES A RESTART. This comment read *"the restart is real and stays until
// quince#577's rung lands, so the copy says so"* — that rung is `qn.6g` and it has landed, so the
// storage stops being served at the moment of the write and there is no restart to mention.
//
// THE REFUSAL SET GREW BY ONE, and it arrives through the same path: `DELETE` now also answers
// `422` while a backup is running on that storage (Operator ruling 2026-08-06, quince#577). No code
// here changes for it — `firstError` already renders the server's sentence, which names the job and
// both remedies — and that is the point of having refused to reword refusals client-side.
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
      // The config is what changed, so the config query is what is stale.
      //
      // THE REASON FOR NOT INVALIDATING THE STORAGE LIST HAS CHANGED, AND SO HAS THE ANSWER TO
      // "how". This read *"deliberately NOT invalidated: it still lists this storage, correctly —
      // the process is still serving it"*. False as of `qn.6g`: the storage is gone from
      // `GET /api/storages` the moment this call returns.
      //
      // But there is NO STORAGE QUERY TO INVALIDATE. `useStorages` (features/jobs/useStorages.ts)
      // is a `useState` + `useEffect` hook with no shared cache, not a react-query one, so
      // `invalidateQueries` has no key to reach it by. The spec's *"the storage list is
      // invalidated"* describes a mechanism this codebase does not have.
      //
      // It does not need one HERE: this component is on the storage's own details page, and the
      // only way out of the `done` state is navigating away, which remounts whatever list you land
      // on and refetches. Stated rather than left as a silent omission — if a storage list is ever
      // rendered beside this component, it will need a real refresh and this comment is the note
      // saying so.
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
        {/* THE RESTART SENTENCE IS GONE, and what stays is the half that was never about the
            restart. It read *"quince is still serving this disk until it restarts"*, which existed
            because the card DID linger on Home with no explanation. As of `qn.6g` it does not
            linger, so the sentence explains a state that no longer occurs — and a remedy offered
            for a problem that is already solved is the same defect as a silent failure, pointing
            the other way.

            WHAT SURVIVES IS THE RULED HALF (quince#443): you do not delete a disk, you detach it,
            and the backups stay. That is what stops a user leaving a stale storage in their config
            out of fear, and it is true whatever the process is doing. */}
        <div className="mt-1 text-muted">
          Nothing on the disk was deleted — the backups that were there are still there.
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
          {/* The confirm's own restart line is gone for the same reason as the one above. Nothing
              replaces it: a dialog that has to explain what will NOT happen is a dialog describing
              a defect, and after `qn.6g` there is no longer one to describe. The sentence a user
              needs before pressing is the one above it — the backups are not deleted. */}
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
