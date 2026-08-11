import { useNavigate } from "react-router-dom";
import { Button } from "@/components/ui/button";
import { AddStorageForm } from "@/features/storage/AddStorageForm";
import { DocLink } from "@/components/DocLink";
import { useConfig } from "@/lib/config";

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
  // THE CONFIG IS ALREADY IN THE CACHE — `RequireStorage` is what routed the user here and it reads
  // the same query, so this costs no extra request on the path that matters.
  const cfg = useConfig();
  const warnings = cfg.data?.warnings ?? [];
  const configPath = cfg.data?.source.path ?? "config.yml";
  const fileText = cfg.data?.file_text ?? "";

  return (
    <div className="mx-auto min-h-dvh max-w-xl px-6 pb-16 pt-10">
      {warnings.length > 0 ? (
        <>
          {/* THE HEADLINE MUST NOT CLAIM THE OPERATOR HAS NO STORAGE when their own file declares
              one (quince#849). That claim is what this screen was making: an operator whose
              `config.yml` had just become illegal was told *"quince needs somewhere to keep
              backups"*, which is the state-honesty rule broken in the largest text on the page.

              INTERIM WORDING, AND DELIBERATELY NEUTRAL BETWEEN TWO STATES THE WIRE CANNOT YET
              SEPARATE. `GET /api/config` serves `warnings` but not `Loaded.Errors`, so a client
              cannot tell a DISCARDED config — where the declared storage is not running and nothing
              is backing up — from a config that parsed with an ignored unknown key, where the
              storage is fine. `config.storage: null` does not separate them either: a fresh install
              with a typo has that too.

              Those two want opposite headlines, and the fact the user came here for is which one
              they are in. The architect ruled the bit gets added to the config response
              (quince#849, `needs-operator` — it is a contracts addition); until it lands this says
              something TRUE OF BOTH rather than something confident and wrong half the time. When
              the bit arrives, this branch splits and the warnings-only case gets `Add your first
              storage` back. */}
          <h1 className="text-xl font-semibold tracking-tight">
            quince reported a problem with your configuration
          </h1>
          <p className="mt-2 text-sm text-muted">
            quince is running on the settings it could load, so what your file declares may not all
            be in effect.
          </p>

          <div
            className="mt-4 rounded-card border border-line bg-accent-soft p-3 text-sm text-warn"
            data-testid="config-warnings"
          >
            {/* THE DAEMON'S OWN PATH AND SENTENCE, which is the half `qn.6g` makes non-optional: a
                remedy the user cannot follow is the same defect as a silent failure, and "there is
                a problem" without the line is exactly that. Same treatment as `ConfigView`'s list —
                both fields are arbitrary-length server strings (quince#631). */}
            <ul className="list-disc pl-5 font-mono text-xs break-words">
              {warnings.map((w, i) => (
                <li key={i}>
                  {w.path ? `${w.path}: ` : ""}
                  {w.message}
                </li>
              ))}
            </ul>
          </div>

          {/* THE RESTART IS PART OF THE REMEDY, not padding. There is no reload path — `Load` runs
              at construction and nothing re-reads the file (quince#727) — so editing `config.yml`
              alone changes nothing about the running process. */}
          <p className="mt-2 text-sm text-muted">
            Fix it in <code className="font-mono text-xs">{configPath}</code> and restart quince.
          </p>

          {fileText !== "" ? (
            <details className="mt-3">
              <summary className="cursor-pointer text-sm text-muted">
                Show the file quince read
              </summary>
              {/* `file_text` IS THE FILE, not a rendering of the parsed document (contracts §6) —
                  which is the whole reason it is worth showing here. The paths above point INTO
                  this, and after a bad hand-edit it is the only place the offending line exists. */}
              <pre className="mt-2 overflow-x-auto rounded bg-card p-2 text-xs whitespace-pre-wrap break-words">
                {fileText}
              </pre>
            </details>
          ) : null}

          <h2 className="mt-8 text-sm font-semibold text-muted">Add a storage</h2>
          {/* THE FIRST-RUN PREMISE IS NOT REPEATED HERE, and dropping it is the fix rather than a
              trim. *"quince needs somewhere to keep backups before it can do anything else"* does
              not literally claim the operator has none — but above a form, on a screen reached
              because their declaration was discarded, it implies exactly that, which is the claim
              this issue exists to remove. Caught by its own e2e assertion, which was written
              against the ruling and failed on this paragraph. */}
          <p className="mt-2 text-sm text-muted">
            If you meant to declare a storage in the file, fixing the problem above is the thing to
            do — adding one here will not clear it.
          </p>
        </>
      ) : (
        <>
          <h1 className="text-xl font-semibold tracking-tight">Add your first storage</h1>
          <p className="mt-2 text-sm text-muted">
            quince needs somewhere to keep backups before it can do anything else. Point it at a
            folder it can reach from inside its container — a mounted disk, a NAS share, or a ZFS
            dataset.
          </p>
          <p className="mt-2 text-sm text-muted">
            Nothing is created or changed until you save. If the path is wrong, quince says so
            rather than making it — see <DocLink path="deploy/storage.md" />.
          </p>
        </>
      )}

      {/* THE FORM STAYS IN BOTH BRANCHES (quince#849, ruled). It is not a trap any more: quince#857
          refuses an add while the config on disk was discarded, and the refusal names the offending
          line — so pressing it fails honestly rather than replacing the file. Removing it would
          also break the ordinary first-run path, which is the same screen. */}
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
