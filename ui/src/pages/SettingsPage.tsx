import { SectionHeading } from "@/components/ui/section-heading";
import { useConfig } from "@/lib/config";
import { ConfigView } from "@/features/settings/ConfigView";
import { ConfigEditor } from "@/features/settings/ConfigEditor";
import { Link } from "react-router-dom";
import { ChevronRight } from "lucide-react";

export function SettingsPage() {
  const { data, isLoading, isError } = useConfig();
  return (
    <section>
      <h1 className="text-xl font-semibold tracking-tight">Settings</h1>
      {/* THE TWO EDITING PATHS DIFFER, AND THIS LINE IS WHERE A USER FINDS THAT OUT (`qn.6g`,
          quince#577). It read *"changes apply on restart (live reload lands later)"* — false for
          the form below as of this rung, and this is the only place the difference is stated.

          A change made HERE applies immediately. The SAME change hand-edited into `config.yml` does
          not, because nothing watches the file — file-watch was ruled into its own rung on
          2026-08-04, option (a). Saying only *"changes apply immediately"* would be the wider claim
          this project keeps paying for; saying nothing would leave a hand-editor waiting for an
          effect that never arrives. */}
      <p className="mt-1 text-sm text-muted">
        quince is configured by one file, <code className="font-mono">config.yml</code>. Changes made
        here apply immediately. You can edit the file by hand instead — quince picks those up when it
        restarts.
      </p>

      {/* THE COLUMNS RENDER UNCONDITIONALLY; THEIR CONTENTS ARE GUARDED INDIVIDUALLY.
          Operator-reported 2026-08-12: the Sign-in row used to sit ABOVE this grid, which pushed
          BOTH columns down and moved "Current configuration" off the top of the page where it had
          always been.

          Putting the row inside the left column fixes desktop and phone with one move — the right
          column returns to the top, and on a phone the columns stack in DOM order, so the config
          dump is last. It could not simply move inside the old `{data ? …}` block, because the row
          must survive a config that FAILS TO LOAD (quince#853): that was the whole point of getting
          it out of that guard, and `SettingsAuthPage.test.tsx` renders this page with the config
          query REJECTING and requires the link to still be there.

          Hence the shape: the grid and both columns are unconditional, and `data ?` now wraps only
          the config-derived parts. A broken config costs you the editor and the dump, not the way to
          your own credentials.

          `min-w-0` ON BOTH COLUMNS IS THE STRUCTURAL HALF OF quince#631, and it is not decoration.
          A grid item defaults to `min-width: auto`, so a column will not shrink below the INTRINSIC
          width of its widest child. One long line in the config dump therefore widened this column,
          this grid, and the whole content area with it — sliding the editor's own fields off the
          left edge on a phone, so you had to scroll back to reach them. `AppLayout` installs the
          same guard one level up; the chain was broken here, and a guard is only as strong as its
          shortest link. Both columns, not only the one that overflowed: the editor renders config
          VALUES too and is one long default away from the same behaviour. */}
      <div className="mt-6 grid gap-8 lg:grid-cols-2">
        <div className="min-w-0">
          {/* NOT A CARD ANY MORE. As a bordered `bg-card` block it was the only card on the page and
              read as a banner dropped on top of the settings rather than as part of them — which is
              what "looks off" was about. It is now a ROW: same border and radius as the inputs
              beside it, so it belongs to the column it sits in, with the chevron carrying the "this
              goes somewhere" affordance the border used to shout.

              ABOVE the `Edit` heading rather than under it: it is not a config field, and quince#841
              ruling A is that auth is not configuration. It sits in this column because that is
              where "things you change" live, not because it is one of them. */}
          {/* `max-w-md` IS `ConfigEditor`'S OWN CONSTRAINT, not a number chosen to look right. That
              form is `flex max-w-md flex-col gap-4`, so this row ends exactly where the fields below
              it do — Operator-reported 2026-08-12, DESKTOP ONLY, because the column is wider than
              the form and a full-width row overhangs everything it sits above. On a phone the column
              is narrower than `md` and this changes nothing, which is why it read fine there.

              A SHARED TOKEN RATHER THAN A MATCHING GUESS: `SettingsPage.test.tsx` asserts the two
              carry the same max-width class, so changing one without the other is a test failure
              rather than a slow drift nobody notices until a screenshot. */}
          <Link
            to="/settings/auth"
            className="flex max-w-md items-center justify-between rounded-lg border border-line px-3 py-2.5 text-sm transition-colors hover:bg-elevated"
          >
            <span>
              <span className="font-medium">Sign-in</span>
              <span className="mt-0.5 block text-muted">Password and passkeys</span>
            </span>
            <ChevronRight size={18} strokeWidth={1.75} className="shrink-0 text-muted" />
          </Link>

          {/* IN THIS COLUMN, NOT ABOVE THE GRID. Above, they pushed both columns down for as long
              as the query was in flight — the same defect as the Sign-in row, arriving on every
              load rather than permanently. */}
          {isLoading ? <div className="mt-6 text-sm text-muted">Loading…</div> : null}
          {isError ? (
            <div className="mt-6 text-sm text-danger">Could not load configuration.</div>
          ) : null}

          {data ? (
            <div className="mt-6">
              <SectionHeading>Edit</SectionHeading>
              <div className="mt-3">
                <ConfigEditor config={data.config} />
              </div>
            </div>
          ) : null}
        </div>

        {/* LAST ON A PHONE, BY DOM ORDER RATHER THAN BY A CLASS. The columns stack below `lg`, so
            second-in-source is last-on-screen — and this is the one block that should be, because
            it is long, read-only, and buries whatever follows it. */}
        <div className="min-w-0">
          {data ? (
            <>
              <SectionHeading>Current configuration</SectionHeading>
              <div className="mt-3">
                <ConfigView data={data} />
              </div>
            </>
          ) : null}
        </div>
      </div>
    </section>
  );
}
