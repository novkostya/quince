import { Link, useLocation, useNavigate } from "react-router-dom";
import { ArrowLeft } from "lucide-react";
import { Button } from "@/components/ui/button";
import { AddStorageForm } from "@/features/storage/AddStorageForm";

// AddStoragePage is the Home route into adding a storage — a PAGE, not a dialog (quince#846).
//
// IT REPLACED A DIALOG BECAUSE THE FLOW IS ABOUT TO WRITE TO DISK. quince#818 generates or
// discovers an SSH keypair under `/data/keys/` partway through this form, and the dialog it
// replaces had no dismissal guard: nothing in `dialog.tsx` or the old `AddStorage.tsx` set
// `onPointerDownOutside` or `onEscapeKeyDown`, so Radix's defaults applied and an outside tap
// dismissed it silently. Losing five typed fields is annoying; leaving a keypair on disk that no
// storage references is a different class, and quince#818's own discovery half would then have to
// reason about keys left behind by abandoned attempts.
//
// THE FORM ITSELF IS UNCHANGED, and that is the point of it living in its own file since qn.6e PR
// 9b: the first-run page, this page and the dialog before it all render one component, so what a
// path IS, which backends are offered and whether the zfs helper answered cannot drift between
// containers. This file is chrome — a back link, a heading, and a footer with one action.
//
// `max-w-xl` MATCHES THE FIRST-RUN PAGE. The shell puts no width limit on its pages, so without it
// these fields stretch the width of a desktop; the dialog got the same constraint from
// `DialogContent` and it should not be lost in the move.
export function AddStoragePage() {
  const navigate = useNavigate();
  const location = useLocation();

  return (
    <section>
      {/* BACK IS THE CANCEL — the issue's own words, and what a phone user reaches for anyway. It
          is drawn as a link rather than as a second button beside Save because that is how
          `StorageDetailsPage` and `DeviceDetailsPage` already offer the same escape, and a desktop
          user with no back gesture needs it to be visible. */}
      <Link to="/" className="inline-flex items-center gap-1 text-sm text-muted hover:text-fg">
        <ArrowLeft size={16} /> Home
      </Link>

      <h1 className="mt-4 text-xl font-semibold tracking-tight">Add a storage</h1>
      <p className="mt-1 text-sm text-muted">
        Type the path as quince sees it inside its container. Nothing is created or changed until
        you save.
      </p>

      <div className="mt-6 max-w-xl">
        <AddStorageForm
          // A CLEAN SHEET ON EVERY VISIT, which the dialog got from remounting on open and a page
          // does NOT get for free. Keyed on the navigation entry rather than on the path: Home →
          // add → back → add is two visits to one URL, and a stale probe result still attached to a
          // field the user is about to retype is worse than an empty form.
          key={location.key}
          // SUCCESS LANDS ON THE NEW STORAGE, not on Home — navigating to the thing you just made
          // is strictly more useful than closing and leaving the user to find its card.
          //
          // `replace` so Back from those details goes Home rather than back into a re-armed form
          // for a storage that now exists.
          onSaved={(name) =>
            navigate(name === "" ? "/" : `/storage/${encodeURIComponent(name)}`, { replace: true })
          }
          footer={({ save, canSave, saving, adopting }) => (
            <div className="mt-6">
              <Button onClick={save} disabled={!canSave || saving} data-testid="add-storage-save">
                {adopting ? "Add this storage" : "Add storage"}
              </Button>
            </div>
          )}
        />
      </div>
    </section>
  );
}
