import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { makeStorageDefault, configKey } from "@/lib/config";
import { APIError } from "@/lib/api";
import type { ConfigFieldError, Storage } from "@/lib/types";

// firstError renders the server's own sentence, exactly as ForgetStorage does and for the same
// reason: a refusal from this route names the storage, and re-wording it client-side would drop
// whatever the server knew that this component does not.
function firstError(err: unknown): string {
  if (err instanceof APIError) {
    const details = err.details as { errors?: ConfigFieldError[] } | undefined;
    const errs = details?.errors;
    if (errs !== undefined && errs.length > 0) return errs[0].message;
    return err.message;
  }
  return "could not make this the default storage";
}

// MakeDefaultStorage is the control the Forget refusal has been naming since `qn.6d` (quince#722).
//
// THE DEFECT IT CLOSES IS A SENTENCE, NOT A MISSING FEATURE. Forgetting the default is refused with
// *"Make another storage the default first, then forget this one"*, and adding a storage is refused
// for claiming `default` — two messages pointing at an edit no screen could make. `qn.6g` already
// ruled what that costs: a remedy that was never going to work is the same defect as a silent
// failure. Both messages now name this control, and this is the control they name.
//
// NO CONFIRM DIALOG, and the asymmetry with Forget beside it on this page is deliberate. Forget is
// destructive-shaped — a user reasonably fears it deletes their backups, which is why its dialog
// exists to say otherwise. This changes where the NEXT unbound backup is written and nothing else:
// no data moves, no version is touched, and the way to undo it is to press the same button on the
// other storage. A confirm step on a reversible, non-destructive change teaches people to click
// through confirms.
//
// IT DOES NOT RENDER WHEN THIS STORAGE ALREADY IS THE DEFAULT. The endpoint answers 200 to that
// request on purpose — asking for a state you are in has been satisfied — but a button that does
// visibly nothing is a worse surface than no button, and the badge two rows up already says
// `Default`.
export function MakeDefaultStorage({
  storage,
  onDone,
}: {
  storage: Storage;
  onDone: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const queryClient = useQueryClient();

  async function onClick() {
    setBusy(true);
    setError(null);
    try {
      await makeStorageDefault(storage.name);
      // The config is what changed, so the config query is what is stale.
      //
      // THE STORAGE LIST IS ALSO STALE AND THERE IS NO QUERY TO INVALIDATE. `GET /api/storages`
      // derives `default` from the rebuilt slot list, so this page's own badge is now wrong — but
      // `useStorages` is a `useState` + `useEffect` hook with no shared cache, so
      // `invalidateQueries` has no key that reaches it. ForgetStorage carries the same note.
      //
      // Unlike the forget, this component does NOT navigate away afterwards, so it cannot rely on
      // a remount to refetch. That is what `onDone` is for: the page owns the refresh, because the
      // page owns the hook.
      await queryClient.invalidateQueries({ queryKey: configKey });
      onDone();
    } catch (err) {
      setError(firstError(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div>
      <Button
        variant="outline"
        size="sm"
        data-testid="storage-make-default"
        disabled={busy}
        onClick={() => void onClick()}
      >
        {busy ? "Making default…" : "Make default"}
      </Button>
      {error !== null ? (
        <div
          role="alert"
          data-testid="storage-make-default-error"
          className="mt-3 flex gap-2 text-sm text-danger"
        >
          <AlertTriangle size={16} className="mt-0.5 shrink-0" />
          <span>{error}</span>
        </div>
      ) : null}
    </div>
  );
}
