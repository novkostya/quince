import { useState } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { AddStorageForm } from "./AddStorageForm";

// AddStorage is the DIALOG container for `AddStorageForm` — the Home-page route into adding a
// storage, from the foot of the Storage section.
//
// THE FORM LIVES IN ITS OWN FILE because the first-run page uses the same one (qn.6e PR 9b). This
// component is now chrome and nothing else: a trigger, a modal, and a footer with Cancel beside
// Save. Everything that decides what a path IS, which backends are offered and whether a zfs helper
// answered belongs to the form, so the two containers cannot drift apart.
//
// `key` REMOUNTS THE FORM ON EVERY OPEN, which is how a dialog gets a clean sheet without the form
// exposing a reset. Re-opening after a cancel must not show the previous path's probe result — a
// stale recommendation attached to a field the user is about to retype is worse than an empty form.
export function AddStorage({ onAdded }: { onAdded: () => void }) {
  const [open, setOpen] = useState(false);

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        {/* Ghost at a section foot, matching Rescan's move and WifiSyncControl's quiet arm. `-ml-3`
            cancels the size's px-3 inset so the text starts at the column margin — a ghost button
            has no background, so without it the label reads as a stray indent. */}
        <Button variant="ghost" size="sm" className="-ml-3" data-testid="add-storage">
          <Plus size={14} />
          Add storage
        </Button>
      </DialogTrigger>
      <DialogContent>
        <DialogTitle>Add a storage</DialogTitle>
        <DialogDescription>
          Type the path as quince sees it inside its container. Nothing is created or changed until
          you save.
        </DialogDescription>

        <AddStorageForm
          key={String(open)}
          onSaved={() => {
            setOpen(false);
            onAdded();
          }}
          footer={({ save, canSave, saving, adopting }) => (
            <div className="mt-5 flex justify-end gap-2">
              <Button variant="ghost" size="sm" onClick={() => setOpen(false)}>
                Cancel
              </Button>
              <Button size="sm" onClick={save} disabled={!canSave || saving} data-testid="add-storage-save">
                {adopting ? "Add this storage" : "Add storage"}
              </Button>
            </div>
          )}
        />
      </DialogContent>
    </Dialog>
  );
}
