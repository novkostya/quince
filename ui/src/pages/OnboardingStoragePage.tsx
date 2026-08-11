import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { AddStorageForm } from "@/features/storage/AddStorageForm";
import { DocLink } from "@/components/DocLink";

// The first-run storage step (qn.6e PR 9b). quince is running with NO storage declared and refusing
// every API outside setup, and this is the screen that ends that state.
//
// A PAGE, NOT A DIALOG — Operator direction 2026-08-07, and it is not a preference:
//
//   - There is no Home to open a dialog from. The whole premise is a daemon serving with zero
//     storages, where Home renders nothing and a modal would be a modal over an empty page.
//   - A first-run step is a DESTINATION, not an interruption. It wants a URL, so a reload returns
//     here rather than dumping the user on a page that cannot render.
//   - The zfs branch is exactly the case a first-run user hits, and it is the one that overflows a
//     dialog on a phone. A page has no height constraint by construction.
//
// NAMED FOR ITS SUBJECT, not a position — `/onboarding/storage`, never `/onboarding/step3`. That is
// contracts §1's 2026-08-02 ruling, and quince#558's finding that §9 numbers no steps at all.
//
// BEHIND `RequireAuth`, unlike the HTTPS step. That step is pre-auth because you cannot log in
// without https — a genuine deadlock. Nothing about declaring a storage is a prerequisite of
// logging in, so this one takes the ordinary guard and the exempt set stays at five.
export function OnboardingStoragePage() {
  const navigate = useNavigate();

  return (
    <div className="mx-auto min-h-dvh max-w-xl px-6 pb-16 pt-10">
      <h1 className="text-xl font-semibold tracking-tight">Add your first storage</h1>
      <p className="mt-2 text-sm text-muted">
        quince needs somewhere to keep backups before it can do anything else. Point it at a folder
        it can reach from inside its container — a mounted disk, a NAS share, or a ZFS dataset.
      </p>
      <p className="mt-2 text-sm text-muted">
        Nothing is created or changed until you save. If the path is wrong, quince says so rather
        than making it — see <DocLink path="deploy/storage.md" />.
      </p>

      <div className="mt-8">
        <AddStorageForm
          // THE ONLY WAY OUT OF THIS SCREEN IS TO SUCCEED, so there is no cancel. Adding a storage
          // is what lifts the daemon's setup mode; a dismissal would return the user to a Home that
          // cannot render and an API that refuses.
          //
          // THE NAME IS DELIBERATELY IGNORED, unlike `AddStoragePage`, which navigates to it
          // (quince#846). This step's destination is Home and is not cosmetic: quince#683 was a
          // bounce straight back to this page, caused by ordering, and story 11's last test gates
          // the landing. First run ends on the page the product opens with, not on a details page
          // for the only storage there is.
          onSaved={() => navigate("/", { replace: true })}
          footer={({ save, canSave, saving, adopting }) => (
            <div className="mt-6">
              <Button onClick={save} disabled={!canSave || saving} data-testid="add-storage-save">
                {adopting ? "Use this storage" : "Add storage"}
              </Button>
            </div>
          )}
        />
      </div>
    </div>
  );
}
